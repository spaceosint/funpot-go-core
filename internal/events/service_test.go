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

func TestUpdateSettingsStoresNicknameChangeCost(t *testing.T) {
	svc := NewService(nil)
	updated, err := svc.UpdateSettings(Settings{VotePlatformFeePercent: 5, NicknameChangeCostINT: 42})
	if err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	if updated.NicknameChangeCostINT != 42 {
		t.Fatalf("expected nickname cost 42, got %d", updated.NicknameChangeCostINT)
	}
	if got := svc.Settings().NicknameChangeCostINT; got != 42 {
		t.Fatalf("expected stored nickname cost 42, got %d", got)
	}
}

func TestListUserHistoryReturnsLatestFirstWithoutDuplicatesForIdempotentVotes(t *testing.T) {
	svc := NewService([]LiveEvent{
		{
			ID:         "evt-1",
			StreamerID: "s-1",
			ScenarioID: "scenario-1",
			TerminalID: "terminal-1",
			Title:      map[string]string{"ru": "Победитель карты"},
			ClosesAt:   time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
			Totals: map[string]int64{
				"a": 0,
				"b": 0,
			},
			Options: []Option{
				{ID: "a", Title: map[string]string{"ru": "A"}},
				{ID: "b", Title: map[string]string{"ru": "B"}},
			},
		},
	})
	if _, err := svc.Vote(context.Background(), VoteRequest{
		EventID:        "evt-1",
		StreamerID:     "s-1",
		UserID:         "u-1",
		OptionID:       "a",
		Amount:         100,
		IdempotencyKey: "vote-1",
	}); err != nil {
		t.Fatalf("Vote() error = %v", err)
	}
	if _, err := svc.Vote(context.Background(), VoteRequest{
		EventID:        "evt-1",
		StreamerID:     "s-1",
		UserID:         "u-1",
		OptionID:       "a",
		Amount:         100,
		IdempotencyKey: "vote-1",
	}); err != nil {
		t.Fatalf("Vote() idempotency replay error = %v", err)
	}
	if _, err := svc.Vote(context.Background(), VoteRequest{
		EventID:        "evt-1",
		StreamerID:     "s-1",
		UserID:         "u-1",
		OptionID:       "b",
		Amount:         50,
		IdempotencyKey: "vote-2",
	}); err != nil {
		t.Fatalf("Vote() second vote error = %v", err)
	}

	history := svc.ListUserHistory(context.Background(), "u-1")
	if len(history) != 2 {
		t.Fatalf("expected history length 2, got %d", len(history))
	}
	if history[0].OptionID != "b" || history[0].AmountINT != 50 {
		t.Fatalf("expected latest history item for option b amount 50, got %+v", history[0])
	}
	if history[1].OptionID != "a" || history[1].AmountINT != 100 {
		t.Fatalf("expected oldest history item for option a amount 100, got %+v", history[1])
	}
	if history[0].Coefficient <= 0 || history[1].Coefficient <= 0 {
		t.Fatalf("expected positive coefficients, got latest=%f oldest=%f", history[0].Coefficient, history[1].Coefficient)
	}
	if history[0].PotentialWinINT <= 0 || history[1].PotentialWinINT <= 0 {
		t.Fatalf("expected positive potential wins, got latest=%d oldest=%d", history[0].PotentialWinINT, history[1].PotentialWinINT)
	}
}

type stubSettingsStore struct {
	loaded Settings
	ok     bool
	saved  Settings
}

func (s *stubSettingsStore) Load(_ context.Context) (Settings, bool, error) {
	return s.loaded, s.ok, nil
}
func (s *stubSettingsStore) Save(_ context.Context, settings Settings) error {
	s.saved = settings
	return nil
}

func TestConfigureSettingsPersistenceLoadsExistingSettings(t *testing.T) {
	svc := NewService(nil)
	store := &stubSettingsStore{ok: true, loaded: Settings{VotePlatformFeePercent: 12.5, NicknameChangeCostINT: 15, WeeklyRewardByDayINT: [7]int64{1, 2, 3, 4, 5, 6, 7}}}
	if err := svc.ConfigureSettingsPersistence(context.Background(), store); err != nil {
		t.Fatalf("ConfigureSettingsPersistence() error = %v", err)
	}
	got := svc.Settings()
	if got.VotePlatformFeePercent != 12.5 || got.NicknameChangeCostINT != 15 || got.WeeklyRewardByDayINT[6] != 7 {
		t.Fatalf("unexpected loaded settings: %+v", got)
	}
}

func TestUpdateSettingsPersistsToStore(t *testing.T) {
	svc := NewService(nil)
	store := &stubSettingsStore{}
	if err := svc.ConfigureSettingsPersistence(context.Background(), store); err != nil {
		t.Fatalf("ConfigureSettingsPersistence() error = %v", err)
	}
	updated, err := svc.UpdateSettings(Settings{VotePlatformFeePercent: 17, NicknameChangeCostINT: 40})
	if err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	if store.saved.VotePlatformFeePercent != 17 || store.saved.NicknameChangeCostINT != 40 {
		t.Fatalf("expected persisted settings, got %+v", store.saved)
	}
	if updated.VotePlatformFeePercent != 17 {
		t.Fatalf("unexpected updated settings: %+v", updated)
	}
}
