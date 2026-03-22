package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/funpot/funpot-go-core/internal/prompts"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewGeminiStageClassifierRequiresAPIKey(t *testing.T) {
	if _, err := NewGeminiStageClassifier(GeminiClassifierConfig{}); err == nil {
		t.Fatal("expected missing api key error")
	}
}

func TestGeminiStageClassifierClassify(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.mp4")
	if err := os.WriteFile(chunkPath, []byte("fake transport stream"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	var gotBody string
	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:  "gemini-key",
		BaseURL: "https://gemini.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			gotBody = string(body)
			if req.URL.String() != "https://gemini.test/v1beta/models/gemini-2.0-flash:generateContent?key=gemini-key" {
				return nil, fmt.Errorf("unexpected url %s", req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"label\":\"cs_detected\",\"confidence\":0.93,\"summary\":\"Counter-Strike HUD visible\"}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 111, "candidatesTokenCount": 22}
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	result, err := classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "detector",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt: prompts.PromptVersion{
			Stage:       "detector",
			Template:    "Detect the game being played",
			Model:       "gemini",
			Temperature: 0.2,
			MaxTokens:   128,
			TimeoutMS:   1000,
		},
	})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if result.Label != "cs_detected" {
		t.Fatalf("expected label cs_detected, got %q", result.Label)
	}
	if result.Confidence != 0.93 {
		t.Fatalf("expected confidence 0.93, got %v", result.Confidence)
	}
	if result.TokensIn != 111 || result.TokensOut != 22 {
		t.Fatalf("unexpected token usage: in=%d out=%d", result.TokensIn, result.TokensOut)
	}
	if !strings.Contains(gotBody, `"mimeType":"video/mp4"`) {
		t.Fatalf("expected transport stream mime type in request body: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"mediaResolution":"MEDIA_RESOLUTION_LOW"`) {
		t.Fatalf("expected low media resolution in generation config: %s", gotBody)
	}
	if !strings.Contains(gotBody, "Detect the game being played") {
		t.Fatalf("expected prompt template in request body: %s", gotBody)
	}
}

func TestGeminiStageClassifierRejectsLargeChunk(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.ts")
	if err := os.WriteFile(chunkPath, []byte("12345"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:         "gemini-key",
		BaseURL:        "https://gemini.test",
		MaxInlineBytes: 4,
		HTTPClient:     &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, fmt.Errorf("unexpected request") })},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "detector",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt: prompts.PromptVersion{
			Stage:     "detector",
			Template:  "Detect the game being played",
			Model:     "gemini-2.0-flash",
			MaxTokens: 128,
		},
	})
	if err == nil || !strings.Contains(err.Error(), ErrGeminiChunkTooLarge.Error()) {
		t.Fatalf("expected large chunk error, got %v", err)
	}
}

func TestGeminiStageClassifierRejectsUnsupportedChunkMimeType(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.ts")
	if err := os.WriteFile(chunkPath, []byte("fake transport stream"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:     "gemini-key",
		BaseURL:    "https://gemini.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, fmt.Errorf("unexpected request") })},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "detector",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt: prompts.PromptVersion{
			Stage:     "detector",
			Template:  "Detect the game being played",
			Model:     "gemini",
			MaxTokens: 128,
		},
	})
	if err == nil || !strings.Contains(err.Error(), ErrGeminiUnsupportedMIME.Error()) {
		t.Fatalf("expected unsupported mime error, got %v", err)
	}
	if !strings.Contains(err.Error(), "video/mp4") {
		t.Fatalf("expected conversion hint, got %v", err)
	}
}

func TestBuildGeminiInstructionUsesTrackerContract(t *testing.T) {
	instruction := buildGeminiInstruction(StageRequest{
		StreamerID: "str-42",
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: "/tmp/chunk.mp4", CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)},
		Prompt:     prompts.PromptVersion{Template: "Update the CS2 tracker state"},
	})
	for _, fragment := range []string{
		"Streamer ID: str-42",
		"Chunk reference: /tmp/chunk.mp4",
		"Previous persisted tracker state JSON:",
		defaultTrackerState(),
		"updated_state",
		"next_needed_evidence",
	} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected instruction to contain %q, got %s", fragment, instruction)
		}
	}
}

func TestGeminiStageClassifierRejectsTrackerResponseWithoutStatePayload(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.mp4")
	if err := os.WriteFile(chunkPath, []byte("fake transport stream"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:  "gemini-key",
		BaseURL: "https://gemini.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"label\":\"state_updated\",\"confidence\":0.93}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 111, "candidatesTokenCount": 22}
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt:     prompts.PromptVersion{Stage: "match_update", Template: "Update the game state", Model: "gemini", MaxTokens: 128, TimeoutMS: 1000},
	})
	if err == nil || !strings.Contains(err.Error(), "updated_state") {
		t.Fatalf("expected missing updated_state error, got %v", err)
	}
}
