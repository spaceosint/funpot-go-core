# Local Setup

This document describes how to boot the FunPot Go core service locally. As of
Milestone M0 the service exposes health checks, baseline telemetry, and Sentry
instrumentation scaffolding.

## Prerequisites
- Go 1.22+
- Make sure ports `8080` (HTTP) are available on your machine.

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
FUNPOT_FEATURE_FLAGS=wallet=false,votes=false
```

Update this table whenever you introduce a new configuration surface.

## Running the Server
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
- `GET /readyz` – readiness probe (currently always ready).
- `GET /metrics` – Prometheus metrics when enabled, `204 No Content` otherwise.
- `POST /api/auth/telegram` – verifies Telegram Mini App `initData` and returns a short-lived JWT.
- `GET /api/me` – returns the authenticated user's profile when called with the issued JWT.
- `GET /api/config` – exposes seeded feature flags for the authenticated user.

Logs are emitted in JSON format using `zap`. Telemetry spans are exported to
stdout through the OpenTelemetry SDK, and Sentry is initialized when a DSN is
provided.

## Observability Notes
- Disable Prometheus scraping locally by setting `FUNPOT_TELEMETRY_METRICS_ENABLED=false`.
- Adjust the log level (`debug`, `info`, `warn`, `error`) via `FUNPOT_LOG_LEVEL`.
- When Sentry is enabled, the shutdown process flushes pending events with a
  2-second timeout.
- Telegram authentication requires a bot token; for local development you can
  use a sandbox bot token from BotFather. The JWT secret defaults to
  `dev-secret` but should be overridden in non-development environments.

As subsequent milestones introduce persistence, authentication, and domain
modules, extend this guide with database and queue dependencies.

## Continuous Delivery
The repository ships with an automated CD workflow defined in
`.gitea/workflows/cd.yml`. The pipeline runs on pushes to `dev` and `main`, as
well as manual `workflow_dispatch` invocations, and performs the following:

1. Determines which environment corresponds to the branch (`dev` → development,
   `main` → production).
2. Sends an HTTPS POST request to the configured deployment webhook with the
   target environment, branch name, commit SHA, and repository identifier.

The webhook consumer is responsible for building and rolling out the
application (for example, by pulling the latest sources and running a local
Docker build). This keeps the workflow registry-free for teams that prefer to
build images on the deployment host.

### Required secrets
Configure the following repository secrets before enabling the workflow:

- `DEV_DEPLOY_WEBHOOK_URL` – HTTPS endpoint that accepts a POST request to
  deploy the dev environment.
- `PROD_DEPLOY_WEBHOOK_URL` – HTTPS endpoint for the production deployment.

Each webhook receives a JSON payload with the keys `environment`, `branch`,
`git_sha`, and `repository`. Use those fields to orchestrate the rollout on your
hosting platform of choice.
