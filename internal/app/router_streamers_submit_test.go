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
	"github.com/funpot/funpot-go-core/internal/streamers"
)

func TestSubmitStreamerUsesTwitchNickname(t *testing.T) {
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

	body := bytes.NewBufferString(`{"twitchNickname":"Best_Streamer"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/streamers", body)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	items := streamersService.List(req.Context(), "best_streamer", "pending", 1)
	if len(items) != 1 {
		t.Fatalf("expected one streamer, got %d", len(items))
	}
	if items[0].TwitchNickname != "best_streamer" {
		t.Fatalf("expected stored twitchNickname best_streamer, got %q", items[0].TwitchNickname)
	}
}

func TestSubmitStreamerFallsBackToLegacyTwitchUsername(t *testing.T) {
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

	payload, _ := json.Marshal(map[string]string{"twitchUsername": "LegacyName"})
	req := httptest.NewRequest(http.MethodPost, "/api/streamers", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	items := streamersService.List(req.Context(), "legacyname", "pending", 1)
	if len(items) != 1 {
		t.Fatalf("expected one streamer, got %d", len(items))
	}
	if items[0].TwitchNickname != "legacyname" {
		t.Fatalf("expected stored twitchNickname legacyname, got %q", items[0].TwitchNickname)
	}
}

func TestSubmitStreamerStartsAnalysisStatusWhenHookConfigured(t *testing.T) {
	streamersService := streamers.NewService()
	streamersService.SetSubmissionHook(func(_ context.Context, streamerID string) error {
		if streamerID == "" {
			t.Fatal("expected streamerID in submission hook")
		}
		return nil
	})
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

	body := bytes.NewBufferString(`{"twitchNickname":"HookedStreamer"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/streamers", body)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	items := streamersService.List(req.Context(), "hookedstreamer", "pending", 1)
	if len(items) != 1 {
		t.Fatalf("expected one streamer, got %d", len(items))
	}

	status := streamersService.GetLLMStatus(req.Context(), items[0].ID)
	if status.State != "active" {
		t.Fatalf("expected active status after submission hook, got %q", status.State)
	}
}
