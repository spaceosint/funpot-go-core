package media

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

type fakeCapture struct {
	chunk          ChunkRef
	err            error
	lastDuration   time.Duration
	captureInvoked int
}

func (f *fakeCapture) Capture(_ context.Context, _ string) (ChunkRef, error) {
	f.captureInvoked++
	if f.err != nil {
		return ChunkRef{}, f.err
	}
	return f.chunk, nil
}

func (f *fakeCapture) CaptureWithDuration(ctx context.Context, streamerID string, duration time.Duration) (ChunkRef, error) {
	f.lastDuration = duration
	return f.Capture(ctx, streamerID)
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

type fakePromptResolver struct {
	prompts        []prompts.PromptVersion
	scenario       prompts.ScenarioPackage
	scenarioErr    error
	llmModelConfig prompts.LLMModelConfig
	llmConfigErr   error
}

func (f fakePromptResolver) GetActiveScenarioPackage(_ context.Context, _ string) (prompts.ScenarioPackage, error) {
	if f.scenarioErr != nil {
		return prompts.ScenarioPackage{}, f.scenarioErr
	}
	if len(f.scenario.Steps) > 0 {
		return f.scenario, nil
	}
	if len(f.prompts) == 0 {
		return prompts.ScenarioPackage{}, prompts.ErrScenarioPackageNotFound
	}

	steps := make([]prompts.ScenarioStep, 0, len(f.prompts))
	transitions := make([]prompts.ScenarioTransition, 0, len(f.prompts)-1)
	for i, prompt := range f.prompts {
		stepID := strings.TrimSpace(prompt.Stage)
		if stepID == "" {
			stepID = "step_" + strconv.Itoa(i+1)
		}
		steps = append(steps, prompts.ScenarioStep{
			ID:                 stepID,
			Name:               stepID,
			PromptTemplate:     prompt.Template,
			ResponseSchemaJSON: "{}",
			Initial:            i == 0,
			Order:              i + 1,
		})
		if i > 0 {
			transitions = append(transitions, prompts.ScenarioTransition{
				FromStepID: steps[i-1].ID,
				ToStepID:   stepID,
				Condition:  "",
			})
		}
	}
	return prompts.ScenarioPackage{
		ID:               "scenario-test",
		GameSlug:         "global",
		Name:             "generated",
		LLMModelConfigID: "cfg-test",
		Steps:            steps,
		Transitions:      transitions,
		IsActive:         true,
	}, nil
}

func (f fakePromptResolver) GetScenarioPackage(_ context.Context, id string) (prompts.ScenarioPackage, error) {
	if f.scenario.ID == id {
		return f.scenario, nil
	}
	return prompts.ScenarioPackage{}, prompts.ErrScenarioPackageNotFound
}

func (f fakePromptResolver) GetLLMModelConfig(_ context.Context, _ string) (prompts.LLMModelConfig, error) {
	if f.llmConfigErr != nil {
		return prompts.LLMModelConfig{}, f.llmConfigErr
	}
	if strings.TrimSpace(f.llmModelConfig.Model) != "" {
		return f.llmModelConfig, nil
	}
	for _, prompt := range f.prompts {
		if model := strings.TrimSpace(prompt.Model); model != "" {
			return prompts.LLMModelConfig{ID: "cfg-test", Model: model}, nil
		}
	}
	return prompts.LLMModelConfig{}, prompts.ErrLLMModelConfigNotFound
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
	err      error
}

type fakeChunkPublisher struct {
	err   error
	calls int
}

func (f *flakyClassifier) Classify(_ context.Context, input StageRequest) (StageClassification, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[input.Stage]++
	if f.calls[input.Stage] <= f.failures {
		if f.err != nil {
			return StageClassification{}, f.err
		}
		return StageClassification{}, errors.New("temporary llm failure")
	}
	return f.result, nil
}

func (f *fakeChunkPublisher) Publish(_ context.Context, _ string, _ ChunkRef) error {
	f.calls++
	return f.err
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

func TestWorkerProcessStreamerUsesScenarioPackageFirstStep(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{results: map[string]StageClassification{
			"detector":       {Label: "cs_detected", Confidence: 0.99},
			"match_update":   {Label: "state_updated", Confidence: 0.97, UpdatedStateJSON: `{"match_status":"in_progress"}`, EvidenceDeltaJSON: `["opened session"]`, NextEvidenceJSON: `["final scoreboard"]`},
			"match_finalize": {Label: "finalized", Confidence: 0.95, UpdatedStateJSON: `{"match_status":"finished"}`, EvidenceDeltaJSON: `["final scoreboard seen"]`, NextEvidenceJSON: `[]`, FinalOutcome: "win"},
		}},
		fakePromptResolver{prompts: []prompts.PromptVersion{
			{ID: "legacy-1", Stage: "detector", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "legacy detector", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000},
			{ID: "tracker-1", Stage: "match_update", Position: 2, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000},
			{ID: "tracker-2", Stage: "match_finalize", Position: 3, IsActive: true, MinConfidence: 0.5, Template: "finalize tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000},
		}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)

	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Stage != "detector" || got.FinalOutcome != "" {
		t.Fatalf("final decision = %#v", got)
	}
	if len(decisions.items) != 1 {
		t.Fatalf("recorded %d decisions, want 1", len(decisions.items))
	}
	if decisions.items[0].Stage != "detector" {
		t.Fatalf("unexpected stage: %#v", decisions.items[0].Stage)
	}
}

func TestWorkerProcessStreamerResetsToInitialStepWhenLatestStepMissingInActivePackage(t *testing.T) {
	decisions := &fakeDecisionStore{
		items: []streamers.RecordDecisionRequest{
			{
				RunID:            "run-old",
				StreamerID:       "str-1",
				Stage:            "legacy_step",
				Label:            "state_updated",
				Confidence:       0.9,
				UpdatedStateJSON: `{"game":"cs2"}`,
			},
		},
	}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{results: map[string]StageClassification{
			"root_detect": {Label: "cs2_detected", Confidence: 0.99, UpdatedStateJSON: `{"game":"cs2"}`},
		}},
		fakePromptResolver{scenario: prompts.ScenarioPackage{
			ID:               "scenario-v2",
			GameSlug:         "global",
			Name:             "v2",
			LLMModelConfigID: "cfg-test",
			Steps: []prompts.ScenarioStep{
				{ID: "root_detect", Name: "Root detect", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1},
				{ID: "cs2_mode", Name: "CS2 mode", PromptTemplate: "mode", ResponseSchemaJSON: `{}`, Order: 2},
			},
			Transitions: []prompts.ScenarioTransition{
				{FromStepID: "root_detect", ToStepID: "cs2_mode", Condition: `$.game == "cs2"`, Priority: 1},
			},
			IsActive: true,
		}, llmModelConfig: prompts.LLMModelConfig{ID: "cfg-test", Model: "gemini-2.5-flash"}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)

	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Stage != "root_detect" {
		t.Fatalf("stage = %q, want root_detect", got.Stage)
	}
	if len(decisions.items) != 2 {
		t.Fatalf("recorded %d decisions, want 2", len(decisions.items))
	}
	if decisions.items[1].Stage != "root_detect" {
		t.Fatalf("recorded stage = %q, want root_detect", decisions.items[1].Stage)
	}
}

func TestWorkerResolvePreviousStateDefaultsToTrackerBootstrap(t *testing.T) {
	worker := NewWorker(&fakeCapture{}, fakeClassifier{}, fakePromptResolver{}, &InMemoryRunStore{}, nil, NewInMemoryLocker(), WorkerConfig{})
	if got := worker.resolvePreviousState(context.Background(), "str-1"); got != defaultTrackerState() {
		t.Fatalf("resolvePreviousState() = %q", got)
	}
}

func TestWorkerProcessStreamerRunsAllOrderedStages(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}},
		fakeClassifier{results: map[string]StageClassification{
			"detector":    {Label: "cs_detected", Confidence: 0.91, RawResponse: `{"label":"cs_detected"}`, RequestRef: "req-1", ResponseRef: "res-1", TokensIn: 128, TokensOut: 32, Latency: 230 * time.Millisecond},
			"ranked_mode": {Label: "competitive", Confidence: 0.89, RawResponse: `{"label":"competitive"}`, RequestRef: "req-2", ResponseRef: "res-2", TokensIn: 96, TokensOut: 18, Latency: 180 * time.Millisecond},
			"result":      {Label: "win", Confidence: 0.93, RawResponse: `{"label":"win"}`, RequestRef: "req-3", ResponseRef: "res-3", TokensIn: 75, TokensOut: 14, Latency: 140 * time.Millisecond},
		}},
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "prompt-a", Stage: "detector", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "detect cs", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}, {ID: "prompt-b", Stage: "ranked_mode", Position: 2, IsActive: true, MinConfidence: 0.5, Template: "detect mode", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}, {ID: "prompt-c", Stage: "result", Position: 3, IsActive: true, MinConfidence: 0.5, Template: "detect result", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Stage != "detector" || got.Label != "cs_detected" {
		t.Fatalf("final decision = %#v", got)
	}
	if len(decisions.items) != 1 {
		t.Fatalf("recorded %d decisions, want 1", len(decisions.items))
	}
	if decisions.items[0].RequestRef == "" || decisions.items[0].ResponseRef == "" || decisions.items[0].ChunkCapturedAt.IsZero() {
		t.Fatalf("expected request/response/chunk metadata, got %#v", decisions.items[0])
	}
}

func TestWorkerProcessStreamerUsesGenericUncertainFallback(t *testing.T) {
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "whatever", Confidence: 0.1}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "custom", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
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
	worker := NewWorker(&fakeCapture{}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, locker, WorkerConfig{})
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
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: chunkPath}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "ok", Confidence: 0.9}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
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
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: chunkPath}}, fakeClassifier{errByStage: map[string]error{"custom": errors.New("llm failed")}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected classifier error")
	}
	if _, err := os.Stat(chunkPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deletion, err=%v", err)
	}
}

