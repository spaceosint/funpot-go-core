CREATE TABLE IF NOT EXISTS prompt_global_detectors (
    id TEXT PRIMARY KEY,
    stage TEXT NOT NULL,
    version INTEGER NOT NULL,
    template TEXT NOT NULL,
    model TEXT NOT NULL,
    temperature DOUBLE PRECISION NOT NULL,
    max_tokens INTEGER NOT NULL,
    timeout_ms INTEGER NOT NULL,
    retry_count INTEGER NOT NULL,
    backoff_ms INTEGER NOT NULL,
    cooldown_ms INTEGER NOT NULL,
    min_confidence DOUBLE PRECISION NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(stage) > 0),
    CHECK (char_length(template) > 0),
    CHECK (char_length(model) > 0),
    CHECK (version > 0),
    CHECK (temperature >= 0 AND temperature <= 2),
    CHECK (max_tokens > 0),
    CHECK (timeout_ms > 0),
    CHECK (retry_count >= 0 AND retry_count <= 10),
    CHECK (backoff_ms >= 0),
    CHECK (cooldown_ms >= 0),
    CHECK (min_confidence >= 0 AND min_confidence <= 1)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_global_detectors_active
    ON prompt_global_detectors ((is_active)) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_prompt_global_detectors_version
    ON prompt_global_detectors (version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS prompt_scenarios (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    steps_json JSONB NOT NULL,
    transitions_json JSONB NOT NULL,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_scenarios_active_game
    ON prompt_scenarios (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_prompt_scenarios_game_version
    ON prompt_scenarios (game_slug, version DESC, created_at DESC);
