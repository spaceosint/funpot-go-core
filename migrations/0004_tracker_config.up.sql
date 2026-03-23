DROP INDEX IF EXISTS idx_prompt_scenarios_game_version;
DROP INDEX IF EXISTS idx_prompt_scenarios_active_game;
DROP TABLE IF EXISTS prompt_scenarios;
DROP INDEX IF EXISTS idx_prompt_global_detectors_version;
DROP INDEX IF EXISTS idx_prompt_global_detectors_active;
DROP TABLE IF EXISTS prompt_global_detectors;

CREATE TABLE IF NOT EXISTS llm_prompt_versions (
    id TEXT PRIMARY KEY,
    stage TEXT NOT NULL,
    position INTEGER NOT NULL,
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
    CHECK (position > 0),
    CHECK (version > 0),
    CHECK (temperature >= 0 AND temperature <= 2),
    CHECK (max_tokens > 0),
    CHECK (timeout_ms > 0),
    CHECK (retry_count >= 0 AND retry_count <= 10),
    CHECK (backoff_ms >= 0),
    CHECK (cooldown_ms >= 0),
    CHECK (min_confidence >= 0 AND min_confidence <= 1)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_prompt_versions_active_stage
    ON llm_prompt_versions (stage) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_prompt_versions_order
    ON llm_prompt_versions (position ASC, stage ASC, version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS llm_state_schema_versions (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    fields_json JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_state_schema_versions_active_game
    ON llm_state_schema_versions (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_state_schema_versions_order
    ON llm_state_schema_versions (game_slug ASC, version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS llm_rule_set_versions (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    rule_items_json JSONB NOT NULL,
    finalization_rules_json JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_rule_set_versions_active_game
    ON llm_rule_set_versions (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_rule_set_versions_order
    ON llm_rule_set_versions (game_slug ASC, version DESC, created_at DESC);
