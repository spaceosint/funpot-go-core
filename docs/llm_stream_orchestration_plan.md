# LLM Stream Orchestration Plan (State Tracker + Streamlink)

## Goal
Design and implement the M2.1 stream-analysis flow around a **single-match state tracker** instead of a long prompt-chain narrative.

The target behavior is:
1. Admin configures the **state schema**, **update rules**, **finalization rules**, and runtime limits used by the LLM.
2. Background workers read livestream fragments via Streamlink every 10 seconds.
3. Each active match session is treated as **one chat / one match**.
4. For every fragment, the worker sends:
   - the previous compact state JSON,
   - the new chunk observations,
   - the active admin-managed update prompt/rules.
5. The LLM returns **updated state JSON only**.
6. When the match is finished or the video ends, the worker sends the accumulated state to a finalization prompt and stores the final outcome (`win | loss | draw | unknown`).
7. State changes and final decisions are persisted and published to websocket consumers.

## Scope of this iteration
- Replace the older "scenario graph / multi-step prompt chain" plan with a **state-machine style** design.
- Make admin CRUD for state fields and rules the primary product requirement.
- Keep the MVP focused on Counter-Strike match outcome tracking.

## Priority implementation target
Treat the following as the top-priority slice for immediate delivery:
- streamer added -> analysis job starts automatically;
- Streamlink captures/reads a fragment every 10 seconds;
- one active match session is tracked as one chat/session;
- each chunk updates a compact persisted state via LLM;
- admin can CRUD state fields, evidence fields, and rule sets used by the tracker;
- legacy detector/scenario-chain codepaths are removed or refactored so only one orchestration model remains;
- final outcome is derived from accumulated evidence only;
- decisions and state updates are persisted and published to websocket consumers.

## Product flow (high level)
1. User adds streamer via `POST /api/streamers`.
2. Worker scheduler starts analysis for the streamer immediately after onboarding.
3. The worker samples the stream every 10 seconds via Streamlink.
4. The system opens or resumes the active **match session** for that streamer/game.
5. For each chunk, the worker calls the LLM with:
   - `previous_state`,
   - `new_chunk`,
   - the active admin-managed update prompt,
   - the active admin-managed rule set.
6. The LLM returns `updated_state`, `delta`, and `next_needed_evidence` in strict JSON.
7. The backend persists the state snapshot, evidence log entries, and derived status.
8. When end-of-match evidence appears (or the stream/video ends), the worker calls the finalization prompt with the current state.
9. The backend persists the final decision and resets the streamer back to match discovery mode.

## Core modeling decision: one chat = one match
The LLM must behave as a **finite match state tracker**, not as a commentator.

### Required operating rules
- One chat/session contains exactly one match.
- Every update call must include `previous_state` explicitly.
- The LLM must always return valid JSON only.
- The model must not guess the outcome from gameplay quality or vague impressions.
- Outcome changes are allowed only when the state contains direct or strongly-supported evidence.
- If player team/side is not confirmed, or the ending is not visible, the result remains `unknown`.

## State model

### Canonical session state
```json
{
  "session_type": "single_match_single_chat",
  "game": "cs2",
  "mode": "competitive | faceit | wingman | unknown",
  "session_status": {
    "value": "in_progress | likely_finished | confirmed_finished | likely_truncated | unknown",
    "confidence": 0.0,
    "reason": null
  },
  "focus_player": {
    "name": null,
    "team_side": "CT | T | unknown",
    "team_label": "team_1 | team_2 | unknown",
    "confidence": 0.0
  },
  "score_state": {
    "ct_score": null,
    "t_score": null,
    "source": "hud | scoreboard | endscreen | inferred | unknown",
    "confidence": 0.0
  },
  "round_tracking": {
    "observed_round_wins_ct": 0,
    "observed_round_wins_t": 0,
    "observed_round_history": [],
    "confidence": 0.0
  },
  "winner_state": {
    "winner_side": "CT | T | draw | unknown",
    "winner_team_label": "team_1 | team_2 | unknown",
    "source": "final_banner | final_scoreboard | round_accumulation | unknown",
    "confidence": 0.0
  },
  "player_result": {
    "outcome": "win | loss | draw | unknown",
    "confidence": 0.0,
    "reason": null,
    "is_final": false
  },
  "terminal_evidence": {
    "final_banner_seen": false,
    "final_banner_text": null,
    "final_scoreboard_seen": false,
    "final_scoreboard_text": null,
    "post_match_ui_seen": false,
    "return_to_lobby_seen": false,
    "strong_terminal_signals": [],
    "weak_terminal_signals": []
  },
  "supporting_evidence": [],
  "open_uncertainties": [],
  "hard_conflicts": [],
  "next_needed_evidence": [
    "clear final screen",
    "clear final scoreboard",
    "clear player team confirmation"
  ]
}
```


