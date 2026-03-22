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

type fakeScenarioResolver struct {
	global      prompts.PromptTemplate
	scenario    prompts.ScenarioVersion
	globalErr   error
	scenarioErr error
}

func (f fakeScenarioResolver) GetActiveGlobalDetector(_ context.Context) (prompts.PromptTemplate, error) {
	if f.globalErr != nil {
		return prompts.PromptTemplate{}, f.globalErr
	}
	return f.global, nil
}

func (f fakeScenarioResolver) GetActiveScenarioByGame(_ context.Context, _ string) (prompts.ScenarioVersion, error) {
	if f.scenarioErr != nil {
		return prompts.ScenarioVersion{}, f.scenarioErr
	}
	return f.scenario, nil
}

func (f fakePromptResolver) ListActive(_ context.Context) []prompts.PromptVersion {
	out := make([]prompts.PromptVersion, len(f.prompts))
	copy(out, f.prompts)
	return out
}

type fakeDecisionStore struct {
	items []streamers.RecordDecisionRequest
}

type countingRunStore struct {
	count int
}

type flakyCapture struct {
	failures int
	calls    int
	chunk    ChunkRef
}

func (f *flakyCapture) Capture(_ context.Context, _ string) (ChunkRef, error) {
	f.calls++
	if f.calls <= f.failures {
		return ChunkRef{}, errors.New("capture failed")
	}
	return f.chunk, nil
}

type flakyClassifier struct {
	failures int
	calls    map[string]int
	result   StageClassification
}

func (f *flakyClassifier) Classify(_ context.Context, input StageRequest) (StageClassification, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[input.Stage]++
	if f.calls[input.Stage] <= f.failures {
		return StageClassification{}, errors.New("temporary llm failure")
	}
	return f.result, nil
}

func (s *fakeDecisionStore) RecordLLMDecision(_ context.Context, req streamers.RecordDecisionRequest) (streamers.LLMDecision, error) {
	s.items = append(s.items, req)
	return streamers.LLMDecision{RunID: req.RunID, StreamerID: req.StreamerID, Stage: req.Stage, Label: req.Label, Confidence: req.Confidence, UpdatedStateJSON: req.UpdatedStateJSON}, nil
}

func (s *fakeDecisionStore) ListAllLLMDecisions(_ context.Context, streamerID string) []streamers.LLMDecision {
	items := make([]streamers.LLMDecision, 0, len(s.items))
	for _, item := range s.items {
		if item.StreamerID != streamerID {
			continue
		}
		items = append(items, streamers.LLMDecision{
			RunID:            item.RunID,
			StreamerID:       item.StreamerID,
			Stage:            item.Stage,
			Label:            item.Label,
			Confidence:       item.Confidence,
			UpdatedStateJSON: item.UpdatedStateJSON,
		})
	}
	return items
}

func (s *countingRunStore) CreateRun(_ context.Context, streamerID string) (string, error) {
	s.count++
	return streamerID + "-run", nil
}

func TestWorkerProcessStreamerRunsAllOrderedStages(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}},
		fakeClassifier{results: map[string]StageClassification{
			"detector":    {Label: "cs_detected", Confidence: 0.91, RawResponse: `{"label":"cs_detected"}`, RequestRef: "req-1", ResponseRef: "res-1", TokensIn: 128, TokensOut: 32, Latency: 230 * time.Millisecond},
			"ranked_mode": {Label: "competitive", Confidence: 0.89, RawResponse: `{"label":"competitive"}`, RequestRef: "req-2", ResponseRef: "res-2", TokensIn: 96, TokensOut: 18, Latency: 180 * time.Millisecond},
			"result":      {Label: "win", Confidence: 0.93, RawResponse: `{"label":"win"}`, RequestRef: "req-3", ResponseRef: "res-3", TokensIn: 75, TokensOut: 14, Latency: 140 * time.Millisecond},
		}},
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "prompt-a", Stage: "detector", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "detect cs", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}, {ID: "prompt-b", Stage: "ranked_mode", Position: 2, IsActive: true, MinConfidence: 0.5, Template: "detect mode", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}, {ID: "prompt-c", Stage: "result", Position: 3, IsActive: true, MinConfidence: 0.5, Template: "detect result", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		nil, &InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
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
	if decisions.items[0].RequestRef == "" || decisions.items[0].ResponseRef == "" || decisions.items[0].ChunkCapturedAt.IsZero() {
		t.Fatalf("expected request/response/chunk metadata, got %#v", decisions.items[0])
	}
}

