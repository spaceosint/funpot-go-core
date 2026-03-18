package streamers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PostgresDecisionRepository persists LLM decisions in PostgreSQL for audit/history APIs.
type PostgresDecisionRepository struct {
	db *sql.DB
}

func NewPostgresDecisionRepository(db *sql.DB) *PostgresDecisionRepository {
	return &PostgresDecisionRepository{db: db}
}

func (r *PostgresDecisionRepository) RecordLLMDecision(ctx context.Context, item LLMDecision) error {
	const query = `
INSERT INTO streamer_llm_decisions (
	id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
	prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
	chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
	latency_ms, transition_outcome, transition_to_step, transition_terminal, created_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7,
	$8, $9, $10, $11, $12, $13,
	$14, $15, $16, $17, $18, $19,
	$20, $21, $22, $23, $24
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
		parseRFC3339Time(item.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert streamer llm decision: %w", err)
	}
	return nil
}

func (r *PostgresDecisionRepository) ListLLMDecisions(ctx context.Context, streamerID string, limit int) ([]LLMDecision, error) {
	if limit <= 0 {
		limit = 20
	}
	const query = `
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal, created_at
FROM streamer_llm_decisions
WHERE streamer_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2`
	return r.queryDecisions(ctx, query, streamerID, limit)
}

func (r *PostgresDecisionRepository) ListAllLLMDecisions(ctx context.Context, streamerID string) ([]LLMDecision, error) {
	const query = `
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal, created_at
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
		&createdAt,
	); err != nil {
		return LLMDecision{}, fmt.Errorf("scan streamer llm decision: %w", err)
	}
	item.ChunkCapturedAt = formatNullTime(chunkCapturedAt)
	item.PromptVersionID = promptVersionID.String
	item.PromptText = promptText.String
	item.Model = model.String
	item.ChunkRef = chunkRef.String
	item.RequestRef = requestRef.String
	item.ResponseRef = responseRef.String
	item.RawResponse = rawResponse.String
	item.TransitionOutcome = transitionOutcome.String
	item.TransitionToStep = transitionToStep.String
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
