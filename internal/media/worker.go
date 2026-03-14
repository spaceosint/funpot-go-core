package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/funpot/funpot-go-core/internal/streamers"
)

var (
	ErrStreamerIDRequired = errors.New("streamerID is required")
	ErrStreamerBusy       = errors.New("streamer is already being processed")
)

type ChunkRef struct {
	Reference string
}

type StageAClassification struct {
	Label       string
	Confidence  float64
	RawResponse string
	TokensIn    int
	TokensOut   int
	Latency     time.Duration
}

type StreamCapture interface {
	Capture(ctx context.Context, streamerID string) (ChunkRef, error)
}

type StageAClassifier interface {
	Classify(ctx context.Context, input ChunkRef) (StageAClassification, error)
}

type RunStore interface {
	CreateRun(ctx context.Context, streamerID string) (string, error)
}

type DecisionStore interface {
	RecordLLMDecision(ctx context.Context, req streamers.RecordDecisionRequest) (streamers.LLMDecision, error)
}

type Locker interface {
	TryLock(key string, ttl time.Duration) bool
	Unlock(key string)
}

type Worker struct {
	capture       StreamCapture
	classifier    StageAClassifier
	runs          RunStore
	decisions     DecisionStore
	locker        Locker
	lockTTL       time.Duration
	minConfidence float64
}

type WorkerConfig struct {
	LockTTL       time.Duration
	MinConfidence float64
}

func NewWorker(capture StreamCapture, classifier StageAClassifier, runs RunStore, decisions DecisionStore, locker Locker, cfg WorkerConfig) *Worker {
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 30 * time.Second
	}
	if cfg.MinConfidence < 0 || cfg.MinConfidence > 1 {
		cfg.MinConfidence = 0.5
	}
	return &Worker{
		capture:       capture,
		classifier:    classifier,
		runs:          runs,
		decisions:     decisions,
		locker:        locker,
		lockTTL:       cfg.LockTTL,
		minConfidence: cfg.MinConfidence,
	}
}

func (w *Worker) ProcessStreamer(ctx context.Context, streamerID string) (streamers.LLMDecision, error) {
	id := strings.TrimSpace(streamerID)
	if id == "" {
		return streamers.LLMDecision{}, ErrStreamerIDRequired
	}

	lockKey := fmt.Sprintf("stream-capture:%s", id)
	if !w.locker.TryLock(lockKey, w.lockTTL) {
		return streamers.LLMDecision{}, ErrStreamerBusy
	}
	defer w.locker.Unlock(lockKey)

	runID, err := w.runs.CreateRun(ctx, id)
	if err != nil {
		return streamers.LLMDecision{}, err
	}

	chunk, err := w.capture.Capture(ctx, id)
	if err != nil {
		return streamers.LLMDecision{}, err
	}

	result, err := w.classifier.Classify(ctx, chunk)
	if err != nil {
		return streamers.LLMDecision{}, err
	}

	label := NormalizeStageALabel(result.Label)
	if result.Confidence < w.minConfidence {
		label = StageALabelUncertain
	}

	return w.decisions.RecordLLMDecision(ctx, streamers.RecordDecisionRequest{
		RunID:      runID,
		StreamerID: id,
		Stage:      "stage_a",
		Label:      string(label),
		Confidence: result.Confidence,
	})
}
