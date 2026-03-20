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
	StreamerID string
	Stage      string
	Chunk      ChunkRef
	Prompt     prompts.PromptVersion
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

	if w.scenarios != nil {
		globalPrompt, err := w.scenarios.GetActiveGlobalDetector(ctx)
		if err != nil {
			logger.Warn("no active global detector configured", zap.String("streamerID", streamerID), zap.Error(err))
			return streamers.LLMDecision{}, err
		}
		detectorDecision, err := w.processPromptTemplate(ctx, runID, streamerID, chunk, globalPrompt, nil)
		if err != nil {
			logger.Error("global detector stage failed", zap.String("streamerID", streamerID), zap.Error(err))
			return streamers.LLMDecision{}, err
		}
		gameSlug := strings.TrimSpace(detectorDecision.Label)
		if gameSlug == "" || gameSlug == "uncertain" {
			return detectorDecision, nil
		}
		scenario, err := w.scenarios.GetActiveScenarioByGame(ctx, gameSlug)
		if err != nil {
			logger.Info("no active game scenario found after detector step", zap.String("streamerID", streamerID), zap.String("gameSlug", gameSlug), zap.Error(err))
			return detectorDecision, nil
		}
		currentStep, ok := scenario.EntryStep()
		if !ok {
			return detectorDecision, nil
		}
		lastDecision := detectorDecision
		for {
			stepDecision, transition, ok, err := w.processScenarioStep(ctx, runID, streamerID, chunk, currentStep, scenario)
			if err != nil {
				return streamers.LLMDecision{}, err
			}
			lastDecision = stepDecision
			if !ok || transition.Terminal {
				return lastDecision, nil
			}
			nextStep, ok := scenario.FindStep(transition.ToStepCode)
			if !ok {
				return lastDecision, nil
			}
			currentStep = nextStep
		}
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
		decision, err := w.processStage(ctx, runID, streamerID, chunk, activePrompt, nil)
		if err != nil {
			logger.Error("prompt stage processing failed", zap.String("streamerID", streamerID), zap.String("runID", runID), zap.String("stage", activePrompt.Stage), zap.Error(err))
			return streamers.LLMDecision{}, err
		}
		logger.Info("prompt stage processed", zap.String("streamerID", streamerID), zap.String("runID", runID), zap.String("stage", decision.Stage), zap.String("label", decision.Label), zap.Float64("confidence", decision.Confidence))
		lastDecision = decision
	}
	return lastDecision, nil
}

func (w *Worker) processPromptTemplate(ctx context.Context, runID, streamerID string, chunk ChunkRef, activePrompt prompts.PromptTemplate, transition *prompts.ScenarioTransition) (streamers.LLMDecision, error) {
	return w.processStage(ctx, runID, streamerID, chunk, promptVersionFromTemplate(activePrompt), transition)
}

func (w *Worker) processScenarioStep(ctx context.Context, runID, streamerID string, chunk ChunkRef, step prompts.ScenarioStep, scenario prompts.ScenarioVersion) (streamers.LLMDecision, prompts.ScenarioTransition, bool, error) {
	activePrompt := promptVersionFromTemplate(step.Prompt)
	result, err := w.classifyWithRetry(ctx, StageRequest{
		StreamerID: streamerID,
		Stage:      activePrompt.Stage,
		Chunk:      chunk,
		Prompt:     activePrompt,
	}, activePrompt)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, activePrompt.Stage)
		return streamers.LLMDecision{}, prompts.ScenarioTransition{}, false, err
	}
	label := strings.TrimSpace(result.Label)
	if label == "" || result.Confidence < effectiveConfidenceThreshold(w.minConfidence, activePrompt.MinConfidence) {
		label = "uncertain"
	}
	transition, ok := scenario.ResolveTransition(step.Code, label)
	decision, err := w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, func() *prompts.ScenarioTransition {
		if !ok {
			return nil
		}
		return &transition
	}())
	return decision, transition, ok, err
}

func promptVersionFromTemplate(activePrompt prompts.PromptTemplate) prompts.PromptVersion {
	return prompts.PromptVersion{
		ID:            activePrompt.ID,
		Stage:         activePrompt.Stage,
		Template:      activePrompt.Template,
		Model:         activePrompt.Model,
		Temperature:   activePrompt.Temperature,
		MaxTokens:     activePrompt.MaxTokens,
		TimeoutMS:     activePrompt.TimeoutMS,
		RetryCount:    activePrompt.RetryCount,
		BackoffMS:     activePrompt.BackoffMS,
		CooldownMS:    activePrompt.CooldownMS,
		MinConfidence: activePrompt.MinConfidence,
	}
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

func (w *Worker) processStage(ctx context.Context, runID, streamerID string, chunk ChunkRef, activePrompt prompts.PromptVersion, transition *prompts.ScenarioTransition) (streamers.LLMDecision, error) {
	result, err := w.classifyWithRetry(ctx, StageRequest{StreamerID: streamerID, Stage: activePrompt.Stage, Chunk: chunk, Prompt: activePrompt}, activePrompt)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, activePrompt.Stage)
		return streamers.LLMDecision{}, err
	}
	return w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, transition)
}

func (w *Worker) processStageResult(ctx context.Context, activePrompt prompts.PromptVersion, result StageClassification, chunk ChunkRef, runID, streamerID string, transition *prompts.ScenarioTransition) (streamers.LLMDecision, error) {
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
	}
	if transition != nil {
		recordReq.TransitionToStep = transition.ToStepCode
		recordReq.TransitionTerminal = transition.Terminal
	}
	decision, err := w.decisions.RecordLLMDecision(ctx, recordReq)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, "record_decision")
		return streamers.LLMDecision{}, err
	}
	return decision, nil
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
