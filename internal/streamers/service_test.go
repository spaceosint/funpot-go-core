package streamers

import (
	"context"
	"errors"
	"testing"
	"time"
)

type validatorStub struct {
	displayName string
	err         error
}

func (v validatorStub) ValidateUsername(_ context.Context, _ string) (string, error) {
	if v.err != nil {
		return "", v.err
	}
	return v.displayName, nil
}

func TestServiceSubmitValidationAndListing(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		validator     TwitchValidator
		expectedError error
	}{
		{name: "empty username", username: "", expectedError: ErrInvalidUsername},
		{name: "validator failure", username: "bad@user", validator: validatorStub{err: errors.New("not found")}, expectedError: ErrTwitchUnavailable},
		{name: "success", username: "Best_Streamer", validator: validatorStub{displayName: "Best Streamer"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewServiceWithValidator(tt.validator)
			sub, err := svc.Submit(context.Background(), tt.username, "user-1")
			if tt.expectedError != nil {
				if !errors.Is(err, tt.expectedError) {
					t.Fatalf("expected error %v, got %v", tt.expectedError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if sub.Status != "pending" {
				t.Fatalf("expected pending status, got %s", sub.Status)
			}

			items := svc.List(context.Background(), "best", "pending", 1)
			if len(items) != 1 {
				t.Fatalf("expected one result, got %d", len(items))
			}
			if items[0].TwitchNickname != "best_streamer" {
				t.Fatalf("unexpected twitch nickname: %s", items[0].TwitchNickname)
			}
		})
	}
}

func TestServiceSubmitRateLimit(t *testing.T) {
	svc := NewServiceWithValidator(validatorStub{displayName: "Display"})
	clock := time.Now().UTC()
	svc.nowFn = func() time.Time { return clock }

	for i := 0; i < 3; i++ {
		if _, err := svc.Submit(context.Background(), "streamername", "user-1"); err != nil {
			t.Fatalf("unexpected error on submission %d: %v", i+1, err)
		}
	}

	if _, err := svc.Submit(context.Background(), "streamername", "user-1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit error, got %v", err)
	}

	clock = clock.Add(time.Minute + time.Second)
	if _, err := svc.Submit(context.Background(), "streamername", "user-1"); err != nil {
		t.Fatalf("expected limiter reset after window, got %v", err)
	}
}

func TestRecordAndListLLMDecisions(t *testing.T) {
	svc := NewService()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	_, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "detector", Label: "yes", Confidence: 0.9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now = now.Add(time.Second)
	second, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{
		RunID:              "run-1",
		StreamerID:         "str-1",
		Stage:              "ranked_mode",
		Label:              "competitive",
		Confidence:         0.8,
		ChunkCapturedAt:    now,
		PromptVersionID:    "prompt-2",
		PromptText:         "classify match type",
		Model:              "gemini-2.0-flash",
		Temperature:        0.1,
		MaxTokens:          500,
		TimeoutMS:          2500,
		ChunkRef:           "streamlink://str-1/123",
		RequestRef:         "gemini-request-1",
		ResponseRef:        "gemini-response-1",
		RawResponse:        "{\"label\":\"competitive\"}",
		TokensIn:           144,
		TokensOut:          27,
		LatencyMS:          180,
		TransitionOutcome:  "competitive",
		TransitionToStep:   "wait_for_result",
		TransitionTerminal: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := svc.ListLLMDecisions(context.Background(), "str-1", 1)
	if len(items) != 1 {
		t.Fatalf("expected one result, got %d", len(items))
	}
	if items[0].ID != second.ID {
		t.Fatalf("expected latest decision first, got %s", items[0].ID)
	}
	if items[0].PromptVersionID != "prompt-2" || items[0].ChunkRef == "" || items[0].LatencyMS != 180 || items[0].RequestRef == "" || items[0].TransitionToStep != "wait_for_result" {
		t.Fatalf("expected metadata to be persisted, got %#v", items[0])
	}
	if items[0].ID == "" || items[0].ID[:4] != "llm_" {
		t.Fatalf("expected generated llm id, got %q", items[0].ID)
	}
}

func TestRecordLLMDecisionGeneratesUniqueIDs(t *testing.T) {
	svc := NewService()

	first, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{
		RunID:      "run-1",
		StreamerID: "str-1",
		Stage:      "detector",
		Label:      "counter_strike",
		Confidence: 0.95,
	})
	if err != nil {
		t.Fatalf("first RecordLLMDecision() error = %v", err)
	}

	second, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{
		RunID:      "run-2",
		StreamerID: "str-1",
		Stage:      "detector",
		Label:      "valorant",
		Confidence: 0.91,
	})
	if err != nil {
		t.Fatalf("second RecordLLMDecision() error = %v", err)
	}

	if first.ID == second.ID {
		t.Fatalf("expected unique ids, got %q and %q", first.ID, second.ID)
	}
	if first.ID == "" || second.ID == "" || first.ID[:4] != "llm_" || second.ID[:4] != "llm_" {
		t.Fatalf("expected llm-prefixed ids, got %q and %q", first.ID, second.ID)
	}
}

