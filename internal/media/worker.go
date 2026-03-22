package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
)

var (
	ErrStreamerIDRequired = errors.New("streamerID is required")
	ErrStreamerBusy       = errors.New("streamer is already being processed")
)

type ChunkRef struct {
	Reference  string
	CapturedAt time.Time
}

type StageRequest struct {
	StreamerID    string
	Stage         string
	Chunk         ChunkRef
	Prompt        prompts.PromptVersion
	PreviousState string
}

type StageClassification struct {
	Label             string
	Confidence        float64
	RawResponse       string
	RequestRef        string
	ResponseRef       string
	TokensIn          int
	TokensOut         int
	Latency           time.Duration
	NormalizedOutcome string
	UpdatedStateJSON  string
	EvidenceDeltaJSON string
	NextEvidenceJSON  string
	ConflictsJSON     string
	FinalOutcome      string
}

type StreamCapture interface {
	Capture(ctx context.Context, streamerID string) (ChunkRef, error)
}

type StageClassifier interface {
	Classify(ctx context.Context, input StageRequest) (StageClassification, error)
}

type PromptResolver interface {
	ListActive(ctx context.Context) []prompts.PromptVersion
}

type ScenarioResolver interface {
	GetActiveGlobalDetector(ctx context.Context) (prompts.PromptTemplate, error)
	GetActiveScenarioByGame(ctx context.Context, gameSlug string) (prompts.ScenarioVersion, error)
}

type RunStore interface {
	CreateRun(ctx context.Context, streamerID string) (string, error)
}

type DecisionStore interface {
	RecordLLMDecision(ctx context.Context, req streamers.RecordDecisionRequest) (streamers.LLMDecision, error)
	ListAllLLMDecisions(ctx context.Context, streamerID string) []streamers.LLMDecision
}

type Locker interface {
	TryLock(key string, ttl time.Duration) bool
	Unlock(key string)
}

type Worker struct {
	logger              *zap.Logger
	metrics             *workerMetrics
	capture             StreamCapture
	classifier          StageClassifier
	prompts             PromptResolver
	scenarios           ScenarioResolver
	runs                RunStore
	decisions           DecisionStore
	locker              Locker
	lockTTL             time.Duration
	minConfidence       float64
	captureRetryCount   int
	captureRetryBackoff time.Duration
	sleepFn             func(context.Context, time.Duration) error
}

type WorkerConfig struct {
	LockTTL             time.Duration
	MinConfidence       float64
	CaptureRetryCount   int
	CaptureRetryBackoff time.Duration
}

func NewWorker(capture StreamCapture, classifier StageClassifier, promptResolver PromptResolver, scenarioResolver ScenarioResolver, runs RunStore, decisions DecisionStore, locker Locker, cfg WorkerConfig) *Worker {
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 30 * time.Second
	}
	if cfg.MinConfidence < 0 || cfg.MinConfidence > 1 {
		cfg.MinConfidence = 0.5
	}
	if cfg.CaptureRetryCount < 0 {
		cfg.CaptureRetryCount = 0
	}
	if cfg.CaptureRetryBackoff < 0 {
		cfg.CaptureRetryBackoff = 0
	}
	return &Worker{
		logger:              zap.NewNop(),
		metrics:             newWorkerMetrics(),
		capture:             capture,
		classifier:          classifier,
		prompts:             promptResolver,
		scenarios:           scenarioResolver,
		runs:                runs,
		decisions:           decisions,
		locker:              locker,
		lockTTL:             cfg.LockTTL,
		minConfidence:       cfg.MinConfidence,
		captureRetryCount:   cfg.CaptureRetryCount,
		captureRetryBackoff: cfg.CaptureRetryBackoff,
		sleepFn:             sleepContext,
	}
}

func (w *Worker) SetLogger(logger *zap.Logger) {
	if w == nil {
		return
	}
	if logger == nil {
		w.logger = zap.NewNop()
		return
	}
	w.logger = logger
}

