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
	                        "content": {"parts": [{"text": "{\"updated_state\":{\"game\":\"cs2\"}}"}]}
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
	if result.Label != "state_updated" {
		t.Fatalf("expected synthesized label state_updated, got %q", result.Label)
	}
	if result.Confidence != 1 {
		t.Fatalf("expected synthesized confidence 1, got %v", result.Confidence)
	}
	if result.TokensIn != 111 || result.TokensOut != 22 {
		t.Fatalf("unexpected token usage: in=%d out=%d", result.TokensIn, result.TokensOut)
	}
	if !strings.Contains(gotBody, `"mimeType":"video/mp4"`) {
		t.Fatalf("expected transport stream mime type in request body: %s", gotBody)
	}
	if !strings.Contains(gotBody, "Detect the game being played") {
		t.Fatalf("expected prompt template in request body: %s", gotBody)
	}
}

func TestGeminiStageClassifierReusesChatSessionWithoutResendingPrompt(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.mp4")
	if err := os.WriteFile(chunkPath, []byte("fake transport stream"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	requestBodies := make([]string, 0, 2)
	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:  "gemini-key",
		BaseURL: "https://gemini.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			requestBodies = append(requestBodies, string(body))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"},\"delta\":[\"score_seen\"],\"next_needed_evidence\":[\"winner_banner\"],\"final_outcome\":\"unknown\"}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 120, "candidatesTokenCount": 30, "totalTokenCount": 150}
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	req := StageRequest{
		StreamerID: "str-1",
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: chunkPath, CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)},
		Prompt: prompts.PromptVersion{
			ID:       "prompt-1",
			Stage:    "match_update",
			Template: "Update the game state",
			Model:    "gemini",
		},
		PreviousState: `{"status":"discovering"}`,
	}
	if _, err := classifier.Classify(context.Background(), req); err != nil {
		t.Fatalf("first Classify() error = %v", err)
	}
	req.Chunk.CapturedAt = req.Chunk.CapturedAt.Add(10 * time.Second)
	if _, err := classifier.Classify(context.Background(), req); err != nil {
		t.Fatalf("second Classify() error = %v", err)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requestBodies))
	}
	if !strings.Contains(requestBodies[0], "Use this admin-managed tracker prompt as the source of truth") {
		t.Fatalf("expected first request to include full prompt bootstrap, got %s", requestBodies[0])
	}
	if !strings.Contains(requestBodies[1], "Continue the existing match chat session.") {
		t.Fatalf("expected second request to include continuation marker, got %s", requestBodies[1])
	}
	if !strings.Contains(requestBodies[1], "Expected response schema:") {
		t.Fatalf("expected second request to include active state schema reminder, got %s", requestBodies[1])
	}
	if strings.Contains(requestBodies[1], "Previous persisted tracker state JSON:") {
		t.Fatalf("expected second request to avoid re-sending full state, got %s", requestBodies[1])
	}
}

func TestGeminiStageClassifierSanitizesGenerationConfig(t *testing.T) {
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
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"updated_state\":{\"game\":\"cs2\"}}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 10}
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "detector",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt: prompts.PromptVersion{
			Stage:       "detector",
			Template:    "Detect the game being played",
			Model:       "gemini",
			Temperature: -1,
			MaxTokens:   geminiMaxOutputTokensLimit + 1,
			TimeoutMS:   1000,
		},
	})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if strings.Contains(gotBody, `"temperature":`) {
		t.Fatalf("expected invalid temperature to be omitted from generation config: %s", gotBody)
	}
	if strings.Contains(gotBody, `"maxOutputTokens":`) {
		t.Fatalf("expected oversized maxOutputTokens to be omitted from generation config: %s", gotBody)
	}
}

