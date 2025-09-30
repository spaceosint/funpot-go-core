# Data Model Overview

## Core Tables
- **users** `(id uuid PK, tg_user_id bigint unique, nickname text, language text, roles text[], created_at timestamptz)`
- **referrals** `(user_id uuid PK FK users, code text unique, inviter_user_id uuid FK users, percent numeric(5,2), created_at timestamptz)`
- **wallet_accounts** `(user_id uuid PK FK users, balance_int bigint, updated_at timestamptz)`
- **wallet_ledger** `(id uuid PK, user_id uuid FK users, type text CHECK (type IN ('credit','debit')), amount_int bigint CHECK (amount_int>0), currency text DEFAULT 'INT', reason text CHECK (reason IN ('stars_topup','vote_cost','reward','withdraw','referral_bonus')), ref_id text, idempotency_key text, created_at timestamptz)` with indexes on `(user_id, created_at)`, `(idempotency_key)`.
- **payments** `(id uuid PK, user_id uuid FK users, provider text CHECK (provider='telegram_stars'), invoice_id text unique, amount_int bigint, status text CHECK (status IN ('pending','paid','failed')), payload jsonb, created_at timestamptz, updated_at timestamptz)`
- **streamers** `(id uuid PK, platform text CHECK (platform='twitch'), username text unique, display_name text, online boolean, viewers int, status text CHECK (status IN ('ok','pending','rejected','banned')), added_by uuid FK users, created_at timestamptz, updated_at timestamptz)`
- **games** `(id uuid PK, streamer_id uuid FK streamers, title text, rules_json jsonb, status text CHECK (status IN ('draft','active','closed','paused')), start_at timestamptz, end_at timestamptz)`
- **events** `(id uuid PK, streamer_id uuid FK streamers, game_id uuid FK games, title text, options_json jsonb, state text CHECK (state IN ('live','closed','cancelled')), closes_at timestamptz, totals_json jsonb, result_json jsonb, source_clip_id uuid FK media_clips, prompt_versions_json jsonb, confidence numeric(4,2), created_at timestamptz, updated_at timestamptz)` with indexes on `(streamer_id, state)`, `(game_id, state)`.
- **votes** `(id uuid PK, event_id uuid FK events, user_id uuid FK users, option_id text, cost_int bigint, idempotency_key text, created_at timestamptz)` with unique constraint `(user_id, event_id)` and indexes `(event_id)`, `(idempotency_key)`.
- **media_clips** `(id uuid PK, streamer_id uuid FK streamers, url text, thumbnail_url text, started_at timestamptz, duration_sec int, source text DEFAULT 'bunny', created_at timestamptz)`.
- **prompts** `(id uuid PK, scope text CHECK (scope IN ('session','game','per_clip')), streamer_id uuid FK streamers NULLABLE, game_id uuid FK games NULLABLE, version text, body_text text, schema_version text, status text CHECK (status IN ('active','inactive')), created_by uuid FK users, created_at timestamptz)`.
- **config** `(key text PK, value_json jsonb, updated_at timestamptz)`.
- **idempotency** `(id uuid PK, key text unique, first_seen_at timestamptz, last_seen_at timestamptz, response_cache_json jsonb)`.

## Relationships
- `users` 1—1 `wallet_accounts`.
- `users` 1—n `wallet_ledger`, `payments`, `votes`.
- `referrals` optionally link `users` to inviter.
- `streamers` reference `users` via `added_by`.
- `games`, `events`, `media_clips` tie to `streamers`.
- `events` belong to a `game` (optional) and may reference a `media_clip`.
- `votes` belong to both `events` and `users`.
- `prompts` optionally scoped by streamer/game.

## Partitioning Strategy (Future)
- Partition `wallet_ledger` by month once record counts exceed 10M.
- Partition `votes` by `(streamer_id HASH)` or `(created_at RANGE)` depending on access patterns.

