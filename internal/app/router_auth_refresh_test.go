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

	"github.com/funpot/funpot-go-core/internal/auth"
)

func TestAuthRefreshEndpointRotatesTokens(t *testing.T) {
	authService := buildAuthService(t)
	store := auth.NewInMemoryRefreshSessionStore(5)
	authService.WithRefreshSessionStore(store)

	now := time.Now().UTC()
	refreshToken := "session-1.secret-1"
	if err := store.Create(context.Background(), auth.RefreshSession{
		SessionID:   "session-1",
		UserID:      "user-1",
		TelegramID:  42,
		TokenHash:   auth.HashRefreshToken(refreshToken),
		ExpiresAt:   now.Add(30 * time.Minute),
		CreatedAt:   now,
		LastRotated: now,
	}); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, authService, nil, nil, nil, nil, nil, nil, ClientConfigResponse{})
	body, _ := json.Marshal(map[string]string{"refreshToken": refreshToken})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", bytes.NewReader(body))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", res.Code, res.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["token"] == "" {
		t.Fatal("expected access token in response")
	}
	if payload["refreshToken"] == "" {
		t.Fatal("expected rotated refresh token in response")
	}
}

func TestAuthLogoutAllEndpoint(t *testing.T) {
	authService := buildAuthService(t)
	store := auth.NewInMemoryRefreshSessionStore(5)
	authService.WithRefreshSessionStore(store)
	now := time.Now().UTC()
	if err := store.Create(context.Background(), auth.RefreshSession{
		SessionID:   "session-2",
		UserID:      "user-1",
		TelegramID:  42,
		TokenHash:   auth.HashRefreshToken("session-2.secret-2"),
		ExpiresAt:   now.Add(30 * time.Minute),
		CreatedAt:   now,
		LastRotated: now,
	}); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, authService, nil, nil, nil, nil, nil, nil, ClientConfigResponse{})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout-all", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", res.Code, res.Body.String())
	}

	session, err := store.Get(context.Background(), "session-2")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("expected session to be revoked")
	}
}