func TestWorkerProcessStreamerRetriesCapture(t *testing.T) {
	capture := &flakyCapture{failures: 1, chunk: ChunkRef{Reference: "chunk-1"}}
	worker := NewWorker(capture, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "ok", Confidence: 0.9}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5, CaptureRetryCount: 1})
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
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, classifier, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1, RetryCount: 1, BackoffMS: 10}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected classifier retry exhaustion error")
	}
	if got := classifier.calls["custom"]; got != 1 {
		t.Fatalf("classifier calls = %d, want 1", got)
	}
}

func TestWorkerProcessStreamerPublishesChunkAfterAnalysis(t *testing.T) {
	publisher := &fakeChunkPublisher{}
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, fakeClassifier{results: map[string]StageClassification{"custom": {Label: "ok", Confidence: 0.9}}}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5, ChunkPublisher: publisher})
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher calls = %d, want 1", publisher.calls)
	}
}

func TestWorkerProcessStreamerReturnsErrorAfterRetryExhausted(t *testing.T) {
	classifier := &flakyClassifier{failures: 2, result: StageClassification{Label: "ok", Confidence: 0.9}}
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, classifier, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1, RetryCount: 1, BackoffMS: 10}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected classifier retry exhaustion error")
	}
	if got := classifier.calls["custom"]; got != 1 {
		t.Fatalf("classifier calls = %d, want 1", got)
	}
}

