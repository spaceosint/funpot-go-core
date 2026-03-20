CREATE TABLE IF NOT EXISTS streamer_llm_decisions (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    streamer_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    label TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    chunk_captured_at TIMESTAMPTZ,
    prompt_version_id TEXT,
    prompt_text TEXT,
    model TEXT,
    temperature DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_tokens INTEGER NOT NULL DEFAULT 0,
    timeout_ms INTEGER NOT NULL DEFAULT 0,
    chunk_ref TEXT,
    request_ref TEXT,
    response_ref TEXT,
    raw_response TEXT,
    tokens_in INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    transition_outcome TEXT,
    transition_to_step TEXT,
    transition_terminal BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (char_length(id) > 0),
    CHECK (char_length(run_id) > 0),
    CHECK (char_length(streamer_id) > 0),
    CHECK (char_length(stage) > 0),
    CHECK (char_length(label) > 0),
    CHECK (confidence >= 0 AND confidence <= 1),
    CHECK (temperature >= 0),
    CHECK (max_tokens >= 0),
    CHECK (timeout_ms >= 0),
    CHECK (tokens_in >= 0),
    CHECK (tokens_out >= 0),
    CHECK (latency_ms >= 0)
);

CREATE INDEX IF NOT EXISTS idx_streamer_llm_decisions_streamer_created_at
    ON streamer_llm_decisions (streamer_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_streamer_llm_decisions_run_id
    ON streamer_llm_decisions (run_id);
CREATE INDEX IF NOT EXISTS idx_streamer_llm_decisions_streamer_stage_created_at
    ON streamer_llm_decisions (streamer_id, stage, created_at DESC, id DESC);
