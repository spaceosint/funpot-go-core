# LLM Stream Orchestration Plan (Gemini + Streamlink)

## Goal
Design and implement an MVP where:
1. Admin configures a global game-detection prompt plus per-game scenario prompts and runtime limits.
2. Background workers read livestream fragments via Streamlink.
3. Workers first detect the game, then execute the active per-game scenario step-by-step and store normalized decisions/transitions.
4. Users open streamer page and observe live LLM status updates.

## Scope of this iteration
- Planning and target architecture for M2.1.
- Security model for admin access (Telegram-based identity).
- Redis usage model for orchestration and realtime delivery.

## Priority implementation target
- Treat the following as the top-priority slice for immediate delivery:
  - streamer added -> analysis job starts automatically;
  - Streamlink captures/reads fragment each 10 seconds;
  - each fragment is sent first to the active global game-detection prompt, then to the active game-scenario step prompt when applicable;
  - decisions and step transitions are persisted and published to websocket consumers.

## Product flow (high level)
1. User (regular user or admin) adds streamer via `POST /api/streamers`.
2. Worker scheduler picks active streamers and starts analysis cycle (or an immediate bootstrap job right after streamer creation).
3. Every fragment first goes through the active global game-detection prompt.
4. If a supported game is detected, the worker resolves the active admin-managed scenario for that game.
5. The scenario runs step-by-step, and each next step is chosen from the previous normalized LLM answer / transition rule.
6. Step outputs are persisted and broadcast to clients.
7. UI shows timeline + current game/scenario state for each streamer.

## Scenario model

### Global detector (always-on entrypoint)
Question: What game is currently on the stream?
- Output enum should map into known games, e.g. `counter_strike | dota2 | valorant | unknown`.
- If output is `unknown`, pipeline stays on the global detector and retries on the next capture window.
- If output matches a configured game scenario, the worker loads that scenario and enters its first step.

### Per-game scenarios
- Each game can have one active scenario at a time.
- A scenario contains ordered or graph-based steps.
- Step count is admin-defined: some games may have 2 stages, others 4+ stages.
- Each step references an active admin-managed prompt version plus runtime config.
- Each step declares transition rules: which normalized answer(s) move to which next step, pause state, terminal state, or fallback.
- Admin must be able to create, edit, activate, deactivate, and delete scenarios/steps/transitions.

## Counter-Strike scenario (initial target)

### CS Step 1 — Match entry / queue type detection
Question: Is a new ranked Counter-Strike match starting, and if so is it competitive, faceit, premier, or other?
- Output enum: `competitive | faceit | premier | other | no_ranked_match | uncertain`
- If `no_ranked_match`: scenario waits and retries on the next capture window.
- If `faceit` / `competitive` / `premier`: transition into the corresponding active branch.

### CS Step 2 — Match progress / completion wait
Question: Has the tracked match finished yet?
- Output enum: `in_progress | finished | uncertain`
- If `in_progress`: stay on the same step and keep polling.
- If `finished`: transition to result detection.

### CS Step 3 — Match result detection
Question: Did the streamer win the tracked match?
- Output enum: `win | loss | draw | unknown`
- Stores final decision and confidence, then returns control to the global detector for the next cycle.

## Transition rules
- Every scenario step must define normalized outputs and the next action for each output.
- Transitions may point to another step, remain on the current step, pause with cooldown, or end the scenario run.
- The worker should never infer a next step outside admin-configured transition rules.
- Admin changes must be versioned/auditable so historical decisions keep prompt linkage.

## Admin capabilities (backend requirements)
Admin scope in MVP is focused on LLM prompt/config management; broader admin tools are expected in later milestones.
- Manage the always-on global game-detection prompt (`versioned`, `is_active`).
- Manage per-game scenarios, their steps, and transition rules.
- Attach an active prompt template/version to each scenario step.
- Configure model/runtime params (`model`, `temperature`, `max_tokens`, `timeout_ms`).
- Configure orchestration controls (`retry_count`, `backoff_ms`, `cooldown_ms`, `min_confidence`).
- Enable/disable a whole scenario, a branch, or an individual step per game.
- View audit trail: who changed prompt/version/scenario/transition and when.

## Security: should admin rely only on Telegram ID?
Short answer: **Telegram ID alone is not enough**.

