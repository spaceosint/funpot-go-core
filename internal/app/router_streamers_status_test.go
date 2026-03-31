package app

import (
	"context"
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
		nil,
		ClientConfigResponse{},
	)

	for _, req := range []streamers.RecordDecisionRequest{
		{RunID: "run-1", StreamerID: "str-1", Stage: "detector", Label: "cs_detected", Confidence: 0.94, PromptVersionID: "prompt-a"},
		{RunID: "run-1", StreamerID: "str-1", Stage: "ranked_mode", Label: "competitive", Confidence: 0.77, PromptVersionID: "prompt-b"},
	} {
		if _, err := streamersService.RecordLLMDecision(context.Background(), req); err != nil {
			t.Fatalf("RecordLLMDecision() error = %v", err)
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
	history, ok := got["history"].([]any)
	if !ok || len(history) != 2 {
		t.Fatalf("expected full history with two decisions, got %#v", got["history"])
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
	history, ok := got["history"].([]any)
	if !ok || len(history) != 0 {
		t.Fatalf("expected empty history in idle status, got %#v", got["history"])
	}
}

func TestStreamerTrackingDeleteStopsMonitoring(t *testing.T) {
	streamersService := streamers.NewService()
	if _, err := streamersService.Submit(context.Background(), "stopstreamer", "user-1"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	items := streamersService.List(context.Background(), "stopstreamer", "pending", 1)
	if len(items) != 1 {
		t.Fatalf("expected one streamer, got %d", len(items))
	}

	stoppedID := ""
	streamersService.SetTrackingStopHook(func(_ context.Context, streamerID string) error {
		stoppedID = streamerID
		return nil
	})
	streamersService.MarkAnalysisActive(items[0].ID)

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
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodDelete, "/api/streamers/"+items[0].ID+"/tracking", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if stoppedID != items[0].ID {
		t.Fatalf("expected stop hook for %q, got %q", items[0].ID, stoppedID)
	}

	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got["state"] != "stopped" {
		t.Fatalf("expected stopped state, got %#v", got)
	}
}
