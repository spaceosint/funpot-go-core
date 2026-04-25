package events

import (
	"context"
	"testing"
	"time"
)

func TestListLiveByStreamer(t *testing.T) {
	svc := NewService([]LiveEvent{
		{ID: "evt-1", StreamerID: "s-1", ClosesAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)},
		{ID: "evt-2", StreamerID: "s-2", ClosesAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)},
		{ID: "evt-3", StreamerID: "s-1", ClosesAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)},
	})

	items := svc.ListLiveByStreamer(context.Background(), "s-1")
	if len(items) != 2 {
		t.Fatalf("expected 2 events, got %d", len(items))
	}
}

func TestCreateLiveEventAvoidsDuplicateActiveByTemplate(t *testing.T) {
	svc := NewService(nil)
	req := CreateLiveEventRequest{
		StreamerID:      "s-1",
		ScenarioID:      "gs-1",
		TerminalID:      "term-1",
		DefaultLanguage: "ru",
		Title:           map[string]string{"ru": "Победитель карты"},
		Options: []Option{
			{ID: "opt-1", Title: map[string]string{"ru": "Команда A"}},
			{ID: "opt-2", Title: map[string]string{"ru": "Команда B"}},
		},
		Duration: time.Minute,
	}
	if _, err := svc.CreateLiveEvent(context.Background(), req); err != nil {
		t.Fatalf("CreateLiveEvent() error = %v", err)
	}
	_, err := svc.CreateLiveEvent(context.Background(), req)
	if err == nil {
		t.Fatalf("expected duplicate active event error")
	}
}
