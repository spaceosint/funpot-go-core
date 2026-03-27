CREATE TABLE IF NOT EXISTS llm_scenario_packages (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    steps_json JSONB NOT NULL,
    transitions_json JSONB NOT NULL,
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_scenario_packages_active_game
    ON llm_scenario_packages (game_slug) WHERE is_active;

CREATE INDEX IF NOT EXISTS idx_llm_scenario_packages_order
    ON llm_scenario_packages (game_slug ASC, version DESC, created_at DESC);
