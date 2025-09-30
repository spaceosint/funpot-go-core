# Local Development Guide

## Prerequisites
- Go 1.22+
- PostgreSQL 15+
- Redis 7+
- `golang-migrate` CLI
- `direnv` (optional) for environment management

## Environment Variables
Create `.env.local` with:
```
APP_ENV=dev
HTTP_ADDR=:8080
WS_ADDR=:8080
DATABASE_URL=postgres://funpot:funpot@localhost:5432/funpot?sslmode=disable
REDIS_URL=redis://localhost:6379/0
TELEGRAM_BOT_TOKEN=<bot_token>
TELEGRAM_BOT_NAME=<bot_username>
JWT_SIGNING_KEY=<base64-encoded secret>
WORKER_SECRET=<shared_hmac_secret>
SENTRY_DSN=
PROMETHEUS_BIND=:9090
```
Document any new variable in this file.

## Database Setup
```bash
createdb funpot
psql funpot < migrations/0001_init.sql
```
Run migrations via:
```bash
migrate -path migrations -database "$DATABASE_URL" up
```

## Running the Server
```bash
go run ./cmd/funpot
```
The server binds to `HTTP_ADDR` for REST and shares the same port for WebSocket upgrades.

## Feature Flags & Config
Populate the `config` table with JSON values, e.g.:
```sql
INSERT INTO config(key, value_json) VALUES
  ('minViewers', '100'),
  ('starsRate', '{"XTR": 1.0, "INT": 10.0}'),
  ('features', '{"paymentsEnabled": true, "referralsEnabled": true, "mediaEnabled": true, "adminEnabled": true}'),
  ('limits', '{"votePerMin": 30, "streamerSubmitPer10m": 5, "invoicePer5m": 3, "withdrawPerHour": 2}');
```

## Telegram Webhook Setup (Dev)
Telegram webhooks require a public URL. Options:
1. Use [ngrok](https://ngrok.com/):
   ```bash
   ngrok http 8080
   ```
   Then register the webhook:
   ```bash
   curl -X POST https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/setWebhook \
     -d url="https://<ngrok-id>.ngrok.io/integrations/telegram/payments"
   ```
2. Deploy a lightweight tunnel (e.g., Cloudflare Tunnel) to your dev machine.

For local testing without webhook delivery, simulate callbacks:
```bash
curl -X POST http://localhost:8080/integrations/telegram/payments \
  -H "X-Telegram-Signature: <mock>" \
  -d '{"test":true,"invoice_payload":"demo"}'
```
Ensure your handler bypasses signature validation when `APP_ENV=dev` (or provide mock secret).

## Worker Integration Testing
- Configure the worker service URL in `WORKER_SECRET`.
- Use `curl` to send signed requests:
```bash
body='{"streamerId":"00000000-0000-0000-0000-000000000000","source":{"clipId":"clip-1","startedAt":"2024-01-01T00:00:00Z","durationSec":30,"llmModel":"gemini-2.5-flash-lite"},"events":[]}'
signature=$(echo -n "$body" | openssl dgst -sha256 -hmac "$WORKER_SECRET" -binary | base64)
curl -X POST http://localhost:8080/internal/worker/events \
  -H "X-Worker-Signature: $signature" \
  -H "X-Idempotency-Key: demo" \
  -d "$body"
```

## Testing
- Unit tests: `go test ./...`
- Lint: `golangci-lint run`
- Load smoke (optional): `k6 run load/scenarios/smoke.js`

## Observability
- Prometheus metrics exposed on `/metrics`.
- Enable tracing exporter via env `OTEL_EXPORTER_OTLP_ENDPOINT`.
- Sentry enabled when `SENTRY_DSN` is set.

## Admin Access
Assign admin role directly:
```sql
UPDATE users SET roles = array_append(roles, 'admin') WHERE tg_user_id = <id>;
```
Access admin routes with JWT containing `admin` role.

