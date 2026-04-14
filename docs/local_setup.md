# Local Setup

This document describes how to boot the FunPot Go core service locally. As of
Milestone M0 the service exposes health checks, baseline telemetry, and Sentry
instrumentation scaffolding.

## Prerequisites
- Go 1.22+
- Make sure ports `8080` (HTTP) are available on your machine.
- PostgreSQL 15+ (or compatible managed PostgreSQL).

## Environment Variables
Configuration is provided via environment variables. For local development you
can create a `.env` file in the repository root—variables declared there are
loaded automatically on startup.

```env
FUNPOT_ENV=development
FUNPOT_SERVER_ADDRESS=:8080
FUNPOT_SERVER_READ_TIMEOUT=5s
FUNPOT_SERVER_WRITE_TIMEOUT=10s
FUNPOT_SERVER_SHUTDOWN_TIMEOUT=15s
FUNPOT_LOG_LEVEL=info
FUNPOT_TELEMETRY_SERVICE_NAME=funpot-core
FUNPOT_TELEMETRY_METRICS_ENABLED=true
FUNPOT_SENTRY_DSN=
FUNPOT_SENTRY_ENVIRONMENT=development
FUNPOT_SENTRY_TRACES_SAMPLE_RATE=0.0
FUNPOT_SENTRY_DEBUG=false
FUNPOT_AUTH_TELEGRAM_BOT_TOKEN=<telegram_bot_token>
FUNPOT_AUTH_JWT_SECRET=dev-secret
FUNPOT_AUTH_JWT_TTL=15m
FUNPOT_AUTH_REFRESH_ENABLED=false
FUNPOT_AUTH_REFRESH_TTL=720h
FUNPOT_AUTH_REFRESH_MAX_SESSIONS=5
FUNPOT_AUTH_REFRESH_KEY_PREFIX=funpot:auth
FUNPOT_REDIS_ENABLED=false
FUNPOT_REDIS_ADDR=127.0.0.1:6379
FUNPOT_REDIS_PASSWORD=
FUNPOT_REDIS_DB=0
FUNPOT_REDIS_CONNECT_TIMEOUT=2s
FUNPOT_STREAMLINK_ENABLED=false
FUNPOT_STREAMLINK_BINARY=streamlink
FUNPOT_STREAMLINK_FFMPEG_BINARY=ffmpeg
FUNPOT_STREAMLINK_QUALITY=1080p60,1080p,720p60,720p,936p60,936p,648p60,648p,480p,best
FUNPOT_STREAMLINK_CAPTURE_TIMEOUT=30s
FUNPOT_STREAMLINK_OUTPUT_DIR=tmp/stream_chunks
FUNPOT_STREAMLINK_URL_TEMPLATE=https://twitch.tv/%s
FUNPOT_STREAMLINK_ARCHIVE_AGGREGATE_COUNT=5
FUNPOT_STREAMLINK_BUNNY_BASE_URL=https://video.bunnycdn.com
FUNPOT_STREAMLINK_BUNNY_LIBRARY_ID=
FUNPOT_STREAMLINK_BUNNY_API_KEY=
FUNPOT_GEMINI_API_KEY=<google_ai_studio_api_key>
FUNPOT_GEMINI_BASE_URL=https://generativelanguage.googleapis.com
FUNPOT_GEMINI_MAX_INLINE_BYTES=19922944
FUNPOT_GEMINI_CHAT_MAX_TOKENS=900000
FUNPOT_ADMIN_USER_IDS=<admin_user_uuid_1>,<admin_user_uuid_2>
FUNPOT_DATABASE_ENABLED=true
FUNPOT_DATABASE_HOST=localhost
FUNPOT_DATABASE_PORT=5432
FUNPOT_DATABASE_NAME=funpot
FUNPOT_DATABASE_USER=funpot
FUNPOT_DATABASE_PASSWORD=funpot
FUNPOT_DATABASE_SSLMODE=disable
FUNPOT_DATABASE_URL=
FUNPOT_RUN_MIGRATIONS_ON_STARTUP=true
FUNPOT_DATABASE_MAX_OPEN_CONNS=10
FUNPOT_DATABASE_MIN_OPEN_CONNS=1
FUNPOT_DATABASE_CONNECT_TIMEOUT=5s
FUNPOT_DATABASE_HEALTHCHECK_TIMEOUT=1s
FUNPOT_FEATURE_FLAGS=wallet=false,votes=false
FUNPOT_CLIENT_STARS_RATE=1
FUNPOT_CLIENT_MIN_VIEWERS=100
FUNPOT_CLIENT_CURRENCIES=INT
FUNPOT_CLIENT_LIMIT_VOTE_PER_MIN=30
FUNPOT_DATABASE_MAX_OPEN_CONNS=10
FUNPOT_DATABASE_MAX_IDLE_CONNS=5
FUNPOT_DATABASE_CONN_MAX_IDLE_TIME=5m
FUNPOT_DATABASE_CONN_MAX_LIFETIME=30m
```

