# Обзор текущей реализации Scenario Package + Game Scenario и варианты упрощения

Дата обзора: 2026-04-24

## Что есть сейчас (кратко)

Текущий runtime-оркестратор в `internal/media/worker.go` работает в несколько фаз:
1. Определяет активный `GameScenario` и узел графа (`ResolveNode`).
2. Вычисляет terminal-условия на уровне `GameScenario` (`ResolveTerminalCondition`).
3. Затем внутри выбранного `ScenarioPackage` дополнительно вычисляет package-переходы (`ResolveNextPackage`).
4. И только потом выбирает шаг внутри пакета (`ResolveStep`).

Это даёт гибкость, но создаёт дублирование уровней переходов и много повторной работы на каждом цикле.

## Наблюдения по усложнениям

### 1) Два уровня межпакетных переходов одновременно
- Уровень A: `GameScenario.ResolveNode` переводит между package-узлами.
- Уровень B: `ScenarioPackage.ResolveNextPackage` тоже переводит между package.

Из-за этого один и тот же бизнес-смысл («перейти на другой пакет») может быть описан в двух местах и давать конфликтующие/непрозрачные траектории.

**Упрощение:** оставить один канонический уровень для package->package:
- либо только `GameScenario` (рекомендуется для явного DAG/graph),
- либо только `PackageTransitions` (если нужен полностью data-driven пакет без внешнего графа).

Для Scenario Graph v2 обычно проще сопровождать явный `GameScenario`-граф, а package-транзишены оставить только для `stop_tracking` (terminal) и не использовать для hop между пакетами.

### 2) Повторная сортировка и построение индексов на каждом тике
Методы `ResolveNode`, `ResolveStep`, `ResolveNextPackage`, `ResolveTerminalCondition` каждый раз:
- создают map по id,
- сортируют transitions/terminals,
- заново парсят state JSON.

На длинных сессиях это лишняя CPU/GC-нагрузка.

**Упрощение:** добавить «скомпилированный snapshot» активного графа:
- pre-index by id,
- pre-sort transitions,
- pre-validate references,
- опционально pre-parse условий в AST/bytecode-представление.

Worker будет использовать immutable snapshot до следующей активации сценария.

### 3) Condition DSL парсится ad-hoc строковыми операциями
Сейчас условный язык реализован через ручной string parsing (операторы, скобки, exists/not_exists).

Риски:
- трудно расширять (in/not in, contains, функции, строгая типизация),
- сложно объяснять ошибки администраторам,
- дублируемые сценарии edge-case поведения.

**Упрощение:** ввести слой `ConditionEngine`:
- compile-time валидация + понятные ошибки,
- runtime `Evaluate(compiledExpr, state)` без повторного парсинга,
- единое место для новых операторов.

### 4) Неочевидный fallback при рассинхроне stage/package
В `planScenarioExecution` есть fallback на initial step, если текущий `latest.Stage` отсутствует в пакете.

Это делает систему живучей, но может скрывать ошибки миграций/данных.

**Упрощение:** добавить режимы:
- `strict` (ошибка + метрика),
- `recover` (текущий fallback).

Для prod можно оставить `recover`, но обязательно эмитить telemetry-событие `stage_not_found_recovered`.

### 5) Логика terminal-condition размазана
Terminal-остановка есть:
- на уровне `GameScenario` (terminal conditions в transitions),
- на уровне `ScenarioPackage` (`stop_tracking`).

**Упрощение:** договориться о едином источнике terminal-логики:
- либо terminal только в GameScenario,
- либо только в Package (если хотим финализацию близко к шагам).

Компромиссный вариант: GameScenario для межпакетной жизненной стадии матча, Package terminal — только локальные in-package финалы с явным приоритетом, задокументированным в одном месте.

### 6) Избыточные round-trip к store для связанных сущностей
При переключении узлов worker может дополнительно грузить пакет по id (`GetScenarioPackage`) в середине цикла.

**Упрощение:** активный `GameScenario` должен возвращаться вместе с уже "hydrated" package map (`nodeID -> package snapshot`) в memory cache (TTL/версия по activatedAt).

## Более простой целевой алгоритм

Рекомендуемый упрощённый runtime цикл:
1. Взять `CompiledScenarioGraph` (активная версия, immutable).
2. По текущему `nodeID` и `state` вычислить переход узла (без повторной сортировки).
3. Проверить terminal-условия в одной канонической точке.
4. Внутри выбранного package вычислить следующий step (pre-index transitions).
5. Выполнить LLM для step, merge state, persist event.

Сложность каждого тика становится в основном O(k), где `k` — число исходящих переходов текущего узла/шага, без глобальных повторных проходов и без повторного парсинга выражений.

## Приоритет внедрения (минимальный риск)

1. **P1 (быстрый выигрыш):** pre-sort/pre-index transitions и parseJSON один раз на тик.
2. **P1:** убрать дублирование package-hop (оставить один канал межпакетных переходов).
3. **P2:** выделить `ConditionEngine` с compile/eval API.
4. **P2:** telemetry для fallback-восстановлений и конфликтов приоритетов.
5. **P3:** compiled snapshot + кеш активной оркестрации.

## Влияние на roadmap (связь с M2.1 и Scenario Graph v2)

- Полностью соответствует `docs/llm_stream_orchestration_plan.md` (цель: единая оркестрация шагов и переходов, stay-on-step fallback, data-driven управление).
- Помогает закрыть пункт M2.1 про game-scenario orchestration без лишней сложности двух параллельных механизмов package-switch.
