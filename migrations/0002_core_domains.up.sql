CREATE TABLE IF NOT EXISTS streamers (
    id TEXT PRIMARY KEY,
    twitch_username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS games (
    id TEXT PRIMARY KEY,
    streamer_id TEXT NOT NULL REFERENCES streamers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS events (
    id TEXT PRIMARY KEY,
    streamer_id TEXT NOT NULL REFERENCES streamers(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS votes (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    option_id TEXT NOT NULL,
    user_telegram_id BIGINT NOT NULL,
    cost_int INT NOT NULL,
    idempotency_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_telegram_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS wallet_accounts (
    user_telegram_id BIGINT PRIMARY KEY,
    balance_int INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wallet_ledger (
    id TEXT PRIMARY KEY,
    user_telegram_id BIGINT NOT NULL,
    tx_type TEXT NOT NULL,
    amount_int INT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments (
    id TEXT PRIMARY KEY,
    user_telegram_id BIGINT NOT NULL,
    amount_int INT NOT NULL,
    currency TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_payload TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS referral_invites (
    referrer_telegram_id BIGINT NOT NULL,
    invited_telegram_id BIGINT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS referral_payouts (
    id TEXT PRIMARY KEY,
    user_telegram_id BIGINT NOT NULL,
    amount_int INT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS media_clips (
    id TEXT PRIMARY KEY,
    streamer_id TEXT NOT NULL REFERENCES streamers(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    duration_s INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_games_streamer_id ON games(streamer_id);
CREATE INDEX IF NOT EXISTS idx_events_streamer_id_status ON events(streamer_id, status);
CREATE INDEX IF NOT EXISTS idx_votes_event_id ON votes(event_id);
CREATE INDEX IF NOT EXISTS idx_wallet_ledger_user_created_at ON wallet_ledger(user_telegram_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_user_created_at ON payments(user_telegram_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_referral_payouts_user_created_at ON referral_payouts(user_telegram_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_clips_streamer_created_at ON media_clips(streamer_id, created_at DESC);
