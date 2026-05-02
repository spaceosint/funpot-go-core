CREATE TABLE IF NOT EXISTS wallet_accounts (
    user_id TEXT PRIMARY KEY,
    balance_int BIGINT NOT NULL DEFAULT 0 CHECK (balance_int >= 0),
    currency TEXT NOT NULL DEFAULT 'FPC' CHECK (currency = 'FPC'),
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wallet_ledger (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('credit', 'debit')),
    amount_int BIGINT NOT NULL CHECK (amount_int > 0),
    currency TEXT NOT NULL DEFAULT 'FPC' CHECK (currency = 'FPC'),
    reason TEXT NOT NULL,
    ref_id TEXT,
    idempotency_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_wallet_ledger_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

ALTER TABLE IF EXISTS wallet_ledger
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

UPDATE wallet_ledger
SET idempotency_key = id::text
WHERE idempotency_key IS NULL;

ALTER TABLE wallet_ledger
    ALTER COLUMN idempotency_key SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_wallet_ledger_idempotency_key
    ON wallet_ledger (idempotency_key);

CREATE INDEX IF NOT EXISTS idx_wallet_ledger_user_created_at
    ON wallet_ledger (user_id, created_at DESC);
