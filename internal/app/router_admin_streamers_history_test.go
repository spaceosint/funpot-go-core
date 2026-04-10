package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/media"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

type fakeAdminVideoManager struct {
	videosByStreamer  map[string][]media.UploadedVideo
	deletedByStreamer map[string]int
}

func (f *fakeAdminVideoManager) ListUploadedVideos(streamerID string) []media.UploadedVideo {
	items := f.videosByStreamer[streamerID]
	out := make([]media.UploadedVideo, len(items))
	copy(out, items)
	return out
}

func (f *fakeAdminVideoManager) DeleteStreamerVideos(_ context.Context, streamerID string) (int, error) {
	count := len(f.videosByStreamer[streamerID])
	f.deletedByStreamer[streamerID] = count
	delete(f.videosByStreamer, streamerID)
	return count, nil
}

func TestAdminStreamerHistoryGetWithPagination(t *testing.T) {
	streamersService := streamers.NewService()
	videoManager := &fakeAdminVideoManager{videosByStreamer: map[string][]media.UploadedVideo{
		"str-1": {{ID: "video-1", URL: "https://video.bunnycdn.com/library/lib-1/videos/video-1", CreatedAt: "2026-01-01T00:00:00Z"}},
	}, deletedByStreamer: map[string]int{}}
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
		videoManager,
		nil,
		ClientConfigResponse{},
	)

	for _, req := range []streamers.RecordDecisionRequest{
		{RunID: "run-1", StreamerID: "str-1", Stage: "root_detect", Label: "cs_detected", Confidence: 0.9, UpdatedStateJSON: `{"state":{"game":"cs2"}}`},
		{RunID: "run-1", StreamerID: "str-1", Stage: "mode_detect", Label: "competitive", Confidence: 0.8, UpdatedStateJSON: `{"state":{"mode":"competitive"}}`},
		{RunID: "run-1", StreamerID: "str-1", Stage: "state_tracker", Label: "score_update", Confidence: 0.7, UpdatedStateJSON: `{"state":{"ct":10,"t":8}}`},
	} {
		if _, err := streamersService.RecordLLMDecision(context.Background(), req); err != nil {
			t.Fatalf("RecordLLMDecision() error = %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/streamers/str-1/llm-history?page=2&pageSize=2", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["total"] != float64(3) {
		t.Fatalf("expected total=3 got %#v", payload["total"])
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item on second page, got %#v", payload["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["stepName"] != "state_tracker" {
		t.Fatalf("expected state_tracker step, got %#v", item["stepName"])
	}
	videos, ok := payload["videos"].([]any)
	if !ok || len(videos) != 1 {
		t.Fatalf("expected one uploaded video, got %#v", payload["videos"])
	}
}

func TestAdminStreamerHistoryDeleteRemovesDecisionsAndVideos(t *testing.T) {
	streamersService := streamers.NewService()
	videoManager := &fakeAdminVideoManager{videosByStreamer: map[string][]media.UploadedVideo{
		"str-1": {{ID: "video-1"}, {ID: "video-2"}},
	}, deletedByStreamer: map[string]int{}}
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
		videoManager,
		nil,
		ClientConfigResponse{},
	)
	if _, err := streamersService.RecordLLMDecision(context.Background(), streamers.RecordDecisionRequest{RunID: "run", StreamerID: "str-1", Stage: "root", Label: "ok", Confidence: 0.9}); err != nil {
		t.Fatalf("RecordLLMDecision() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/streamers/str-1/llm-history", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	if got := len(streamersService.ListAllLLMDecisions(context.Background(), "str-1")); got != 0 {
		t.Fatalf("expected cleared history, got %d decisions", got)
	}
	if got := videoManager.deletedByStreamer["str-1"]; got != 2 {
		t.Fatalf("expected 2 deleted videos, got %d", got)
	}
}
