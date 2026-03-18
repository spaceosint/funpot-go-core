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

func TestStreamerLLMDecisionsCreateAndList(t *testing.T) {
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

	adminToken := buildToken(t, "admin-1")
	body, _ := json.Marshal(map[string]any{
		"runId":              "run-1",
		"stage":              "detector",
		"label":              "cs_detected",
		"confidence":         0.93,
		"chunkCapturedAt":    "2025-01-01T12:00:00Z",
		"promptVersionId":    "prompt-1",
		"promptText":         "detect stage",
		"model":              "gemini-2.0-flash",
		"temperature":        0.2,
		"maxTokens":          512,
		"timeoutMs":          3500,
		"chunkRef":           "streamlink://str-1/100",
		"requestRef":         "gemini-request-1",
		"responseRef":        "gemini-response-1",
		"rawResponse":        "{}",
		"tokensIn":           123,
		"tokensOut":          19,
		"latencyMs":          120,
		"transitionOutcome":  "cs_detected",
		"transitionTerminal": true,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/streamers/str-1/llm-decisions", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createRes.Code)
	}

	userToken := buildToken(t, "user-1")
	listReq := httptest.NewRequest(http.MethodGet, "/api/streamers/str-1/llm-decisions?limit=1", nil)
	listReq.Header.Set("Authorization", "Bearer "+userToken)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listRes.Code)
	}

	var items []map[string]any
	if err := json.Unmarshal(listRes.Body.Bytes(), &items); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	if items[0]["promptVersionId"] != "prompt-1" || items[0]["requestRef"] != "gemini-request-1" || items[0]["transitionOutcome"] != "cs_detected" {
		t.Fatalf("expected metadata to be returned, got %#v", items[0])
	}
}

func TestStreamerLLMDecisionCreateForbiddenForNonAdmin(t *testing.T) {
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

	body, _ := json.Marshal(map[string]any{
		"runId":      "run-1",
		"stage":      "detector",
		"label":      "cs_detected",
		"confidence": 0.93,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/streamers/str-1/llm-decisions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestStreamerLLMDecisionCreateRejectsInvalidChunkTimestamp(t *testing.T) {
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

	body, _ := json.Marshal(map[string]any{
		"runId":           "run-1",
		"stage":           "detector",
		"label":           "cs_detected",
		"confidence":      0.93,
		"chunkCapturedAt": "not-a-time",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/streamers/str-1/llm-decisions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}
