-- 0006_live_event_history.up.sql
-- Redis should hold active live state.
-- PostgreSQL stores durable session/event/vote history.

CREATE TABLE public.game_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    streamer_id UUID NOT NULL REFERENCES public.streamers(id) ON DELETE CASCADE,

    game_slug TEXT NOT NULL DEFAULT 'unknown' CHECK (game_slug <> ''),
    external_match_id TEXT NOT NULL DEFAULT '',

    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'completed', 'cancelled')),

    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ,

    summary_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(summary_json) = 'object'),

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (ended_at IS NULL OR ended_at >= started_at)
);

CREATE INDEX idx_game_sessions_streamer_started_at
    ON public.game_sessions (streamer_id, started_at DESC);

CREATE INDEX idx_game_sessions_status_started_at
    ON public.game_sessions (status, started_at DESC);

CREATE INDEX idx_game_sessions_game_slug_started_at
    ON public.game_sessions (game_slug, started_at DESC);

CREATE TRIGGER trg_game_sessions_set_updated_at
BEFORE UPDATE ON public.game_sessions
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.live_event_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    streamer_id UUID NOT NULL REFERENCES public.streamers(id) ON DELETE CASCADE,
    session_id UUID REFERENCES public.game_sessions(id) ON DELETE SET NULL,

    scenario_id UUID REFERENCES public.llm_scenarios(id) ON DELETE SET NULL,
    llm_request_log_id UUID REFERENCES public.llm_request_logs(id) ON DELETE SET NULL,

    source TEXT NOT NULL DEFAULT 'llm'
        CHECK (source IN ('llm', 'manual', 'system')),

    template_id TEXT NOT NULL DEFAULT '',
    transition_id TEXT NOT NULL DEFAULT '',
    terminal_id TEXT NOT NULL DEFAULT '',

    title_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(title_json) = 'object'),

    options_json JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(options_json) = 'array'),

    final_totals_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(final_totals_json) = 'object'),

    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'closed', 'cancelled', 'settled')),

    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closes_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (closes_at IS NULL OR closes_at > opened_at),
    CHECK (closed_at IS NULL OR closed_at >= opened_at)
);

CREATE INDEX idx_live_event_history_streamer_opened_at
    ON public.live_event_history (streamer_id, opened_at DESC);

CREATE INDEX idx_live_event_history_session_opened_at
    ON public.live_event_history (session_id, opened_at DESC)
    WHERE session_id IS NOT NULL;

CREATE INDEX idx_live_event_history_status_opened_at
    ON public.live_event_history (status, opened_at DESC);

CREATE INDEX idx_live_event_history_scenario_opened_at
    ON public.live_event_history (scenario_id, opened_at DESC)
    WHERE scenario_id IS NOT NULL;

CREATE TRIGGER trg_live_event_history_set_updated_at
BEFORE UPDATE ON public.live_event_history
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.live_event_vote_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    event_id UUID NOT NULL REFERENCES public.live_event_history(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,

    option_id TEXT NOT NULL CHECK (option_id <> ''),
    amount_int BIGINT NOT NULL CHECK (amount_int > 0),

    wallet_ledger_id UUID NOT NULL UNIQUE REFERENCES public.wallet_ledger(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_live_event_vote_history_event_created_at
    ON public.live_event_vote_history (event_id, created_at DESC);

CREATE INDEX idx_live_event_vote_history_event_option
    ON public.live_event_vote_history (event_id, option_id);

CREATE INDEX idx_live_event_vote_history_user_created_at
    ON public.live_event_vote_history (user_id, created_at DESC);
