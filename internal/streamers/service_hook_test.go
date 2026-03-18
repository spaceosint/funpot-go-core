package streamers

import (
	"context"
	"errors"
	"testing"
)

func TestSubmitCallsSubmissionHook(t *testing.T) {
	svc := NewService()
	called := false
	svc.SetSubmissionHook(func(_ context.Context, streamerID string) error {
		called = streamerID != ""
		return nil
	})

	_, err := svc.Submit(context.Background(), "stream_name", "user-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if !called {
		t.Fatalf("expected submission hook to be called")
	}
	status := svc.GetLLMStatus(context.Background(), svc.List(context.Background(), "", "", 1)[0].ID)
	if status.State != "active" {
		t.Fatalf("expected active analysis status after successful hook, got %q", status.State)
	}
	if status.UpdatedAt == "" {
		t.Fatal("expected status updated timestamp after successful hook")
	}
}

func TestSubmitRollsBackOnHookError(t *testing.T) {
	svc := NewService()
	svc.SetSubmissionHook(func(_ context.Context, _ string) error {
		return errors.New("scheduler unavailable")
	})

	_, err := svc.Submit(context.Background(), "stream_name", "user-1")
	if err == nil {
		t.Fatalf("expected error from hook")
	}
	if got := len(svc.List(context.Background(), "", "", 1)); got != 0 {
		t.Fatalf("expected rollback of submission, got %d streamers", got)
	}
}

func TestResolveStreamlinkChannel(t *testing.T) {
	svc := NewService()
	result, err := svc.Submit(context.Background(), "stream_name", "user-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	channel, err := svc.ResolveStreamlinkChannel(context.Background(), result.ID)
	if err != nil {
		t.Fatalf("ResolveStreamlinkChannel() error = %v", err)
	}
	if channel != "stream_name" {
		t.Fatalf("channel = %q, want stream_name", channel)
	}
}

func TestMarkAnalysisInactiveResetsIdleStatusWithoutDecisions(t *testing.T) {
	svc := NewService()
	result, err := svc.Submit(context.Background(), "stream_name", "user-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	svc.MarkAnalysisInactive(result.ID)

	status := svc.GetLLMStatus(context.Background(), result.ID)
	if status.State != "idle" {
		t.Fatalf("expected idle status after deactivation, got %q", status.State)
	}
}
