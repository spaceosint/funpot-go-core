# Idempotency & Rate Limit Policies

## Idempotency Requirements
| Endpoint / Operation | Idempotency Key Source | Storage | TTL | Notes |
| --- | --- | --- | --- | --- |
| `POST /api/votes` | `Idempotency-Key` header | Redis + `votes.idempotency_key` column | 24h | Prevent duplicate votes and ledger debits. Cache final response for retries. |
| `POST /api/payments/stars/createInvoice` | `Idempotency-Key` (optional, generated server-side) | Redis | 1h | Ensures duplicate invoice creation requests reuse existing invoice. |
| `POST /api/wallet/withdraw` | `Idempotency-Key` header | Redis + `wallet_ledger.idempotency_key` | 24h | Guarantees single withdrawal record. |
| `POST /internal/worker/events` | `X-Idempotency-Key` header | Redis + `events` uniqueness `(streamer_id, external_id)` | 24h | Deduplicate worker batches. |
| `POST /internal/worker/media` | `X-Idempotency-Key` header | Redis + `media_clips.id` | 24h | Avoid duplicate clip records. |
| `POST /internal/worker/streamer-status` | `X-Idempotency-Key` header | Redis | 5m | Prevent rapid duplicate status updates from causing churn. |
| `POST /integrations/telegram/payments` | Telegram payload `invoice_payload` | Redis + `payments.invoice_id` | 24h | Webhook replay safe; ledger credit executed once. |

## Rate Limits (Redis Tokens)
| Scope | Endpoint | Limit | Window | Configuration Key |
| --- | --- | --- | --- | --- |
| Per user per streamer | `POST /api/votes` | 30 requests | 60s rolling | `limits.votePerMin` |
| Per user | `POST /api/streamers` | 5 requests | 10m | `limits.streamerSubmitPer10m` |
| Per user | `POST /api/payments/stars/createInvoice` | 3 requests | 5m | `limits.invoicePer5m` |
| Per user | `POST /api/wallet/withdraw` | 2 requests | 1h | `limits.withdrawPerHour` |
| Per worker | `/internal/worker/*` | 120 requests | 60s | `limits.workerBatchPerMin` |
| Global | `/integrations/telegram/payments` | 100 requests | 60s | `limits.telegramWebhookPerMin` |

Redis implementation uses token bucket counters keyed by `{scope}:{entity}` with expiration equal to the window.

