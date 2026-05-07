package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/events"
	"github.com/funpot/funpot-go-core/internal/users"
)

func TestEventsVoteDebitsWalletAndIsIdempotent(t *testing.T) {
	eventsService := events.NewService([]events.LiveEvent{
		{
			ID:              "event-1",
			TemplateID:      "streamer-1:terminal-1",
			StreamerID:      "streamer-1",
			ScenarioID:      "scenario-1",
			TerminalID:      "terminal-1",
			Title:           map[string]string{"ru": "Победитель карты"},
			DefaultLanguage: "ru",
			Options: []events.Option{
				{ID: "ct", Title: map[string]string{"ru": "CT"}},
				{ID: "t", Title: map[string]string{"ru": "T"}},
			},
			ClosesAt:  time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Status:    "open",
			Totals: map[string]int64{
				"ct": 0,
				"t":  0,
			},
		},
	})
	userService := users.NewService(users.NewInMemoryRepository())
	created, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		eventsService,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")
	userToken := buildToken(t, created.ID)

	adjustReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID, bytes.NewReader([]byte(`{"balanceDeltaINT":100,"balanceReason":"seed"}`)))
	adjustReq.Header.Set("Authorization", "Bearer "+adminToken)
	adjustReq.Header.Set("Idempotency-Key", "adj-seed")
	adjustRes := httptest.NewRecorder()
	handler.ServeHTTP(adjustRes, adjustReq)
	if adjustRes.Code != http.StatusOK {
		t.Fatalf("seed wallet status=%d body=%s", adjustRes.Code, adjustRes.Body.String())
	}

	voteBody := []byte(`{"streamerId":"streamer-1","optionId":"ct","amountINT":10}`)
	voteReq := httptest.NewRequest(http.MethodPost, "/api/events/event-1/vote", bytes.NewReader(voteBody))
	voteReq.Header.Set("Authorization", "Bearer "+userToken)
	voteReq.Header.Set("Idempotency-Key", "vote-1")
	voteRes := httptest.NewRecorder()
	handler.ServeHTTP(voteRes, voteReq)
	if voteRes.Code != http.StatusOK {
		t.Fatalf("vote status=%d body=%s", voteRes.Code, voteRes.Body.String())
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/events/event-1/vote", bytes.NewReader(voteBody))
	replayReq.Header.Set("Authorization", "Bearer "+userToken)
	replayReq.Header.Set("Idempotency-Key", "vote-1")
	replayRes := httptest.NewRecorder()
	handler.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusOK {
		t.Fatalf("replay vote status=%d body=%s", replayRes.Code, replayRes.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(replayRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal replay vote response: %v", err)
	}
	totals, _ := payload["totals"].(map[string]any)
	if got := totals["ct"]; got != float64(10) {
		t.Fatalf("expected ct total 10, got %#v", got)
	}

	walletReq := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	walletReq.Header.Set("Authorization", "Bearer "+userToken)
	walletRes := httptest.NewRecorder()
	handler.ServeHTTP(walletRes, walletReq)
	if walletRes.Code != http.StatusOK {
		t.Fatalf("wallet status=%d body=%s", walletRes.Code, walletRes.Body.String())
	}
	var walletPayload struct {
		Balance int64 `json:"balance"`
	}
	if err := json.Unmarshal(walletRes.Body.Bytes(), &walletPayload); err != nil {
		t.Fatalf("unmarshal wallet response: %v", err)
	}
	if walletPayload.Balance != 90 {
		t.Fatalf("expected wallet balance 90 after idempotent vote replay, got %d", walletPayload.Balance)
	}
}

func TestAdminGeneralSettingsAffectVotePlatformFee(t *testing.T) {
	eventsService := events.NewService([]events.LiveEvent{
		{
			ID:              "event-1",
			TemplateID:      "streamer-1:terminal-1",
			StreamerID:      "streamer-1",
			ScenarioID:      "scenario-1",
			TerminalID:      "terminal-1",
			Title:           map[string]string{"ru": "Победитель карты"},
			DefaultLanguage: "ru",
			Options: []events.Option{
				{ID: "ct", Title: map[string]string{"ru": "CT"}},
			},
			ClosesAt:  time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Status:    "open",
			Totals: map[string]int64{
				"ct": 0,
			},
		},
	})
	userService := users.NewService(users.NewInMemoryRepository())
	created, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		eventsService,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")
	userToken := buildToken(t, created.ID)

	settingsReq := httptest.NewRequest(http.MethodPut, "/api/admin/settings/general", bytes.NewReader([]byte(`{"votePlatformFeePercent":15}`)))
	settingsReq.Header.Set("Authorization", "Bearer "+adminToken)
	settingsRes := httptest.NewRecorder()
	handler.ServeHTTP(settingsRes, settingsReq)
	if settingsRes.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", settingsRes.Code, settingsRes.Body.String())
	}

	adjustReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID, bytes.NewReader([]byte(`{"balanceDeltaINT":100,"balanceReason":"seed"}`)))
	adjustReq.Header.Set("Authorization", "Bearer "+adminToken)
	adjustReq.Header.Set("Idempotency-Key", "adj-seed")
	adjustRes := httptest.NewRecorder()
	handler.ServeHTTP(adjustRes, adjustReq)
	if adjustRes.Code != http.StatusOK {
		t.Fatalf("seed wallet status=%d body=%s", adjustRes.Code, adjustRes.Body.String())
	}

	voteReq := httptest.NewRequest(http.MethodPost, "/api/events/event-1/vote", bytes.NewReader([]byte(`{"streamerId":"streamer-1","optionId":"ct","amountINT":100}`)))
	voteReq.Header.Set("Authorization", "Bearer "+userToken)
	voteReq.Header.Set("Idempotency-Key", "vote-1")
	voteRes := httptest.NewRecorder()
	handler.ServeHTTP(voteRes, voteReq)
	if voteRes.Code != http.StatusOK {
		t.Fatalf("vote status=%d body=%s", voteRes.Code, voteRes.Body.String())
	}

	var payload struct {
		Totals           map[string]int64 `json:"totals"`
		TotalContributed int64            `json:"totalContributed"`
		PlatformFeeINT   int64            `json:"platformFeeINT"`
		DistributableINT int64            `json:"distributableINT"`
	}
	if err := json.Unmarshal(voteRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal vote response: %v", err)
	}
	if payload.Totals["ct"] != 85 {
		t.Fatalf("expected net total 85, got %d", payload.Totals["ct"])
	}
	if payload.TotalContributed != 100 || payload.PlatformFeeINT != 15 || payload.DistributableINT != 85 {
		t.Fatalf("unexpected pool values: %+v", payload)
	}
}