### Practical runtime shape
```json
{
  "match_id": "chat_session_match_001",
  "game": "cs2",
  "mode": "competitive",
  "status": "in_progress",
  "player": {
    "name": "unknown",
    "team_side": "CT",
    "team_label": "team_1",
    "team_confidence": 0.82
  },
  "score": {
    "ct": 7,
    "t": 5,
    "source": "hud",
    "confidence": 0.88
  },
  "winner": {
    "side": "unknown",
    "team_label": "unknown",
    "source": "unknown",
    "confidence": 0.0
  },
  "player_outcome": {
    "value": "unknown",
    "confidence": 0.0
  },
  "evidence_log": [],
  "uncertainties": [
    "final result not visible yet"
  ],
  "hard_conflicts": []
}
```

## Update/close protocol

### Update step (`match_update`)
Each 10-second chunk produces one update request containing:
- `previous_state`
- `new_chunk.time_range`
- structured observations (`observations`, `visible_hud_text`, `scoreboard_text`, `round_result_signals`, `team_identity_signals`, `final_screen_signals`, `post_match_signals`, `truncation_signals`)
- active admin-managed rules and prompt version metadata

Expected response shape:
```json
{
  "updated_state": {},
  "delta": [
    "score updated from 7-5 to 8-5"
  ],
  "next_needed_evidence": [
    "clear final scoreboard"
  ]
}
```

### Session close step (`close_current_session`)
When no more chunks are currently available and the reason is unknown, backend triggers `close_current_session`.

Expected response shape:
```json
{
  "updated_state": {
    "session_status": {
      "value": "likely_truncated",
      "confidence": 0.86,
      "reason": "last chunk showed active gameplay without terminal UI"
    },
    "player_result": {
      "outcome": "unknown",
      "confidence": 0.0,
      "reason": "match ending not confirmed",
      "is_final": false
    }
  },
  "final_outcome": "unknown"
}
```

## Evidence-first decision rules
Strong terminal signals can set `session_status=confirmed_finished`:
1. explicit final banner / match end screen;
2. explicit final scoreboard;
3. explicit post-match UI / return to lobby after results;
4. repeated strong terminal indicators across chunks.

Weak signals can suggest `session_status=likely_finished`:
- long scoreboard without new gameplay,
- summary/MVP-like screens without explicit final banner,
- menu transition without explicit winner confirmation.

Truncation signals should prefer `session_status=likely_truncated`:
- last chunk shows active gameplay,
- no terminal UI and no post-match transition,
- data ends abruptly.

Additional hard rules:
- Never infer `win` because the player "looked stronger".
- If player side/team is not confirmed, keep `player_result.outcome=unknown`.
- `player_result.is_final=true` only when `session_status=confirmed_finished` and winner/player-team evidence is strong.
- Contradictions must be stored in `hard_conflicts`, not silently overwritten.

## Admin capabilities (backend requirements)
Admin scope changes in this design. The primary object is no longer a prompt-chain scenario, but a **state/rules configuration package**.

Admins must be able to:
- CRUD **state schemas** for supported games/modes.
- CRUD **state fields** and field metadata:
  - field key,
  - label/description,
  - enum constraints,
  - confidence requirements,
  - whether the field is evidence-bearing, inferred, or final-only.
- CRUD **update rules** that define how the LLM should treat new chunks.
- CRUD **finalization rules** that define how the final outcome is derived from accumulated state.
- CRUD **evidence categories** such as `team_identification`, `score_update`, `round_result`, `final_screen`, `scoreboard`, `side_switch`, `inference`.
- Configure active prompt templates for:
  - match update,
  - match finalization,
  - optional match-start detection.
- Configure runtime limits (`model`, `temperature`, `max_tokens`, `timeout_ms`, `retry_count`, `backoff_ms`).
- Activate/deactivate a versioned state/rules package.
- View audit trail for all schema/rule/prompt changes.

