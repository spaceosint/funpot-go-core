-- 0001_core.up.sql
-- PostgreSQL clean core schema.
-- Internal IDs are UUIDs. Telegram ID is an external unique identifier.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE FUNCTION public.set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TABLE public.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    telegram_id BIGINT NOT NULL UNIQUE,
    username TEXT NOT NULL DEFAULT '',
    first_name TEXT NOT NULL DEFAULT '',
    last_name TEXT NOT NULL DEFAULT '',
    language_code TEXT NOT NULL DEFAULT '',

    nickname TEXT NOT NULL DEFAULT '',
    referral_code TEXT NOT NULL CHECK (referral_code <> ''),

    is_banned BOOLEAN NOT NULL DEFAULT FALSE,
    ban_reason TEXT NOT NULL DEFAULT '',
    banned_at TIMESTAMPTZ,
    banned_until TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_users_referral_code_lower
    ON public.users (lower(referral_code));

CREATE UNIQUE INDEX uq_users_nickname_lower
    ON public.users (lower(nickname))
    WHERE nickname <> '';

CREATE INDEX idx_users_created_at
    ON public.users (created_at DESC);

CREATE TRIGGER trg_users_set_updated_at
BEFORE UPDATE ON public.users
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.referral_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    referrer_user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    invited_telegram_id BIGINT NOT NULL,
    invited_user_id UUID REFERENCES public.users(id) ON DELETE SET NULL,

    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'claimed', 'cancelled')),

    claimed_at TIMESTAMPTZ,
    rewarded_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_referral_invites_referrer_invited_telegram
    ON public.referral_invites (referrer_user_id, invited_telegram_id);

CREATE UNIQUE INDEX uq_referral_invites_invited_user
    ON public.referral_invites (invited_user_id)
    WHERE invited_user_id IS NOT NULL;

CREATE INDEX idx_referral_invites_referrer_created_at
    ON public.referral_invites (referrer_user_id, created_at DESC);

CREATE TRIGGER trg_referral_invites_set_updated_at
BEFORE UPDATE ON public.referral_invites
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.user_ban_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,

    action TEXT NOT NULL
        CHECK (action IN ('ban', 'unban', 'extend', 'expire')),

    reason TEXT NOT NULL DEFAULT '',
    banned_until TIMESTAMPTZ,

    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_user_ban_history_user_created_at
    ON public.user_ban_history (user_id, created_at DESC);
