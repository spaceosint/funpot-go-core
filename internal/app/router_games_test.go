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
	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/games"
	"github.com/funpot/funpot-go-core/internal/users"
)

func buildAuthService(t *testing.T) *auth.Service {
	t.Helper()
	svc, err := auth.NewService(zap.NewNop(), config.AuthConfig{
		BotToken: "test-bot-token",
		JWT: config.JWTConfig{
			Secret: "test-secret",
			TTL:    time.Hour,
		},
		Refresh: config.RefreshConfig{
			TTL: 24 * time.Hour,
		},
	}, users.NewService(users.NewInMemoryRepository()))
	if err != nil {
		t.Fatalf("auth.NewService() error = %v", err)
	}
	svc.WithRefreshSessionStore(auth.NewInMemoryRefreshSessionStore(5))
	return svc
}

func buildToken(t *testing.T, userID string) string {
	t.Helper()
	issuer, err := auth.NewJWTIssuer("test-secret", time.Hour)
	if err != nil {
		t.Fatalf("auth.NewJWTIssuer() error = %v", err)
	}
	token, _, err := issuer.Issue(userID, 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("issuer.Issue() error = %v", err)
	}
	return token
}

func TestAdminGamesForbiddenForNonAdmin(t *testing.T) {
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, nil, games.NewService(), nil, nil, nil, ClientConfigResponse{})
	req := httptest.NewRequest(http.MethodGet, "/api/admin/games", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestAdminGamesCreateAndList(t *testing.T) {
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, nil, games.NewService(), nil, nil, nil, ClientConfigResponse{})
	token := buildToken(t, "admin-1")

	body, _ := json.Marshal(map[string]any{"slug": "cs2", "title": "Counter-Strike 2", "status": "draft"})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/games", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createRes.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/games", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listRes.Code)
	}
}
