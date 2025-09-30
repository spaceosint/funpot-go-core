# Risk Matrix & Environment Launch Checklist

## Risk Matrix
| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Redis outage leading to vote loss | Medium | High | Write-through snapshots to PostgreSQL every 1–3 s; feature flag to disable paid voting when Redis unavailable; alerting on Redis connectivity. |
| Telegram webhook replay/duplication | High | Medium | Enforce idempotency via invoice IDs and Redis locks; monitoring for duplicate ledger entries. |
| Worker signature compromise | Low | High | Rotate `WORKER_SECRET`, enforce IP allowlist, monitor signature failures, disable processing on repeated failures. |
| Twitch viewer validation failure | Medium | Medium | Implement fallback polling and caching; notify ops when viewer API unavailable. |
| WebSocket overload > capacity | Medium | Medium | Autoscale horizontally; enforce per-channel backpressure; degrade to polling fallback. |
| LLM produces inappropriate content | Medium | Medium | Implement moderation filters, manual admin review queue, maintain audit logs. |
| Database hot partition on votes | Medium | High | Plan for partitioning (v2), monitor index bloat, tune connection pool. |
| Referral abuse (self-invite) | Medium | Low | Detect same Telegram IDs/IP ranges; manual review; limit bonus payout to paid invoices only. |
| Configuration drift between envs | Low | Medium | Manage configs via migrations and feature flag dashboards; environment checklists below. |

## Environment Launch Checklist

### Dev
- [ ] Run migrations against local PostgreSQL.
- [ ] Seed `config` table with default limits and feature flags.
- [ ] Start Redis locally and verify connection from app.
- [ ] Configure `.env.local` and load via `direnv` or `dotenv`.
- [ ] Optionally expose endpoint via ngrok for Telegram webhook testing.

### Staging
- [ ] Deploy migrations using CI (dry-run + apply).
- [ ] Configure separate Telegram bot/token and set webhook to staging URL.
- [ ] Load staging-specific feature flags (e.g., enable sandbox payments only).
- [ ] Point worker service to staging `/internal/worker/*` endpoints with staging secret.
- [ ] Run smoke tests (`go test`, lint, k6 smoke`).
- [ ] Verify Prometheus scraping and Sentry DSN configured.

### Production
- [ ] Confirm blue/green deployment slots available.
- [ ] Apply migrations during low-traffic window or using zero-downtime strategy.
- [ ] Update DNS/LB to point to new release once health checks pass.
- [ ] Rotate and store secrets securely (Vault/SM) and inject into environment.
- [ ] Confirm Telegram webhook points to production URL.
- [ ] Validate worker heartbeat metrics and L7 logs.
- [ ] Enable alerting for SLO breaches, Redis issues, payment webhook failures.

