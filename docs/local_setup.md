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
FUNPOT_DATABASE_ENABLED=true
FUNPOT_DATABASE_HOST=localhost
FUNPOT_DATABASE_PORT=5432
FUNPOT_DATABASE_NAME=funpot
FUNPOT_DATABASE_USER=funpot
FUNPOT_DATABASE_PASSWORD=funpot
FUNPOT_DATABASE_SSLMODE=disable
FUNPOT_DATABASE_MAX_OPEN_CONNS=10
FUNPOT_DATABASE_MIN_OPEN_CONNS=1
FUNPOT_DATABASE_CONNECT_TIMEOUT=5s
FUNPOT_DATABASE_HEALTHCHECK_TIMEOUT=1s
FUNPOT_FEATURE_FLAGS=wallet=false,votes=false
FUNPOT_DATABASE_MAX_OPEN_CONNS=10
FUNPOT_DATABASE_MAX_IDLE_CONNS=5
FUNPOT_DATABASE_CONN_MAX_IDLE_TIME=5m
FUNPOT_DATABASE_CONN_MAX_LIFETIME=30m
```

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

On startup the server listens on `FUNPOT_SERVER_ADDRESS` and provides:
- `GET /healthz` – liveness probe returning the current timestamp.
- `GET /readyz` – readiness probe (`ready` by default; DB connectivity check when PostgreSQL mode is enabled).
- `GET /metrics` – Prometheus metrics when enabled, `204 No Content` otherwise.
- `POST /api/auth/telegram` – verifies Telegram Mini App `initData` and returns a short-lived JWT.
- `GET /api/me` – returns the authenticated user's profile when called with the issued JWT.
- `GET /api/config` – exposes seeded feature flags for the authenticated user.

When database connection fields are unset the server falls back to the in-memory
repository for user profiles. This is useful for quick smoke tests but bypasses
database persistence; prefer configuring PostgreSQL locally to exercise the
full stack.

Logs are emitted in JSON format using `zap`. Telemetry spans are exported to
stdout through the OpenTelemetry SDK, and Sentry is initialized when a DSN is
provided. When `FUNPOT_DATABASE_ENABLED=true`, startup validates PostgreSQL
connectivity and `/readyz` depends on successful DB ping checks.

## Observability Notes
- Disable Prometheus scraping locally by setting `FUNPOT_TELEMETRY_METRICS_ENABLED=false`.
- Adjust the log level (`debug`, `info`, `warn`, `error`) via `FUNPOT_LOG_LEVEL`.
- When Sentry is enabled, the shutdown process flushes pending events with a
  2-second timeout.
- Telegram authentication requires a bot token; for local development you can
  use a sandbox bot token from BotFather. The JWT secret defaults to
  `dev-secret` but should be overridden in non-development environments.

Set `FUNPOT_DATABASE_ENABLED=false` to run with in-memory users persistence for
quick smoke testing.

## Deployment (Watchtower)
Deployment is handled outside this repository by Watchtower. The CI pipeline
builds and publishes images; Watchtower is responsible for pulling updated
rolling tags and restarting containers.

Recommended tag mapping:

- `dev` branch → `<REGISTRY_URL>/<REGISTRY_REPOSITORY>:dev`
- `main` branch → `<REGISTRY_URL>/<REGISTRY_REPOSITORY>:prod`

### Migration checks in CI
Database migration validation now runs in `FunPot Core CI`:

- `dev` branch uses `DEV_DATABASE_URL` + `DEV_MIGRATIONS_MODE`
- `main` branch uses `PROD_DATABASE_URL` + `PROD_MIGRATIONS_MODE`

Mode behavior:

- `check` (default): execute migration metadata preflight only.
- `apply`: execute preflight and then `migrate up`.

Set these repository secrets before enabling branch deployment automation:

- `DEV_DATABASE_URL`
- `PROD_DATABASE_URL`
- `DEV_MIGRATIONS_MODE` (`check` or `apply`)
- `PROD_MIGRATIONS_MODE` (`check` or `apply`)