func TestRecordLLMDecisionValidation(t *testing.T) {
	tests := []struct {
		name string
		req  RecordDecisionRequest
	}{
		{name: "missing streamer", req: RecordDecisionRequest{RunID: "run-1", Stage: "detector", Label: "yes", Confidence: 0.9}},
		{name: "missing run", req: RecordDecisionRequest{StreamerID: "str-1", Stage: "detector", Label: "yes", Confidence: 0.9}},
		{name: "missing stage", req: RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "   ", Label: "yes", Confidence: 0.9}},
		{name: "missing label", req: RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "detector", Confidence: 0.9}},
		{name: "invalid confidence", req: RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "detector", Label: "yes", Confidence: 1.9}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService()
			if _, err := svc.RecordLLMDecision(context.Background(), tt.req); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestGetLLMStatusAggregatesLatestStageSnapshots(t *testing.T) {
	svc := NewService()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	if _, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "detector", Label: "cs_detected", Confidence: 0.9}); err != nil {
		t.Fatalf("unexpected error recording stage_a: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "ranked_mode", Label: "competitive", Confidence: 0.8}); err != nil {
		t.Fatalf("unexpected error recording stage_b: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-2", StreamerID: "str-1", Stage: "detector", Label: "uncertain", Confidence: 0.4}); err != nil {
		t.Fatalf("unexpected error recording second stage_a: %v", err)
	}

	status := svc.GetLLMStatus(context.Background(), "str-1")
	if status.State != "active" {
		t.Fatalf("expected active state, got %#v", status)
	}
	if status.CurrentRunID != "run-2" || status.CurrentStage != "detector" || status.CurrentLabel != "uncertain" {
		t.Fatalf("unexpected current status: %#v", status)
	}
	if status.DetectedGameKey != "counter_strike" {
		t.Fatalf("expected detected game from latest positive stage_a, got %#v", status)
	}
	if len(status.LatestByStage) != 2 {
		t.Fatalf("expected two stage snapshots, got %#v", status.LatestByStage)
	}
	if status.LatestByStage[0].Stage != "detector" || status.LatestByStage[1].Stage != "ranked_mode" {
		t.Fatalf("expected snapshots ordered by stage, got %#v", status.LatestByStage)
	}
}

func TestRecordLLMDecisionAllowsCustomStageAndStatusIncludesIt(t *testing.T) {
	svc := NewService()
	if _, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "ranked_mode", Label: "competitive", Confidence: 0.9}); err != nil {
		t.Fatalf("RecordLLMDecision() error = %v", err)
	}
	status := svc.GetLLMStatus(context.Background(), "str-1")
	if len(status.LatestByStage) != 1 {
		t.Fatalf("len(LatestByStage) = %d, want 1", len(status.LatestByStage))
	}
	if status.LatestByStage[0].Stage != "ranked_mode" {
		t.Fatalf("stage = %q, want ranked_mode", status.LatestByStage[0].Stage)
	}
}

func TestGetLLMHistoryReturnsLatestStateUpdatesAndFinalDecisions(t *testing.T) {
	svc := NewService()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	if _, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{
		RunID:            "run-1",
		StreamerID:       "str-1",
		Stage:            "match_update",
		Label:            "awaiting_changes",
		Confidence:       0.7,
		UpdatedStateJSON: `{"score":"5:4"}`,
	}); err != nil {
		t.Fatalf("first RecordLLMDecision() error = %v", err)
	}
	now = now.Add(time.Second)
	if _, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{
		RunID:              "run-1",
		StreamerID:         "str-1",
		Stage:              "match_finalize",
		Label:              "finalized",
		Confidence:         0.9,
		UpdatedStateJSON:   `{"score":"13:11"}`,
		FinalOutcome:       "win",
		TransitionTerminal: true,
	}); err != nil {
		t.Fatalf("second RecordLLMDecision() error = %v", err)
	}

	history := svc.GetLLMHistory(context.Background(), "str-1", 1)
	if len(history.LatestStateUpdates) != 1 {
		t.Fatalf("len(LatestStateUpdates) = %d, want 1", len(history.LatestStateUpdates))
	}
	if history.LatestStateUpdates[0].Stage != "match_finalize" {
		t.Fatalf("latest update stage = %q, want match_finalize", history.LatestStateUpdates[0].Stage)
	}
	if len(history.FinalDecisions) != 1 {
		t.Fatalf("len(FinalDecisions) = %d, want 1", len(history.FinalDecisions))
	}
	if history.FinalDecisions[0].FinalOutcome != "win" {
		t.Fatalf("final outcome = %q, want win", history.FinalDecisions[0].FinalOutcome)
	}
}
