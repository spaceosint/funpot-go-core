package streamers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

const streamerLLMDecisionsDDL = `
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
    previous_state_json TEXT,
    updated_state_json TEXT,
    evidence_delta_json TEXT,
    conflicts_json TEXT,
    final_outcome TEXT,
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
`

const streamerLLMDecisionsBackfillDDL = `
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS previous_state_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS updated_state_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS evidence_delta_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS conflicts_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS final_outcome TEXT;
`

// PostgresDecisionRepository persists LLM decisions in PostgreSQL for audit/history APIs.
type PostgresDecisionRepository struct {
	db *sql.DB

	schemaMu        sync.Mutex
	schemaEnsured   bool
	schemaEnsureErr error
}

func NewPostgresDecisionRepository(db *sql.DB) *PostgresDecisionRepository {
	return &PostgresDecisionRepository{db: db}
}

func (r *PostgresDecisionRepository) ensureSchema(ctx context.Context) error {
	r.schemaMu.Lock()
	defer r.schemaMu.Unlock()

	if r.schemaEnsured {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, streamerLLMDecisionsDDL); err != nil {
		r.schemaEnsureErr = fmt.Errorf("ensure streamer llm decisions schema: %w", err)
		return r.schemaEnsureErr
	}
	if _, err := r.db.ExecContext(ctx, streamerLLMDecisionsBackfillDDL); err != nil {
		r.schemaEnsureErr = fmt.Errorf("backfill streamer llm decisions schema: %w", err)
		return r.schemaEnsureErr
	}
	r.schemaEnsured = true
	r.schemaEnsureErr = nil
	return nil
}

func (r *PostgresDecisionRepository) RecordLLMDecision(ctx context.Context, item LLMDecision) error {
	if err := r.ensureSchema(ctx); err != nil {
		return err
	}
	const query = `
INSERT INTO streamer_llm_decisions (
	id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
	prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
	chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
	latency_ms, transition_outcome, transition_to_step, transition_terminal,
	previous_state_json, updated_state_json, evidence_delta_json, conflicts_json, final_outcome, created_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7,
	$8, $9, $10, $11, $12, $13,
	$14, $15, $16, $17, $18, $19,
	$20, $21, $22, $23,
	$24, $25, $26, $27, $28, $29
)`
	_, err := r.db.ExecContext(ctx, query,
		item.ID,
		item.RunID,
		item.StreamerID,
		item.Stage,
		item.Label,
		item.Confidence,
		parseRFC3339Null(item.ChunkCapturedAt),
		nullString(item.PromptVersionID),
		nullString(item.PromptText),
		nullString(item.Model),
		item.Temperature,
		item.MaxTokens,
		item.TimeoutMS,
		nullString(item.ChunkRef),
		nullString(item.RequestRef),
		nullString(item.ResponseRef),
		nullString(item.RawResponse),
		item.TokensIn,
		item.TokensOut,
		item.LatencyMS,
		nullString(item.TransitionOutcome),
		nullString(item.TransitionToStep),
		item.TransitionTerminal,
		nullString(item.PreviousStateJSON),
		nullString(item.UpdatedStateJSON),
		nullString(item.EvidenceDeltaJSON),
		nullString(item.ConflictsJSON),
		nullString(item.FinalOutcome),
		parseRFC3339Time(item.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert streamer llm decision: %w", err)
	}
	return nil
}

func (r *PostgresDecisionRepository) ListLLMDecisions(ctx context.Context, streamerID string, limit int) ([]LLMDecision, error) {
	if err := r.ensureSchema(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	const query = `
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal,
       previous_state_json, updated_state_json, evidence_delta_json, conflicts_json, final_outcome, created_at
FROM streamer_llm_decisions
WHERE streamer_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2`
	return r.queryDecisions(ctx, query, streamerID, limit)
}

func (r *PostgresDecisionRepository) ListAllLLMDecisions(ctx context.Context, streamerID string) ([]LLMDecision, error) {
	if err := r.ensureSchema(ctx); err != nil {
		return nil, err
	}
	const query = `
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal,
       previous_state_json, updated_state_json, evidence_delta_json, conflicts_json, final_outcome, created_at
FROM streamer_llm_decisions
WHERE streamer_id = $1
ORDER BY created_at ASC, id ASC`
	return r.queryDecisions(ctx, query, streamerID)
}

func (r *PostgresDecisionRepository) queryDecisions(ctx context.Context, query string, args ...any) ([]LLMDecision, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query streamer llm decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := make([]LLMDecision, 0)
	for rows.Next() {
		item, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate streamer llm decisions: %w", err)
	}
	return items, nil
}

type decisionScanner interface {
	Scan(dest ...any) error
}

func scanDecision(row decisionScanner) (LLMDecision, error) {
	var (
		item              LLMDecision
		chunkCapturedAt   sql.NullTime
		promptVersionID   sql.NullString
		promptText        sql.NullString
		model             sql.NullString
		chunkRef          sql.NullString
		requestRef        sql.NullString
		responseRef       sql.NullString
		rawResponse       sql.NullString
		transitionOutcome sql.NullString
		transitionToStep  sql.NullString
		previousState     sql.NullString
		updatedState      sql.NullString
		evidenceDelta     sql.NullString
		conflicts         sql.NullString
		finalOutcome      sql.NullString
		createdAt         time.Time
	)
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.StreamerID,
		&item.Stage,
		&item.Label,
		&item.Confidence,
		&chunkCapturedAt,
		&promptVersionID,
		&promptText,
		&model,
		&item.Temperature,
		&item.MaxTokens,
		&item.TimeoutMS,
		&chunkRef,
		&requestRef,
		&responseRef,
		&rawResponse,
		&item.TokensIn,
		&item.TokensOut,
		&item.LatencyMS,
		&transitionOutcome,
		&transitionToStep,
		&item.TransitionTerminal,
		&previousState,
		&updatedState,
		&evidenceDelta,
		&conflicts,
		&finalOutcome,
		&createdAt,
	); err != nil {
		return LLMDecision{}, fmt.Errorf("scan streamer llm decision: %w", err)
	}
	item.ChunkCapturedAt = formatNullTime(chunkCapturedAt)
	item.PromptVersionID = strings.TrimSpace(promptVersionID.String)
	item.PromptText = promptText.String
	item.Model = strings.TrimSpace(model.String)
	item.ChunkRef = strings.TrimSpace(chunkRef.String)
	item.RequestRef = strings.TrimSpace(requestRef.String)
	item.ResponseRef = strings.TrimSpace(responseRef.String)
	item.RawResponse = rawResponse.String
	item.TransitionOutcome = strings.TrimSpace(transitionOutcome.String)
	item.TransitionToStep = strings.TrimSpace(transitionToStep.String)
	item.PreviousStateJSON = previousState.String
	item.UpdatedStateJSON = updatedState.String
	item.EvidenceDeltaJSON = evidenceDelta.String
	item.ConflictsJSON = conflicts.String
	item.FinalOutcome = strings.TrimSpace(finalOutcome.String)
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	return item, nil
}

func nullString(value string) sql.NullString {
	trimmed := strings.TrimSpace(value)
	return sql.NullString{String: trimmed, Valid: trimmed != ""}
}

func parseRFC3339Null(value string) sql.NullTime {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return sql.NullTime{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: parsed.UTC(), Valid: true}
}

func parseRFC3339Time(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func formatNullTime(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}
