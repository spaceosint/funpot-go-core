package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"context"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

func TestAdminLLMScenarioPackageRoutes(t *testing.T) {
	promptsService := prompts.NewService()
	cfg, err := promptsService.CreateLLMModelConfig(context.Background(), prompts.LLMModelConfigCreateRequest{
		Name:          "default",
		Model:         "gemini-2.5-flash",
		Temperature:   0.2,
		MaxTokens:     1024,
		TimeoutMS:     4000,
		RetryCount:    2,
		BackoffMS:     300,
		CooldownMS:    500,
		MinConfidence: 0.7,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateLLMModelConfig() error = %v", err)
	}
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		streamers.NewService(),
		nil,
		promptsService,
		nil,
		nil,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")

	createBody, _ := json.Marshal(map[string]any{
		"gameSlug":         "global",
		"name":             "default graph",
		"llmModelConfigId": cfg.ID,
		"steps": []map[string]any{
			{
				"id":                 "root_detect",
				"name":               "Root detect",
				"gameSlug":           "global",
				"promptTemplate":     "detect",
				"responseSchemaJson": "{}",
				"initial":            true,
				"order":              1,
			},
			{
				"id":                 "cs2_mode",
				"name":               "CS2 mode",
				"gameSlug":           "cs2",
				"folder":             "cs2",
				"promptTemplate":     "mode",
				"responseSchemaJson": "{}",
				"order":              2,
			},
		},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/scenario-packages", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("scenario package create status = %d body=%s", createRes.Code, createRes.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("scenario package create decode error = %v", err)
	}
	packageID, _ := created["id"].(string)
	if packageID == "" {
		t.Fatalf("expected created scenario package id, got %#v", created)
	}
	transitions, _ := created["transitions"].([]any)
	if len(transitions) != 1 {
		t.Fatalf("expected auto-generated transitions in response, got %#v", created["transitions"])
	}
	steps, _ := created["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("expected created package steps, got %#v", created["steps"])
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/scenario-packages", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminToken)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("scenario package list status = %d body=%s", listRes.Code, listRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/scenario-packages/"+packageID, nil)
	getReq.Header.Set("Authorization", "Bearer "+adminToken)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("scenario package get status = %d body=%s", getRes.Code, getRes.Body.String())
	}

	graphReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/scenario-packages/"+packageID+"/graph", nil)
	graphReq.Header.Set("Authorization", "Bearer "+adminToken)
	graphRes := httptest.NewRecorder()
	handler.ServeHTTP(graphRes, graphReq)
	if graphRes.Code != http.StatusOK {
		t.Fatalf("scenario package graph status = %d body=%s", graphRes.Code, graphRes.Body.String())
	}
	var graph map[string]any
	if err := json.Unmarshal(graphRes.Body.Bytes(), &graph); err != nil {
		t.Fatalf("scenario package graph decode error = %v", err)
	}
	if graph["packageId"] != packageID {
		t.Fatalf("expected packageId %q, got %#v", packageID, graph["packageId"])
	}
	if _, ok := graph["nodes"].([]any); !ok {
		t.Fatalf("expected nodes array in graph response, got %#v", graph["nodes"])
	}

	updateBody, _ := json.Marshal(map[string]any{
		"gameSlug":         "global",
		"name":             "default graph v2",
		"llmModelConfigId": cfg.ID,
		"steps": []map[string]any{
			{
				"id":                 "root_detect",
				"name":               "Root detect",
				"gameSlug":           "global",
				"promptTemplate":     "detect-v2",
				"responseSchemaJson": "{}",
				"initial":            true,
				"order":              1,
			},
			{
				"id":                 "cs2_mode",
				"name":               "CS2 mode",
				"gameSlug":           "cs2",
				"folder":             "cs2",
				"promptTemplate":     "mode-v2",
				"responseSchemaJson": "{}",
				"order":              2,
			},
		},
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/llm/scenario-packages/"+packageID, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+adminToken)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("scenario package update status = %d body=%s", updateRes.Code, updateRes.Body.String())
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/scenario-packages/"+packageID+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+adminToken)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("scenario package activate status = %d body=%s", activateRes.Code, activateRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/llm/scenario-packages/"+packageID, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("scenario package delete status = %d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestAdminLLMSettingsRoutes(t *testing.T) {
	promptsService := prompts.NewService()
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		streamers.NewService(),
		nil,
		promptsService,
		nil,
		nil,
		ClientConfigResponse{},
	)
	adminToken := buildToken(t, "admin-1")

	createBody, _ := json.Marshal(map[string]any{
		"name":          "Gemini Flash profile",
		"model":         "gemini-2.5-flash",
		"temperature":   0.2,
		"maxTokens":     1024,
		"timeoutMs":     4000,
		"retryCount":    2,
		"backoffMs":     300,
		"cooldownMs":    500,
		"minConfidence": 0.75,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/settings", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("llm settings create status = %d body=%s", createRes.Code, createRes.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("llm settings create decode error = %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("expected created llm settings id, got %#v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/settings", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminToken)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("llm settings list status = %d body=%s", listRes.Code, listRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/settings/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer "+adminToken)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("llm settings get status = %d body=%s", getRes.Code, getRes.Body.String())
	}

	updateBody, _ := json.Marshal(map[string]any{
		"name":          "Gemini Pro profile",
		"model":         "gemini-2.5-pro",
		"temperature":   0.4,
		"maxTokens":     2048,
		"timeoutMs":     5000,
		"retryCount":    3,
		"backoffMs":     500,
		"cooldownMs":    600,
		"minConfidence": 0.8,
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/llm/settings/"+id, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+adminToken)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("llm settings update status = %d body=%s", updateRes.Code, updateRes.Body.String())
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/settings/"+id+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+adminToken)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("llm settings activate status = %d body=%s", activateRes.Code, activateRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/llm/settings/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("llm settings delete status = %d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}
