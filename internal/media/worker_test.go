package media

import (
	"context"
	"errors"
	"testing"

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
	result StageAClassification
	err    error
}

func (f fakeClassifier) Classify(_ context.Context, _ StageARequest) (StageAClassification, error) {
	if f.err != nil {
		return StageAClassification{}, f.err
	}
	return f.result, nil
}

type fakePromptResolver struct {
	prompt prompts.PromptVersion
	err    error
}

func (f fakePromptResolver) GetActiveByStage(_ context.Context, _ string) (prompts.PromptVersion, error) {
	if f.err != nil {
		return prompts.PromptVersion{}, f.err
	}
	return f.prompt, nil
}

type fakeDecisionStore struct {
	last streamers.RecordDecisionRequest
}

func (s *fakeDecisionStore) RecordLLMDecision(_ context.Context, req streamers.RecordDecisionRequest) (streamers.LLMDecision, error) {
	s.last = req
	return streamers.LLMDecision{RunID: req.RunID, StreamerID: req.StreamerID, Stage: req.Stage, Label: req.Label, Confidence: req.Confidence}, nil
}

func TestWorkerProcessStreamerStageASuccess(t *testing.T) {
	locker := NewInMemoryLocker()
	runs := &InMemoryRunStore{}
	decisions := &fakeDecisionStore{}

	worker := NewWorker(
		fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{result: StageAClassification{Label: "cs_detected", Confidence: 0.91}},
		fakePromptResolver{prompt: prompts.PromptVersion{Stage: prompts.StageA, IsActive: true, MinConfidence: 0.5}},
		runs,
		decisions,
		locker,
		WorkerConfig{MinConfidence: 0.5},
	)

	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Label != string(StageALabelCSDetected) {
		t.Fatalf("label = %q, want %q", got.Label, StageALabelCSDetected)
	}
	if got.Stage != prompts.StageA {
		t.Fatalf("stage = %q, want stage_a", got.Stage)
	}
	if decisions.last.StreamerID != "str-1" {
		t.Fatalf("recorded streamer = %q", decisions.last.StreamerID)
	}
}

func TestWorkerProcessStreamerLowConfidenceFallsBackToUncertain(t *testing.T) {
	worker := NewWorker(
		fakeCapture{chunk: ChunkRef{Reference: "chunk-1"}},
		fakeClassifier{result: StageAClassification{Label: "cs_detected", Confidence: 0.21}},
		fakePromptResolver{prompt: prompts.PromptVersion{Stage: prompts.StageA, IsActive: true, MinConfidence: 0.5}},
		&InMemoryRunStore{},
		&fakeDecisionStore{},
		NewInMemoryLocker(),
		WorkerConfig{MinConfidence: 0.5},
	)

	got, err := worker.ProcessStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ProcessStreamer() error = %v", err)
	}
	if got.Label != string(StageALabelUncertain) {
		t.Fatalf("label = %q, want %q", got.Label, StageALabelUncertain)
	}
}

func TestWorkerProcessStreamerBusy(t *testing.T) {
	locker := NewInMemoryLocker()
	if ok := locker.TryLock("stream-capture:str-1", 1000000000); !ok {
		t.Fatalf("expected initial lock acquisition to succeed")
	}

	worker := NewWorker(
		fakeCapture{},
		fakeClassifier{result: StageAClassification{Label: "cs_detected", Confidence: 0.9}},
		fakePromptResolver{prompt: prompts.PromptVersion{Stage: prompts.StageA, IsActive: true, MinConfidence: 0.5}},
		&InMemoryRunStore{},
		&fakeDecisionStore{},
		locker,
		WorkerConfig{},
	)

	_, err := worker.ProcessStreamer(context.Background(), "str-1")
	if !errors.Is(err, ErrStreamerBusy) {
		t.Fatalf("error = %v, want %v", err, ErrStreamerBusy)
	}
}
