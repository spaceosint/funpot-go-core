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
	"github.com/funpot/funpot-go-core/internal/streamers"
)

func TestAdminLLMStateSchemaRoutes(t *testing.T) {
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

	stateSchemaBody, _ := json.Marshal(map[string]any{
		"gameSlug": "cs2",
		"name":     "CS2 tracker",
		"fields":   []map[string]any{{"key": "score.ct", "type": "number"}},
	})
	stateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/state-schemas", bytes.NewReader(stateSchemaBody))
	stateReq.Header.Set("Authorization", "Bearer "+adminToken)
	stateRes := httptest.NewRecorder()
	handler.ServeHTTP(stateRes, stateReq)
	if stateRes.Code != http.StatusCreated {
		t.Fatalf("state schema create status = %d body=%s", stateRes.Code, stateRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/state-schemas", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminToken)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("state schema list status = %d", listRes.Code)
	}
	var schemas []map[string]any
	if err := json.Unmarshal(listRes.Body.Bytes(), &schemas); err != nil {
		t.Fatalf("state schema list decode error = %v", err)
	}
	if len(schemas) == 0 {
		t.Fatal("expected non-empty state schema list")
	}
	ruleSetBody, _ := json.Marshal(map[string]any{
		"gameSlug": "cs2",
		"name":     "CS2 finalize rules",
		"ruleItems": []map[string]any{
			{
				"id":             "r1",
				"fieldKey":       "score.ct",
				"operation":      "set",
				"evidenceKinds":  []string{"scoreboard"},
				"confidenceMode": "direct",
				"finalOnly":      false,
			},
		},
		"finalizationRules": []map[string]any{
			{
				"id":        "f1",
				"priority":  1,
				"condition": "score.ct == 13",
				"action":    "set_outcome_win",
			},
		},
	})
	ruleCreateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/rule-sets", bytes.NewReader(ruleSetBody))
	ruleCreateReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleCreateRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleCreateRes, ruleCreateReq)
	if ruleCreateRes.Code != http.StatusCreated {
		t.Fatalf("rule set create status = %d body=%s", ruleCreateRes.Code, ruleCreateRes.Body.String())
	}
	var createdRuleSet map[string]any
	if err := json.Unmarshal(ruleCreateRes.Body.Bytes(), &createdRuleSet); err != nil {
		t.Fatalf("rule set create decode error = %v", err)
	}
	ruleSetID, _ := createdRuleSet["id"].(string)
	if ruleSetID == "" {
		t.Fatalf("expected created rule set id, got %#v", createdRuleSet)
	}

	ruleListReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/rule-sets", nil)
	ruleListReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleListRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleListRes, ruleListReq)
	if ruleListRes.Code != http.StatusOK {
		t.Fatalf("rule set list status = %d body=%s", ruleListRes.Code, ruleListRes.Body.String())
	}

	ruleGetReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/rule-sets/"+ruleSetID, nil)
	ruleGetReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleGetRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleGetRes, ruleGetReq)
	if ruleGetRes.Code != http.StatusOK {
		t.Fatalf("rule set get status = %d body=%s", ruleGetRes.Code, ruleGetRes.Body.String())
	}

	ruleUpdateBody, _ := json.Marshal(map[string]any{
		"gameSlug": "cs2",
		"name":     "CS2 finalize rules v2",
		"ruleItems": []map[string]any{
			{
				"id":             "r1",
				"fieldKey":       "winner_state.winner_side",
				"operation":      "set",
				"evidenceKinds":  []string{"final_banner"},
				"confidenceMode": "direct",
				"finalOnly":      true,
			},
		},
		"finalizationRules": []map[string]any{
			{
				"id":        "f1",
				"priority":  1,
				"condition": "winner_state.winner_side == ct",
				"action":    "set_outcome_win",
			},
		},
	})
	ruleUpdateReq := httptest.NewRequest(http.MethodPut, "/api/admin/llm/rule-sets/"+ruleSetID, bytes.NewReader(ruleUpdateBody))
	ruleUpdateReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleUpdateRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleUpdateRes, ruleUpdateReq)
	if ruleUpdateRes.Code != http.StatusOK {
		t.Fatalf("rule set update status = %d body=%s", ruleUpdateRes.Code, ruleUpdateRes.Body.String())
	}

	ruleActivateReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/rule-sets/"+ruleSetID+"/activate", nil)
	ruleActivateReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleActivateRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleActivateRes, ruleActivateReq)
	if ruleActivateRes.Code != http.StatusOK {
		t.Fatalf("rule set activate status = %d body=%s", ruleActivateRes.Code, ruleActivateRes.Body.String())
	}

	ruleDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/llm/rule-sets/"+ruleSetID, nil)
	ruleDeleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleDeleteRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleDeleteRes, ruleDeleteReq)
	if ruleDeleteRes.Code != http.StatusNoContent {
		t.Fatalf("rule set delete status = %d body=%s", ruleDeleteRes.Code, ruleDeleteRes.Body.String())
	}
}

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

	createBody, _ := json.Marshal(map[string]any{
		"gameSlug": "global",
		"name":     "default graph",
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
		"transitions": []map[string]any{
			{"fromStepId": "root_detect", "toStepId": "cs2_mode", "condition": "game == 'cs2'", "priority": 1},
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
		"gameSlug": "global",
		"name":     "default graph v2",
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
		"transitions": []map[string]any{
			{"fromStepId": "root_detect", "toStepId": "cs2_mode", "condition": "game == 'cs2'", "priority": 1},
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