func TestEventsHistoryReturnsUserEventVotes(t *testing.T) {
	eventsService := events.NewService([]events.LiveEvent{
		{
			ID:              "event-1",
			TemplateID:      "streamer-1:terminal-1",
			StreamerID:      "streamer-1",
			ScenarioID:      "scenario-1",
			TerminalID:      "terminal-1",
			Title:           map[string]string{"ru": "Победитель карты"},
			DefaultLanguage: "ru",
			Options: []events.Option{
				{ID: "ct", Title: map[string]string{"ru": "CT"}},
				{ID: "t", Title: map[string]string{"ru": "T"}},
			},
			ClosesAt:  time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Status:    "open",
			Totals: map[string]int64{
				"ct": 0,
				"t":  0,
			},
		},
	})
	userService := users.NewService(users.NewInMemoryRepository())
	created, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		eventsService,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")
	userToken := buildToken(t, created.ID)

	adjustReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID, bytes.NewReader([]byte(`{"balanceDeltaINT":100,"balanceReason":"seed"}`)))
	adjustReq.Header.Set("Authorization", "Bearer "+adminToken)
	adjustReq.Header.Set("Idempotency-Key", "adj-seed")
	adjustRes := httptest.NewRecorder()
	handler.ServeHTTP(adjustRes, adjustReq)
	if adjustRes.Code != http.StatusOK {
		t.Fatalf("seed wallet status=%d body=%s", adjustRes.Code, adjustRes.Body.String())
	}

	voteReq := httptest.NewRequest(http.MethodPost, "/api/events/event-1/vote", bytes.NewReader([]byte(`{"streamerId":"streamer-1","optionId":"ct","amountINT":10}`)))
	voteReq.Header.Set("Authorization", "Bearer "+userToken)
	voteReq.Header.Set("Idempotency-Key", "vote-1")
	voteRes := httptest.NewRecorder()
	handler.ServeHTTP(voteRes, voteReq)
	if voteRes.Code != http.StatusOK {
		t.Fatalf("vote status=%d body=%s", voteRes.Code, voteRes.Body.String())
	}

	historyReq := httptest.NewRequest(http.MethodGet, "/api/events/history", nil)
	historyReq.Header.Set("Authorization", "Bearer "+userToken)
	historyRes := httptest.NewRecorder()
	handler.ServeHTTP(historyRes, historyReq)
	if historyRes.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", historyRes.Code, historyRes.Body.String())
	}

	var history []struct {
		StreamerID       string `json:"streamerId"`
		StreamerNickname string `json:"streamerNickname"`
		NetAmountINT     int64  `json:"netAmountINT"`
		Details          []struct {
			EventID      string  `json:"eventId"`
			OptionID     string  `json:"optionId"`
			AmountINT    int64   `json:"amountINT"`
			Coefficient  float64 `json:"coefficient"`
			ResultStatus string  `json:"resultStatus"`
			PotentialWin int64   `json:"potentialWinINT"`
		} `json:"details"`
	}
	if err := json.Unmarshal(historyRes.Body.Bytes(), &history); err != nil {
		t.Fatalf("unmarshal history response: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected history length 1, got %d", len(history))
	}
	if len(history[0].Details) != 1 {
		t.Fatalf("expected one detail item, got %d", len(history[0].Details))
	}
	if history[0].StreamerID != "streamer-1" || history[0].StreamerNickname == "" || history[0].NetAmountINT != -10 {
		t.Fatalf("unexpected grouped history item: %+v", history[0])
	}
	if history[0].Details[0].EventID != "event-1" || history[0].Details[0].OptionID != "ct" || history[0].Details[0].AmountINT != 10 {
		t.Fatalf("unexpected history item: %+v", history[0])
	}
	if history[0].Details[0].Coefficient <= 0 || history[0].Details[0].PotentialWin <= 0 {
		t.Fatalf("expected positive coefficient and potential win, got %+v", history[0])
	}
	if history[0].Details[0].ResultStatus != "pending" {
		t.Fatalf("expected pending result status, got %s", history[0].Details[0].ResultStatus)
	}
}

