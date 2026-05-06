# Implementation Plan

This document enumerates the end-to-end plan for delivering the FunPot modular
monolith. The plan is structured into milestone phases with clear deliverables,
approximate sequencing, and validation criteria.

## Guiding Principles
- **Iterative vertical slices**: each milestone ships a usable subset of
  functionality across HTTP, WebSocket, persistence, and background jobs.
- **Strong domain boundaries**: modules remain isolated behind interfaces;
  cross-module calls go through service contracts defined in `internal/*`.
- **Operational readiness first**: tracing, metrics, and idempotency are enabled
  before public exposure of new routes.

## Priority Directive (deprecated)
The old "Priority Directive / Priority checklist" for stream analysis has been
deprecated. Agents and contributors must use
`docs/llm_stream_orchestration_plan.md` as the canonical source for current
business logic and immediate backend scope.

## Milestones

### M0 – Foundation & Tooling
- [x] Initialize Go module, dependency management, and project layout under
  `internal/` and `pkg/` according to `AGENTS.md`.
- [x] Implement configuration loader with env overrides and local `.env`
  support, updating `docs/local_setup.md` accordingly.
- [x] Wire logging, metrics (OpenTelemetry/Prometheus), and Sentry stubs.
- [x] Provide health endpoints (`/healthz`, `/readyz`) and CI sanity pipeline.
- Exit Criteria: containerized service boots, responds to health checks, and
  emits baseline telemetry.

### M1 – Authentication & User Profiles
- [x] Verify Telegram `initData`, issue short-lived JWTs, and implement auth
  middleware shared by REST/WSS.
- [x] Create `users` module (profile CRUD, referral code generation) with DB
  migrations.
- [x] Seed configuration flags and expose `/api/me`, `/api/config`.
- [x] Deliver acceptance tests for valid/invalid initData flows.
- Exit Criteria: Mini App can authenticate, fetch own profile, and retrieve
  feature flags.

### M2 – Streamer Catalog & Games Skeleton
- [x] Implement streamer onboarding (`POST /api/streamers`) with Twitch
  validation integration stub and rate limits.
- [x] Expose streamer listings (`GET /api/streamers`) with pagination and
  moderation states.
- [x] Create `games` module storing rules, statuses, and admin CRUD endpoints.
- [x] Introduce admin role enforcement and basic UI scaffolds.
- Exit Criteria: authenticated users can register streamers, while admins can
  configure games ready for live events.

### M2.1 – LLM Stream Orchestration (State Tracker) for Streamers
- [x] Delete or refactor legacy detector/scenario-chain codepaths so only the new tracker model remains active.
- [x] Narrow admin panel backend contracts to Scenario Package v2 only
  (`/api/admin/llm/scenario-packages` + activation + graph).
- [x] Remove legacy admin surfaces for prompt versions, state schemas, rule sets, and model-config CRUD across code + docs.
- [ ] Re-introduce scenario-package persistence in storage (PostgreSQL) after the scenario-only cleanup baseline stabilizes.
- [x] Implement stream capture worker pipeline:
  `streamlink -> media chunking -> previous_state + new_chunk -> updated_state`,
  including step-level `segmentSeconds` assembly from contiguous source segments
  so no seconds are skipped between LLM chunks, plus cleanup of consumed source
  segments, assembled local videos, and stale artifacts from interrupted sessions.
- [ ] Implement match-session lifecycle so one detected match is tracked as one
  chat/session with explicit persisted state JSON.
- [ ] Ship the initial Counter-Strike tracker flow:
  match discovery/opening;
  iterative state updates for player side, score, evidence, and uncertainties;
  finalization into `win | loss | draw | unknown` only from accumulated
  evidence.
- [x] Start migration to scenario-graph orchestration (root game-detection step + game-folder steps + concrete game sub-steps) with condition-based transitions and stay-on-step fallback.
- [ ] Implement Game Scenario graph orchestration over scenario-packages (node = package, edge = transition), including terminal finish conditions and first-step guard validation on package transitions.
- [ ] Add resilient orchestration with retries, idempotency keys, and dead-letter
  handling for failed LLM jobs.
- [ ] Publish live match-state/finalization updates to clients via WebSocket.
- [ ] Provide REST history endpoints for latest LLM state updates and final decisions.
- [x] Introduce Redis-backed refresh session store for admin/user session
  revocation, rotation, and concurrent session controls.
- [x] Integrate refresh session store into auth refresh/login/logout flows
  (token pair issuance, rotation endpoint, and revoke-all/user-device controls).
