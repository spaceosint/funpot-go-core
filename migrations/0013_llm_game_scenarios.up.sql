CREATE TABLE IF NOT EXISTS llm_game_scenarios (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    initial_node_id TEXT NOT NULL,
    nodes_json JSONB NOT NULL,
    transitions_json JSONB NOT NULL,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_llm_game_scenarios_game_slug_version ON llm_game_scenarios (game_slug, version DESC);
CREATE INDEX IF NOT EXISTS idx_llm_game_scenarios_active ON llm_game_scenarios (is_active);
