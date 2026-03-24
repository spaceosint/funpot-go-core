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

func TestAdminLLMStateSchemaAndRuleSetRoutes(t *testing.T) {
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
	stateSchemaBodyWithInitialState, _ := json.Marshal(map[string]any{
		"gameSlug":         "cs2",
		"name":             "CS2 tracker with initial state",
		"initialStateJson": `{"session_type":"single_match_single_chat","game":"cs2","session_status":{"value":"unknown"}}`,
	})
	stateInitialReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/state-schemas", bytes.NewReader(stateSchemaBodyWithInitialState))
	stateInitialReq.Header.Set("Authorization", "Bearer "+adminToken)
	stateInitialRes := httptest.NewRecorder()
	handler.ServeHTTP(stateInitialRes, stateInitialReq)
	if stateInitialRes.Code != http.StatusCreated {
		t.Fatalf("state schema with initial state create status = %d body=%s", stateInitialRes.Code, stateInitialRes.Body.String())
	}

	ruleBody, _ := json.Marshal(map[string]any{
		"gameSlug":          "cs2",
		"name":              "CS2 rules",
		"ruleItems":         []map[string]any{{"fieldKey": "score.ct", "operation": "set", "confidenceMode": "strict"}},
		"finalizationRules": []map[string]any{{"priority": 1, "condition": "final_banner_seen", "action": "finalize_win"}},
	})
	ruleReq := httptest.NewRequest(http.MethodPost, "/api/admin/llm/rule-sets", bytes.NewReader(ruleBody))
	ruleReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleRes, ruleReq)
	if ruleRes.Code != http.StatusCreated {
		t.Fatalf("rule set create status = %d body=%s", ruleRes.Code, ruleRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/state-schemas", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminToken)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("state schema list status = %d", listRes.Code)
	}

	ruleListReq := httptest.NewRequest(http.MethodGet, "/api/admin/llm/rule-sets", nil)
	ruleListReq.Header.Set("Authorization", "Bearer "+adminToken)
	ruleListRes := httptest.NewRecorder()
	handler.ServeHTTP(ruleListRes, ruleListReq)
	if ruleListRes.Code != http.StatusOK {
		t.Fatalf("rule set list status = %d", ruleListRes.Code)
	}
}
