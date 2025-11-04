-- +migrate Up
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    telegram_id BIGINT NOT NULL UNIQUE,
    username TEXT,
    first_name TEXT,
    last_name TEXT,
    language_code TEXT,
    referral_code TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (char_length(referral_code) > 0)
);

CREATE INDEX IF NOT EXISTS idx_users_language_code ON users (language_code);

-- +migrate Down
DROP INDEX IF EXISTS idx_users_language_code;
DROP TABLE IF EXISTS users;
