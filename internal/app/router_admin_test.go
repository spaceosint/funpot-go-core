package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
)

func TestAdminMeReturnsTrueForAdmin(t *testing.T) {
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, nil, nil, nil, nil, ClientConfigResponse{})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var payload struct {
		IsAdmin   bool     `json:"isAdmin"`
		AdminTabs []string `json:"adminTabs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if !payload.IsAdmin {
		t.Fatalf("expected isAdmin=true, got %v", payload.IsAdmin)
	}

	expectedTabs := []string{"settings", "games", "prompts"}
	if !reflect.DeepEqual(payload.AdminTabs, expectedTabs) {
		t.Fatalf("expected admin tabs %v, got %v", expectedTabs, payload.AdminTabs)
	}
}

func TestAdminMeReturnsFalseForNonAdmin(t *testing.T) {
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, nil, nil, nil, nil, ClientConfigResponse{})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var payload struct {
		IsAdmin   bool     `json:"isAdmin"`
		AdminTabs []string `json:"adminTabs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if payload.IsAdmin {
		t.Fatalf("expected isAdmin=false, got %v", payload.IsAdmin)
	}

	if len(payload.AdminTabs) != 0 {
		t.Fatalf("expected empty adminTabs for non-admin, got %v", payload.AdminTabs)
	}
}
