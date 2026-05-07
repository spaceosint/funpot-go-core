package events

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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

func TestPostgresCreateLiveEventPersistsHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db, nil)
	activeRows := sqlmock.NewRows([]string{"id", "streamer_id", "scenario_id", "template_id", "transition_id", "terminal_id", "title_json", "options_json", "final_totals_json", "status", "opened_at", "closes_at", "metadata"})
	mock.ExpectQuery("SELECT id, streamer_id, scenario_id, template_id").
		WithArgs("00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000001:terminal-1", sqlmock.AnyArg()).
		WillReturnRows(activeRows)
	mock.ExpectExec("INSERT INTO live_event_history").
		WithArgs(sqlmock.AnyArg(), "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000001:terminal-1", "transition-1", "terminal-1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "open", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	created, err := svc.CreateLiveEvent(context.Background(), CreateLiveEventRequest{
		StreamerID:      "00000000-0000-0000-0000-000000000001",
		ScenarioID:      "00000000-0000-0000-0000-000000000002",
		TransitionID:    "transition-1",
		TerminalID:      "terminal-1",
		Title:           map[string]string{"ru": "Победитель карты"},
		DefaultLanguage: "ru",
		Options:         []Option{{ID: "ct", Title: map[string]string{"ru": "CT"}}},
		Duration:        time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateLiveEvent() error = %v", err)
	}
	if created.TemplateID != "00000000-0000-0000-0000-000000000001:terminal-1" || created.Status != "open" {
		t.Fatalf("unexpected created event: %+v", created)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet db expectations: %v", err)
	}
}

func TestPostgresVotePersistsVoteHistoryAndTotals(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	eventID := "00000000-0000-0000-0000-000000000011"
	streamerID := "00000000-0000-0000-0000-000000000012"
	scenarioID := "00000000-0000-0000-0000-000000000013"
	ledgerID := "00000000-0000-0000-0000-000000000014"
	now := time.Now().UTC()
	svc := NewPostgresService(db, nil)
	mock.ExpectQuery("SELECT event_id, option_id, amount_int FROM live_event_vote_history").
		WithArgs("vote-1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id, streamer_id, scenario_id, template_id").
		WithArgs(eventID, streamerID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "streamer_id", "scenario_id", "template_id", "transition_id", "terminal_id", "title_json", "options_json", "final_totals_json", "status", "opened_at", "closes_at", "metadata"}).
			AddRow(eventID, streamerID, scenarioID, streamerID+":terminal-1", "transition-1", "terminal-1", []byte(`{"ru":"Победитель карты"}`), []byte(`[{"id":"ct","title":{"ru":"CT"}}]`), []byte(`{"ct":0}`), "open", now, now.Add(time.Minute), []byte(`{"defaultLanguage":"ru"}`)))
	mock.ExpectExec("UPDATE live_event_history SET final_totals_json").
		WithArgs(eventID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO live_event_vote_history").
		WithArgs(eventID, "00000000-0000-0000-0000-000000000015", "ct", int64(100), ledgerID, "vote-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event, err := svc.Vote(context.Background(), VoteRequest{
		EventID:        eventID,
		StreamerID:     streamerID,
		UserID:         "00000000-0000-0000-0000-000000000015",
		OptionID:       "ct",
		Amount:         100,
		IdempotencyKey: "vote-1",
		WalletLedgerID: ledgerID,
	})
	if err != nil {
		t.Fatalf("Vote() error = %v", err)
	}
	if event.Totals["ct"] != 100 || event.TotalContributed != 100 {
		t.Fatalf("unexpected vote totals: %+v", event)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet db expectations: %v", err)
	}
}