func (w *Worker) ProcessStreamer(ctx context.Context, streamerID string) (streamers.LLMDecision, error) {
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	id := strings.TrimSpace(streamerID)
	if id == "" {
		logger.Warn("worker rejected empty streamer id")
		w.metrics.recordCycle(ctx, id, "invalid")
		return streamers.LLMDecision{}, ErrStreamerIDRequired
	}
	logger.Info("streamer processing cycle started", zap.String("streamerID", id))
	lockKey := fmt.Sprintf("stream-capture:%s", id)
	if !w.locker.TryLock(lockKey, w.lockTTL) {
		logger.Info("streamer processing skipped because worker is busy", zap.String("streamerID", id), zap.String("lockKey", lockKey))
		w.metrics.recordCycle(ctx, id, "busy")
		return streamers.LLMDecision{}, ErrStreamerBusy
	}
	logger.Info("streamer processing lock acquired", zap.String("streamerID", id), zap.String("lockKey", lockKey), zap.Duration("lockTTL", w.lockTTL))
	defer func() {
		w.locker.Unlock(lockKey)
		logger.Info("streamer processing lock released", zap.String("streamerID", id), zap.String("lockKey", lockKey))
	}()

	chunk, err := w.captureWithRetry(ctx, id)
	if err != nil {
		if errors.Is(err, ErrStreamlinkAdBreak) {
			logger.Info("stream chunk capture skipped because stream is on ad break", zap.String("streamerID", id), zap.Error(err))
			w.metrics.recordCycle(ctx, id, "ad_break")
			return streamers.LLMDecision{}, nil
		}
		if errors.Is(err, ErrStreamlinkStreamEnded) {
			logger.Info("stream chunk capture skipped because stream has ended or is unavailable", zap.String("streamerID", id), zap.Error(err))
			w.metrics.recordCycle(ctx, id, "stream_unavailable")
			return streamers.LLMDecision{}, nil
		}
		logger.Error("stream chunk capture failed", zap.String("streamerID", id), zap.Error(err))
		w.metrics.recordFailure(ctx, id, "capture")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	logger.Info("stream chunk captured", zap.String("streamerID", id), zap.String("chunkRef", chunk.Reference))
	defer cleanupChunkRef(chunk.Reference)

	runID, err := w.runs.CreateRun(ctx, id)
	if err != nil {
		logger.Error("failed to create analysis run", zap.String("streamerID", id), zap.Error(err))
		w.metrics.recordFailure(ctx, id, "create_run")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	logger.Info("analysis run created", zap.String("streamerID", id), zap.String("runID", runID))

	lastDecision, err := w.processExecutionPlan(ctx, runID, id, chunk)
	if err != nil {
		w.metrics.recordFailure(ctx, id, "execution_plan")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	w.metrics.recordCycle(ctx, id, "completed")
	logger.Info("streamer processing cycle completed", zap.String("streamerID", id), zap.String("runID", runID), zap.String("finalStage", lastDecision.Stage), zap.String("finalLabel", lastDecision.Label), zap.Float64("finalConfidence", lastDecision.Confidence))
	return lastDecision, nil
}

func (w *Worker) processExecutionPlan(ctx context.Context, runID, streamerID string, chunk ChunkRef) (streamers.LLMDecision, error) {
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}

	activePrompts := w.prompts.ListActive(ctx)
	if len(activePrompts) == 0 {
		logger.Warn("no active prompts found for streamer processing", zap.String("streamerID", streamerID))
		return streamers.LLMDecision{}, prompts.ErrNotFound
	}
	logger.Info("active prompts loaded for streamer processing", zap.String("streamerID", streamerID), zap.Int("promptCount", len(activePrompts)))

	var lastDecision streamers.LLMDecision
	for _, activePrompt := range activePrompts {
		logger.Info("processing prompt stage", zap.String("streamerID", streamerID), zap.String("runID", runID), zap.String("stage", activePrompt.Stage), zap.String("promptVersionID", activePrompt.ID))
		decision, err := w.processStage(ctx, runID, streamerID, chunk, activePrompt)
		if err != nil {
			logger.Error("prompt stage processing failed", zap.String("streamerID", streamerID), zap.String("runID", runID), zap.String("stage", activePrompt.Stage), zap.Error(err))
			return streamers.LLMDecision{}, err
		}
		logger.Info("prompt stage processed", zap.String("streamerID", streamerID), zap.String("runID", runID), zap.String("stage", decision.Stage), zap.String("label", decision.Label), zap.Float64("confidence", decision.Confidence))
		lastDecision = decision
	}
	return lastDecision, nil
}

func (w *Worker) captureWithRetry(ctx context.Context, streamerID string) (ChunkRef, error) {
	attempts := w.captureRetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		chunk, err := w.capture.Capture(ctx, streamerID)
		if err == nil {
			return chunk, nil
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		if err := w.waitForRetry(ctx, w.captureRetryBackoff, attempt); err != nil {
			return ChunkRef{}, err
		}
	}
	return ChunkRef{}, lastErr
}

func (w *Worker) processStage(ctx context.Context, runID, streamerID string, chunk ChunkRef, activePrompt prompts.PromptVersion) (streamers.LLMDecision, error) {
	previousState := w.resolvePreviousState(ctx, streamerID)
	result, err := w.classifyWithRetry(ctx, StageRequest{StreamerID: streamerID, Stage: activePrompt.Stage, Chunk: chunk, Prompt: activePrompt, PreviousState: previousState}, activePrompt)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, activePrompt.Stage)
		return streamers.LLMDecision{}, err
	}
	return w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, previousState)
}

