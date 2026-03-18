package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

type fakeCapture struct {
	chunk ChunkRef
	err   error
}

func (f fakeCapture) Capture(_ context.Context, _ string) (ChunkRef, error) {
	if f.err != nil {
		return ChunkRef{}, f.err
	}
	return f.chunk, nil
}

type fakeClassifier struct {
	results    map[string]StageClassification
	errByStage map[string]error
}

func (f fakeClassifier) Classify(_ context.Context, input StageRequest) (StageClassification, error) {
	if err := f.errByStage[input.Stage]; err != nil {
		return StageClassification{}, err
	}
	if r, ok := f.results[input.Stage]; ok {
		return r, nil
	}
	return StageClassification{}, nil
}

type fakePromptResolver struct{ prompts []prompts.PromptVersion }

func (f fakePromptResolver) ListActive(_ context.Context) []prompts.PromptVersion {
	out := make([]prompts.PromptVersion, len(f.prompts))
	copy(out, f.prompts)
	return out
}

type fakeDecisionStore struct {
	items []streamers.RecordDecisionRequest
}

func (s *fakeDecisionStore) RecordLLMDecision(_ context.Context, req streamers.RecordDecisionRequest) (streamers.LLMDecision, error) {
	s.items = append(s.items, req)
	return streamers.LLMDecision{RunID: req.RunID, StreamerID: req.StreamerID, Stage: req.Stage, Label: req.Label, Confidence: req.Confidence}, nil
}

func TestWorkerProcessStreamerRunsAllOrderedStages(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{results: map[string]StageClassification{
			"detector":    {Label: "cs_detected", Confidence: 0.91, RawResponse: `{"label":"cs_detected"}`, TokensIn: 128, TokensOut: 32, Latency: 230 * time.Millisecond},
			"ranked_mode": {Label: "competitive", Confidence: 0.89, RawResponse: `{"label":"competitive"}`, TokensIn: 96, TokensOut: 18, Latency: 180 * time.Millisecond},
			"result":      {Label: "win", Confidence: 0.93, RawResponse: `{"label":"win"}`, TokensIn: 75, TokensOut: 14, Latency: 140 * time.Millisecond},
		}},
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "prompt-a", Stage: "detector", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "detect cs", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}, {ID: "prompt-b", Stage: "ranked_mode", Position: 2, IsActive: true, MinConfidence: 0.5, Template: "detect mode", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}, {ID: "prompt-c", Stage: "result", Position: 3, IsActive: true, MinConfidence: 0.5, Template: "detect result", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Stage != "result" || got.Label != "win" {
		t.Fatalf("final decision = %#v", got)
	}
	if len(decisions.items) != 3 {
		t.Fatalf("recorded %d decisions, want 3", len(decisions.items))
	}
}

func TestWorkerProcessStreamerUsesGenericUncertainFallback(t *testing.T) {
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "whatever", Confidence: 0.1}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "custom", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Label != "uncertain" {
		t.Fatalf("label = %q, want uncertain", got.Label)
	}
}

func TestWorkerProcessStreamerBusy(t *testing.T) {
	locker := NewInMemoryLocker()
	if ok := locker.TryLock("stream-capture:str-1", time.Second); !ok {
		t.Fatal("expected lock")
	}
	worker := NewWorker(fakeCapture{}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, locker, WorkerConfig{})
	_, err := worker.ProcessStreamer(context.Background(), "str-1")
	if !errors.Is(err, ErrStreamerBusy) {
		t.Fatalf("error = %v, want %v", err, ErrStreamerBusy)
	}
}

func TestWorkerProcessStreamerCleansUpChunkFileOnSuccess(t *testing.T) {
	chunkPath := filepath.Join(t.TempDir(), "chunk.ts")
	if err := os.WriteFile(chunkPath, []byte("chunk"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: chunkPath}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "ok", Confidence: 0.9}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if _, err := os.Stat(chunkPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deletion, err=%v", err)
	}
}

func TestWorkerProcessStreamerCleansUpChunkFileOnClassifierError(t *testing.T) {
	chunkPath := filepath.Join(t.TempDir(), "chunk.ts")
	if err := os.WriteFile(chunkPath, []byte("chunk"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: chunkPath}}, fakeClassifier{errByStage: map[string]error{"custom": errors.New("llm failed")}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected classifier error")
	}
	if _, err := os.Stat(chunkPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deletion, err=%v", err)
	}
}
