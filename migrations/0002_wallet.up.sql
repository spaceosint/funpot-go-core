-- 0002_wallet.up.sql
-- FPC wallet. PostgreSQL is the source of truth for balances and ledger history.

CREATE TABLE public.wallet_accounts (
    user_id UUID PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,

    balance_int BIGINT NOT NULL DEFAULT 0 CHECK (balance_int >= 0),
    currency TEXT NOT NULL DEFAULT 'FPC' CHECK (currency = 'FPC'),

    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_wallet_accounts_balance_int
    ON public.wallet_accounts (balance_int);

CREATE TRIGGER trg_wallet_accounts_set_updated_at
BEFORE UPDATE ON public.wallet_accounts
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.wallet_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,

    tx_type TEXT NOT NULL
        CHECK (tx_type IN ('credit', 'debit')),

    amount_int BIGINT NOT NULL CHECK (amount_int > 0),
    balance_after_int BIGINT NOT NULL CHECK (balance_after_int >= 0),

    currency TEXT NOT NULL DEFAULT 'FPC' CHECK (currency = 'FPC'),

    reason TEXT NOT NULL CHECK (reason <> ''),
    ref_type TEXT NOT NULL DEFAULT '',
    ref_id UUID,

    idempotency_key TEXT NOT NULL UNIQUE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_wallet_ledger_user_created_at
    ON public.wallet_ledger (user_id, created_at DESC);

CREATE INDEX idx_wallet_ledger_reason_created_at
    ON public.wallet_ledger (reason, created_at DESC);

CREATE INDEX idx_wallet_ledger_ref
    ON public.wallet_ledger (ref_type, ref_id)
    WHERE ref_type <> '' AND ref_id IS NOT NULL;
