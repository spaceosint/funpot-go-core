-- 0003_rewards.up.sql
-- Reward state and reward claim history.
-- Actual FPC credits are recorded in wallet_ledger.

CREATE TABLE public.weekly_reward_claims (
    user_id UUID PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,

    last_claim_at TIMESTAMPTZ,
    streak_day INT NOT NULL DEFAULT 0 CHECK (streak_day >= 0 AND streak_day <= 7),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_weekly_reward_claims_set_updated_at
BEFORE UPDATE ON public.weekly_reward_claims
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.reward_claim_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,

    reward_type TEXT NOT NULL CHECK (reward_type <> ''),
    reward_amount_int BIGINT NOT NULL CHECK (reward_amount_int > 0),

    streak_day INT CHECK (streak_day IS NULL OR streak_day >= 0),

    wallet_ledger_id UUID NOT NULL UNIQUE REFERENCES public.wallet_ledger(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_reward_claim_history_user_created_at
    ON public.reward_claim_history (user_id, created_at DESC);

CREATE INDEX idx_reward_claim_history_reward_type_created_at
    ON public.reward_claim_history (reward_type, created_at DESC);
