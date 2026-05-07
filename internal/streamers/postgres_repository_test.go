package streamers

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

func TestServiceSubmitPersistsPostgresStreamerWithUUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close() //nolint:errcheck

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id::text
FROM streamers
WHERE lower(twitch_username) = lower($1)
FOR UPDATE`)).
		WithArgs("best_streamer").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(regexp.QuoteMeta(`
INSERT INTO streamers (id, twitch_username, display_name, status, metadata)
VALUES ($1, $2, $3, $4, $5::jsonb)`)).
		WithArgs(sqlmock.AnyArg(), "best_streamer", "Best Streamer", "active", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	svc := NewServiceWithValidator(validatorStub{displayName: "Best Streamer", miniIconURL: "https://cdn.twitch.tv/icon.png"})
	svc.SetStreamerRepository(NewPostgresStreamerRepository(db))
	var hookStreamerID string
	svc.SetSubmissionHook(func(_ context.Context, streamerID string) error {
		hookStreamerID = streamerID
		return nil
	})

	sub, err := svc.Submit(context.Background(), "Best_Streamer", "user-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if _, err := uuid.Parse(sub.ID); err != nil {
		t.Fatalf("expected UUID streamer id, got %q: %v", sub.ID, err)
	}
	if hookStreamerID != sub.ID {
		t.Fatalf("submission hook streamerID = %q, want %q", hookStreamerID, sub.ID)
	}
	items := svc.List(context.Background(), "best", "pending", 1)
	if len(items) != 1 || items[0].ID != sub.ID {
		t.Fatalf("expected cached submitted streamer after db insert, got %#v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestPostgresStreamerRepositoryMapsMetadataSubmissionStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close() //nolint:errcheck

	id := uuid.NewString()
	metadata, err := json.Marshal(streamerMetadata{
		Platform:         "twitch",
		MiniIconURL:      "https://cdn.twitch.tv/icon.png",
		Online:           true,
		Viewers:          321,
		AddedBy:          "user-1",
		SubmissionStatus: "pending",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id::text, twitch_username, display_name, status, metadata
FROM streamers
WHERE id = $1`)).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"id", "twitch_username", "display_name", "status", "metadata"}).
			AddRow(id, "best_streamer", "Best Streamer", "active", metadata))

	repo := NewPostgresStreamerRepository(db)
	item, ok, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if !ok {
		t.Fatalf("expected streamer to be found")
	}
	if item.Status != "pending" || item.Platform != "twitch" || !item.Online || item.Viewers != 321 || item.AddedBy != "user-1" || item.MiniIconURL == "" {
		t.Fatalf("unexpected mapped streamer: %#v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestPostgresDecisionRepositoryRecordsDecisionAndRequestLog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close() //nolint:errcheck

	mock.ExpectExec("INSERT INTO llm_request_logs").
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := NewPostgresDecisionRepository(db)
	err = repo.RecordLLMDecision(context.Background(), LLMDecision{
		ID:               "llm_1",
		RunID:            "run-1",
		StreamerID:       uuid.NewString(),
		Stage:            "cs2_mode",
		Label:            "competitive",
		Confidence:       0.91,
		ChunkCapturedAt:  "2026-05-07T10:00:00Z",
		PromptVersionID:  "prompt-1",
		PromptText:       "detect mode",
		Model:            "gemini-2.0-flash",
		Temperature:      0.2,
		MaxTokens:        512,
		TimeoutMS:        30000,
		ChunkRef:         "https://player.mediadelivery.net/video.mp4",
		RequestRef:       "req-1",
		ResponseRef:      "200",
		RequestPayload:   `{"contents":[]}`,
		ResponsePayload:  `{"candidates":[]}`,
		RawResponse:      `{"mode":"competitive"}`,
		TokensIn:         111,
		TokensOut:        22,
		LatencyMS:        1234,
		UpdatedStateJSON: `{"mode":"competitive"}`,
		CreatedAt:        "2026-05-07T10:00:01Z",
	})
	if err != nil {
		t.Fatalf("RecordLLMDecision() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestPostgresDecisionRepositoryListsAndDeletesHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close() //nolint:errcheck

	streamerID := uuid.NewString()
	createdAt := time.Date(2026, 5, 7, 10, 0, 1, 0, time.UTC)
	inputJSON := `{"runId":"run-1","chunkCapturedAt":"2026-05-07T10:00:00Z","promptVersionId":"prompt-1","promptText":"detect mode","model":"gemini-2.0-flash","temperature":0.2,"maxTokens":512,"timeoutMs":30000,"chunkRef":"video-url","requestRef":"req-1","requestPayload":"{\\\"contents\\\":[]}","previousStateJson":"{\\\"game\\\":\\\"cs2\\\"}"}`
	outputJSON := `{"decisionId":"llm_1","stage":"cs2_mode","label":"competitive","confidence":0.91,"responseRef":"200","responsePayload":"{\\\"candidates\\\":[]}","rawResponse":"{\\\"mode\\\":\\\"competitive\\\"}","transitionOutcome":"competitive","transitionToStep":"cs2_match","updatedStateJson":"{\\\"mode\\\":\\\"competitive\\\"}","evidenceDeltaJson":"{}","conflictsJson":"{}"}`
	mock.ExpectQuery("SELECT id::text, streamer_id::text, request_type").
		WithArgs(streamerID, 10).
		WillReturnRows(llmDecisionRows().AddRow(
			"log-1", streamerID, "cs2_mode", "llm_1",
			[]byte(inputJSON), []byte(outputJSON), 111, 22,
			int64(1234), createdAt,
		))

	repo := NewPostgresDecisionRepository(db)
	items, err := repo.ListLLMDecisions(context.Background(), streamerID, 10)
	if err != nil {
		t.Fatalf("ListLLMDecisions() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "llm_1" || items[0].StreamerID != streamerID || items[0].TokensIn != 111 {
		t.Fatalf("unexpected decisions: %#v", items)
	}
	if items[0].CreatedAt != "2026-05-07T10:00:01Z" || items[0].ChunkCapturedAt != "2026-05-07T10:00:00Z" {
		t.Fatalf("unexpected timestamps: %#v", items[0])
	}
	if items[0].RunID != "run-1" || items[0].Label != "competitive" || items[0].UpdatedStateJSON != `{\"mode\":\"competitive\"}` {
		t.Fatalf("unexpected reconstructed decision: %#v", items[0])
	}

	mock.ExpectExec("DELETE FROM llm_request_logs").
		WithArgs(streamerID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	deleted, err := repo.DeleteAllLLMDecisions(context.Background(), streamerID)
	if err != nil {
		t.Fatalf("DeleteAllLLMDecisions() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func llmDecisionRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "streamer_id", "request_type", "provider_request_id",
		"input_json", "output_json", "prompt_tokens", "completion_tokens",
		"latency_ms", "created_at",
	})
}
