# Migration Plan

## v1 (Initial Release)
1. Create core tables: `users`, `wallet_accounts`, `wallet_ledger`, `payments`, `streamers`, `games`, `events`, `votes`, `media_clips`, `prompts`, `config`, `referrals`, `idempotency`.
2. Seed configuration values: `minViewers=100`, `starsRate`, `limits.votePerMin`, feature flags (`paymentsEnabled`, `referralsEnabled`, `mediaEnabled`, `adminEnabled`).
3. Create indexes and unique constraints specified in `docs/erd.md`.
4. Install triggers/functions:
   - Balance maintenance for `wallet_accounts` (materialized from ledger).
   - Audit trail triggers for admin operations (optional but recommended).
5. Deploy initial Go binary and run `/readyz` checks.

## v1.x Enhancements
- Add `votes_totals_cache` table (optional) to persist aggregated snapshots if Redis unavailable.
- Introduce background job scheduling table (`jobs`) if we outgrow simple cron-based closures.

## v2 Roadmap
1. Partition `wallet_ledger` by month using declarative partitioning. Migrate historical data into partitions.
2. Partition `votes` either by month or by streamer hash to reduce hot indexes.
3. Add materialized view `events_live_view` for quick reads of live events plus totals.
4. Introduce `worker_jobs` table to track worker assignments and heartbeats (if moving to push model).
5. Add `events.analytics_json` column for storing aggregated LLM insights beyond live voting.

## Deployment Considerations
- All migrations executed via `golang-migrate` with versioned files (`migrations/0001_init.sql`, etc.).
- Use transactional DDL where supported; wrap partition changes in maintenance windows.
- For partition rollout, deploy application support before data migration to ensure compatibility.

