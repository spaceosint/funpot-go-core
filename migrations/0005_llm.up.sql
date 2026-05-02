-- 0005_llm.up.sql
-- LLM configs, scenarios and request logs.
-- llm_request_logs are designed for at least 365 days of retention.

CREATE TABLE public.llm_model_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    name TEXT NOT NULL UNIQUE CHECK (name <> ''),
    provider TEXT NOT NULL CHECK (provider <> ''),
    model TEXT NOT NULL CHECK (model <> ''),

    temperature DOUBLE PRECISION NOT NULL DEFAULT 0.2
        CHECK (temperature >= 0 AND temperature <= 2),

    max_tokens INT NOT NULL DEFAULT 2048 CHECK (max_tokens > 0),
    timeout_ms INT NOT NULL DEFAULT 30000 CHECK (timeout_ms > 0),
    retry_count INT NOT NULL DEFAULT 2 CHECK (retry_count >= 0),
    backoff_ms INT NOT NULL DEFAULT 500 CHECK (backoff_ms >= 0),
    cooldown_ms INT NOT NULL DEFAULT 0 CHECK (cooldown_ms >= 0),

    min_confidence DOUBLE PRECISION NOT NULL DEFAULT 0.0
        CHECK (min_confidence >= 0 AND min_confidence <= 1),

    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_by TEXT NOT NULL DEFAULT '',
    activated_by TEXT NOT NULL DEFAULT '',
    activated_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_llm_model_configs_active
    ON public.llm_model_configs (is_active);

CREATE TRIGGER trg_llm_model_configs_set_updated_at
BEFORE UPDATE ON public.llm_model_configs
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.llm_scenarios (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    game_slug TEXT NOT NULL CHECK (game_slug <> ''),
    name TEXT NOT NULL CHECK (name <> ''),
    version INT NOT NULL CHECK (version > 0),

    is_active BOOLEAN NOT NULL DEFAULT FALSE,

    initial_node_id TEXT NOT NULL CHECK (initial_node_id <> ''),
    nodes_json JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(nodes_json) = 'array'),

    transitions_json JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(transitions_json) = 'array'),

    model_config_id UUID REFERENCES public.llm_model_configs(id) ON DELETE SET NULL,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_by TEXT NOT NULL DEFAULT '',
    activated_by TEXT NOT NULL DEFAULT '',
    activated_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_llm_scenarios_game_version
    ON public.llm_scenarios (game_slug, version);

CREATE UNIQUE INDEX uq_llm_scenarios_active_per_game
    ON public.llm_scenarios (game_slug)
    WHERE is_active;

CREATE INDEX idx_llm_scenarios_game_created_at
    ON public.llm_scenarios (game_slug, created_at DESC);

CREATE TRIGGER trg_llm_scenarios_set_updated_at
BEFORE UPDATE ON public.llm_scenarios
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.llm_request_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    streamer_id UUID REFERENCES public.streamers(id) ON DELETE SET NULL,
    scenario_id UUID REFERENCES public.llm_scenarios(id) ON DELETE SET NULL,
    model_config_id UUID REFERENCES public.llm_model_configs(id) ON DELETE SET NULL,

    request_type TEXT NOT NULL DEFAULT '',

    status TEXT NOT NULL
        CHECK (status IN ('success', 'failed', 'timeout', 'cancelled')),

    provider_request_id TEXT NOT NULL DEFAULT '',

    input_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_json JSONB NOT NULL DEFAULT '{}'::jsonb,

    prompt_tokens INT NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens INT NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    total_tokens INT NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),

    latency_ms INT NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    cost_microunits BIGINT NOT NULL DEFAULT 0 CHECK (cost_microunits >= 0),

    error_message TEXT NOT NULL DEFAULT '',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '365 days')
);

CREATE INDEX idx_llm_request_logs_created_at
    ON public.llm_request_logs (created_at DESC);

CREATE INDEX idx_llm_request_logs_expires_at
    ON public.llm_request_logs (expires_at);

CREATE INDEX idx_llm_request_logs_streamer_created_at
    ON public.llm_request_logs (streamer_id, created_at DESC);

CREATE INDEX idx_llm_request_logs_scenario_created_at
    ON public.llm_request_logs (scenario_id, created_at DESC);

CREATE INDEX idx_llm_request_logs_status_created_at
    ON public.llm_request_logs (status, created_at DESC);
