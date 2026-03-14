# LLM Stream Orchestration Plan (Gemini + Streamlink)

## Goal
Design and implement an MVP where:
1. Admin configures LLM stage prompts and runtime limits.
2. Background workers read livestream fragments via Streamlink.
3. Workers send staged requests to Gemini and store normalized decisions.
4. Users open streamer page and observe live LLM status updates.

## Scope of this iteration
- Planning and target architecture for M2.1.
- Security model for admin access (Telegram-based identity).
- Redis usage model for orchestration and realtime delivery.

## Product flow (high level)
1. User (regular user or admin) adds streamer via `POST /api/streamers`.
2. Worker scheduler picks active streamers and starts analysis cycle.
3. Pipeline runs stage-by-stage (gating by previous stage output).
4. Stage outputs are persisted and broadcast to clients.
5. UI shows timeline + current stage state for each streamer.

## Stage model (Counter-Strike)

### Stage A — Game detection
Question: Is streamer currently playing Counter-Strike?
- Output enum: `cs_detected | not_cs | uncertain`
- If `not_cs`: pipeline pauses for cooldown period.
- If `uncertain`: retry policy and confidence threshold apply.

### Stage B — Match type detection
Question: If CS detected, what match mode?
- Output enum: `competitive | faceit | premier | casual | unknown`
- Required for downstream logic (different prompts/rules).

### Stage C — Match state detection
Question: Current game state?
- Output enum: `pregame | in_progress | finished | unknown`
- Pipeline proceeds to Stage D only when `finished` (or explicit terminal signal).

### Stage D — Match result detection
Question: Did streamer win?
- Output enum: `win | loss | draw | unknown`
- Stores final decision and confidence; opens next cycle.

## Admin capabilities (backend requirements)
Admin scope in MVP is focused on LLM prompt/config management; broader admin tools are expected in later milestones.
- Manage prompt templates by stage (`versioned`, `is_active`).
- Configure model/runtime params (`model`, `temperature`, `max_tokens`, `timeout_ms`).
- Configure orchestration controls (`retry_count`, `backoff_ms`, `cooldown_ms`, `min_confidence`).
- Enable/disable stage or entire pipeline per streamer/game.
- View audit trail: who changed prompt/version and when.

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

### Stage decision
- `id`, `run_id`, `stage`
- `prompt_version_id`
- `input_ref` (clip/chunk reference)
- `raw_response` (json/text)
- `normalized_label`
- `confidence`
- `latency_ms`, `tokens_in`, `tokens_out`
- `error_code`, `error_message`
- `created_at`

## API/WSS plan (MVP)
- `POST /api/streamers` — add streamer (available to authenticated users, not admin-only).
- `GET /api/streamers/:id/status` — current aggregated stage status.
- `GET /api/streamers/:id/llm-decisions?limit=` — decision history.
- `GET /api/admin/prompts` / `POST /api/admin/prompts` / `POST /api/admin/prompts/:id/activate`.
- `WS /ws` event `LLM_STAGE_UPDATED` with payload `{streamerId, stage, label, confidence, ts}`.

## Phased implementation

### Phase 1 — Admin + prompt management
- Add admin authorization middleware and role checks.
- Add prompt template CRUD + activation.
- Add audit logging for prompt changes.

### Phase 2 — Worker orchestration skeleton
- Scheduler selects active streamers.
- Streamlink chunk fetch + storage reference.
- Gemini client wrapper + normalized parser per stage.

### Phase 3 — Realtime delivery
- Persist stage decisions.
- Publish WS events and expose REST status/history.
- Add reconnect/backfill logic for client timeline.

### Phase 4 — Reliability & observability
- Retries/backoff, DLQ, idempotency guards.
- Metrics: per-stage latency, success/fail ratio, token usage.
- Alerts on model drift / error spikes.