## Data model (draft)

### Match sessions
- `match_session_id`
- `streamer_id`
- `game_key`
- `status` (`discovering|in_progress|finished|interrupted|failed`)
- `started_at`, `finished_at`
- `state_schema_version_id`
- `update_prompt_version_id`
- `finalize_prompt_version_id`

### Match state snapshots
- `id`
- `match_session_id`
- `chunk_id`
- `state_json`
- `delta_json`
- `next_needed_evidence_json`
- `confidence_summary`
- `created_at`

### Match evidence log
- `id`
- `match_session_id`
- `chunk_id`
- `time_range`
- `kind`
- `text`
- `confidence`
- `source_type` (`observed|inferred`)
- `created_at`

### Match final decisions
- `id`
- `match_session_id`
- `final_outcome`
- `final_score_json`
- `player_team_json`
- `winner_team_json`
- `confidence`
- `evidence_json`
- `unresolved_issues_json`
- `created_at`

### Admin-managed tracker configs
- `state_schema_versions`
- `state_schema_fields`
- `tracker_rule_versions`
- `tracker_rule_items`
- `tracker_prompt_versions`
- `tracker_config_audit_log`

## API/WSS plan (MVP)

### Streamer / tracker read APIs
- `POST /api/streamers` — add streamer and auto-start analysis.
- `GET /api/streamers/:id/status` — current aggregated match-tracker status.
- `GET /api/streamers/:id/llm-decisions?limit=` — recent state-update/finalize history.
- `GET /api/streamers/:id/match-sessions` — recent match sessions with latest state summary.

### Admin CRUD APIs
- `GET /api/admin/llm/state-schemas`
- `POST /api/admin/llm/state-schemas`
- `PUT /api/admin/llm/state-schemas/:id`
- `POST /api/admin/llm/state-schemas/:id/activate`
- `GET /api/admin/llm/rule-sets`
- `POST /api/admin/llm/rule-sets`
- `PUT /api/admin/llm/rule-sets/:id`
- `POST /api/admin/llm/rule-sets/:id/activate`
- `GET /api/admin/llm/prompts`
- `POST /api/admin/llm/prompts`
- `POST /api/admin/llm/prompts/:id/activate`

### WebSocket events
- `LLM_MATCH_STATE_UPDATED` with payload:
  `{streamerId, matchSessionId, gameKey, status, stateSummary, confidence, ts}`
- `LLM_MATCH_FINALIZED` with payload:
  `{streamerId, matchSessionId, outcome, finalScore, confidence, ts}`

## Phased implementation

### Phase 1 — Legacy removal + admin CRUD for tracker configuration
- Delete or refactor legacy detector/scenario-chain runtime codepaths before enabling the tracker in production.
- Add DB-backed CRUD for state schemas, state fields, rule sets, and prompt versions.
- Add activation/versioning and audit logging.
- Remove in-memory-only scenario-chain configuration from the roadmap and services.

### Phase 2 — Worker state tracker loop
- Scheduler selects active streamers.
- Streamlink chunk fetch + storage reference.
- Match session discovery/open/resume.
- LLM update call with `previous_state + new_chunk`.
- Persist updated state/evidence/conflicts.

### Phase 3 — Finalization and delivery
- Detect terminal evidence / session end.
- LLM finalization call from accumulated state.
- Persist final decision and publish websocket notifications.
- Expose REST history and state backfill.

### Phase 4 — Reliability & observability
- Retries/backoff, DLQ, idempotency guards.
- Metrics for chunk lag, update latency, finalization latency, state conflicts, and unknown-rate.
- Alerting on drift in final-outcome confidence and conflict frequency.

## Risks and mitigations
- **Narrative drift**: force strict JSON-only responses and always provide `previous_state`.
- **Prompt sprawl**: version state schemas/rules separately from prompt text so admins can change logic without rewriting everything.
- **Conflicting evidence**: keep `hard_conflicts` instead of overwriting prior facts.
- **False certainty**: outcome remains `unknown` unless evidence passes admin-defined thresholds.