Recommended approach:
1. Authenticate via Telegram `initData` signature verification.
2. Map `telegram_id` to internal user record.
3. Authorize admin using role/permissions in DB (`users.role = admin/superadmin`).
4. Enforce admin access with middleware + token claims (`role`, `permissions`, `token_version`).
5. Add optional hardening for sensitive endpoints:
   - allowlist of admin Telegram IDs in env for bootstrap,
   - short JWT TTL + refresh rotation,
   - IP/device anomaly alerts,
   - action audit logs.


## Redis and sessions strategy
For this backend we should **use Redis for server-side session state**, not only for queues/realtime.

Recommended split:
1. **Access token (JWT)** remains short-lived and stateless for normal API checks.
2. **Refresh session** is stored in Redis (`session:{id}` with TTL) so we can:
   - revoke sessions instantly (logout / suspicious activity),
   - limit concurrent sessions per admin/user,
   - rotate refresh tokens safely,
   - invalidate all sessions by `token_version` bump.
3. Keep PostgreSQL as source of truth for users/roles, Redis as fast volatile session store.

Why this matters for admin security:
- admin actions are sensitive; quick session revocation is required,
- Redis TTL gives automatic expiry and reduces DB writes,
- multi-instance API nodes share the same session view without sticky sessions.

## Why Redis is useful here
Redis should be introduced before full orchestration rollout because it reduces load and enables near-real-time behavior.

Primary usage:
1. **Job queue / stream analysis scheduling**
   - pending jobs by streamer and stage,
   - delayed retries/backoff,
   - dead-letter queue.
2. **Distributed locks**
   - prevent duplicate processing for same streamer/stage/window.
3. **Realtime pub/sub**
   - broadcast stage updates to websocket nodes.
4. **Hot cache**
   - last known stage/status for fast streamer page rendering.
5. **Rate limiting**
   - protect admin update endpoints and worker-triggered LLM calls.
6. **Idempotency keys with TTL**
   - deduplicate repeated events/chunks.

## Data contracts (draft)

### Stream analysis run
- `run_id`
- `streamer_id`
- `started_at`, `finished_at`
- `source` (`streamlink`)
- `status` (`running|completed|failed|partial`)

### Scenario step decision
- `id`, `run_id`, `scenario_id`, `step_id`
- `game_key`
- `prompt_version_id`
- `input_ref` (clip/chunk reference)
- `raw_response` (json/text)
- `normalized_label`
- `transition_key`, `next_step_id`
- `confidence`
- `latency_ms`, `tokens_in`, `tokens_out`
- `error_code`, `error_message`
- `created_at`

## API/WSS plan (MVP)
- `POST /api/streamers` — add streamer (available to authenticated users, not admin-only).
- `GET /api/streamers/:id/status` — current aggregated game/scenario status.
- `GET /api/streamers/:id/llm-decisions?limit=` — detector + scenario step decision history.
- `GET /api/admin/prompts` / `POST /api/admin/prompts` / `POST /api/admin/prompts/:id/activate`.
- `GET /api/admin/scenarios` / `POST /api/admin/scenarios` / scenario step/transition management endpoints.
- `WS /ws` event `LLM_STAGE_UPDATED` with payload `{streamerId, gameKey, scenarioId, stepId, label, confidence, ts}`.

## Phased implementation

### Phase 1 — Admin + prompt management
- Add admin authorization middleware and role checks.
- Add global detector prompt CRUD + activation.
- Add per-game scenario/step/transition CRUD + activation.
- Add audit logging for prompt/scenario changes.

### Phase 2 — Worker orchestration skeleton
- Scheduler selects active streamers.
- Streamlink chunk fetch + storage reference.
- Gemini client wrapper + normalized parser for detector and scenario steps.

### Phase 3 — Realtime delivery
- Persist detector + scenario step decisions.
- Publish WS events and expose REST status/history.
- Add reconnect/backfill logic for client timeline.

### Phase 4 — Reliability & observability
- Retries/backoff, DLQ, idempotency guards.
- Metrics: detector/scenario-step latency, success/fail ratio, token usage.
- Alerts on model drift / error spikes.

## Risks and mitigations
- Ambiguous visual/audio context -> use confidence threshold + `unknown` path.
- LLM cost spikes -> rate limits, batching windows, token budgets.
- Duplicate pipeline executions -> Redis lock + idempotency keys.
- Prompt regressions -> versioning, canary activation, rollback to previous prompt.

## Open questions before coding
1. Which non-CS games must the global detector recognize in MVP versus return as `unknown`?
2. Fixed for current priority scope: Streamlink chunks must be sampled every 10 seconds (revisit only after baseline stability).
3. Should result determination rely only on LLM, or also optional external match APIs later?
4. What freshness SLA do we target on streamer page (e.g., update every <=10 seconds)?