func TestEventsVoteDoesNotDebitWalletWhenEventClosed(t *testing.T) {
	eventsService := events.NewService([]events.LiveEvent{
		{
			ID:              "event-closed",
			TemplateID:      "streamer-1:terminal-1",
			StreamerID:      "streamer-1",
			ScenarioID:      "scenario-1",
			TerminalID:      "terminal-1",
			Title:           map[string]string{"ru": "Победитель карты"},
			DefaultLanguage: "ru",
			Options: []events.Option{
				{ID: "ct", Title: map[string]string{"ru": "CT"}},
			},
			ClosesAt:  time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
			CreatedAt: time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano),
			Status:    "open",
			Totals:    map[string]int64{"ct": 0},
		},
	})
	userService := users.NewService(users.NewInMemoryRepository())
	created, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), userService, nil, nil, nil, nil, eventsService, ClientConfigResponse{})
	adminToken := buildToken(t, "admin-1")
	userToken := buildToken(t, created.ID)

	adjustReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID, bytes.NewReader([]byte(`{"balanceDeltaINT":100,"balanceReason":"seed"}`)))
	adjustReq.Header.Set("Authorization", "Bearer "+adminToken)
	adjustReq.Header.Set("Idempotency-Key", "adj-seed")
	adjustRes := httptest.NewRecorder()
	handler.ServeHTTP(adjustRes, adjustReq)
	if adjustRes.Code != http.StatusOK {
		t.Fatalf("seed wallet status=%d body=%s", adjustRes.Code, adjustRes.Body.String())
	}

	voteReq := httptest.NewRequest(http.MethodPost, "/api/events/event-closed/vote", bytes.NewReader([]byte(`{"streamerId":"streamer-1","optionId":"ct","amountINT":10}`)))
	voteReq.Header.Set("Authorization", "Bearer "+userToken)
	voteReq.Header.Set("Idempotency-Key", "vote-closed")
	voteRes := httptest.NewRecorder()
	handler.ServeHTTP(voteRes, voteReq)
	if voteRes.Code != http.StatusConflict {
		t.Fatalf("closed vote status=%d body=%s", voteRes.Code, voteRes.Body.String())
	}

	walletReq := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	walletReq.Header.Set("Authorization", "Bearer "+userToken)
	walletRes := httptest.NewRecorder()
	handler.ServeHTTP(walletRes, walletReq)
	if walletRes.Code != http.StatusOK {
		t.Fatalf("wallet status=%d body=%s", walletRes.Code, walletRes.Body.String())
	}
	var walletPayload struct {
		Balance int64 `json:"balance"`
	}
	if err := json.Unmarshal(walletRes.Body.Bytes(), &walletPayload); err != nil {
		t.Fatalf("unmarshal wallet response: %v", err)
	}
	if walletPayload.Balance != 100 {
		t.Fatalf("expected closed vote to keep wallet balance 100, got %d", walletPayload.Balance)
	}
}

