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
			if items[0].Username != "best_streamer" {
				t.Fatalf("unexpected username: %s", items[0].Username)
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

	_, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "stage_a", Label: "yes", Confidence: 0.9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now = now.Add(time.Second)
	second, err := svc.RecordLLMDecision(context.Background(), RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "stage_b", Label: "competitive", Confidence: 0.8})
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
}

func TestRecordLLMDecisionValidation(t *testing.T) {
	tests := []struct {
		name string
		req  RecordDecisionRequest
	}{
		{name: "missing streamer", req: RecordDecisionRequest{RunID: "run-1", Stage: "stage_a", Label: "yes", Confidence: 0.9}},
		{name: "missing run", req: RecordDecisionRequest{StreamerID: "str-1", Stage: "stage_a", Label: "yes", Confidence: 0.9}},
		{name: "invalid stage", req: RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "bad", Label: "yes", Confidence: 0.9}},
		{name: "missing label", req: RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "stage_a", Confidence: 0.9}},
		{name: "invalid confidence", req: RecordDecisionRequest{RunID: "run-1", StreamerID: "str-1", Stage: "stage_a", Label: "yes", Confidence: 1.9}},
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
