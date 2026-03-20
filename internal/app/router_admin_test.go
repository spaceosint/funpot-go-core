package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/users"
)

func TestMeReturnsIsAdminTrueForAdmin(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	_, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "admin"})
	if err != nil {
		t.Fatalf("userService.SyncTelegramProfile() error = %v", err)
	}

	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), userService, nil, nil, nil, nil, nil, ClientConfigResponse{})

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var payload struct {
		IsAdmin bool `json:"isAdmin"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if !payload.IsAdmin {
		t.Fatalf("expected isAdmin=true, got %v", payload.IsAdmin)
	}
}

func TestMeReturnsIsAdminFalseForNonAdmin(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	_, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{ID: 1, Username: "user"})
	if err != nil {
		t.Fatalf("userService.SyncTelegramProfile() error = %v", err)
	}

	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), userService, nil, nil, nil, nil, nil, ClientConfigResponse{})

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var payload struct {
		IsAdmin bool `json:"isAdmin"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if payload.IsAdmin {
		t.Fatalf("expected isAdmin=false, got %v", payload.IsAdmin)
	}
}

func TestAdminMeEndpointRemovedFallsBackToRoot(t *testing.T) {
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, nil, nil, nil, nil, nil, ClientConfigResponse{})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.Code)
	}
}