- [ ] Add observability: update/finalization latency, success ratio, token usage, conflict/unknown rates, and drift alerts for prompt regressions.
- Exit Criteria: admin can tune active scenario-packages only, the worker pipeline produces persisted match state updates/final decisions for active streamers, and users observe near-real-time status updates on streamer pages.

### M3 – Events Lifecycle & Realtime Delivery
- [ ] Implement `/internal/worker/events` ingestion with validation, dedupe, and
  storage in `events` table.
- [ ] Broadcast `EVENT_CREATED/UPDATED/CLOSED` via WebSocket hub using Redis
  pub/sub for fan-out.
- [ ] Provide `/api/events/live` cache with Redis TTL and backfill logic when
  Redis unavailable.
- [ ] Build background scheduler that closes events on `closesAt` and snapshots
  Redis totals into PostgreSQL.
- Exit Criteria: workers can push events, clients observe them in REST/WSS, and
  events close consistently.

### M4 – Wallet, Payments & Votes
- [ ] Implement double-entry ledger (`wallet` module) with balance projection
  and idempotent postings.
- [ ] Integrate Telegram Stars invoices (`payments` module) and webhook handler
  with signature verification.
- [ ] Expose `/api/wallet`, `/api/payments/stars/createInvoice`, and apply
  referral bonuses on successful credits.
- [ ] Build voting flow (`POST /api/votes`) with cost validation, Redis totals,
  and rate limiting.
- Exit Criteria: users can top up, participate in paid votes, and balances stay
  consistent under retries.

### M5 – Media & Prompts
- [ ] Persist media clips from `/internal/worker/media` and expose
  `/api/media/clips`.
- [ ] Implement prompt management (`prompts` module) with versioning,
  activation, and admin workflows.
- [ ] Ensure ingested events capture prompt versions for auditability.
- Exit Criteria: media artifacts link to events, prompt versions managed via
  admin surface, and worker payloads stay traceable.

### M6 – Referrals, Withdrawals & Advanced Admin
- [ ] Finalize referral reporting endpoints and payout history tracking.
- [ ] Implement withdrawal requests and admin approval loop.
- [ ] Expand admin module with moderation tools, feature flag toggles, and
  manual recalculation jobs.
- Exit Criteria: monetization flows cover deposits, bonuses, and withdrawals
  with full audit logging.

### M7 – Hardening & Scale
- [ ] Add k6/Vegeta load scenarios defined in `docs/load_testing.md` to CI gate.
- [ ] Introduce Redis and PostgreSQL resilience measures (retry, circuit
  breaker, fallback strategies).
- [ ] Execute chaos drills (Redis down, worker burst, WSS degradation) and
  document remediation steps.
- Exit Criteria: service meets SLO under projected load, observability alerts
  configured, runbooks updated.

### Operational Automation
- [x] Ship CD workflow that publishes per-branch images and triggers webhook-based
  deploys for `dev` and `main` environments.
- [x] Add automated post-deployment smoke tests for webhook-driven rollouts.

## Cross-Cutting Workstreams
- Security reviews, secrets rotation, and compliance updates each milestone.
- Database migrations versioned and peer-reviewed.
- Documentation refresh: update OpenAPI, WebSocket schemas, and guides when
  modules change behavior.

## Progress Tracking Directive
For every iteration or PR touching this codebase, contributors **must** copy the
milestone checklist relevant to their scope into the work log or final response
and mark items as `[x]` (completed) or `[ ]` (outstanding). This satisfies the
"model must note what is done vs not" requirement and keeps stakeholders aware
of partial deliveries.

## Out-of-Scope / Future Enhancements
- Multi-level betting, streamer battles, pooled jackpots.
- Dedicated WSS microservice for >200k concurrent connections.
- Advanced fraud detection and machine learning scoring pipelines.
- Multi-currency wallet and blockchain settlement bridges.

## Dependencies & Ordering Notes
- Streamer validation requires Twitch credentials—secure them before M2.
- Payments depend on Telegram Stars sandbox credentials and callback exposure.
- Redis availability is critical from M3 onward; stage cluster provisioning
  during M0.
- Feature flag toggles should wrap every externally visible capability to allow
  safe rollout per environment.

## Acceptance Metrics
Each milestone inherits global SLOs (p95 < 150 ms, p99 < 350 ms) and defines
specific functional tests (see `docs/risk_matrix_and_checklist.md`). Use the
status reporting directive to capture pass/fail per test suite.
