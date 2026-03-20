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

func TestAdminGlobalDetectorsCRUD(t *testing.T) {
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		nil,
		nil,
		nil,
		prompts.NewScenarioService(),
		nil,
		ClientConfigResponse{},
	)
	token := buildToken(t, "admin-1")

	createBody, _ := json.Marshal(map[string]any{
		"stage":         "global_detector",
		"template":      "detect current game",
		"model":         "gemini-2.0-flash",
		"temperature":   0.1,
		"maxTokens":     256,
		"timeoutMs":     2000,
		"retryCount":    1,
		"backoffMs":     250,
		"cooldownMs":    1000,
		"minConfidence": 0.7,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/global-detectors", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal(create) error = %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected created detector id")
	}

	updateBody, _ := json.Marshal(map[string]any{
		"stage":         "global_detector_v2",
		"template":      "detect cs2 exactly",
		"model":         "gemini-2.0-flash",
		"temperature":   0.2,
		"maxTokens":     300,
		"timeoutMs":     2500,
		"retryCount":    2,
		"backoffMs":     500,
		"cooldownMs":    2000,
		"minConfidence": 0.8,
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/global-detectors/"+id, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+token)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateRes.Code, updateRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/global-detectors", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listRes.Code)
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/global-detectors/"+id+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+token)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", activateRes.Code, activateRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/global-detectors/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestAdminScenariosCRUD(t *testing.T) {
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		nil,
		nil,
		nil,
		prompts.NewScenarioService(),
		nil,
		ClientConfigResponse{},
	)
	token := buildToken(t, "admin-1")

	createBody, _ := json.Marshal(map[string]any{
		"gameSlug":    "counter_strike",
		"name":        "CS ranked flow",
		"description": "desc",
		"steps": []map[string]any{
			{
				"code":           "match_start",
				"title":          "Match start",
				"promptTemplate": "Has a ranked match started?",
				"model":          "gemini-2.0-flash",
				"temperature":    0.1,
				"maxTokens":      256,
				"timeoutMs":      2000,
				"retryCount":     1,
				"backoffMs":      250,
				"cooldownMs":     1000,
				"minConfidence":  0.7,
			},
		},
		"transitions": []map[string]any{
			{
				"fromStepCode": "match_start",
				"outcome":      "match_started",
				"terminal":     true,
			},
		},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/scenarios", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal(create) error = %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected created scenario id")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/scenarios/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRes.Code)
	}

	updateBody, _ := json.Marshal(map[string]any{
		"gameSlug":    "counter_strike",
		"name":        "CS ranked flow updated",
		"description": "updated",
		"steps": []map[string]any{
			{
				"code":           "match_start",
				"title":          "Match start",
				"promptTemplate": "Has a ranked match started?",
				"model":          "gemini-2.0-flash",
				"temperature":    0.2,
				"maxTokens":      128,
				"timeoutMs":      1500,
				"retryCount":     1,
				"backoffMs":      250,
				"cooldownMs":     1000,
				"minConfidence":  0.7,
			},
			{
				"code":           "match_result",
				"title":          "Match result",
				"promptTemplate": "Did the streamer win?",
				"model":          "gemini-2.0-flash",
				"temperature":    0.2,
				"maxTokens":      128,
				"timeoutMs":      1500,
				"retryCount":     1,
				"backoffMs":      250,
				"cooldownMs":     1000,
				"minConfidence":  0.7,
			},
		},
		"transitions": []map[string]any{
			{
				"fromStepCode": "match_start",
				"outcome":      "match_started",
				"toStepCode":   "match_result",
			},
			{
				"fromStepCode": "match_result",
				"outcome":      "win",
				"terminal":     true,
			},
		},
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/scenarios/"+id, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+token)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateRes.Code, updateRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/scenarios", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listRes.Code)
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/scenarios/"+id+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+token)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", activateRes.Code, activateRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/scenarios/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteRes.Code, deleteRes.Body.String())
	}
}
