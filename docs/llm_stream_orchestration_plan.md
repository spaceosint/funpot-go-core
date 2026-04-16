# LLM Stream Orchestration Plan (Scenario Graph v2)

> Extension for agent-facing implementation of package-to-package game scenarios:
> `docs/agent_game_scenario_packages_plan_ru.md`.

## Goal
Build a single orchestration model where admin-defined **scenario steps** drive the
LLM analysis loop.

Legacy linear prompt-chain / detector entrypoints are removed from runtime.
Current orchestration uses only the model below:

1. One active **root step** (`initial=true`) for first entry.
2. Per-game folders (for example `cs2/*`) that contain game-specific scenarios.
3. Every step has:
   - entry condition (for first entry optional only for root),
   - prompt text,
   - expected JSON contract,
   - transitions (`if` rules) to other steps.
4. LLM must return strict JSON only.
5. Step response updates persisted state; transitions are evaluated from state.

## Canonical 3-level behavior

### Level 1: game detection
- Root prompt detects game and writes fields such as `game`.
- Example transition: `if game == cs2 -> cs2_mode`.

### Level 2: game scenario (folder level)
- Game-specific step (e.g. `cs2_mode`) asks for mode/context.
- Example transition: `if mode == faceit -> cs2_faceit`.
- If response says there is no concrete mode yet, remain on current step.

### Level 3: concrete in-game scenario
- Deep step (`cs2_faceit`) handles specific logic and outcomes.
- Can transition back to root when game changes.

## State + transitions
- Each step writes JSON delta/state fragment.
- Worker keeps compact persisted state per streamer/session.
- Transition evaluation happens each cycle from current step + current state.
- If no transition matches, worker stays on the same step.
- Backend must not rely on any hardcoded response keys; the only valid payload
  contract is the active step `responseSchemaJson`.

## Admin UX requirements
- Scenario tree grouped by `gameSlug` and `folder` path.
- UI must show graph dependencies: `step -> transitions -> target step`.
- Editing should not require code changes; all conditions are admin-managed data.

## Immediate backend scope (current iteration)
- Add scenario package domain model:
  - `ScenarioPackage`
  - `ScenarioStep`
  - `ScenarioTransition`
- Add active package resolver per game slug.
- Add step resolution algorithm:
  - initial selection,
  - transition by priority,
  - stay-on-step fallback.
- Wire worker to prefer scenario package execution when present.

## Deferred (next iteration)
- Database persistence and versioning for scenario packages.
- Full admin CRUD HTTP routes for scenario graph editing.
- Visual graph API for UI rendering.
- Removal of all legacy prompt endpoints after migration completes.