> `FUNPOT_AUTH_REFRESH_ENABLED=true` requires `FUNPOT_REDIS_ENABLED=true`
> because refresh sessions are stored in Redis.

> `FUNPOT_STREAMLINK_ENABLED=true` requires the `streamlink` binary to be
> available in PATH (or pointed to by `FUNPOT_STREAMLINK_BINARY`).

> Live Gemini analysis expects `.mp4` chunks, so
> `FUNPOT_STREAMLINK_FFMPEG_BINARY` must point to a working `ffmpeg` binary
> when Streamlink capture is enabled.

> Scheduler cycle interval is aligned with `FUNPOT_STREAMLINK_CAPTURE_TIMEOUT`
> (for example, `30s` timeout means one capture/LLM cycle every ~30 seconds)
> and automatically starts the next cycle without an extra idle pause when
> the previous capture overruns the window.
>
> Stream capture now runs as a long-lived Streamlink→FFmpeg pipeline per streamer
> and cuts sequential ~30s segments continuously (`%09d.mp4`) to minimize boundary
> loss between chunks.
>
> Each ~30s chunk is analyzed immediately by the worker.
> In parallel, chunks are accumulated and merged via `ffmpeg -c copy` (no re-encoding)
> into ~2-minute windows (`FUNPOT_STREAMLINK_ARCHIVE_AGGREGATE_COUNT` controls batch size),
> then uploaded to Bunny Stream when Bunny credentials are configured.

> Set `FUNPOT_GEMINI_API_KEY` to enable real Gemini stage classification. When
> it is unset, the server falls back to the deterministic placeholder
> classifier used in early development.
>
> `FUNPOT_GEMINI_CHAT_MAX_TOKENS` controls how long the backend keeps one
> Gemini chat session alive per streamer before rotating to a new chat and
> re-sending the tracker prompt + latest state bootstrap.
>
> Tracker runtime is now Scenario Package v2-first: each cycle resolves the
> active scenario step and sends chunk metadata/media with strict-JSON contract
> expectations derived from that step.
>
> Legacy Gemini response coercion is disabled: every stage must return
> strict JSON that matches the active step `responseSchemaJson` exactly.
> Runtime no longer expects hardcoded tracker fields (`updated_state`, `delta`,
> `next_needed_evidence`, `hard_conflicts`, `final_outcome`) as a built-in
> contract. The full LLM JSON payload is treated as scenario-owned state data.
> Extra keys outside the scenario schema are rejected by runtime.

Update this table whenever you introduce a new configuration surface.

### Database

Milestone M1 introduces PostgreSQL persistence for the `users` module. For
local development you can run Postgres via Docker:

```bash
docker run --rm -p 5432:5432 \
  -e POSTGRES_DB=funpot_core \
  -e POSTGRES_USER=funpot \
  -e POSTGRES_PASSWORD=funpot \
  postgres:16
```

Once the container is running, export database fields from the config example above (or update `.env`). Build a DSN for tools like `migrate` and apply database migrations before starting the server:

```bash
go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
  -path migrations \
  -database "postgres://${FUNPOT_DATABASE_USER}:${FUNPOT_DATABASE_PASSWORD}@${FUNPOT_DATABASE_HOST}:${FUNPOT_DATABASE_PORT}/${FUNPOT_DATABASE_NAME}?sslmode=${FUNPOT_DATABASE_SSLMODE}" up
```

## Running the Server
If you plan to use refresh sessions, run Redis locally (example with Docker):

```bash
docker run --name funpot-redis -p 6379:6379 -d redis:7
```

Run PostgreSQL locally (example with Docker):

```bash
docker run --name funpot-postgres \
  -e POSTGRES_USER=funpot \
  -e POSTGRES_PASSWORD=funpot \
  -e POSTGRES_DB=funpot \
  -p 5432:5432 \
  -d postgres:16
```