func TestWorkerProcessStreamerUsesGenericUncertainFallback(t *testing.T) {
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "whatever", Confidence: 0.1}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "custom", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
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
	worker := NewWorker(fakeCapture{}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, locker, WorkerConfig{})
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
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: chunkPath}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "ok", Confidence: 0.9}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
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
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: chunkPath}}, fakeClassifier{errByStage: map[string]error{"custom": errors.New("llm failed")}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected classifier error")
	}
	if _, err := os.Stat(chunkPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deletion, err=%v", err)
	}
}

func TestWorkerProcessStreamerRetriesCapture(t *testing.T) {
	capture := &flakyCapture{failures: 1, chunk: ChunkRef{Reference: "chunk-1"}}
	worker := NewWorker(capture, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "ok", Confidence: 0.9}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5, CaptureRetryCount: 1})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if capture.calls != 2 {
		t.Fatalf("capture calls = %d, want 2", capture.calls)
	}
}

func TestWorkerProcessStreamerRetriesStageClassification(t *testing.T) {
	classifier := &flakyClassifier{failures: 1, result: StageClassification{Label: "ok", Confidence: 0.9}}
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, classifier, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1, RetryCount: 1, BackoffMS: 10}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got := classifier.calls["custom"]; got != 2 {
		t.Fatalf("classifier calls = %d, want 2", got)
	}
}

func TestWorkerProcessStreamerReturnsErrorAfterRetryExhausted(t *testing.T) {
	classifier := &flakyClassifier{failures: 2, result: StageClassification{Label: "ok", Confidence: 0.9}}
	worker := NewWorker(fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, classifier, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1, RetryCount: 1, BackoffMS: 10}}}, nil, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected classifier retry exhaustion error")
	}
	if got := classifier.calls["custom"]; got != 2 {
		t.Fatalf("classifier calls = %d, want 2", got)
	}
}

func TestWorkerProcessStreamerSkipsAdBreakWithoutFailingCycle(t *testing.T) {
	runStore := &countingRunStore{}
	worker := NewWorker(fakeCapture{err: ErrStreamlinkAdBreak}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, nil, runStore, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})

	got, err := worker.ProcessStreamer(context.Background(), "str-ads")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got != (streamers.LLMDecision{}) {
		t.Fatalf("expected zero decision on ad break, got %#v", got)
	}
	if runStore.count != 0 {
		t.Fatalf("expected ad break to skip run creation, got %d runs", runStore.count)
	}
}

func TestWorkerProcessStreamerSkipsEndedStreamWithoutFailingCycle(t *testing.T) {
	runStore := &countingRunStore{}
	worker := NewWorker(fakeCapture{err: ErrStreamlinkStreamEnded}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, nil, runStore, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})

	got, err := worker.ProcessStreamer(context.Background(), "str-ended")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got != (streamers.LLMDecision{}) {
		t.Fatalf("expected zero decision on ended stream, got %#v", got)
	}
	if runStore.count != 0 {
		t.Fatalf("expected ended stream to skip run creation, got %d runs", runStore.count)
	}
}

func TestWorkerProcessStreamerIgnoresLegacyScenarioResolver(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{results: map[string]StageClassification{
			"match_update": {Label: "state_updated", Confidence: 0.98, UpdatedStateJSON: `{"match_status":"in_progress"}`},
		}},
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "tracker-1", Stage: "match_update", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		fakeScenarioResolver{globalErr: errors.New("legacy scenario resolver should not be called")},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Stage != "match_update" || got.Label != "state_updated" {
		t.Fatalf("final decision = %#v", got)
	}
	if len(decisions.items) != 1 {
		t.Fatalf("recorded %d decisions, want 1", len(decisions.items))
	}
}

func TestWorkerProcessStreamerPassesPersistedPreviousStateToTrackerStages(t *testing.T) {
	decisions := &fakeDecisionStore{}
	classifier := &flakyClassifier{result: StageClassification{Label: "state_updated", Confidence: 0.95, UpdatedStateJSON: `{"score":{"ct":8,"t":5}}`}}
	worker := NewWorker(
		fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		classifier,
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "tracker-1", Stage: "match_update", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		nil,
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("first ProcessStreamer() error = %v", err)
	}
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("second ProcessStreamer() error = %v", err)
	}
	if len(decisions.items) != 2 {
		t.Fatalf("recorded %d decisions, want 2", len(decisions.items))
	}
	if decisions.items[1].PreviousStateJSON != `{"score":{"ct":8,"t":5}}` {
		t.Fatalf("previous state = %q", decisions.items[1].PreviousStateJSON)
	}
}