func TestWorkerProcessStreamerRetriesTransientGeminiFailuresWithSafetyFloor(t *testing.T) {
	classifier := &flakyClassifier{
		failures: 2,
		result:   StageClassification{Label: "ok", Confidence: 0.9},
		err: &GeminiGenerateContentError{
			StatusCode: http.StatusServiceUnavailable,
			Stage:      "custom",
			Model:      "gemini",
		},
	}
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, classifier, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1, RetryCount: 0, BackoffMS: 0}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got := classifier.calls["custom"]; got != 3 {
		t.Fatalf("classifier calls = %d, want 3", got)
	}
}

func TestWorkerProcessStreamerDoesNotRetryNonTransientGeminiFailures(t *testing.T) {
	classifier := &flakyClassifier{
		failures: 2,
		result:   StageClassification{Label: "ok", Confidence: 0.9},
		err: &GeminiGenerateContentError{
			StatusCode: http.StatusBadRequest,
			Stage:      "custom",
			Model:      "gemini",
		},
	}
	worker := NewWorker(&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}}, classifier, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1, RetryCount: 3, BackoffMS: 10}}}, &InMemoryRunStore{}, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})
	worker.sleepFn = func(context.Context, time.Duration) error { return nil }

	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err == nil {
		t.Fatal("expected non-transient gemini error")
	}
	if got := classifier.calls["custom"]; got != 1 {
		t.Fatalf("classifier calls = %d, want 1", got)
	}
}