Apply migrations before starting the API:

```bash
migrate -path ./migrations -database "postgres://${FUNPOT_DATABASE_USER}:${FUNPOT_DATABASE_PASSWORD}@${FUNPOT_DATABASE_HOST}:${FUNPOT_DATABASE_PORT}/${FUNPOT_DATABASE_NAME}?sslmode=${FUNPOT_DATABASE_SSLMODE}" up
```

Then start the service:

```bash
go run ./cmd/server
```

Alternatively, you can build and run the container image defined in the
repository `Dockerfile`:

```bash
docker build -t funpot-core:dev .
docker run --rm -p 8080:8080 --env-file .env funpot-core:dev
```

Container startup now applies migrations automatically (via `migrate up`) when database configuration is present. Set `FUNPOT_RUN_MIGRATIONS_ON_STARTUP=false` to disable this behavior for one-off debug runs.

On startup the server listens on `FUNPOT_SERVER_ADDRESS` and provides:
- `GET /healthz` – liveness probe returning the current timestamp.
- `GET /readyz` – readiness probe (`ready` by default; DB connectivity check when PostgreSQL mode is enabled).
- `GET /metrics` – Prometheus metrics when enabled, `204 No Content` otherwise.
- `POST /api/auth/telegram` – verifies Telegram Mini App `initData` and returns JWT + refresh pair when refresh sessions are enabled.
- `POST /api/auth/refresh` – rotates refresh session and issues a new JWT + refresh token pair.
- `POST /api/auth/logout` – revokes a single refresh session using refresh token.
- `POST /api/auth/logout-all` – revokes all refresh sessions for authenticated user.
- `GET /api/me` – returns the authenticated user's profile plus `isAdmin` flag when called with the issued JWT.
- `GET /api/config` – exposes client configuration and feature flags for the authenticated user.
- `GET /api/streamers` – returns streamer catalog with optional `query` and `page` filters.
- `POST /api/streamers` – submits a Twitch streamer nickname for moderation/validation, then immediately starts the per-streamer Streamlink analysis scheduler when background orchestration is configured.
- When Streamlink reports that a Twitch URL has no playable streams (for example, the stream ended or is offline), the scheduler treats that cycle as a graceful skip instead of a hard worker failure and retries on the next 10-second window.
- `GET /api/streamers/{streamerId}/status` – returns the latest aggregated LLM match-session / state-tracker status for a streamer, including full chronological LLM call history (`history`) with request/response payloads per decision.
- `DELETE /api/streamers/{streamerId}/tracking` – stops the active Streamlink/LLM tracking loop for a streamer and returns the updated `stopped` status so the client can disable the tracking button immediately.
- `GET /api/admin/streamers/{streamerId}/llm-history?page=1&pageSize=20` – admin timeline endpoint with paginated LLM decision history (step name, LLM response, global state delta, event timestamps) plus uploaded Bunny video metadata for the streamer.
- `DELETE /api/admin/streamers/{streamerId}/llm-history` – admin cleanup endpoint that deletes persisted LLM decision history and removes tracked Bunny videos for the streamer.
- `GET /api/events/live` – returns live events for a required `streamerId` query parameter.
- `GET /api/admin/games` – admin-only endpoint listing all configured games.
- `POST /api/admin/games` – admin-only endpoint creating a game definition.
- `PUT /api/admin/games/{gameId}` – admin-only endpoint updating a game definition.
- `DELETE /api/admin/games/{gameId}` – admin-only endpoint deleting a game definition.
- `GET /api/admin/llm/model-configs` / `POST /api/admin/llm/model-configs` – admin CRUD entrypoints for reusable LLM model configs (model + execution params + free-form metadata JSON).
- `PUT /api/admin/llm/model-configs/{id}` / `DELETE /api/admin/llm/model-configs/{id}` / `POST /api/admin/llm/model-configs/{id}/activate` – update, remove, and switch active model configuration.
- `GET /api/admin/llm/scenario-packages` / `POST /api/admin/llm/scenario-packages` – admin CRUD for scenario graph packages with per-game versioning and activation. Admin can provide explicit graph `transitions` (`fromStepId`, `toStepId`, `condition`, `priority`) for branch routing and optional `packageTransitions` (`toPackageId`, `condition`, `priority`) for package-to-package routing. If step `transitions` are omitted backend auto-links steps linearly by `order` (`step_i -> step_(i+1)` uses target `entryCondition`); when the initial step has its own `entryCondition` backend also adds fallback transitions from non-initial steps back to initial using that condition.
- Scenario steps support per-step tuning: `segmentSeconds` (default `15` for `initial=true`, otherwise `30`) and `maxRequests` (default `0` = unlimited). When a non-initial step exceeds `maxRequests`, runtime returns to initial step; when initial exceeds its own limit, streamer tracking stops.
- `GET /api/admin/llm/scenario-packages/{id}/graph` – returns a UI-ready visual graph payload (`nodes + edges + groups`) for scenario-graph editors/renderers.
- Legacy prompt-version/state-schema/rule-set admin surfaces are removed from runtime; scenario-packages + model-configs are the supported LLM control surfaces.
- Scenario-packages are persisted in PostgreSQL table `llm_scenario_packages` when DB is configured; when DB config is missing the service falls back to in-memory storage.