func TestGeminiStageClassifierRotatesChatWhenTokenBudgetReached(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.mp4")
	if err := os.WriteFile(chunkPath, []byte("fake transport stream"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	requestBodies := make([]string, 0, 2)
	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:        "gemini-key",
		BaseURL:       "https://gemini.test",
		ChatMaxTokens: 100,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			requestBodies = append(requestBodies, string(body))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"},\"delta\":[\"score_seen\"],\"next_needed_evidence\":[\"winner_banner\"],\"final_outcome\":\"unknown\"}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 120, "candidatesTokenCount": 30, "totalTokenCount": 150}
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	req := StageRequest{
		StreamerID: "str-1",
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: chunkPath, CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)},
		Prompt: prompts.PromptVersion{
			ID:       "prompt-1",
			Stage:    "match_update",
			Template: "Update the game state",
			Model:    "gemini",
		},
		PreviousState: `{"status":"discovering"}`,
	}
	if _, err := classifier.Classify(context.Background(), req); err != nil {
		t.Fatalf("first Classify() error = %v", err)
	}
	req.Chunk.CapturedAt = req.Chunk.CapturedAt.Add(10 * time.Second)
	if _, err := classifier.Classify(context.Background(), req); err != nil {
		t.Fatalf("second Classify() error = %v", err)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requestBodies))
	}
	if !strings.Contains(requestBodies[0], "Use this admin-managed tracker prompt as the source of truth") {
		t.Fatalf("expected first request to include full prompt bootstrap, got %s", requestBodies[0])
	}
	if !strings.Contains(requestBodies[1], "Use this admin-managed tracker prompt as the source of truth") {
		t.Fatalf("expected second request to rotate chat and include bootstrap prompt again, got %s", requestBodies[1])
	}
}

func TestGeminiStageClassifierDoesNotResetSessionOnEmptyContinuationResponse(t *testing.T) {
	dir := t.TempDir()
	chunkPath := filepath.Join(dir, "chunk.mp4")
	if err := os.WriteFile(chunkPath, []byte("fake transport stream"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}

	requestBodies := make([]string, 0, 4)
	requestCount := 0
	classifier, err := NewGeminiStageClassifier(GeminiClassifierConfig{
		APIKey:  "gemini-key",
		BaseURL: "https://gemini.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			requestCount++
			requestBodies = append(requestBodies, string(body))
			if requestCount == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"},\"delta\":[\"score_seen\"],\"next_needed_evidence\":[\"winner_banner\"],\"final_outcome\":\"unknown\"}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 120, "candidatesTokenCount": 30, "totalTokenCount": 150}
                }`)),
				}, nil
			}
			if requestCount == 2 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": []},
                        "finishReason": "STOP"
                    }],
                    "usageMetadata": {"promptTokenCount": 90, "candidatesTokenCount": 0, "totalTokenCount": 90}
                }`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
                    "candidates": [{
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"},\"delta\":[\"winner_seen\"],\"next_needed_evidence\":[],\"final_outcome\":\"win\"}"}]}
                    }],
                    "usageMetadata": {"promptTokenCount": 140, "candidatesTokenCount": 25, "totalTokenCount": 165}
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	req := StageRequest{
		StreamerID: "str-1",
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: chunkPath, CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)},
		Prompt: prompts.PromptVersion{
			ID:       "prompt-1",
			Stage:    "match_update",
			Template: "Update the game state",
			Model:    "gemini",
		},
		PreviousState: `{"status":"discovering"}`,
	}
	if _, err := classifier.Classify(context.Background(), req); err != nil {
		t.Fatalf("first Classify() error = %v", err)
	}
	req.Chunk.CapturedAt = req.Chunk.CapturedAt.Add(10 * time.Second)
	if _, err := classifier.Classify(context.Background(), req); err == nil {
		t.Fatal("expected second Classify() to fail with empty response")
	} else {
		if !strings.Contains(err.Error(), ErrGeminiEmptyResponse.Error()) {
			t.Fatalf("expected empty response error, got %v", err)
		}
	}
	req.Chunk.CapturedAt = req.Chunk.CapturedAt.Add(10 * time.Second)
	result, err := classifier.Classify(context.Background(), req)
	if err != nil {
		t.Fatalf("third Classify() error = %v", err)
	}
	if result.FinalOutcome != "win" {
		t.Fatalf("expected recovered outcome win, got %q", result.FinalOutcome)
	}
	if len(requestBodies) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(requestBodies))
	}
	if !strings.Contains(requestBodies[1], "Continue the existing match chat session.") {
		t.Fatalf("expected second request to be continuation, got %s", requestBodies[1])
	}
	if !strings.Contains(requestBodies[2], "Continue the existing match chat session.") {
		t.Fatalf("expected third request to keep existing chat session after empty response, got %s", requestBodies[2])
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
		StreamerID:     "str-42",
		Stage:          "match_update",
		Chunk:          ChunkRef{Reference: "/tmp/chunk.mp4", CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)},
		Prompt:         prompts.PromptVersion{Template: "Update the CS2 tracker state"},
		ResponseSchema: `state_schema[CS2 v1]: [{"key":"score.ct"}]`,
	})
	for _, fragment := range []string{
		"Streamer ID: str-42",
		"Chunk reference: /tmp/chunk.mp4",
		"Previous persisted tracker state JSON:",
		defaultTrackerState(),
		"Do not add keys that are not present in the schema/template.",
		"Expected response schema:",
		"state_schema[CS2 v1]",
	} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected instruction to contain %q, got %s", fragment, instruction)
		}
	}
}

