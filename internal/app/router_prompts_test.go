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
		nil,
		ClientConfigResponse{},
	)
	token := buildToken(t, "admin-1")

	body, _ := json.Marshal(map[string]any{
		"stage":         "detector",
		"position":      1,
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
	if created["isActive"] != true {
		t.Fatalf("expected first created prompt to be active automatically, got %#v", created["isActive"])
	}
	if position, ok := created["position"].(float64); !ok || int(position) != 1 {
		t.Fatalf("expected position 1 in response, got %#v", created["position"])
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

func TestAdminPromptsCRUD(t *testing.T) {
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
		nil,
		ClientConfigResponse{},
	)
	token := buildToken(t, "admin-1")

	createBody, _ := json.Marshal(map[string]any{
		"stage":         "detector",
		"position":      1,
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
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/prompts", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createRes.Code)
	}
	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected id")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/prompts/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRes.Code)
	}

	updateBody, _ := json.Marshal(map[string]any{
		"stage":         "detector",
		"position":      2,
		"template":      "updated detector prompt",
		"model":         "gemini-2.0-flash",
		"temperature":   0.4,
		"maxTokens":     256,
		"timeoutMs":     1500,
		"retryCount":    1,
		"backoffMs":     150,
		"cooldownMs":    1000,
		"minConfidence": 0.8,
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/prompts/"+id, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+token)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateRes.Code, updateRes.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(updateRes.Body.Bytes(), &updated); err != nil {
		t.Fatalf("failed to decode update response: %v", err)
	}
	if updated["template"] != "updated detector prompt" {
		t.Fatalf("expected template to update, got %#v", updated["template"])
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/prompts/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", deleteRes.Code)
	}

	getAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/api/admin/prompts/"+id, nil)
	getAfterDeleteReq.Header.Set("Authorization", "Bearer "+token)
	getAfterDeleteRes := httptest.NewRecorder()
	handler.ServeHTTP(getAfterDeleteRes, getAfterDeleteReq)
	if getAfterDeleteRes.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getAfterDeleteRes.Code)
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
