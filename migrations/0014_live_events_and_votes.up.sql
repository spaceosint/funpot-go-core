CREATE TABLE IF NOT EXISTS live_events (
    id TEXT PRIMARY KEY,
    template_id TEXT NOT NULL,
    streamer_id TEXT NOT NULL,
    scenario_id TEXT NOT NULL,
    transition_id TEXT NOT NULL DEFAULT '',
    terminal_id TEXT NOT NULL,
    title_json JSONB NOT NULL,
    default_language TEXT NOT NULL,
    options_json JSONB NOT NULL,
    totals_json JSONB NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closes_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_live_events_streamer_status ON live_events (streamer_id, status);
CREATE INDEX IF NOT EXISTS idx_live_events_template ON live_events (template_id);

CREATE TABLE IF NOT EXISTS live_event_votes (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES live_events(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    option_id TEXT NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    idempotency_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_live_event_votes_event_user_idempotency
    ON live_event_votes (event_id, user_id, idempotency_key);
