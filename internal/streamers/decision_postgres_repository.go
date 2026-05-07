package streamers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PostgresDecisionRepository stores durable LLM decision history in the existing
// llm_request_logs table. Video bytes stay in object storage; only sanitized
// references and payload summaries are written to PostgreSQL.
type PostgresDecisionRepository struct {
	db *sql.DB
}

func NewPostgresDecisionRepository(db *sql.DB) *PostgresDecisionRepository {
	return &PostgresDecisionRepository{db: db}
}

func (r *PostgresDecisionRepository) RecordLLMDecision(ctx context.Context, item LLMDecision) error {
	if r == nil || r.db == nil {
		return nil
	}
	item = normalizeDecisionForPostgres(item)
	if strings.TrimSpace(item.StreamerID) == "" {
		return nil
	}
	createdAt := nullableTimeFromString(item.CreatedAt)
	if !createdAt.Valid {
		createdAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	}
	inputJSON, outputJSON, err := marshalLLMDecisionLogPayloads(item)
	if err != nil {
		return err
	}
	const query = `
	INSERT INTO llm_request_logs (
	    streamer_id, request_type, status, provider_request_id,
	    input_json, output_json, prompt_tokens, completion_tokens,
	    total_tokens, latency_ms, error_message, created_at
	)
	VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11, $12)`
	if _, err := r.db.ExecContext(ctx, query,
		item.StreamerID,
		item.Stage,
		"success",
		firstNonEmptyStreamer(item.ID, item.RequestRef),
		string(inputJSON),
		string(outputJSON),
		item.TokensIn,
		item.TokensOut,
		item.TokensIn+item.TokensOut,
		item.LatencyMS,
		"",
		createdAt,
	); err != nil {
		return fmt.Errorf("record llm decision request log: %w", err)
	}
	return nil
}