func TestWorkerProcessStreamerSkipsAdBreakWithoutFailingCycle(t *testing.T) {
	runStore := &countingRunStore{}
	worker := NewWorker(&fakeCapture{err: ErrStreamlinkAdBreak}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, runStore, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})

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
	worker := NewWorker(&fakeCapture{err: ErrStreamlinkStreamEnded}, fakeClassifier{}, fakePromptResolver{prompts: []prompts.PromptVersion{{Stage: "custom", Position: 1, IsActive: true, Template: "x", Model: "gemini", MaxTokens: 1, TimeoutMS: 1}}}, runStore, &fakeDecisionStore{}, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5})

	got, err := worker.ProcessStreamer(context.Background(), "str-ended")
	if !errors.Is(err, ErrTrackingStop) {
		t.Fatalf("ProcessStreamer() error = %v, want %v", err, ErrTrackingStop)
	}
	if got != (streamers.LLMDecision{}) {
		t.Fatalf("expected zero decision on ended stream, got %#v", got)
	}
	if runStore.count != 0 {
		t.Fatalf("expected ended stream to skip run creation, got %d runs", runStore.count)
	}
}

func TestWorkerProcessStreamerFailsWhenScenarioPackageIsMissing(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{results: map[string]StageClassification{
			"match_update": {Label: "state_updated", Confidence: 0.98, UpdatedStateJSON: `{"match_status":"in_progress"}`},
		}},
		fakePromptResolver{
			prompts:     []prompts.PromptVersion{{ID: "tracker-1", Stage: "match_update", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}},
			scenarioErr: prompts.ErrScenarioPackageNotFound,
		},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); !errors.Is(err, prompts.ErrScenarioPackageNotFound) {
		t.Fatalf("ProcessStreamer() error = %v, want %v", err, prompts.ErrScenarioPackageNotFound)
	}
	if len(decisions.items) != 0 {
		t.Fatalf("recorded %d decisions, want 0", len(decisions.items))
	}
}

func TestWorkerProcessStreamerPassesPersistedPreviousStateToTrackerStages(t *testing.T) {
	decisions := &fakeDecisionStore{}
	classifier := &flakyClassifier{result: StageClassification{Label: "state_updated", Confidence: 0.95, UpdatedStateJSON: `{"score":{"ct":8,"t":5}}`}}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		classifier,
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "tracker-1", Stage: "match_update", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("first ProcessStreamer() error = %v", err)
	}
	second, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("second ProcessStreamer() error = %v", err)
	}
	if len(decisions.items) != 1 {
		t.Fatalf("recorded %d decisions, want 1 (skip unchanged updates)", len(decisions.items))
	}
	if second.Label != "awaiting_changes" {
		t.Fatalf("second label = %q, want awaiting_changes", second.Label)
	}
}

func TestWorkerProcessStreamerIgnoresRawResponseStatePayloads(t *testing.T) {
	decisions := &fakeDecisionStore{}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{results: map[string]StageClassification{
			"start": {
				Confidence: 0.91,
				RawResponse: `{
						"state": {
							"mode": {"value": "competitive", "confidence": 0.9},
							"ct_score": {"value": 8, "confidence": 0.9},
							"t_score": {"value": 5, "confidence": 0.9}
						},
						"final_outcome": "unknown"
					}`,
			},
		}},
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "tracker-1", Stage: "start", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Label != "state_updated" {
		t.Fatalf("label = %q, want state_updated", got.Label)
	}
	if len(decisions.items) != 1 {
		t.Fatalf("recorded %d decisions, want 1", len(decisions.items))
	}
	if got := decisions.items[0].UpdatedStateJSON; got != `{"_scenario":{"packageId":"scenario-test","stepId":"start"}}` {
		t.Fatalf("updated state = %q", decisions.items[0].UpdatedStateJSON)
	}
}