When database connection fields are unset the server falls back to the in-memory
repository for user profiles. This is useful for quick smoke tests but bypasses
database persistence; prefer configuring PostgreSQL locally to exercise the
full stack.

Logs are emitted in JSON format using `zap`. Telemetry spans are exported to
stdout through the OpenTelemetry SDK, and Sentry is initialized when a DSN is
provided. When `FUNPOT_DATABASE_ENABLED=true`, startup validates PostgreSQL
connectivity and `/readyz` depends on successful DB ping checks. When
`FUNPOT_REDIS_ENABLED=true`, startup validates Redis connectivity and includes
Redis ping in `/readyz` checks.

When `FUNPOT_AUTH_REFRESH_ENABLED=true`, the service configures refresh-session
storage automatically. Set `FUNPOT_REDIS_ENABLED=true` to use Redis-backed
session revocation/rotation, or keep it `false` to use in-memory sessions for
local smoke tests.

## Observability Notes
- Disable Prometheus scraping locally by setting `FUNPOT_TELEMETRY_METRICS_ENABLED=false`.
- Adjust the log level (`debug`, `info`, `warn`, `error`) via `FUNPOT_LOG_LEVEL`.
- Stream-analysis metrics now expose worker health signals on `/metrics`, including
  `funpot_stream_chunk_lag_seconds`, `funpot_stream_update_latency_ms`,
  `funpot_stream_finalize_latency_ms`, `funpot_stream_state_conflicts_total`,
  `funpot_stream_unknown_outcomes_total`, and `funpot_stream_streamer_failures_total`
  for M2.1 tracker monitoring.
- When Sentry is enabled, the shutdown process flushes pending events with a
  2-second timeout.
- Telegram authentication requires a bot token; for local development you can
  use a sandbox bot token from BotFather. The JWT secret defaults to
  `dev-secret` but should be overridden in non-development environments.

Set `FUNPOT_DATABASE_ENABLED=false` to run with in-memory users persistence for
quick smoke testing.

## Continuous Delivery
The repository ships with an automated CD workflow defined in
`.gitea/workflows/cd.yml`. The pipeline listens for successful runs of the
"FunPot Core CI" workflow on pushes to `dev` and `main`, and performs the
following for each environment:

1. Checks out the commit that produced the passing CI build.
2. Resolves the container image pushed by the CI pipeline using the configured
   registry secrets.
3. Pulls the image to verify that it is available to downstream infrastructure.
4. Calls an HTTP webhook to trigger the deployment in the corresponding
   environment without relying on SSH access.
5. Polls the environment-specific healthcheck URL to confirm that the
   application is serving `/readyz` successfully before marking the job as
   finished.

### Required secrets
Configure the following repository secrets before enabling the workflow:

- `DEV_DEPLOY_WEBHOOK_URL` – HTTPS endpoint that accepts a POST request to
  deploy the dev environment.
- `PROD_DEPLOY_WEBHOOK_URL` – HTTPS endpoint for the production deployment.
- `DEV_DEPLOY_HEALTHCHECK_URL` – HTTPS address (e.g. `https://dev.funpot.live/readyz`)
  that returns `200` once the dev environment is ready. Used to confirm the
  deployment booted correctly.
- `PROD_DEPLOY_HEALTHCHECK_URL` – Optional HTTPS address for the production
  readiness probe. Leave blank to skip the post-deploy health poll.

Each webhook receives a JSON payload with the target environment label and Git
SHA. Use those fields to orchestrate the rollout or kick off your own build
process on the destination host.