func TestBuildGeminiContinuationInstructionIncludesExpectedResponseSchema(t *testing.T) {
	instruction := buildGeminiContinuationInstruction(StageRequest{
		StreamerID:     "str-42",
		Stage:          "match_update",
		Chunk:          ChunkRef{Reference: "/tmp/chunk.mp4", CapturedAt: time.Date(2025, 1, 1, 12, 0, 10, 0, time.UTC)},
		Prompt:         prompts.PromptVersion{Template: "Update the CS2 tracker state"},
		ResponseSchema: `state_schema[CS2 v1]: [{"key":"score.ct"}]`,
	})
	for _, fragment := range []string{
		"Continue the existing match chat session.",
		"Expected response schema:",
		`state_schema[CS2 v1]: [{"key":"score.ct"}]`,
	} {
		if !strings.Contains(instruction, fragment) {
			t.Fatalf("expected continuation instruction to contain %q, got %s", fragment, instruction)
		}
	}
	for _, unexpected := range []string{
		"Active rule set:",
		"Use this admin-managed tracker prompt as the source of truth",
	} {
		if strings.Contains(instruction, unexpected) {
			t.Fatalf("expected continuation instruction to avoid %q, got %s", unexpected, instruction)
		}
	}
}

func TestGeminiStageClassifierAcceptsTrackerResponseWithoutLabel(t *testing.T) {
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
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"},\"delta\":[\"score_seen\"],\"next_needed_evidence\":[\"winner_banner\"],\"final_outcome\":\"unknown\"}"}]}
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
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt:     prompts.PromptVersion{Stage: "match_update", Template: "Update the game state", Model: "gemini", MaxTokens: 128, TimeoutMS: 1000},
	})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if result.Label != "state_updated" {
		t.Fatalf("expected synthesized label state_updated, got %q", result.Label)
	}
	if result.UpdatedStateJSON != `{"status":"live"}` {
		t.Fatalf("expected updated state payload, got %s", result.UpdatedStateJSON)
	}
	if result.FinalOutcome != "unknown" {
		t.Fatalf("expected final outcome unknown, got %q", result.FinalOutcome)
	}
}

func TestGeminiStageClassifierAcceptsSchemaDrivenTrackerPayloadWithoutLegacyStateFields(t *testing.T) {
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
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"}}"}]}
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
		Stage:      "match_update",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt:     prompts.PromptVersion{Stage: "match_update", Template: "Update the game state", Model: "gemini", MaxTokens: 128, TimeoutMS: 1000},
	})
	if err != nil {
		t.Fatalf("expected schema-driven tracker payload to pass, got %v", err)
	}
	if result.Label != "state_updated" {
		t.Fatalf("expected label from response, got %q", result.Label)
	}
	if result.UpdatedStateJSON != `{"status":"live"}` {
		t.Fatalf("expected updated_state to be passed through, got %q", result.UpdatedStateJSON)
	}
}

func TestGeminiStageClassifierDoesNotBackfillTrackerStartPayload(t *testing.T) {
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
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"}}"}]}
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
		Stage:      "Start",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt:     prompts.PromptVersion{Stage: "Start", Template: "Update the game state", Model: "gemini", MaxTokens: 128, TimeoutMS: 1000},
	})
	if err != nil {
		t.Fatalf("expected start stage payload without backfill to pass, got error %v", err)
	}
	if result.UpdatedStateJSON != `{"status":"live"}` {
		t.Fatalf("expected updated_state to be preserved, got %q", result.UpdatedStateJSON)
	}
	if strings.TrimSpace(result.EvidenceDeltaJSON) != "" {
		t.Fatalf("expected no delta fallback, got %q", result.EvidenceDeltaJSON)
	}
	if strings.TrimSpace(result.NextEvidenceJSON) != "" {
		t.Fatalf("expected no next_needed_evidence fallback, got %q", result.NextEvidenceJSON)
	}
	if strings.TrimSpace(result.FinalOutcome) != "" {
		t.Fatalf("expected no final_outcome fallback, got %q", result.FinalOutcome)
	}
}

