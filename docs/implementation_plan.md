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
- [ ] Create `users` module (profile CRUD, referral code generation) with DB
  migrations. *(In-memory repository stubbed; persistent storage and migrations
  remain outstanding.)*
- [x] Seed configuration flags and expose `/api/me`, `/api/config`.
- [x] Deliver acceptance tests for valid/invalid initData flows.
- Exit Criteria: Mini App can authenticate, fetch own profile, and retrieve
  feature flags.

### M2 – Streamer Catalog & Games Skeleton
- [ ] Implement streamer onboarding (`POST /api/streamers`) with Twitch
  validation integration stub and rate limits.
- [ ] Expose streamer listings (`GET /api/streamers`) with pagination and
  moderation states.
- [ ] Create `games` module storing rules, statuses, and admin CRUD endpoints.
- [ ] Introduce admin role enforcement and basic UI scaffolds.
- Exit Criteria: admins can register streamers and configure games ready for
  live events.

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
