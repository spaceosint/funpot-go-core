package events

import (
	"context"
	"testing"
)

func TestListLiveByStreamer(t *testing.T) {
	svc := NewService([]LiveEvent{
		{ID: "evt-1", StreamerID: "s-1"},
		{ID: "evt-2", StreamerID: "s-2"},
		{ID: "evt-3", StreamerID: "s-1"},
	})

	items := svc.ListLiveByStreamer(context.Background(), "s-1")
	if len(items) != 2 {
		t.Fatalf("expected 2 events, got %d", len(items))
	}
}
