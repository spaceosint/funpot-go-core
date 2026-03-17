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

## Priority Directive (current focus for agents)
The highest-priority delivery for the next iteration is enabling continuous
stream analysis immediately after a streamer is added:

- Trigger background orchestration when a streamer is created/activated.
- Capture stream fragments every **10 seconds** via Streamlink.
- For each fragment, call LLM with the **active admin-managed prompt**
  (stage-specific, versioned template).
- Persist run/stage outputs and broadcast state updates to clients.

### Priority checklist (must be tracked in status updates)
- [ ] Auto-start Streamlink analysis job after `POST /api/streamers` success.
- [ ] Fixed 10-second capture cadence with lock/idempotency protections.
- [ ] Prompt resolution from admin configuration (`active` prompt version per stage).
- [ ] Worker payload includes prompt text + runtime params (model, temperature, token limits).
- [ ] Persist chunk metadata, LLM request/response refs, normalized stage decision, confidence.
- [ ] Publish realtime `LLM_STAGE_UPDATED` events and provide REST backfill/history.
- [ ] Add retry/backoff + DLQ behavior for Streamlink and LLM failures.
- [ ] Add observability for chunk lag, stage latency, and per-streamer failure rate.

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

### M2.1 – LLM Stream Orchestration (Gemini) for Streamers
- [x] Deliver admin panel backend contracts for managing LLM request templates,
  stage transitions, and safety limits (temperature, max tokens, timeout,
  fallback strategy).
- [ ] Implement stream capture worker pipeline:
  `streamlink -> media chunking -> Gemini request -> normalized stage result`.
- [ ] Build staged game flow for Counter-Strike:
  Stage A detects stream context (is streamer playing CS or not),
  Stage B classifies match type (competitive / faceit / other),
  Stage C tracks live match state (pre-game / in-progress / finished),
  Stage D determines result (win / loss / unknown).
- [ ] Add resilient orchestration with retries, idempotency keys, and dead-letter
  handling for failed LLM jobs.
- [ ] Publish live LLM status updates to clients via WebSocket channel.
- [x] Provide REST history endpoint for latest LLM stage decisions.
- [x] Introduce Redis-backed refresh session store for admin/user session
  revocation, rotation, and concurrent session controls.
- [x] Integrate refresh session store into auth refresh/login/logout flows
  (token pair issuance, rotation endpoint, and revoke-all/user-device controls).
- [ ] Add observability: per-stage latency, success ratio, token usage, and
  drift alerts for prompt regressions.
- Exit Criteria: admin can tune prompts per stage, worker pipeline produces
  stage results for active streamers, and users observe near-real-time status
  updates on streamer pages.

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