func TestRedisLiveEventCreateUsesRedisBeforePostgresHistory(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer redisClient.Close() //nolint:errcheck

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	svc := NewPostgresService(db, nil)
	svc.WithRedisLiveState(redisClient, time.Hour)

	streamerID := "00000000-0000-0000-0000-000000000101"
	scenarioID := "00000000-0000-0000-0000-000000000102"
	mock.ExpectExec("INSERT INTO live_event_history").
		WithArgs(sqlmock.AnyArg(), streamerID, scenarioID, streamerID+":terminal-redis", "transition-redis", "terminal-redis", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "open", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	created, err := svc.CreateLiveEvent(context.Background(), CreateLiveEventRequest{
		StreamerID:      streamerID,
		ScenarioID:      scenarioID,
		TransitionID:    "transition-redis",
		TerminalID:      "terminal-redis",
		Title:           map[string]string{"ru": "Победитель карты"},
		DefaultLanguage: "ru",
		Options:         []Option{{ID: "ct", Title: map[string]string{"ru": "CT"}}},
		Duration:        time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateLiveEvent() error = %v", err)
	}
	if _, ok := svc.readLiveEventStateFromRedis(context.Background(), created.ID); !ok {
		t.Fatalf("expected event state to be written to Redis")
	}

	if _, err := svc.CreateLiveEvent(context.Background(), CreateLiveEventRequest{
		StreamerID:      streamerID,
		ScenarioID:      scenarioID,
		TransitionID:    "transition-redis",
		TerminalID:      "terminal-redis",
		Title:           map[string]string{"ru": "Победитель карты"},
		DefaultLanguage: "ru",
		Options:         []Option{{ID: "ct", Title: map[string]string{"ru": "CT"}}},
		Duration:        time.Minute,
	}); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("expected duplicate active event from Redis, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet db expectations: %v", err)
	}
}

func TestListLiveByStreamerUsesRedisAsActiveSourceWhenPostgresExists(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer redisClient.Close() //nolint:errcheck

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	svc := NewPostgresService(db, nil)
	svc.WithRedisLiveState(redisClient, time.Hour)
	event := LiveEvent{
		ID:              "event-redis-list",
		TemplateID:      "00000000-0000-0000-0000-000000000201:terminal-1",
		StreamerID:      "00000000-0000-0000-0000-000000000201",
		ScenarioID:      "00000000-0000-0000-0000-000000000202",
		TerminalID:      "terminal-1",
		Title:           map[string]string{"ru": "Победитель карты"},
		DefaultLanguage: "ru",
		Options:         []Option{{ID: "ct", Title: map[string]string{"ru": "CT"}}},
		ClosesAt:        time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Status:          "open",
		Totals:          map[string]int64{"ct": 0},
	}
	if err := svc.persistLiveState(context.Background(), event); err != nil {
		t.Fatalf("persistLiveState() error = %v", err)
	}

	items := svc.ListLiveByStreamer(context.Background(), event.StreamerID)
	if len(items) != 1 || items[0].ID != event.ID {
		t.Fatalf("expected Redis live event, got %#v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected db calls: %v", err)
	}
}

func TestSettleEventMarksWinLossAndUsesPlatformFee(t *testing.T) {
	svc := NewService([]LiveEvent{{
		ID:              "event-1",
		TemplateID:      "streamer-1:terminal-1",
		StreamerID:      "streamer-1",
		ScenarioID:      "scenario-1",
		TerminalID:      "terminal-1",
		Title:           map[string]string{"ru": "Победитель"},
		DefaultLanguage: "ru",
		Options:         []Option{{ID: "ct", Title: map[string]string{"ru": "CT"}}, {ID: "t", Title: map[string]string{"ru": "T"}}},
		ClosesAt:        time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Status:          "open",
		Totals:          map[string]int64{"ct": 0, "t": 0},
	}})
	if _, err := svc.UpdateSettings(Settings{VotePlatformFeePercent: 10}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	if _, err := svc.Vote(context.Background(), VoteRequest{EventID: "event-1", StreamerID: "streamer-1", UserID: "winner", OptionID: "ct", Amount: 100, IdempotencyKey: "vote-win"}); err != nil {
		t.Fatalf("winner Vote() error = %v", err)
	}
	if _, err := svc.Vote(context.Background(), VoteRequest{EventID: "event-1", StreamerID: "streamer-1", UserID: "loser", OptionID: "t", Amount: 100, IdempotencyKey: "vote-lose"}); err != nil {
		t.Fatalf("loser Vote() error = %v", err)
	}

	settlement, err := svc.SettleEvent(context.Background(), SettleRequest{EventID: "event-1", StreamerID: "streamer-1", WinningOptionID: "ct", Result: SettleResultWin, ActorID: "admin"})
	if err != nil {
		t.Fatalf("SettleEvent() error = %v", err)
	}
	if settlement.Event.Status != "settled" || settlement.PlatformFeeINT != 20 || settlement.DistributableINT != 180 || settlement.TotalPayoutINT != 180 {
		t.Fatalf("unexpected settlement totals: %+v", settlement)
	}
	if len(settlement.Payouts) != 1 || settlement.Payouts[0].UserID != "winner" || settlement.Payouts[0].WinAmountINT != 180 || settlement.Payouts[0].ResultStatus != ResultStatusWon {
		t.Fatalf("unexpected payouts: %+v", settlement.Payouts)
	}

	winnerHistory := svc.ListUserHistory(context.Background(), "winner")
	if len(winnerHistory) != 1 || winnerHistory[0].WinAmountINT == nil || *winnerHistory[0].WinAmountINT != 180 || winnerHistory[0].ResultStatus != ResultStatusWon {
		t.Fatalf("unexpected winner history: %+v", winnerHistory)
	}
	loserHistory := svc.ListUserHistory(context.Background(), "loser")
	if len(loserHistory) != 1 || loserHistory[0].WinAmountINT == nil || *loserHistory[0].WinAmountINT != 0 || loserHistory[0].ResultStatus != ResultStatusLost {
		t.Fatalf("unexpected loser history: %+v", loserHistory)
	}
}

func TestSettleEventDrawRefundsOriginalAmounts(t *testing.T) {
	svc := NewService([]LiveEvent{{
		ID:              "event-draw",
		TemplateID:      "streamer-1:terminal-1",
		StreamerID:      "streamer-1",
		ScenarioID:      "scenario-1",
		TerminalID:      "terminal-1",
		Title:           map[string]string{"ru": "Победитель"},
		DefaultLanguage: "ru",
		Options:         []Option{{ID: "ct", Title: map[string]string{"ru": "CT"}}, {ID: "t", Title: map[string]string{"ru": "T"}}},
		ClosesAt:        time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Status:          "open",
		Totals:          map[string]int64{"ct": 0, "t": 0},
	}})
	if _, err := svc.UpdateSettings(Settings{VotePlatformFeePercent: 25}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	if _, err := svc.Vote(context.Background(), VoteRequest{EventID: "event-draw", StreamerID: "streamer-1", UserID: "u1", OptionID: "ct", Amount: 80, IdempotencyKey: "draw-vote-1"}); err != nil {
		t.Fatalf("u1 Vote() error = %v", err)
	}
	if _, err := svc.Vote(context.Background(), VoteRequest{EventID: "event-draw", StreamerID: "streamer-1", UserID: "u2", OptionID: "t", Amount: 20, IdempotencyKey: "draw-vote-2"}); err != nil {
		t.Fatalf("u2 Vote() error = %v", err)
	}

	settlement, err := svc.SettleEvent(context.Background(), SettleRequest{EventID: "event-draw", StreamerID: "streamer-1", Result: SettleResultDraw, ActorID: "admin"})
	if err != nil {
		t.Fatalf("SettleEvent(draw) error = %v", err)
	}
	if settlement.Result != SettleResultDraw || settlement.WinningOptionID != "" || settlement.TotalPayoutINT != 100 || len(settlement.Payouts) != 2 {
		t.Fatalf("unexpected draw settlement: %+v", settlement)
	}
	for _, userID := range []string{"u1", "u2"} {
		history := svc.ListUserHistory(context.Background(), userID)
		if len(history) != 1 || history[0].ResultStatus != ResultStatusDraw || history[0].WinAmountINT == nil || *history[0].WinAmountINT != history[0].AmountINT {
			t.Fatalf("unexpected draw history for %s: %+v", userID, history)
		}
	}
}