## Execution backlog (next two iterations)

This backlog continues implementation according to `docs/implementation_plan.md` (M2.1)
and is ordered to ship a vertical slice before hardening.

### Iteration A — End-to-end pipeline baseline

Goal: produce and persist global game-detector decisions for active streamers from real worker cycles, then enter the first configured game scenario step when a supported game is found.

#### A1. Worker pipeline skeleton
- [ ] Introduce `internal/media/stream_capture_worker.go` (or equivalent module package)
  with cycle orchestration:
  - acquire streamer lock,
  - fetch fragment via streamlink adapter every 10 seconds,
  - resolve the active global detector prompt and call Gemini,
  - if a supported game is detected, resolve the active scenario + current step prompt,
  - persist normalized detector/step decision and transition outcome.
- [ ] Add streamlink adapter interface to isolate process execution and allow tests.
- [ ] Add DB model/repository for `stream_analysis_runs` and link to stage decisions.

Definition of done:
- one worker pass creates `run` + global detector record for a test streamer;
- duplicate cycle for the same lock window is rejected.

#### A2. Global detector normalization and storage
- [ ] Implement global detector parser mapping model output to supported game keys
  plus `unknown`.
- [ ] Add confidence threshold and cooldown handling for `unknown` branch.
- [ ] Persist `raw_response`, `normalized_label`, `confidence`, `latency_ms`,
  `tokens_in`, `tokens_out`, and chosen scenario transition.

Definition of done:
- parser is covered by table-driven tests for valid/invalid responses;
- confidence fallback path stores `unknown` and does not crash the cycle.

#### A3. Baseline telemetry for pipeline
- [ ] Add metrics for detector/scenario-step latency and success/fail counters.
- [ ] Add structured logs with `run_id`, `streamer_id`, `game_key`, `scenario_id`, `step_id`, and `attempt`.

Definition of done:
- metrics are visible in local `/metrics` output and include stage labels;
- failure logs are correlated by `run_id`.

### Iteration B — Full staged flow + reliability

Goal: complete M2.1 exit criteria with staged flow, WS updates, and resilient
orchestration.

#### B1. Scenario workflow + transitions
- [ ] Implement admin-defined transition engine:
  - load active scenario for the detected game,
  - execute the current step,
  - choose next step strictly from normalized output + transition config.
- [ ] Implement the initial Counter-Strike scenario normalization enums:
  - Step 1: `competitive | faceit | premier | other | no_ranked_match | uncertain`
  - Step 2: `in_progress | finished | uncertain`
  - Step 3: `win | loss | draw | unknown`
- [ ] Support branch-specific behavior, e.g. Faceit branch waits for completion then asks for result.

Definition of done:
- deterministic transition unit tests pass;
- each detector/scenario step emits decision records with prompt version linkage.

#### B2. Retry, idempotency, dead-letter
- [ ] Add per-detector/per-step retry policy with exponential backoff.
- [ ] Add idempotency keys (`streamer_id + scenario_or_detector + step + window`) with Redis TTL.
- [ ] Add DLQ payload format and reprocessing admin command.

Definition of done:
- transient failures are retried and eventually either succeed or move to DLQ;
- duplicate job delivery does not create duplicate terminal decisions.

#### B3. Realtime and session integration
- [ ] Publish `LLM_STAGE_UPDATED` from worker path to WS hub.
- [ ] Add reconnect backfill flow (`GET status` + `GET llm-decisions`).
- [ ] Integrate Redis refresh session store in auth login/refresh/logout endpoints:
  - token pair issuance,
  - rotate on refresh,
  - revoke-by-device,
  - revoke-all sessions.

Definition of done:
- websocket clients receive near-real-time detector/scenario-step updates;
- refresh token replay is rejected after rotation;
- revoke-all immediately invalidates prior refresh sessions.

## Delivery checklist mapped to `docs/implementation_plan.md`

### M2.1 completion checklist
- [x] Implement stream capture worker pipeline with global detector + per-game scenario routing.
- [ ] Build staged CS game flow (A/B/C/D).
- [ ] Add retries, idempotency, and dead-letter handling.
- [ ] Publish live LLM status updates via WebSocket.
- [ ] Integrate refresh session store into auth flows.
- [ ] Add observability (latency, success ratio, token usage, drift alerts).

### Next milestone preview (M3)
- [ ] Start `/internal/worker/events` ingestion only after M2.1 checklist is
  completed.
