# FunPot Modular Monolith Architecture

## Overview
The FunPot backend is a modular Go monolith that exposes REST and WebSocket APIs for the Telegram Mini App and coordinates with an external worker responsible for media processing and LLM-driven event generation. The service is horizontally scalable and stateless, relying on PostgreSQL and Redis for durable state and fast coordination.

## Non-Functional Goals & SLOs
- Support 100–200 concurrent streamers and 1,000–100,000 concurrent users.
- API latency SLO: p95 < 150 ms, p99 < 350 ms (excluding explicitly heavy queries).
- WebSocket event delivery: < 1 s ping for updates.
- Monthly availability: 99.9%.
- Stateless HTTP/WSS nodes with horizontal scaling behind an L7 load balancer.

## Module Boundaries
Each domain lives in a dedicated package under `internal/`:

| Module | Responsibilities |
| --- | --- |
| auth | Telegram initData verification, JWT issuance (5–10 min TTL), role checks, worker signature verification |
| users | User profile storage, nickname, language, Telegram user linkage |
| wallet | Double-entry ledger, balance snapshots, conversion from Telegram Stars |
| payments | Telegram Stars invoices, webhook handling, status lifecycle |
| referrals | Referral code issuance, invitation tracking, reward calculation |
| streamers | Twitch catalog, eligibility checks (>100 viewers), moderation, status lifecycle |
| games | Streamer game definitions, rules, state transitions |
| events | Event creation from worker payloads, lifecycle, prompt version tracking |
| votes | Vote ingestion, balance debits, totals aggregation, deduplication |
| media | Clip metadata ingestion, association with games/events |
| prompts | Prompt versioning (session/game/per-clip), rollout management |
| realtime | WebSocket gateway, Redis pub/sub fan-out, backpressure |
| admin | Admin CRUD for streamers/games/prompts, feature flags, manual replays |
| integrations | Telegram webhook, worker callbacks, Twitch viewer validator |
| config | Feature flags, rate limits, cached configuration delivery |

Cross-cutting packages such as logging, tracing, rate limiting, and configuration utilities live under `pkg/`.

## Data Stores & Infrastructure
- **PostgreSQL**: Primary system of record. Tables are documented in `docs/erd.md`. Transactions for financial/voting paths use `SERIALIZABLE` or `REPEATABLE READ` isolation.
- **Redis**: Caching (`/events/live`, configuration), pub/sub for realtime, idempotency key storage, rate limiting, vote tally counters. Redis Streams may be used for job fan-out.
- **Observability**: OpenTelemetry instrumentation exporting to Prometheus; Sentry captures application errors. Health endpoints `/healthz` (process) and `/readyz` (checks DB/Redis).

## Request Flow Summary
1. **Authentication**: Telegram WebApp `initData` is validated per request. On success, the backend returns a signed JWT (5–10 minutes TTL) used for REST/WSS authentication. Admin users rely on role claims.
2. **Realtime**: Clients connect to `/realtime` with the JWT. Subscriptions are organized per streamer/game/user. Fan-out uses Redis Pub/Sub to guarantee all nodes can broadcast state changes (EVENT_CREATED/UPDATED/CLOSED, BALANCE_UPDATED, SYSTEM_NOTICE).
3. **Worker Integration**: The external worker calls signed internal endpoints (`/internal/worker/*`). The backend validates HMAC signatures, enforces idempotency, persists events/media, and triggers realtime notifications.
4. **Financial Operations**: Idempotent ledger transactions enforce double-entry accounting for Stars top-ups, voting costs, rewards, withdrawals, and referral bonuses.

## Scaling & Performance
- **API Layer**: Stateless; multiple replicas behind an L7 LB. JWT storage uses stateless tokens; sessions are not stored in memory.
- **WebSocket Scaling**: Up to ~10k concurrent WS connections per node; scale horizontally. Backpressure ensures 2–4 updates/sec per subscription, batching totals where possible.
- **Caching**: `/api/events/live` cached for 0.5–1 s. Vote totals aggregated in Redis hash, snapshotted back to PostgreSQL every 1–3 s.
- **Partitioning Strategy**: Future v2 migration introduces partitioning for `wallet_ledger` and `votes` based on time/streamer once data volume grows.

## Background Processing
- Close events at `closes_at` via scheduled jobs, finalize results, emit `EVENT_CLOSED`.
- Snapshot Redis vote totals to PostgreSQL to ensure durability.
- Clean up idempotency keys (>24 h TTL) and expired WebSocket subscriptions.
- Twitch viewer validation runs server-side on streamer submission.

## Security & Compliance
- All external callbacks signed via HMAC-SHA256. Financial endpoints enforce `Idempotency-Key` header persisted to Redis and PostgreSQL.
- Rate limits enforced per user/streamer for vote submissions, streamer creation, and wallet operations.
- Minimize stored PII (Telegram user ID, optional nickname). Audit logs capture administrative and financial actions.

## Future Evolution
- Potential split of realtime subsystem into independent service when concurrent WS > 200k.
- Multi-level betting, cross-streamer battles, off-chain pools, cache invalidation service, richer anti-fraud analytics.

