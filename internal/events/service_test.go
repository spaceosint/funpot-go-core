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

func TestVoteAppliesPlatformFeeToDistributablePool(t *testing.T) {
	svc := NewService([]LiveEvent{
		{
			ID:         "evt-1",
			StreamerID: "s-1",
			ClosesAt:   time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
			Totals:     map[string]int64{"a": 0},
			Options:    []Option{{ID: "a", Title: map[string]string{"ru": "A"}}},
		},
	})
	if _, err := svc.UpdateSettings(Settings{VotePlatformFeePercent: 10}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	event, err := svc.Vote(context.Background(), VoteRequest{
		EventID:        "evt-1",
		StreamerID:     "s-1",
		UserID:         "u-1",
		OptionID:       "a",
		Amount:         100,
		IdempotencyKey: "vote-1",
	})
	if err != nil {
		t.Fatalf("Vote() error = %v", err)
	}
	if event.Totals["a"] != 90 {
		t.Fatalf("expected net option total 90, got %d", event.Totals["a"])
	}
	if event.TotalContributed != 100 {
		t.Fatalf("expected total contributed 100, got %d", event.TotalContributed)
	}
	if event.PlatformFeeINT != 10 {
		t.Fatalf("expected platform fee 10, got %d", event.PlatformFeeINT)
	}
	if event.DistributableINT != 90 {
		t.Fatalf("expected distributable 90, got %d", event.DistributableINT)
	}
}

func TestCalculateAccrualINT(t *testing.T) {
	got := CalculateAccrualINT(1000, 100, 450, 90)
	if got != 180 {
		t.Fatalf("expected accrual 180, got %d", got)
	}
}
