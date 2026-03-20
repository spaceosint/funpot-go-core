package streamers

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresDecisionRepositoryRecordLLMDecision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := NewPostgresDecisionRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(streamerLLMDecisionsDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	createdAt := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	capturedAt := createdAt.Add(-10 * time.Second)
	item := LLMDecision{
		ID:                 "llm_1",
		RunID:              "run_1",
		StreamerID:         "str_1",
		Stage:              "detector",
		Label:              "cs_detected",
		Confidence:         0.91,
		ChunkCapturedAt:    capturedAt.Format(time.RFC3339Nano),
		PromptVersionID:    "prompt_1",
		PromptText:         "detect the game",
		Model:              "gemini-2.0-flash",
		Temperature:        0.2,
		MaxTokens:          256,
		TimeoutMS:          2000,
		ChunkRef:           "streamlink://chunk-1",
		RequestRef:         "req-1",
		ResponseRef:        "resp-1",
		RawResponse:        "raw",
		TokensIn:           123,
		TokensOut:          45,
		LatencyMS:          890,
		TransitionOutcome:  "cs_detected",
		TransitionToStep:   "match_start",
		TransitionTerminal: false,
		CreatedAt:          createdAt.Format(time.RFC3339Nano),
	}

	mock.ExpectExec(regexp.QuoteMeta(`
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
)`)).
		WithArgs(
			item.ID,
			item.RunID,
			item.StreamerID,
			item.Stage,
			item.Label,
			item.Confidence,
			sqlmock.AnyArg(),
			nullDriverString(item.PromptVersionID),
			nullDriverString(item.PromptText),
			nullDriverString(item.Model),
			item.Temperature,
			item.MaxTokens,
			item.TimeoutMS,
			nullDriverString(item.ChunkRef),
			nullDriverString(item.RequestRef),
			nullDriverString(item.ResponseRef),
			nullDriverString(item.RawResponse),
			item.TokensIn,
			item.TokensOut,
			item.LatencyMS,
			nullDriverString(item.TransitionOutcome),
			nullDriverString(item.TransitionToStep),
			item.TransitionTerminal,
			createdAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := repo.RecordLLMDecision(context.Background(), item); err != nil {
		t.Fatalf("RecordLLMDecision() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresDecisionRepositoryListLLMDecisions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := NewPostgresDecisionRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(streamerLLMDecisionsDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	createdAt := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	capturedAt := createdAt.Add(-10 * time.Second)
	rows := sqlmock.NewRows([]string{
		"id", "run_id", "streamer_id", "stage", "label", "confidence", "chunk_captured_at",
		"prompt_version_id", "prompt_text", "model", "temperature", "max_tokens", "timeout_ms",
		"chunk_ref", "request_ref", "response_ref", "raw_response", "tokens_in", "tokens_out",
		"latency_ms", "transition_outcome", "transition_to_step", "transition_terminal", "created_at",
	}).AddRow(
		"llm_1", "run_1", "str_1", "detector", "cs_detected", 0.91, capturedAt,
		"prompt_1", "detect the game", "gemini-2.0-flash", 0.2, 256, 2000,
		"streamlink://chunk-1", "req-1", "resp-1", "raw", 123, 45,
		890, "cs_detected", "match_start", false, createdAt,
	)

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal, created_at
FROM streamer_llm_decisions
WHERE streamer_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2`)).
		WithArgs("str_1", 5).
		WillReturnRows(rows)

	items, err := repo.ListLLMDecisions(context.Background(), "str_1", 5)
	if err != nil {
		t.Fatalf("ListLLMDecisions() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ChunkCapturedAt != capturedAt.Format(time.RFC3339Nano) || items[0].CreatedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected timestamps: %+v", items[0])
	}
	if items[0].TransitionToStep != "match_start" || items[0].RequestRef != "req-1" {
		t.Fatalf("unexpected decision payload: %+v", items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func nullDriverString(value string) driver.Valuer {
	return stringValuer(value)
}

type stringValuer string

func (s stringValuer) Value() (driver.Value, error) {
	if s == "" {
		return nil, nil
	}
	return string(s), nil
}

func TestPostgresDecisionRepositoryEnsuresSchemaOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := NewPostgresDecisionRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(streamerLLMDecisionsDDL)).WillReturnResult(sqlmock.NewResult(0, 0))

	rows := sqlmock.NewRows([]string{
		"id", "run_id", "streamer_id", "stage", "label", "confidence", "chunk_captured_at",
		"prompt_version_id", "prompt_text", "model", "temperature", "max_tokens", "timeout_ms",
		"chunk_ref", "request_ref", "response_ref", "raw_response", "tokens_in", "tokens_out",
		"latency_ms", "transition_outcome", "transition_to_step", "transition_terminal", "created_at",
	})
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal, created_at
FROM streamer_llm_decisions
WHERE streamer_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2`)).
		WithArgs("str_1", 1).
		WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, run_id, streamer_id, stage, label, confidence, chunk_captured_at,
       prompt_version_id, prompt_text, model, temperature, max_tokens, timeout_ms,
       chunk_ref, request_ref, response_ref, raw_response, tokens_in, tokens_out,
       latency_ms, transition_outcome, transition_to_step, transition_terminal, created_at
FROM streamer_llm_decisions
WHERE streamer_id = $1
ORDER BY created_at ASC, id ASC`)).
		WithArgs("str_1").
		WillReturnRows(rows)

	if _, err := repo.ListLLMDecisions(context.Background(), "str_1", 1); err != nil {
		t.Fatalf("ListLLMDecisions() error = %v", err)
	}
	if _, err := repo.ListAllLLMDecisions(context.Background(), "str_1"); err != nil {
		t.Fatalf("ListAllLLMDecisions() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