func TestWorkerProcessStreamerUsesLLMStateEvenWhenUnknownPlaceholdersAreReturned(t *testing.T) {
	decisions := &fakeDecisionStore{}
	classifier := &flakyClassifier{
		result: StageClassification{
			Label:            "state_updated",
			Confidence:       0.95,
			UpdatedStateJSON: `{"state":{"ct_score":0,"t_score":0,"mode":"unknown"},"final_outcome":"unknown"}`,
		},
	}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		classifier,
		fakePromptResolver{prompts: []prompts.PromptVersion{{ID: "tracker-1", Stage: "match_update", Position: 1, IsActive: true, MinConfidence: 0.5, Template: "update tracker state", Model: "gemini", MaxTokens: 100, TimeoutMS: 1000}}},
		&InMemoryRunStore{}, decisions, NewInMemoryLocker(), WorkerConfig{MinConfidence: 0.5},
	)
	if _, err := worker.ProcessStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("first ProcessStreamer() error = %v", err)
	}
	decisions.items[0].UpdatedStateJSON = `{"state":{"ct_score":8,"t_score":5,"mode":"competitive"},"final_outcome":"unknown"}`
	second, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("second ProcessStreamer() error = %v", err)
	}
	if second.Label != "state_updated" {
		t.Fatalf("second label = %q, want state_updated", second.Label)
	}
	if got := second.UpdatedStateJSON; got != `{"_scenario":{"packageId":"scenario-test","stepId":"match_update"},"final_outcome":"unknown","state":{"ct_score":0,"mode":"unknown","t_score":0}}` {
		t.Fatalf("updated state = %q", got)
	}
	if len(decisions.items) != 2 {
		t.Fatalf("recorded %d decisions, want 2", len(decisions.items))
	}
}

func TestWorkerProcessScenarioPackageUsesPackageModelConfigWhenStepModelMissing(t *testing.T) {
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Now().UTC()}},
		fakeClassifier{results: map[string]StageClassification{"root_detect": {Label: "ok", Confidence: 0.9}}},
		fakePromptResolver{
			scenario: prompts.ScenarioPackage{
				ID:               "scenario-1",
				GameSlug:         "global",
				LLMModelConfigID: "cfg-default",
				Steps: []prompts.ScenarioStep{
					{ID: "root_detect", Name: "Root", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1},
				},
			},
			llmModelConfig: prompts.LLMModelConfig{ID: "cfg-default", Model: "gemini-2.5-flash"},
		},
		&InMemoryRunStore{},
		&fakeDecisionStore{},
		NewInMemoryLocker(),
		WorkerConfig{MinConfidence: 0.5},
	)

	decision, err := worker.ProcessStreamer(context.Background(), "streamer-1")
	if err != nil {
		t.Fatalf("process streamer: %v", err)
	}
	if decision.Stage != "root_detect" {
		t.Fatalf("expected root_detect stage, got %q", decision.Stage)
	}
}

func TestWorkerProcessStreamerUsesStepSegmentDuration(t *testing.T) {
	capture := &fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Now().UTC()}}
	worker := NewWorker(
		capture,
		fakeClassifier{results: map[string]StageClassification{"initial": {Label: "ok", Confidence: 0.9}}},
		fakePromptResolver{
			scenario: prompts.ScenarioPackage{
				ID:               "scenario-1",
				GameSlug:         "global",
				LLMModelConfigID: "cfg-default",
				Steps: []prompts.ScenarioStep{
					{ID: "initial", Name: "Initial", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1, SegmentSeconds: 15},
				},
			},
			llmModelConfig: prompts.LLMModelConfig{ID: "cfg-default", Model: "gemini-2.5-flash"},
		},
		&InMemoryRunStore{},
		&fakeDecisionStore{},
		NewInMemoryLocker(),
		WorkerConfig{MinConfidence: 0.5},
	)

	if _, err := worker.ProcessStreamer(context.Background(), "streamer-1"); err != nil {
		t.Fatalf("process streamer: %v", err)
	}
	if capture.lastDuration != 15*time.Second {
		t.Fatalf("capture duration = %v, want 15s", capture.lastDuration)
	}
}

func TestWorkerProcessStreamerStopsWhenInitialMaxRequestsExceeded(t *testing.T) {
	decisions := &fakeDecisionStore{
		items: []streamers.RecordDecisionRequest{
			{StreamerID: "streamer-1", Stage: "initial", UpdatedStateJSON: `{}`},
		},
	}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Now().UTC()}},
		fakeClassifier{results: map[string]StageClassification{"initial": {Label: "ok", Confidence: 0.9}}},
		fakePromptResolver{
			scenario: prompts.ScenarioPackage{
				ID:               "scenario-1",
				GameSlug:         "global",
				LLMModelConfigID: "cfg-default",
				Steps: []prompts.ScenarioStep{
					{ID: "initial", Name: "Initial", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1, MaxRequests: 1},
				},
			},
			llmModelConfig: prompts.LLMModelConfig{ID: "cfg-default", Model: "gemini-2.5-flash"},
		},
		&InMemoryRunStore{},
		decisions,
		NewInMemoryLocker(),
		WorkerConfig{MinConfidence: 0.5},
	)

	_, err := worker.ProcessStreamer(context.Background(), "streamer-1")
	if !errors.Is(err, ErrTrackingStop) {
		t.Fatalf("expected ErrTrackingStop, got %v", err)
	}
}