func (r *PostgresDecisionRepository) ListLLMDecisions(ctx context.Context, streamerID string, limit int) ([]LLMDecision, error) {
	if r == nil || r.db == nil || strings.TrimSpace(streamerID) == "" {
		return []LLMDecision{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	const query = selectLLMRequestLogDecisionColumns + `
	FROM llm_request_logs
	WHERE streamer_id = $1
	ORDER BY created_at DESC, id DESC
	LIMIT $2`
	rows, err := r.db.QueryContext(ctx, query, strings.TrimSpace(streamerID), limit)
	if err != nil {
		return nil, fmt.Errorf("list llm decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	return scanLLMRequestLogDecisionRows(rows)
}

func (r *PostgresDecisionRepository) ListAllLLMDecisions(ctx context.Context, streamerID string) ([]LLMDecision, error) {
	if r == nil || r.db == nil || strings.TrimSpace(streamerID) == "" {
		return []LLMDecision{}, nil
	}
	const query = selectLLMRequestLogDecisionColumns + `
	FROM llm_request_logs
	WHERE streamer_id = $1
	ORDER BY created_at ASC, id ASC`
	rows, err := r.db.QueryContext(ctx, query, strings.TrimSpace(streamerID))
	if err != nil {
		return nil, fmt.Errorf("list all llm decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	return scanLLMRequestLogDecisionRows(rows)
}

func (r *PostgresDecisionRepository) DeleteAllLLMDecisions(ctx context.Context, streamerID string) (int, error) {
	if r == nil || r.db == nil || strings.TrimSpace(streamerID) == "" {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM llm_request_logs WHERE streamer_id = $1`, strings.TrimSpace(streamerID))
	if err != nil {
		return 0, fmt.Errorf("delete llm request logs: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted llm request logs: %w", err)
	}
	return int(deleted), nil
}

const selectLLMRequestLogDecisionColumns = `
	SELECT id::text, streamer_id::text, request_type, provider_request_id,
	       input_json, output_json, prompt_tokens, completion_tokens,
	       latency_ms, created_at`

func scanLLMRequestLogDecisionRows(rows *sql.Rows) ([]LLMDecision, error) {
	items := make([]LLMDecision, 0)
	for rows.Next() {
		item, err := scanLLMRequestLogDecision(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate llm decisions: %w", err)
	}
	return items, nil
}

type llmDecisionScanner interface {
	Scan(dest ...any) error
}

func scanLLMRequestLogDecision(scanner llmDecisionScanner) (LLMDecision, error) {
	var (
		logID             string
		streamerID        string
		requestType       string
		providerRequestID string
		inputRaw          []byte
		outputRaw         []byte
		promptTokens      int
		completionTokens  int
		latencyMS         int64
		createdAt         time.Time
	)
	if err := scanner.Scan(
		&logID, &streamerID, &requestType, &providerRequestID,
		&inputRaw, &outputRaw, &promptTokens, &completionTokens,
		&latencyMS, &createdAt,
	); err != nil {
		return LLMDecision{}, fmt.Errorf("scan llm decision request log: %w", err)
	}
	inputPayload := llmDecisionInputPayload{}
	if len(inputRaw) > 0 {
		if err := json.Unmarshal(inputRaw, &inputPayload); err != nil {
			return LLMDecision{}, fmt.Errorf("decode llm decision input_json: %w", err)
		}
	}
	outputPayload := llmDecisionOutputPayload{}
	if len(outputRaw) > 0 {
		if err := json.Unmarshal(outputRaw, &outputPayload); err != nil {
			return LLMDecision{}, fmt.Errorf("decode llm decision output_json: %w", err)
		}
	}
	item := LLMDecision{
		ID:                 firstNonEmptyStreamer(outputPayload.DecisionID, providerRequestID, logID),
		RunID:              inputPayload.RunID,
		StreamerID:         streamerID,
		Stage:              firstNonEmptyStreamer(outputPayload.Stage, requestType),
		Label:              outputPayload.Label,
		Confidence:         outputPayload.Confidence,
		ChunkCapturedAt:    inputPayload.ChunkCapturedAt,
		PromptVersionID:    inputPayload.PromptVersionID,
		PromptText:         inputPayload.PromptText,
		Model:              inputPayload.Model,
		Temperature:        inputPayload.Temperature,
		MaxTokens:          inputPayload.MaxTokens,
		TimeoutMS:          inputPayload.TimeoutMS,
		ChunkRef:           inputPayload.ChunkRef,
		RequestRef:         inputPayload.RequestRef,
		ResponseRef:        outputPayload.ResponseRef,
		RequestPayload:     inputPayload.RequestPayload,
		ResponsePayload:    outputPayload.ResponsePayload,
		RawResponse:        outputPayload.RawResponse,
		TokensIn:           promptTokens,
		TokensOut:          completionTokens,
		LatencyMS:          latencyMS,
		TransitionOutcome:  outputPayload.TransitionOutcome,
		TransitionToStep:   outputPayload.TransitionToStep,
		TransitionTerminal: outputPayload.TransitionTerminal,
		PreviousStateJSON:  inputPayload.PreviousStateJSON,
		UpdatedStateJSON:   outputPayload.UpdatedStateJSON,
		EvidenceDeltaJSON:  outputPayload.EvidenceDeltaJSON,
		ConflictsJSON:      outputPayload.ConflictsJSON,
		FinalOutcome:       outputPayload.FinalOutcome,
		CreatedAt:          createdAt.UTC().Format(time.RFC3339Nano),
	}
	return item, nil
}

type llmDecisionInputPayload struct {
	RunID             string  `json:"runId,omitempty"`
	ChunkCapturedAt   string  `json:"chunkCapturedAt,omitempty"`
	PromptVersionID   string  `json:"promptVersionId,omitempty"`
	PromptText        string  `json:"promptText,omitempty"`
	Model             string  `json:"model,omitempty"`
	Temperature       float64 `json:"temperature,omitempty"`
	MaxTokens         int     `json:"maxTokens,omitempty"`
	TimeoutMS         int     `json:"timeoutMs,omitempty"`
	ChunkRef          string  `json:"chunkRef,omitempty"`
	RequestRef        string  `json:"requestRef,omitempty"`
	RequestPayload    string  `json:"requestPayload,omitempty"`
	PreviousStateJSON string  `json:"previousStateJson,omitempty"`
}

type llmDecisionOutputPayload struct {
	DecisionID         string  `json:"decisionId,omitempty"`
	Stage              string  `json:"stage,omitempty"`
	Label              string  `json:"label,omitempty"`
	Confidence         float64 `json:"confidence,omitempty"`
	ResponseRef        string  `json:"responseRef,omitempty"`
	ResponsePayload    string  `json:"responsePayload,omitempty"`
	RawResponse        string  `json:"rawResponse,omitempty"`
	TransitionOutcome  string  `json:"transitionOutcome,omitempty"`
	TransitionToStep   string  `json:"transitionToStep,omitempty"`
	TransitionTerminal bool    `json:"transitionTerminal,omitempty"`
	UpdatedStateJSON   string  `json:"updatedStateJson,omitempty"`
	EvidenceDeltaJSON  string  `json:"evidenceDeltaJson,omitempty"`
	ConflictsJSON      string  `json:"conflictsJson,omitempty"`
	FinalOutcome       string  `json:"finalOutcome,omitempty"`
}

func marshalLLMDecisionLogPayloads(item LLMDecision) ([]byte, []byte, error) {
	inputJSON, err := json.Marshal(llmDecisionInputPayload{
		RunID:             item.RunID,
		ChunkCapturedAt:   item.ChunkCapturedAt,
		PromptVersionID:   item.PromptVersionID,
		PromptText:        item.PromptText,
		Model:             item.Model,
		Temperature:       item.Temperature,
		MaxTokens:         item.MaxTokens,
		TimeoutMS:         item.TimeoutMS,
		ChunkRef:          item.ChunkRef,
		RequestRef:        item.RequestRef,
		RequestPayload:    item.RequestPayload,
		PreviousStateJSON: item.PreviousStateJSON,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal llm decision input_json: %w", err)
	}
	outputJSON, err := json.Marshal(llmDecisionOutputPayload{
		DecisionID:         item.ID,
		Stage:              item.Stage,
		Label:              item.Label,
		Confidence:         item.Confidence,
		ResponseRef:        item.ResponseRef,
		ResponsePayload:    item.ResponsePayload,
		RawResponse:        item.RawResponse,
		TransitionOutcome:  item.TransitionOutcome,
		TransitionToStep:   item.TransitionToStep,
		TransitionTerminal: item.TransitionTerminal,
		UpdatedStateJSON:   item.UpdatedStateJSON,
		EvidenceDeltaJSON:  item.EvidenceDeltaJSON,
		ConflictsJSON:      item.ConflictsJSON,
		FinalOutcome:       item.FinalOutcome,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal llm decision output_json: %w", err)
	}
	return inputJSON, outputJSON, nil
}

func normalizeDecisionForPostgres(item LLMDecision) LLMDecision {
	item.ID = strings.TrimSpace(item.ID)
	item.RunID = strings.TrimSpace(item.RunID)
	item.StreamerID = strings.TrimSpace(item.StreamerID)
	item.Stage = strings.TrimSpace(item.Stage)
	item.Label = strings.TrimSpace(item.Label)
	item.PromptVersionID = strings.TrimSpace(item.PromptVersionID)
	item.PromptText = strings.TrimSpace(item.PromptText)
	item.Model = strings.TrimSpace(item.Model)
	item.ChunkRef = strings.TrimSpace(item.ChunkRef)
	item.RequestRef = strings.TrimSpace(item.RequestRef)
	item.ResponseRef = strings.TrimSpace(item.ResponseRef)
	item.RequestPayload = strings.TrimSpace(item.RequestPayload)
	item.ResponsePayload = strings.TrimSpace(item.ResponsePayload)
	item.RawResponse = strings.TrimSpace(item.RawResponse)
	item.TransitionOutcome = strings.TrimSpace(item.TransitionOutcome)
	item.TransitionToStep = strings.TrimSpace(item.TransitionToStep)
	item.PreviousStateJSON = strings.TrimSpace(item.PreviousStateJSON)
	item.UpdatedStateJSON = strings.TrimSpace(item.UpdatedStateJSON)
	item.EvidenceDeltaJSON = strings.TrimSpace(item.EvidenceDeltaJSON)
	item.ConflictsJSON = strings.TrimSpace(item.ConflictsJSON)
	item.FinalOutcome = strings.TrimSpace(item.FinalOutcome)
	item.CreatedAt = strings.TrimSpace(item.CreatedAt)
	return item
}

func nullableTimeFromString(value string) sql.NullTime {
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