## Open questions before coding
1. Do we need a lightweight admin-managed match-start detector, or is manual/heuristic session opening enough for the first slice?
2. Which game modes beyond CS2 competitive/faceit should receive first-class state schemas in MVP?
3. Should admins be able to define rule ordering/priority explicitly per rule item?
4. Which fields must be editable in UI versus stored as advanced JSON config only?

## Execution backlog (next two iterations)

This backlog continues implementation according to `docs/implementation_plan.md` (M2.1)
and is ordered to ship a vertical slice before hardening.

### Iteration A — State tracker baseline

Goal: produce and persist match-session state snapshots for active streamers from real worker cycles.

#### A1. Legacy removal + admin CRUD + persistence
- [ ] Delete or refactor legacy detector/scenario-chain runtime codepaths and feature flags.
- [ ] Add DB model/repository for state schema versions, fields, rule sets, and prompt versions.
- [ ] Add activation + audit history for tracker configs.
- [ ] Remove references to deprecated scenario-chain storage from active implementation docs and services.

Definition of done:
- old prompt-chain runtime entrypoints are no longer reachable in normal execution;
- admin can create/update/activate a tracker config package;
- workers resolve active config from DB only.

#### A2. Worker update loop
- [ ] Introduce/update stream capture worker orchestration to:
  - acquire streamer lock,
  - fetch fragment via Streamlink every 10 seconds,
  - resolve active tracker config,
  - call the LLM with `previous_state + new_chunk`,
  - persist state snapshot and evidence log.
- [ ] Add match session repository and link state snapshots to chunk windows.
- [ ] Add normalized parser/validator for strict update JSON.

Definition of done:
- one worker pass creates or updates a match session state snapshot for a test streamer;
- duplicate cycle for the same lock window is rejected.

#### A3. Baseline telemetry
- [ ] Add metrics for chunk lag, update latency, finalize latency, unknown-rate, and conflict-rate.
- [ ] Add structured logs with `match_session_id`, `streamer_id`, `game_key`, and `chunk_window`.

Definition of done:
- metrics are visible in local `/metrics` output;
- failure logs are correlated by `match_session_id`.

### Iteration B — Final decision flow + reliability

Goal: complete M2.1 exit criteria with finalization, websocket updates, and resilient orchestration.

#### B1. Finalization flow
- [ ] Implement end-of-match detection / terminal-state trigger.
- [ ] Implement finalization prompt execution from accumulated state.
- [ ] Persist final outcome (`win | loss | draw | unknown`) with evidence bundle.

Definition of done:
- finalized matches produce durable outcome records and state summaries;
- unknown remains the default when final evidence is insufficient.

#### B2. Retry, idempotency, dead-letter
- [ ] Add retry policy with exponential backoff for Streamlink and LLM failures.
- [ ] Add idempotency keys (`streamer_id + match_session_id + chunk_window + request_kind`) with Redis TTL.
- [ ] Add DLQ payload format and reprocessing admin command.

Definition of done:
- transient failures are retried and eventually either succeed or move to DLQ;
- duplicate job delivery does not create duplicate state snapshots or final decisions.

#### B3. Realtime and session integration
- [ ] Publish `LLM_MATCH_STATE_UPDATED` and `LLM_MATCH_FINALIZED` from the worker path.
- [ ] Add reconnect backfill flow (`GET status` + `GET llm-decisions` + match session history).
- [ ] Integrate Redis refresh session store in auth login/refresh/logout endpoints.

Definition of done:
- websocket clients receive near-real-time tracker updates;
- refresh token replay is rejected after rotation;
- revoke-all immediately invalidates prior refresh sessions.

## Delivery checklist mapped to `docs/implementation_plan.md`

### M2.1 completion checklist
- [ ] Delete or refactor legacy detector/scenario-chain runtime codepaths.
- [ ] Implement DB-backed admin CRUD for state schemas, rules, and prompt versions.
- [ ] Implement stream capture worker pipeline with `previous_state + new_chunk -> updated_state` flow.
- [ ] Persist match sessions, state snapshots, evidence, and final outcomes.
- [ ] Publish live match-state/finalization updates via WebSocket.
- [ ] Add retries, idempotency, and dead-letter handling.
- [ ] Integrate refresh session store into auth flows.
- [ ] Add observability (latency, unknown-rate, conflict-rate, token usage).

### Next milestone preview (M3)
- [ ] Start `/internal/worker/events` ingestion only after the M2.1 tracker checklist is completed.
