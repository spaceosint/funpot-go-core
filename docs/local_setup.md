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
