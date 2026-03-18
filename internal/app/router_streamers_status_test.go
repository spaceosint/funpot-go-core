package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

func TestStreamerStatusReturnsAggregatedLLMState(t *testing.T) {
	streamersService := streamers.NewService()
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		streamersService,
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	adminToken := buildToken(t, "admin-1")
	for _, payload := range []map[string]any{
		{
			"runId":           "run-1",
			"stage":           "detector",
			"label":           "cs_detected",
			"confidence":      0.94,
			"promptVersionId": "prompt-a",
		},
		{
			"runId":           "run-1",
			"stage":           "ranked_mode",
			"label":           "competitive",
			"confidence":      0.77,
			"promptVersionId": "prompt-b",
		},
	} {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/streamers/str-1/llm-decisions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", res.Code)
		}
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/streamers/str-1/status", nil)
	statusReq.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	statusRes := httptest.NewRecorder()
	handler.ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", statusRes.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(statusRes.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	if got["state"] != "active" {
		t.Fatalf("expected active state, got %#v", got)
	}
	if got["currentStage"] != "ranked_mode" || got["currentLabel"] != "competitive" {
		t.Fatalf("expected latest decision to drive current status, got %#v", got)
	}
	if got["detectedGameKey"] != "counter_strike" {
		t.Fatalf("expected detected game to be inferred from stage_a, got %#v", got)
	}
	latestByStage, ok := got["latestByStage"].([]any)
	if !ok || len(latestByStage) != 2 {
		t.Fatalf("expected two latest stage snapshots, got %#v", got["latestByStage"])
	}
}

func TestStreamerStatusReturnsIdleWhenNoDecisionsYet(t *testing.T) {
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		nil,
		streamers.NewService(),
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/streamers/str-idle/status", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	if got["state"] != "idle" {
		t.Fatalf("expected idle state, got %#v", got)
	}
}
