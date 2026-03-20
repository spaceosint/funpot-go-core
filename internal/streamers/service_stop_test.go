package streamers

import (
	"context"
	"errors"
	"testing"
)

func TestStopTrackingMarksStreamerStoppedAndCallsHook(t *testing.T) {
	svc := NewService()
	if _, err := svc.Submit(context.Background(), "streamername", "user-1"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	items := svc.List(context.Background(), "streamername", "pending", 1)
	if len(items) != 1 {
		t.Fatalf("expected one streamer, got %d", len(items))
	}

	called := false
	svc.SetTrackingStopHook(func(_ context.Context, streamerID string) error {
		called = streamerID == items[0].ID
		return nil
	})

	if err := svc.StopTracking(context.Background(), items[0].ID); err != nil {
		t.Fatalf("StopTracking() error = %v", err)
	}
	if !called {
		t.Fatal("expected stop hook to be called")
	}

	status := svc.GetLLMStatus(context.Background(), items[0].ID)
	if status.State != "stopped" {
		t.Fatalf("expected stopped state, got %q", status.State)
	}
}

func TestStopTrackingReturnsNotFoundForUnknownStreamer(t *testing.T) {
	svc := NewService()
	if err := svc.StopTracking(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
