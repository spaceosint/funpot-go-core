# Руководство для агентов: сценарии игры на базе пакетов сценариев (Scenario Graph v2)

## Зачем нужен этот документ

Этот документ фиксирует **каноничную логику** для новой связи между пакетами сценариев (далее — *сценарии игры*) и задаёт единый план внедрения для backend/admin/runtime.

Цель: перейти от локальной логики шагов внутри одного пакета к оркестрации **цепочек пакетов**, связанных через `Transitions`, с контролем условий старта следующего пакета и условий завершения отслеживания игры.

---

## Термины

- **Пакет сценария (Scenario Package)** — существующая сущность, внутри которой уже есть шаги (`ScenarioStep`) и переходы между шагами.
- **Сценарий игры (Game Scenario)** — новый граф верхнего уровня: ноды = готовые пакеты сценариев, рёбра = `Transitions` между пакетами.
- **Переход пакета (Package Transition)** — правило перехода от текущего пакета к целевому пакету, вычисляемое по накопленному состоянию.
- **Условие завершения цепочки (Terminal Condition)** — правило, при котором игра считается завершённой и отслеживание останавливается.
- **Guard первого шага** — обязательная проверка: даже если `Package Transition` сработал, вход в целевой пакет разрешён только если выполняется условие входа его первого шага (`initial step condition`).

---

## Каноничная бизнес-логика (обязательно к реализации)

1. Админ в панели собирает **сценарий игры** из уже готовых пакетов:
   - создаёт ноды (каждая нода ссылается на существующий `ScenarioPackage`),
   - связывает ноды через `Transitions`.
2. У каждой цепочки есть собственные **условия завершения**.
   - Пример: в state зафиксировано `side=ct` и `winner=ct`.
   - При совпадении условий backend прекращает дальнейшее LLM-отслеживание этой game-session.
3. Результат завершения/прогресса отображается в отслеживании стримера.
4. Переход между пакетами двухфазный:
   - фаза A: совпало условие `Package Transition`,
   - фаза B: валидирован `entry` первого шага целевого пакета.
   - Если фаза B не пройдена — переход **запрещён**, рантайм остаётся в текущем пакете/шаге.

---

## Требования к моделям и хранению

> Ниже логическая модель. Конкретные имена таблиц/полей могут отличаться, но семантика должна быть сохранена.

### 1) GameScenario
- `id`
- `slug` (уникальный ключ сценария игры)
- `title`
- `isActive`
- `initialNodeId`
- `version` / `publishedAt`
- `createdBy`, `updatedBy`, `timestamps`

### 2) GameScenarioNode
- `id`
- `gameScenarioId`
- `scenarioPackageId` (ссылка на существующий пакет)
- `alias` (уникальный в рамках графа)
- `order` (опционально, для UX)
- `metaJson` (опционально)

### 3) GameScenarioTransition
- `id`
- `gameScenarioId`
- `fromNodeId`
- `toNodeId`
- `priority` (чем меньше число — тем раньше проверка)
- `conditionExprJson` (каноничный формат условия)
- `description`

### 4) GameScenarioTerminalCondition
- `id`
- `gameScenarioId`
- `scope` (`global` | `node` | `edge`)
- `nodeId` / `transitionId` (опционально в зависимости от `scope`)
- `conditionExprJson`
- `resultType` (`win` | `loss` | `draw` | `unknown` | кастом из домена)
- `resultPayloadJson`

### 5) Runtime state (на match-session)
- `currentGameScenarioId`
- `currentNodeId`
- `currentPackageId`
- `currentStepId`
- `stateJson`
- `lastTransitionTrace`
- `finishedAt`, `finishReason`, `finalResultJson`

---

## Алгоритм рантайма (worker)

На каждом цикле обработки:

1. Обновить `stateJson` по ответу активного шага текущего пакета.
2. Проверить `TerminalCondition` (по приоритету/детерминированному порядку).
   - Если совпало: зафиксировать `finalResult`, проставить `finishedAt`, остановить цикл.
3. Проверить `GameScenarioTransition` из текущей ноды (по `priority`).
4. Если найден candidate-переход:
   - Получить первый шаг целевого пакета.
   - Проверить `entry condition` первого шага на **текущем** `stateJson`.
   - Если true: переключить `currentNodeId/currentPackageId/currentStepId`.
   - Если false: записать причину в trace и остаться в текущем пакете.
5. Если переходов нет или ни один невалиден — продолжить текущий пакет по его внутренней step-логике.

Ключевые правила:
- Никаких хардкодов под конкретную игру.
- Все условия читаются из данных (admin-managed).
- Поведение детерминировано и идемпотентно для повторной обработки чанка.

---

## Контракт для админки

Админке нужны backend-возможности:

1. CRUD для `GameScenario` (черновик/публикация/активация).
2. Добавление/удаление нод на основе существующих `ScenarioPackage`.
3. Управление `Transitions` между нодами с `priority` и валидатором условий.
4. Управление `TerminalConditions`.
5. Проверка графа перед публикацией:
   - есть `initialNode`,
   - все `toNodeId` существуют,
   - нет недостижимых обязательных нод (по выбранной политике),
   - нет конфликтов приоритета без deterministic tie-break,
   - у каждого целевого пакета существует первый шаг с валидным `entry`-контрактом.
6. API просмотра runtime-трассировки для стримера (что сработало/почему не перешли/почему завершили).

---

## План имплементации (с удалением лишнего)

> План ориентирован на M2.1 и текущий `scenario-graph v2`.

### Этап 1 — Аудит и зачистка
1. Найти и удалить/изолировать код, который:
   - обходит `Scenario Package v2` и использует legacy chain/detector,
   - принимает решение о переходах в коде вместо data-driven условий,
   - допускает переход в пакет без проверки guard первого шага.
2. Удалить устаревшие admin endpoints/DTO/handlers, не соответствующие текущему контракту graph-v2.
3. Удалить/обновить устаревшие участки документации, где описана линейная цепочка без graph-модели.

### Этап 2 — Модель данных верхнего уровня
1. Ввести доменные сущности `GameScenario`, `Node`, `Transition`, `TerminalCondition`.
2. Добавить миграции PostgreSQL и репозитории.
3. Реализовать версионирование и флаг активного сценария игры.

### Этап 3 — Runtime orchestration
1. Расширить match-session state указателями на текущую ноду/пакет.
2. Реализовать детерминированный резолвер переходов между пакетами.
3. Добавить обязательный `first-step guard` check перед входом в целевой пакет.
4. Добавить движок terminal-условий (ранняя остановка и фиксация результата).
5. Добавить трассировку решений (`transition accepted/rejected`, причина).

### Этап 4 — Admin API/валидация графа
1. Добавить REST endpoints для CRUD сценариев игры, нод, переходов и terminal-условий.
2. Добавить endpoint «validate/publish» с полным набором проверок графа.
3. Ограничить админские поверхности только актуальными сущностями graph-v2.

### Этап 5 — Наблюдаемость и выдача результата
1. Публиковать runtime-статус и финальный результат в трекинг стримера (REST/WSS).
2. Метрики: доля успешных переходов, отказов guard-проверки, частота terminal-match, latency цикла.
3. Алерты на аномалии (резкий рост reject по guard, зацикливание в ноде, пустые terminal).

### Этап 6 — Тесты и стабилизация
1. Table-driven тесты для:
   - переходов между пакетами,
   - guard первого шага,
   - terminal-условий,
   - приоритетов и tie-break.
2. Интеграционные тесты worker + storage + admin publish.
3. Нагрузочный план обновить в `docs/load_testing.md` для длинных цепочек и высокочастотных апдейтов state.

---

## Явно удалить как «лишнее» в рамках этой задачи

- Legacy prompt-chain/detector runtime path (если ещё остался в исполняемом коде).
- Админские CRUD, которые не участвуют в graph-v2 и не нужны для управления пакетами/переходами/terminal-условиями.
- Документацию, где финализация игры описана без `TerminalCondition`.
- Runtime-переходы, которые не проверяют guard первого шага целевого пакета.

---

## Критерии готовности

1. Админ может собрать сценарий игры из готовых пакетов и опубликовать его.
2. Worker выполняет переходы между пакетами только через `Transitions` + `first-step guard`.
3. При совпадении `TerminalCondition` отслеживание корректно завершается.
4. В трекинге стримера видны прогресс, причины переходов/отказов и финальный результат.
5. В коде и документации отсутствуют активные legacy-пути, противоречащие graph-v2.

---

## Чеклист статуса (для каждого инкремента)

### M2.1 (`docs/implementation_plan.md`)
- [ ] Re-introduce scenario-package persistence in storage (PostgreSQL) after cleanup.
- [ ] Implement stream capture worker pipeline (`streamlink -> chunking -> state update`).
- [ ] Implement match-session lifecycle with persisted JSON state.
- [ ] Ship initial Counter-Strike tracker with finalization from accumulated evidence.
- [x] Start migration to scenario-graph orchestration.
- [ ] Add resilient orchestration (retry/idempotency/dead-letter).
- [ ] Publish live updates via WebSocket.
- [ ] Provide REST history endpoints for state/final decisions.
- [ ] Add observability metrics + drift alerts.

### `docs/llm_stream_orchestration_plan.md` (Goal + canonical behavior)
- [x] 3-уровневое поведение (root -> game folder -> concrete scenario) сохранено как база.
- [x] Transition-логика data-driven (без hardcoded keys/paths).
- [x] Stay-on-step fallback сохранён.
- [ ] Верхнеуровневый graph сценариев игры (пакет->пакет) внедрён в runtime полностью.
- [ ] TerminalCondition engine внедрён в production runtime.
- [ ] First-step guard при переходе в новый пакет внедрён в production runtime.