func TestWorkerProcessStreamerStopsWhenInitialHasNoMatchingBranchAndLimitReached(t *testing.T) {
	decisions := &fakeDecisionStore{
		items: []streamers.RecordDecisionRequest{
			{StreamerID: "streamer-1", Stage: "initial", UpdatedStateJSON: `{"game":"unknown"}`},
			{StreamerID: "streamer-1", Stage: "initial", UpdatedStateJSON: `{"game":"unknown"}`},
		},
	}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Now().UTC()}},
		fakeClassifier{results: map[string]StageClassification{"initial": {Label: "ok", Confidence: 0.9}}},
		fakePromptResolver{
			scenario: prompts.ScenarioPackage{
				ID:               "scenario-1",
				GameSlug:         "global",
				LLMModelConfigID: "cfg-default",
				Steps: []prompts.ScenarioStep{
					{ID: "initial", Name: "Initial", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1, MaxRequests: 2},
					{ID: "cs2_mode", Name: "CS2", PromptTemplate: "mode", ResponseSchemaJSON: `{}`, Order: 2},
				},
				Transitions: []prompts.ScenarioTransition{
					{FromStepID: "initial", ToStepID: "cs2_mode", Condition: "game == cs2", Priority: 1},
				},
			},
			llmModelConfig: prompts.LLMModelConfig{ID: "cfg-default", Model: "gemini-2.5-flash"},
		},
		&InMemoryRunStore{},
		decisions,
		NewInMemoryLocker(),
		WorkerConfig{MinConfidence: 0.5},
	)

	_, err := worker.ProcessStreamer(context.Background(), "streamer-1")
	if !errors.Is(err, ErrTrackingStop) {
		t.Fatalf("expected ErrTrackingStop when initial branch did not match and max requests reached, got %v", err)
	}
}

func TestWorkerProcessStreamerStopsFromPackageTransitionAndReturnsState(t *testing.T) {
	decisions := &fakeDecisionStore{
		items: []streamers.RecordDecisionRequest{
			{StreamerID: "streamer-1", Stage: "initial", Label: "running", UpdatedStateJSON: `{"outcome":"ct_win","streamer_side":"ct"}`},
		},
	}
	worker := NewWorker(
		&fakeCapture{chunk: ChunkRef{Reference: "chunk-1", CapturedAt: time.Now().UTC()}},
		fakeClassifier{results: map[string]StageClassification{"initial": {Label: "ok", Confidence: 0.9}}},
		fakePromptResolver{
			scenario: prompts.ScenarioPackage{
				ID:               "scenario-1",
				GameSlug:         "global",
				LLMModelConfigID: "cfg-default",
				Steps: []prompts.ScenarioStep{
					{ID: "initial", Name: "Initial", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1},
				},
				PackageTransitions: []prompts.ScenarioPackageTransition{
					{Priority: 1, Action: prompts.ScenarioPackageTransitionActionStopTracking, FinalStateOptionID: "ct_win"},
				},
				FinalStateOptions: []prompts.ScenarioFinalStateOption{
					{ID: "ct_win", Name: "CT Win", Condition: `outcome == "ct_win" && streamer_side == "ct"`, FinalStateJSON: `{"result":"win"}`, FinalLabel: "final_ct_win"},
				},
			},
			llmModelConfig: prompts.LLMModelConfig{ID: "cfg-default", Model: "gemini-2.5-flash"},
		},
		&InMemoryRunStore{},
		decisions,
		NewInMemoryLocker(),
		WorkerConfig{MinConfidence: 0.5},
	)

	decision, err := worker.ProcessStreamer(context.Background(), "streamer-1")
	if !errors.Is(err, ErrTrackingStop) {
		t.Fatalf("expected ErrTrackingStop, got %v", err)
	}
	state := parseJSONMap(decision.UpdatedStateJSON)
	if state["outcome"] != "ct_win" || state["streamer_side"] != "ct" || state["result"] != "win" {
		t.Fatalf("expected merged terminal state, got %#v", state)
	}
	if decision.Label != "final_ct_win" {
		t.Fatalf("expected final label in decision, got %s", decision.Label)
	}
}
