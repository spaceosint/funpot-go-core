package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/prompts"
)

func TestAdminPromptsCreateListAndActivate(t *testing.T) {
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		nil,
		nil,
		prompts.NewService(),
		nil,
		ClientConfigResponse{},
	)
	token := buildToken(t, "admin-1")

	body, _ := json.Marshal(map[string]any{
		"stage":         "stage_a",
		"template":      "detect cs2 on stream",
		"model":         "gemini-2.0-flash",
		"temperature":   0.2,
		"maxTokens":     512,
		"timeoutMs":     2000,
		"retryCount":    2,
		"backoffMs":     300,
		"cooldownMs":    30000,
		"minConfidence": 0.7,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/prompts", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createRes.Code)
	}

	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to unmarshal create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected created prompt id")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/prompts", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listRes.Code)
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/prompts/"+id+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+token)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", activateRes.Code)
	}
}

func TestAdminPromptsForbiddenForNonAdmin(t *testing.T) {
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		nil,
		nil,
		prompts.NewService(),
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/prompts", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}
