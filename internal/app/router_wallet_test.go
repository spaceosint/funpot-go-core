package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/users"
)

func TestWalletAdminUserUpdateBalanceAndWithdrawIdempotency(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	if _, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"}); err != nil {
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
		nil,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")
	userToken := buildToken(t, "tg_1")

	adjustBody := []byte(`{"balanceDeltaINT":100,"balanceReason":"manual grant"}`)
	adjustReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/tg_1", bytes.NewReader(adjustBody))
	adjustReq.Header.Set("Authorization", "Bearer "+adminToken)
	adjustReq.Header.Set("Idempotency-Key", "adj-1")
	adjustRes := httptest.NewRecorder()
	handler.ServeHTTP(adjustRes, adjustReq)
	if adjustRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", adjustRes.Code, adjustRes.Body.String())
	}

	replayReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/tg_1", bytes.NewReader(adjustBody))
	replayReq.Header.Set("Authorization", "Bearer "+adminToken)
	replayReq.Header.Set("Idempotency-Key", "adj-1")
	replayRes := httptest.NewRecorder()
	handler.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", replayRes.Code, replayRes.Body.String())
	}

	walletReq := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	walletReq.Header.Set("Authorization", "Bearer "+userToken)
	walletRes := httptest.NewRecorder()
	handler.ServeHTTP(walletRes, walletReq)
	if walletRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", walletRes.Code, walletRes.Body.String())
	}

	var walletPayload struct {
		Balance int64 `json:"balance"`
		History []struct {
			Type string `json:"type"`
		} `json:"history"`
	}
	if err := json.Unmarshal(walletRes.Body.Bytes(), &walletPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if walletPayload.Balance != 100 {
		t.Fatalf("expected balance 100, got %d", walletPayload.Balance)
	}
	if len(walletPayload.History) != 1 {
		t.Fatalf("expected history length 1 after idempotent replay, got %d", len(walletPayload.History))
	}

	withdrawBody := []byte(`{"amountINT":30}`)
	withdrawReq := httptest.NewRequest(http.MethodPost, "/api/wallet/withdraw", bytes.NewReader(withdrawBody))
	withdrawReq.Header.Set("Authorization", "Bearer "+userToken)
	withdrawReq.Header.Set("Idempotency-Key", "w-1")
	withdrawRes := httptest.NewRecorder()
	handler.ServeHTTP(withdrawRes, withdrawReq)
	if withdrawRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", withdrawRes.Code, withdrawRes.Body.String())
	}

	withdrawReplayReq := httptest.NewRequest(http.MethodPost, "/api/wallet/withdraw", bytes.NewReader(withdrawBody))
	withdrawReplayReq.Header.Set("Authorization", "Bearer "+userToken)
	withdrawReplayReq.Header.Set("Idempotency-Key", "w-1")
	withdrawReplayRes := httptest.NewRecorder()
	handler.ServeHTTP(withdrawReplayRes, withdrawReplayReq)
	if withdrawReplayRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", withdrawReplayRes.Code, withdrawReplayRes.Body.String())
	}

	walletReqAfter := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	walletReqAfter.Header.Set("Authorization", "Bearer "+userToken)
	walletResAfter := httptest.NewRecorder()
	handler.ServeHTTP(walletResAfter, walletReqAfter)
	if walletResAfter.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", walletResAfter.Code, walletResAfter.Body.String())
	}
	if err := json.Unmarshal(walletResAfter.Body.Bytes(), &walletPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if walletPayload.Balance != 70 {
		t.Fatalf("expected balance 70 after idempotent withdraw replay, got %d", walletPayload.Balance)
	}
}

func TestAdminUserBalanceAdjustRequiresAdmin(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	if _, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"}); err != nil {
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
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/tg_1", bytes.NewReader([]byte(`{"balanceDeltaINT":10,"balanceReason":"test"}`)))
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "tg_1"))
	req.Header.Set("Idempotency-Key", "adj-1")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestAdminUserBalanceAdjustRejectsCurrencyOverride(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	if _, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "u1"}); err != nil {
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
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/tg_1", bytes.NewReader([]byte(`{"balanceDeltaINT":10,"balanceReason":"test","currency":"USD"}`)))
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	req.Header.Set("Idempotency-Key", "adj-1")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}