func TestBuildEventUpdatedPayloadIncludesRealtimeMarket(t *testing.T) {
	event := events.LiveEvent{
		ID:               "event-1",
		ClosesAt:         time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		Totals:           map[string]int64{"ct": 90, "t": 10},
		TotalContributed: 100,
		PlatformFeeINT:   0,
		DistributableINT: 100,
	}
	payload := buildEventUpdatedPayload(event, event.Market())
	if payload.TotalContributed != 100 || payload.DistributableINT != 100 || payload.PlatformFeeINT != 0 {
		t.Fatalf("unexpected pool values: %+v", payload)
	}
	if payload.Options["ct"].SharePct != 90 || payload.Options["t"].SharePct != 10 {
		t.Fatalf("unexpected market shares: %+v", payload.Options)
	}
	if payload.Options["ct"].Coefficient <= 0 || payload.Options["t"].Coefficient <= 0 {
		t.Fatalf("expected positive coefficients: %+v", payload.Options)
	}
	feed := buildVoteFeedItem("user-1", "BraveFox123", "ct", 10, event, event.Market())
	if feed.UserID != "user-1" || feed.Nickname != "BraveFox123" || feed.OptionID != "ct" || feed.AmountINT != 10 {
		t.Fatalf("unexpected vote feed identity fields: %+v", feed)
	}
	if feed.OptionPoolSharePct != 90 || feed.Coefficient <= 0 || feed.PotentialWinINT <= 0 {
		t.Fatalf("expected realtime market fields on vote feed item: %+v", feed)
	}
}

func TestWeeklyRewardClaimCreditsWalletAndRespects24h(t *testing.T) {
	eventsService := events.NewService(nil)
	_, err := eventsService.UpdateSettings(events.Settings{WeeklyRewardByDayINT: [7]int64{10, 20, 30, 40, 50, 60, 70}})
	if err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	userService := users.NewService(users.NewInMemoryRepository())
	created, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), userService, nil, nil, nil, nil, eventsService, ClientConfigResponse{})
	userToken := buildToken(t, created.ID)

	claimReq := httptest.NewRequest(http.MethodPost, "/api/rewards/weekly/claim", nil)
	claimReq.Header.Set("Authorization", "Bearer "+userToken)
	claimRes := httptest.NewRecorder()
	handler.ServeHTTP(claimRes, claimReq)
	if claimRes.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimRes.Code, claimRes.Body.String())
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/rewards/weekly/claim", nil)
	replayReq.Header.Set("Authorization", "Bearer "+userToken)
	replayRes := httptest.NewRecorder()
	handler.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusConflict {
		t.Fatalf("replay claim status=%d body=%s", replayRes.Code, replayRes.Body.String())
	}

	walletReq := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	walletReq.Header.Set("Authorization", "Bearer "+userToken)
	walletRes := httptest.NewRecorder()
	handler.ServeHTTP(walletRes, walletReq)
	if walletRes.Code != http.StatusOK {
		t.Fatalf("wallet status=%d body=%s", walletRes.Code, walletRes.Body.String())
	}
	var walletPayload struct {
		Balance int64 `json:"balance"`
	}
	if err := json.Unmarshal(walletRes.Body.Bytes(), &walletPayload); err != nil {
		t.Fatalf("unmarshal wallet response: %v", err)
	}
	if walletPayload.Balance != 10 {
		t.Fatalf("expected wallet balance 10, got %d", walletPayload.Balance)
	}
}