func TestGeminiStageClassifierKeepsNullFinalOutcomeEmptyWhenSchemaOmitsFallback(t *testing.T) {
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
                        "content": {"parts": [{"text": "{\"updated_state\":{\"status\":\"live\"},\"delta\":[],\"next_needed_evidence\":[],\"final_outcome\":null}"}]}
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
		Stage:      "Start",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt:     prompts.PromptVersion{Stage: "Start", Template: "Update the game state", Model: "gemini", MaxTokens: 128, TimeoutMS: 1000},
	})
	if err != nil {
		t.Fatalf("expected null final_outcome to pass without normalization, got error %v", err)
	}
	if strings.TrimSpace(result.FinalOutcome) != "" {
		t.Fatalf("expected empty final_outcome when model returns null, got %q", result.FinalOutcome)
	}
}

func TestGeminiStageClassifierDoesNotCoerceNonTrackerStatePayload(t *testing.T) {
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
                        "content": {"parts": [{"text": "{\"status\":\"discovering\"}"}]}
                    }]
                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "detector",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt: prompts.PromptVersion{
			ID:        "prompt-detector",
			Stage:     "detector",
			Template:  "Detect scene",
			Model:     "gemini",
			MaxTokens: 128,
			TimeoutMS: 1000,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict-schema unknown field error for non-tracker stage, got %v", err)
	}
}

func TestGeminiStageClassifierReportsEmptyResponseDiagnostics(t *testing.T) {
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
	                    "candidates": [{"finishReason": "MAX_TOKENS", "content": {"parts": []}}],
	                    "promptFeedback": {"blockReason": "SAFETY", "safetyRatings": [{"category": "HARM_CATEGORY_HATE_SPEECH", "probability": "HIGH", "blocked": true}]}
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
	if err == nil {
		t.Fatal("expected empty response diagnostics error")
	}
	for _, fragment := range []string{
		ErrGeminiEmptyResponse.Error(),
		"finish_reasons=MAX_TOKENS",
		"block_reason=SAFETY",
		"blocked_safety_categories=HARM_CATEGORY_HATE_SPEECH:HIGH",
		`body="{`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected error to contain %q, got %v", fragment, err)
		}
	}
}

func TestGeminiStageClassifierReportsNoCandidatesInEmptyResponse(t *testing.T) {
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
				Body:       io.NopCloser(strings.NewReader(`{"candidates": []}`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "detector",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt:     prompts.PromptVersion{Stage: "detector", Template: "Detect the game", Model: "gemini", MaxTokens: 128, TimeoutMS: 1000},
	})
	if err == nil || !strings.Contains(err.Error(), "candidates=0") {
		t.Fatalf("expected candidates=0 diagnostic, got %v", err)
	}
}

func TestGeminiStageClassifierReportsEmptyParsedPayloadDiagnostics(t *testing.T) {
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
	                    "candidates": [{"content": {"parts": [{"text": "{}"}]}}]
	                }`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewGeminiStageClassifier() error = %v", err)
	}

	_, err = classifier.Classify(context.Background(), StageRequest{
		StreamerID: "str-1",
		Stage:      "Start",
		Chunk:      ChunkRef{Reference: chunkPath},
		Prompt: prompts.PromptVersion{
			ID:        "prompt-start",
			Stage:     "Start",
			Template:  "Update tracker state",
			Model:     "gemini",
			MaxTokens: 128,
			TimeoutMS: 1000,
		},
	})
	if err == nil {
		t.Fatal("expected parsed empty response diagnostics error")
	}
	for _, fragment := range []string{
		ErrGeminiEmptyResponse.Error(),
		"stage=Start",
		"streamer_id=str-1",
		"prompt_id=prompt-start",
		`raw_text="{}"`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected error to contain %q, got %v", fragment, err)
		}
	}
}
