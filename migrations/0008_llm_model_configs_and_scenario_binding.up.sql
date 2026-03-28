CREATE TABLE IF NOT EXISTS llm_model_configs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
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
    activated_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_llm_model_configs_order
    ON llm_model_configs (created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_model_configs_single_active
    ON llm_model_configs (is_active) WHERE is_active;

ALTER TABLE llm_scenario_packages
    ADD COLUMN IF NOT EXISTS llm_model_config_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_llm_scenario_packages_model_config
    ON llm_scenario_packages (llm_model_config_id);
