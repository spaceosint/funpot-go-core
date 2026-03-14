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