func (w *Worker) processStageResult(ctx context.Context, activePrompt prompts.PromptVersion, result StageClassification, chunk ChunkRef, runID, streamerID, previousState string) (streamers.LLMDecision, error) {
	label := strings.TrimSpace(result.Label)
	if label == "" || result.Confidence < effectiveConfidenceThreshold(w.minConfidence, activePrompt.MinConfidence) {
		label = "uncertain"
	}
	w.metrics.recordStageResult(ctx, activePrompt.Stage, label, result.Latency, result.TokensIn, result.TokensOut)
	w.metrics.recordChunkLag(ctx, activePrompt.Stage, chunk.CapturedAt, time.Now().UTC())
	transitionOutcome := strings.TrimSpace(result.NormalizedOutcome)
	if transitionOutcome == "" {
		transitionOutcome = label
	}
	recordReq := streamers.RecordDecisionRequest{
		RunID:             runID,
		StreamerID:        streamerID,
		Stage:             activePrompt.Stage,
		Label:             label,
		Confidence:        result.Confidence,
		ChunkCapturedAt:   chunk.CapturedAt,
		PromptVersionID:   activePrompt.ID,
		PromptText:        activePrompt.Template,
		Model:             activePrompt.Model,
		Temperature:       activePrompt.Temperature,
		MaxTokens:         activePrompt.MaxTokens,
		TimeoutMS:         activePrompt.TimeoutMS,
		ChunkRef:          chunk.Reference,
		RequestRef:        result.RequestRef,
		ResponseRef:       result.ResponseRef,
		RawResponse:       result.RawResponse,
		TokensIn:          result.TokensIn,
		TokensOut:         result.TokensOut,
		LatencyMS:         result.Latency.Milliseconds(),
		TransitionOutcome: transitionOutcome,
		PreviousStateJSON: previousState,
		UpdatedStateJSON:  firstNonEmpty(strings.TrimSpace(result.UpdatedStateJSON), strings.TrimSpace(result.RawResponse)),
		EvidenceDeltaJSON: firstNonEmpty(strings.TrimSpace(result.EvidenceDeltaJSON), strings.TrimSpace(result.NextEvidenceJSON)),
		ConflictsJSON:     strings.TrimSpace(result.ConflictsJSON),
		FinalOutcome:      strings.TrimSpace(result.FinalOutcome),
	}
	decision, err := w.decisions.RecordLLMDecision(ctx, recordReq)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, "record_decision")
		return streamers.LLMDecision{}, err
	}
	return decision, nil
}

func (w *Worker) resolvePreviousState(ctx context.Context, streamerID string) string {
	if w == nil || w.decisions == nil {
		return ""
	}
	items := w.decisions.ListAllLLMDecisions(ctx, streamerID)
	for i := len(items) - 1; i >= 0; i-- {
		if state := strings.TrimSpace(items[i].UpdatedStateJSON); state != "" {
			return state
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (w *Worker) classifyWithRetry(ctx context.Context, input StageRequest, activePrompt prompts.PromptVersion) (StageClassification, error) {
	attempts := activePrompt.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := w.classifier.Classify(ctx, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		if err := w.waitForRetry(ctx, time.Duration(activePrompt.BackoffMS)*time.Millisecond, attempt); err != nil {
			return StageClassification{}, err
		}
	}
	return StageClassification{}, lastErr
}

func (w *Worker) waitForRetry(ctx context.Context, base time.Duration, attempt int) error {
	if attempt < 1 || base <= 0 {
		return nil
	}
	backoff := base
	for i := 1; i < attempt; i++ {
		backoff *= 2
	}
	return w.sleepFn(ctx, backoff)
}

func effectiveConfidenceThreshold(defaultValue, promptValue float64) float64 {
	if promptValue > 0 {
		return promptValue
	}
	return defaultValue
}

func cleanupChunkRef(ref string) {
	path := strings.TrimSpace(ref)
	if path == "" || strings.Contains(path, "://") {
		return
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext == "" {
		return
	}
	_ = os.Remove(path)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