## Risks and mitigations
- Ambiguous visual/audio context -> use confidence threshold + `unknown` path.
- LLM cost spikes -> rate limits, batching windows, token budgets.
- Duplicate pipeline executions -> Redis lock + idempotency keys.
- Prompt regressions -> versioning, canary activation, rollback to previous prompt.

## Open questions before coding
1. Is Faceit detection mandatory in MVP or can it be `unknown` fallback in first release?
2. How often should Streamlink chunks be sampled (e.g., every 15s / 30s / 60s)?
3. Should result determination rely only on LLM, or also optional external match APIs later?
4. What freshness SLA do we target on streamer page (e.g., update every <=10 seconds)?

## Execution backlog (next two iterations)

This backlog continues implementation according to `docs/implementation_plan.md` (M2.1)
and is ordered to ship a vertical slice before hardening.

### Iteration A — End-to-end pipeline baseline

Goal: produce and persist Stage A decisions for active streamers from real worker
cycles.

#### A1. Worker pipeline skeleton
- [ ] Introduce `internal/media/stream_capture_worker.go` (or equivalent module package)
  with cycle orchestration:
  - acquire streamer lock,
  - fetch fragment via streamlink adapter,
  - enqueue Gemini stage call,
  - persist normalized stage decision.
- [ ] Add streamlink adapter interface to isolate process execution and allow tests.
- [ ] Add DB model/repository for `stream_analysis_runs` and link to stage decisions.

Definition of done:
- one worker pass creates `run` + `Stage A` record for a test streamer;
- duplicate cycle for the same lock window is rejected.

#### A2. Stage A normalization and storage
- [ ] Implement Stage A parser mapping model output to:
  `cs_detected | not_cs | uncertain`.
- [ ] Add confidence threshold and cooldown handling for `not_cs` branch.
- [ ] Persist `raw_response`, `normalized_label`, `confidence`, `latency_ms`,
  `tokens_in`, `tokens_out`.

Definition of done:
- parser is covered by table-driven tests for valid/invalid responses;
- confidence fallback path stores `uncertain` and does not crash the cycle.

#### A3. Baseline telemetry for pipeline
- [ ] Add metrics for stage latency and stage success/fail counters.
- [ ] Add structured logs with `run_id`, `streamer_id`, `stage`, and `attempt`.

Definition of done:
- metrics are visible in local `/metrics` output and include stage labels;
- failure logs are correlated by `run_id`.

### Iteration B — Full staged flow + reliability

Goal: complete M2.1 exit criteria with staged flow, WS updates, and resilient
orchestration.

#### B1. Stage B/C/D workflow
- [ ] Implement stage gate transitions:
  - Stage B runs only after `Stage A = cs_detected`.
  - Stage C runs only after match type is known/accepted.
  - Stage D runs only after `Stage C = finished`.
- [ ] Implement normalization enums:
  - B: `competitive | faceit | premier | casual | unknown`
  - C: `pregame | in_progress | finished | unknown`
  - D: `win | loss | draw | unknown`

Definition of done:
- deterministic stage transition unit tests pass;
- each stage emits decision records with prompt version linkage.

#### B2. Retry, idempotency, dead-letter
- [ ] Add per-stage retry policy with exponential backoff.
- [ ] Add idempotency keys (`streamer_id + stage + window`) with Redis TTL.
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
- websocket clients receive near-real-time stage updates;
- refresh token replay is rejected after rotation;
- revoke-all immediately invalidates prior refresh sessions.

## Delivery checklist mapped to `docs/implementation_plan.md`

### M2.1 completion checklist
- [ ] Implement stream capture worker pipeline.
- [ ] Build staged CS game flow (A/B/C/D).
- [ ] Add retries, idempotency, and dead-letter handling.
- [ ] Publish live LLM status updates via WebSocket.
- [ ] Integrate refresh session store into auth flows.
- [ ] Add observability (latency, success ratio, token usage, drift alerts).

### Next milestone preview (M3)
- [ ] Start `/internal/worker/events` ingestion only after M2.1 checklist is
  completed.
