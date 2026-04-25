package app

import (
	"bytes"
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
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		users.NewService(users.NewInMemoryRepository()),
		nil,
		nil,
		nil,
		nil,
		eventsService,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")
	userToken := buildToken(t, "user-1")

	adjustReq := httptest.NewRequest(http.MethodPost, "/api/admin/wallet/adjust", bytes.NewReader([]byte(`{"userId":"user-1","deltaINT":100,"reason":"seed"}`)))
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
