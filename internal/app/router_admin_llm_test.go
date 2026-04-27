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
	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

func TestAdminLLMScenarioPackageRoutes(t *testing.T) {
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

	cfgBody, _ := json.Marshal(map[string]any{
		"name":         "Global Gemini",
		"model":        "gemini-2.5-flash",
		"metadataJson": `{"provider":"google","tier":"fast"}`,
	})
	cfgReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/model-configs", bytes.NewReader(cfgBody))
	cfgReq.Header.Set("Authorization", "Bearer "+adminToken)
	cfgRes := httptest.NewRecorder()
	handler.ServeHTTP(cfgRes, cfgReq)
	if cfgRes.Code != http.StatusCreated {
		t.Fatalf("llm model config create status = %d body=%s", cfgRes.Code, cfgRes.Body.String())
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgRes.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("llm model config decode error = %v", err)
	}
	configID, _ := cfg["id"].(string)
	if configID == "" {
		t.Fatalf("expected created model config id, got %#v", cfg)
	}

	createBody, _ := json.Marshal(map[string]any{
		"gameSlug":         "global",
		"name":             "default graph",
		"llmModelConfigId": configID,
		"steps": []map[string]any{
			{
				"id":                 "root_detect",
				"name":               "Root detect",
				"gameSlug":           "global",
				"promptTemplate":     "detect",
				"responseSchemaJson": "{}",
				"segmentSeconds":     15,
				"maxRequests":        4,
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
		"transitions": []map[string]any{
			{
				"fromStepId": "root_detect",
				"toStepId":   "cs2_mode",
				"condition":  "game = cs2",
				"priority":   3,
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
	transitions, ok := created["transitions"].([]any)
	if !ok || len(transitions) != 1 {
		t.Fatalf("expected explicit transitions in response, got %#v", created["transitions"])
	}
	steps, ok := created["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected steps in response, got %#v", created["steps"])
	}
	firstStep, ok := steps[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first step object, got %#v", steps[0])
	}
	if got, _ := firstStep["segmentSeconds"].(float64); int(got) != 15 {
		t.Fatalf("expected segmentSeconds=15, got %#v", firstStep["segmentSeconds"])
	}
	if got, _ := firstStep["maxRequests"].(float64); int(got) != 4 {
		t.Fatalf("expected maxRequests=4, got %#v", firstStep["maxRequests"])
	}

	updateBody, _ := json.Marshal(map[string]any{
		"gameSlug":         "global",
		"name":             "default graph v2",
		"llmModelConfigId": configID,
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
		},
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/llm/scenario-packages/"+packageID, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+adminToken)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("scenario package update status = %d body=%s", updateRes.Code, updateRes.Body.String())
	}

	invalidBody, _ := json.Marshal(map[string]any{
		"gameSlug":         "global",
		"name":             "default graph invalid",
		"llmModelConfigId": configID,
		"steps": []map[string]any{
			{
				"id":                 "root_detect",
				"name":               "Root detect",
				"llmModelConfigId":   "step-level-not-allowed",
				"gameSlug":           "global",
				"promptTemplate":     "detect-v3",
				"responseSchemaJson": "{}",
				"initial":            true,
				"order":              1,
			},
		},
	})
	invalidReq := httptest.NewRequest(http.MethodPut, "/api/admin/llm/scenario-packages/"+packageID, bytes.NewReader(invalidBody))
	invalidReq.Header.Set("Authorization", "Bearer "+adminToken)
	invalidRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidRes, invalidReq)
	if invalidRes.Code != http.StatusBadRequest {
		t.Fatalf("scenario package update with unknown step field status = %d body=%s", invalidRes.Code, invalidRes.Body.String())
	}
}

func TestAdminLLMModelConfigRoutes(t *testing.T) {
	promptsService := prompts.NewService()
	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, streamers.NewService(), nil, promptsService, nil, nil, ClientConfigResponse{})
	adminToken := buildToken(t, "admin-1")

	createBody := []byte(`{"name":"Primary","model":"gemini-2.5-pro","metadataJson":"{\"provider\":\"google\"}"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/model-configs", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create model config status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create model config: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing model config id: %#v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/model-configs", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminToken)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list model config status=%d body=%s", listRes.Code, listRes.Body.String())
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/model-configs/"+id+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+adminToken)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("activate model config status=%d body=%s", activateRes.Code, activateRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/llm/model-configs/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("delete model config status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestAdminLLMGameScenarioRoutes(t *testing.T) {
	promptsService := prompts.NewService()
	cfg, err := promptsService.CreateLLMModelConfig(context.Background(), prompts.LLMModelConfigUpsertRequest{
		Name:    "Primary",
		Model:   "gemini-2.5-flash",
		ActorID: "admin-1",
	})
	if err != nil {
		t.Fatalf("create llm config: %v", err)
	}
	rootPkg, err := promptsService.CreateScenarioPackage(context.Background(), prompts.ScenarioPackageCreateRequest{
		Name:             "root pkg",
		GameSlug:         "global",
		LLMModelConfigID: cfg.ID,
		ActorID:          "admin-1",
		Steps: []prompts.ScenarioStep{
			{ID: "root_step", Name: "Root", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1},
		},
	})
	if err != nil {
		t.Fatalf("create root package: %v", err)
	}
	targetPkg, err := promptsService.CreateScenarioPackage(context.Background(), prompts.ScenarioPackageCreateRequest{
		Name:             "target pkg",
		GameSlug:         "cs2",
		LLMModelConfigID: cfg.ID,
		ActorID:          "admin-1",
		Steps: []prompts.ScenarioStep{
			{ID: "target_step", Name: "Target", PromptTemplate: "mode", ResponseSchemaJSON: `{}`, Initial: true, Order: 1, EntryCondition: `game == "cs2"`},
		},
	})
	if err != nil {
		t.Fatalf("create target package: %v", err)
	}

	handler := NewHandler(zap.NewNop(), func() bool { return true }, nil, buildAuthService(t), admin.NewService([]string{"admin-1"}), nil, streamers.NewService(), nil, promptsService, nil, nil, ClientConfigResponse{})
	adminToken := buildToken(t, "admin-1")

	createBody, _ := json.Marshal(map[string]any{
		"name":          "cs2 game scenario",
		"gameSlug":      "cs2",
		"initialNodeId": "node-root",
		"nodes": []map[string]any{
			{"id": "node-root", "alias": "Root Node", "scenarioPackageId": rootPkg.ID},
			{"id": "node-target", "alias": "Target Node", "scenarioPackageId": targetPkg.ID},
		},
		"transitions": []map[string]any{
			{
				"id": "tr-1", "fromNodeId": "node-root", "toNodeId": "node-target", "condition": `game == "cs2"`, "priority": 10,
				"terminalConditions": []map[string]any{
					{
						"id":              "tm-1",
						"gameTitle":       map[string]string{"ru": "Победа CT", "en": "CT win"},
						"defaultLanguage": "ru",
						"outcomeTemplates": []map[string]any{
							{"id": "ct", "title": map[string]string{"ru": "CT", "en": "CT"}, "condition": `winner == "ct" && side == "ct"`, "priority": 100},
							{"id": "t", "title": map[string]string{"ru": "T", "en": "T"}, "condition": `winner == "t" && side == "t"`, "priority": 90},
						},
						"priority": 100,
					},
				},
			},
		},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/game-scenarios", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("game scenario create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created game scenario: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("expected created id, got %#v", created)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/game-scenarios/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer "+adminToken)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("game scenario get status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	var loaded map[string]any
	if err := json.Unmarshal(getRes.Body.Bytes(), &loaded); err != nil {
		t.Fatalf("decode game scenario: %v", err)
	}
	if loaded["name"] != "cs2 game scenario" {
		t.Fatalf("expected original name, got %#v", loaded["name"])
	}

	updateBody, _ := json.Marshal(map[string]any{
		"name":          "cs2 game scenario updated",
		"gameSlug":      "cs2",
		"initialNodeId": "node-target",
		"nodes": []map[string]any{
			{"id": "node-root", "alias": "Root Node", "scenarioPackageId": rootPkg.ID},
			{"id": "node-target", "alias": "Target Node", "scenarioPackageId": targetPkg.ID},
		},
		"transitions": []map[string]any{
			{"id": "tr-2", "fromNodeId": "node-target", "toNodeId": "node-root", "condition": `game == "cs2"`, "priority": 9},
		},
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/llm/game-scenarios/"+id, bytes.NewReader(updateBody))
	updateReq.Header.Set("Authorization", "Bearer "+adminToken)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("game scenario update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(updateRes.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated game scenario: %v", err)
	}
	if updated["name"] != "cs2 game scenario updated" {
		t.Fatalf("expected updated name, got %#v", updated["name"])
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/game-scenarios/"+id+"/activate", nil)
	activateReq.Header.Set("Authorization", "Bearer "+adminToken)
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("game scenario activate status=%d body=%s", activateRes.Code, activateRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/llm/game-scenarios/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("game scenario delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}
