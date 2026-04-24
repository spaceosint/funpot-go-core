package media

import (
	"context"
	"encoding/json"
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
	ErrTrackingStop       = errors.New("stream tracking should stop")
)

type ChunkRef struct {
	Reference  string
	CapturedAt time.Time
}

type StageRequest struct {
	StreamerID      string
	Stage           string
	Chunk           ChunkRef
	Prompt          prompts.PromptVersion
	ResponseSchema  string
	SendPrompt      bool
	ScenarioFolder  string
	ScenarioPackage string
}

const (
	trackerStageDiscovery = "match_discovery"
	trackerStageUpdate    = "match_update"
	trackerStageFinalize  = "match_finalize"
	trackerStageClose     = "close_current_session"
)

type StageClassification struct {
	Label             string
	Confidence        float64
	RawResponse       string
	RequestRef        string
	ResponseRef       string
	RequestPayload    string
	ResponsePayload   string
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

type StepDurationCapture interface {
	CaptureWithDuration(ctx context.Context, streamerID string, duration time.Duration) (ChunkRef, error)
}

type StageClassifier interface {
	Classify(ctx context.Context, input StageRequest) (StageClassification, error)
}

type PromptResolver interface {
	GetGameScenario(ctx context.Context, id string) (prompts.GameScenario, error)
	GetActiveGameScenario(ctx context.Context, gameSlug string) (prompts.GameScenario, error)
	GetActiveScenarioPackage(ctx context.Context, gameSlug string) (prompts.ScenarioPackage, error)
	GetScenarioPackage(ctx context.Context, id string) (prompts.ScenarioPackage, error)
	GetLLMModelConfig(ctx context.Context, id string) (prompts.LLMModelConfig, error)
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

type ChunkPublisher interface {
	Publish(ctx context.Context, streamerID string, chunk ChunkRef) error
}

type ChunkFinalizer interface {
	Finalize(ctx context.Context, streamerID string, capturedAt time.Time) error
}

type Worker struct {
	logger              *zap.Logger
	metrics             *workerMetrics
	capture             StreamCapture
	classifier          StageClassifier
	prompts             PromptResolver
	runs                RunStore
	decisions           DecisionStore
	locker              Locker
	lockTTL             time.Duration
	minConfidence       float64
	captureRetryCount   int
	captureRetryBackoff time.Duration
	chunkPublisher      ChunkPublisher
	sleepFn             func(context.Context, time.Duration) error
}

type WorkerConfig struct {
	LockTTL             time.Duration
	MinConfidence       float64
	CaptureRetryCount   int
	CaptureRetryBackoff time.Duration
	ChunkPublisher      ChunkPublisher
}

func NewWorker(capture StreamCapture, classifier StageClassifier, promptResolver PromptResolver, runs RunStore, decisions DecisionStore, locker Locker, cfg WorkerConfig) *Worker {
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
		runs:                runs,
		decisions:           decisions,
		locker:              locker,
		lockTTL:             cfg.LockTTL,
		minConfidence:       cfg.MinConfidence,
		captureRetryCount:   cfg.CaptureRetryCount,
		captureRetryBackoff: cfg.CaptureRetryBackoff,
		chunkPublisher:      cfg.ChunkPublisher,
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

	previousState := w.resolvePreviousState(ctx, id)
	gameScenarioSlug := resolveGameScenarioSlug(previousState)
	gameScenarioID := scenarioStateGameScenarioID(previousState)
	gameScenario, err := prompts.GameScenario{}, prompts.ErrGameScenarioNotFound
	if gameScenarioID != "" {
		gameScenario, err = w.prompts.GetGameScenario(ctx, gameScenarioID)
	}
	if err != nil {
		gameScenario, err = w.prompts.GetActiveGameScenario(ctx, gameScenarioSlug)
		if err != nil && !strings.EqualFold(gameScenarioSlug, "global") {
			gameScenario, err = w.prompts.GetActiveGameScenario(ctx, "global")
		}
	}
	if err != nil {
		logger.Error("active game scenario lookup failed", zap.String("streamerID", id), zap.String("gameSlug", gameScenarioSlug), zap.Error(err))
		w.metrics.recordFailure(ctx, id, "lookup_game_scenario")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	rootNode, err := gameScenario.InitialNode()
	if err != nil {
		logger.Error("game scenario initial node resolve failed", zap.String("streamerID", id), zap.String("gameScenarioID", gameScenario.ID), zap.Error(err))
		w.metrics.recordFailure(ctx, id, "resolve_game_scenario_initial")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	pkg, err := w.prompts.GetScenarioPackage(ctx, rootNode.ScenarioPackageID)
	if err != nil {
		logger.Error("initial scenario package lookup failed", zap.String("streamerID", id), zap.String("gameScenarioID", gameScenario.ID), zap.String("packageID", rootNode.ScenarioPackageID), zap.Error(err))
		w.metrics.recordFailure(ctx, id, "lookup_scenario_package")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	execution, err := w.planScenarioExecution(ctx, id, gameScenario, pkg)
	if err != nil {
		if errors.Is(err, ErrTrackingStop) {
			w.metrics.recordCycle(ctx, id, "tracking_stop")
			lastDecision := w.latestDecisionByStreamer(ctx, id)
			if strings.TrimSpace(lastDecision.UpdatedStateJSON) == "" {
				lastDecision.UpdatedStateJSON = w.resolvePreviousState(ctx, id)
			}
			if strings.TrimSpace(lastDecision.Label) == "" {
				lastDecision.Label = "tracking_stopped"
			}
			return lastDecision, ErrTrackingStop
		}
		w.metrics.recordFailure(ctx, id, "plan_execution")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	if execution.StopTracking {
		w.metrics.recordCycle(ctx, id, "tracking_stop")
		lastDecision := w.latestDecisionByStreamer(ctx, id)
		lastDecision.StreamerID = id
		lastDecision.TransitionTerminal = true
		lastDecision.Label = firstNonEmpty(strings.TrimSpace(execution.TerminalLabel), strings.TrimSpace(lastDecision.Label), "tracking_stopped")
		terminalState := firstNonEmpty(strings.TrimSpace(execution.TerminalStateJSON), strings.TrimSpace(lastDecision.UpdatedStateJSON), w.resolvePreviousState(ctx, id))
		lastDecision.UpdatedStateJSON = enrichScenarioState(`{}`, terminalState, execution.GameScenarioID, execution.CurrentPackageID, lastDecision.Stage, execution.TransitionTrace)
		return lastDecision, ErrTrackingStop
	}

	chunk, err := w.captureWithRetry(ctx, id, execution.Step.SegmentSeconds)
	if err != nil {
		if errors.Is(err, ErrStreamlinkAdBreak) {
			logger.Info("stream chunk capture skipped because stream is on ad break", zap.String("streamerID", id), zap.Error(err))
			w.metrics.recordCycle(ctx, id, "ad_break")
			return streamers.LLMDecision{}, nil
		}
		if errors.Is(err, ErrStreamlinkStreamEnded) {
			if finalizer, ok := w.chunkPublisher.(ChunkFinalizer); ok {
				if finalizeErr := finalizer.Finalize(ctx, id, time.Now().UTC()); finalizeErr != nil {
					logger.Error("finalize stream video failed after stream end", zap.String("streamerID", id), zap.Error(finalizeErr))
					w.metrics.recordFailure(ctx, id, "finalize_stream")
					w.metrics.recordCycle(ctx, id, "failed")
					return streamers.LLMDecision{}, finalizeErr
				}
			}
			logger.Info("stream chunk capture skipped because stream has ended or is unavailable", zap.String("streamerID", id), zap.Error(err))
			w.metrics.recordCycle(ctx, id, "stream_unavailable")
			return streamers.LLMDecision{}, ErrTrackingStop
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

	lastDecision, err := w.processExecutionPlan(ctx, runID, id, chunk, pkg, execution)
	if err != nil {
		if execution.CurrentPackageID != "" && execution.StartPackageID != "" && execution.CurrentPackageID != execution.StartPackageID {
			rootPkg, rootErr := w.prompts.GetScenarioPackage(ctx, execution.StartPackageID)
			if rootErr == nil {
				rootExecution, planErr := w.planScenarioExecution(ctx, id, gameScenario, rootPkg)
				if planErr == nil {
					lastDecision, err = w.processExecutionPlan(ctx, runID, id, chunk, rootPkg, rootExecution)
				}
			}
		}
	}
	if err != nil {
		w.metrics.recordFailure(ctx, id, "execution_plan")
		w.metrics.recordCycle(ctx, id, "failed")
		return streamers.LLMDecision{}, err
	}
	if w.chunkPublisher != nil {
		if err := w.chunkPublisher.Publish(ctx, id, chunk); err != nil {
			logger.Error("chunk publish failed", zap.String("streamerID", id), zap.String("chunkRef", chunk.Reference), zap.Error(err))
			w.metrics.recordFailure(ctx, id, "publish_chunk")
			w.metrics.recordCycle(ctx, id, "failed")
			return streamers.LLMDecision{}, err
		}
	}
	w.metrics.recordCycle(ctx, id, "completed")
	logger.Info("streamer processing cycle completed", zap.String("streamerID", id), zap.String("runID", runID), zap.String("finalStage", lastDecision.Stage), zap.String("finalLabel", lastDecision.Label), zap.Float64("finalConfidence", lastDecision.Confidence))
	return lastDecision, nil
}

func (w *Worker) processExecutionPlan(ctx context.Context, runID, streamerID string, chunk ChunkRef, pkg prompts.ScenarioPackage, execution scenarioExecutionPlan) (streamers.LLMDecision, error) {
	return w.processScenarioPackage(ctx, runID, streamerID, chunk, pkg, execution)
}

func isTrackerStage(stage string) bool {
	switch strings.TrimSpace(strings.ToLower(stage)) {
	case trackerStageDiscovery, trackerStageUpdate, trackerStageFinalize, trackerStageClose, "start", "update", "finalize", "finish", "end", "close":
		return true
	default:
		return false
	}
}

func defaultTrackerState() string {
	return `{}`
}

func (w *Worker) captureWithRetry(ctx context.Context, streamerID string, segmentSeconds int) (ChunkRef, error) {
	attempts := w.captureRetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}
	duration := time.Duration(segmentSeconds) * time.Second
	if duration <= 0 {
		duration = 30 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		chunk, err := w.captureChunk(ctx, streamerID, duration)
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

func (w *Worker) captureChunk(ctx context.Context, streamerID string, duration time.Duration) (ChunkRef, error) {
	if timedCapture, ok := w.capture.(StepDurationCapture); ok {
		return timedCapture.CaptureWithDuration(ctx, streamerID, duration)
	}
	return w.capture.Capture(ctx, streamerID)
}

func (w *Worker) processStageResult(ctx context.Context, activePrompt prompts.PromptVersion, result StageClassification, chunk ChunkRef, runID, streamerID, previousState string) (streamers.LLMDecision, error) {
	updatedStateJSON := normalizeStateSnapshot(previousState, result)
	evidenceDelta := firstNonEmpty(strings.TrimSpace(result.EvidenceDeltaJSON), strings.TrimSpace(result.NextEvidenceJSON))
	conflicts := strings.TrimSpace(result.ConflictsJSON)
	finalOutcome := strings.TrimSpace(result.FinalOutcome)
	if strings.EqualFold(strings.TrimSpace(activePrompt.Stage), trackerStageUpdate) &&
		!hasConcreteTrackerChange(previousState, updatedStateJSON, evidenceDelta, conflicts, finalOutcome) {
		return streamers.LLMDecision{
			RunID:            runID,
			StreamerID:       streamerID,
			Stage:            activePrompt.Stage,
			Label:            "awaiting_changes",
			Confidence:       result.Confidence,
			UpdatedStateJSON: updatedStateJSON,
		}, nil
	}
	label := normalizeDecisionLabel(result, updatedStateJSON)
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
		RequestPayload:    result.RequestPayload,
		ResponsePayload:   result.ResponsePayload,
		RawResponse:       result.RawResponse,
		TokensIn:          result.TokensIn,
		TokensOut:         result.TokensOut,
		LatencyMS:         result.Latency.Milliseconds(),
		TransitionOutcome: transitionOutcome,
		PreviousStateJSON: previousState,
		UpdatedStateJSON:  updatedStateJSON,
		EvidenceDeltaJSON: evidenceDelta,
		ConflictsJSON:     conflicts,
		FinalOutcome:      finalOutcome,
	}
	decision, err := w.decisions.RecordLLMDecision(ctx, recordReq)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, "record_decision")
		return streamers.LLMDecision{}, err
	}
	return decision, nil
}

func hasConcreteTrackerChange(previousState, updatedState, evidenceDelta, conflicts, finalOutcome string) bool {
	prevNormalized := normalizeStateJSONWithoutScenario(previousState)
	updatedNormalized := normalizeStateJSONWithoutScenario(updatedState)
	if prevNormalized != updatedNormalized {
		return true
	}
	if normalized := normalizeStateJSON(evidenceDelta); normalized != "" && normalized != "[]" {
		return true
	}
	if normalized := normalizeStateJSON(conflicts); normalized != "" && normalized != "[]" {
		return true
	}
	outcome := strings.TrimSpace(strings.ToLower(finalOutcome))
	return outcome != "" && outcome != "unknown"
}

func normalizeStateJSONWithoutScenario(raw string) string {
	state := parseJSONMap(raw)
	delete(state, "_scenario")
	body, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return string(body)
}

func (w *Worker) resolvePreviousState(ctx context.Context, streamerID string) string {
	if w == nil || w.decisions == nil {
		return defaultTrackerState()
	}
	items := w.decisions.ListAllLLMDecisions(ctx, streamerID)
	for i := len(items) - 1; i >= 0; i-- {
		if state := strings.TrimSpace(items[i].UpdatedStateJSON); state != "" {
			return state
		}
	}
	return defaultTrackerState()
}

func normalizeDecisionLabel(result StageClassification, updatedStateJSON string) string {
	label := strings.TrimSpace(result.Label)
	if label != "" {
		return label
	}
	if strings.TrimSpace(result.FinalOutcome) != "" && !strings.EqualFold(strings.TrimSpace(result.FinalOutcome), "unknown") {
		return "finalized"
	}
	if strings.TrimSpace(updatedStateJSON) != "" {
		return "state_updated"
	}
	return ""
}

func normalizeStateSnapshot(previousState string, result StageClassification) string {
	current := strings.TrimSpace(result.UpdatedStateJSON)
	if current == "" {
		return strings.TrimSpace(previousState)
	}
	normalizedCurrent := normalizeStateJSON(current)
	if normalizedCurrent == "" {
		return strings.TrimSpace(previousState)
	}
	normalizedPrevious := normalizeStateJSON(previousState)
	if normalizedPrevious == "" {
		return normalizedCurrent
	}
	merged, ok := mergeStateSnapshots(normalizedPrevious, normalizedCurrent)
	if !ok {
		return normalizedCurrent
	}
	return merged
}

func normalizeStateJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	normalized := normalizeStateValue(decoded)
	body, err := json.Marshal(normalized)
	if err != nil {
		return trimmed
	}
	return string(body)
}

func normalizeStateValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if state, ok := typed["updated_state"]; ok {
			finalOutcome := strings.TrimSpace(stringValue(valueFromMap(typed, "final_outcome")))
			typed = map[string]any{
				"state": state,
			}
			if finalOutcome != "" {
				typed["final_outcome"] = finalOutcome
			}
		}
		if state, ok := typed["state"]; ok {
			if stateMap, ok := state.(map[string]any); ok {
				typed["state"] = normalizeStateFields(stateMap)
			}
		}
		for key, item := range typed {
			if key == "state" {
				continue
			}
			typed[key] = normalizeStateValue(item)
		}
		return typed
	case []any:
		for idx, item := range typed {
			typed[idx] = normalizeStateValue(item)
		}
		return typed
	default:
		return typed
	}
}

func normalizeStateFields(fields map[string]any) map[string]any {
	normalized := make(map[string]any, len(fields))
	for key, item := range fields {
		switch typed := item.(type) {
		case map[string]any:
			if rawValue, ok := typed["value"]; ok {
				normalized[key] = normalizeStateValue(rawValue)
				continue
			}
			normalized[key] = normalizeStateValue(typed)
		default:
			normalized[key] = normalizeStateValue(item)
		}
	}
	return normalized
}

func mergeStateSnapshots(previousState, currentState string) (string, bool) {
	var previous any
	if err := json.Unmarshal([]byte(previousState), &previous); err != nil {
		return "", false
	}
	var current any
	if err := json.Unmarshal([]byte(currentState), &current); err != nil {
		return "", false
	}
	merged := mergeStateValue(previous, current)
	body, err := json.Marshal(merged)
	if err != nil {
		return "", false
	}
	return string(body), true
}

func mergeStateValue(previous, current any) any {
	switch currentTyped := current.(type) {
	case map[string]any:
		previousTyped, _ := previous.(map[string]any)
		merged := make(map[string]any, len(previousTyped)+len(currentTyped))
		for key, item := range previousTyped {
			merged[key] = item
		}
		for key, item := range currentTyped {
			merged[key] = mergeStateValue(previousTyped[key], item)
		}
		return merged
	default:
		return current
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func valueFromMap(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
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
	const (
		minTransientAttempts    = 3
		defaultTransientBackoff = 500 * time.Millisecond
	)

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := w.classifier.Classify(ctx, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
		geminiRetryable, isGeminiError := geminiRetryableClassificationError(err)
		retryable := geminiRetryable || !isGeminiError
		maxAttempts := attempts
		if geminiRetryable && maxAttempts < minTransientAttempts {
			maxAttempts = minTransientAttempts
		}
		if !retryable || attempt == maxAttempts {
			break
		}
		backoff := time.Duration(activePrompt.BackoffMS) * time.Millisecond
		if geminiRetryable && backoff <= 0 {
			backoff = defaultTransientBackoff
		}
		if err := w.waitForRetry(ctx, backoff, attempt); err != nil {
			return StageClassification{}, err
		}
		attempts = maxAttempts
	}
	return StageClassification{}, lastErr
}

func geminiRetryableClassificationError(err error) (retryable bool, isGemini bool) {
	if err == nil {
		return false, false
	}
	var geminiErr *GeminiGenerateContentError
	if errors.As(err, &geminiErr) {
		return geminiErr.Retryable(), true
	}
	return false, false
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

type scenarioExecutionPlan struct {
	Step                  prompts.ScenarioStep
	Entering              bool
	StopTracking          bool
	TerminalStateJSON     string
	TerminalLabel         string
	TransitionTrace       map[string]any
	PreviousState         string
	GameScenarioID        string
	StartPackageID        string
	CurrentPackageID      string
	CurrentPackageChanged bool
}

func (w *Worker) planScenarioExecution(ctx context.Context, streamerID string, gameScenario prompts.GameScenario, pkg prompts.ScenarioPackage) (scenarioExecutionPlan, error) {
	latest := w.latestDecisionByStreamer(ctx, streamerID)
	previousState := w.resolvePreviousState(ctx, streamerID)
	startPackageID := strings.TrimSpace(pkg.ID)
	currentNodeID := scenarioStateNodeID(previousState)
	resolvedNode, matchedTransitionID, nodeChanged, err := gameScenario.ResolveNode(currentNodeID, previousState)
	if err != nil {
		return scenarioExecutionPlan{}, err
	}
	currentPackageID := startPackageID
	if strings.TrimSpace(resolvedNode.ScenarioPackageID) != "" {
		currentPackageID = strings.TrimSpace(resolvedNode.ScenarioPackageID)
	}
	activePackage := pkg
	if currentPackageID != "" && currentPackageID != startPackageID {
		resolved, pkgErr := w.prompts.GetScenarioPackage(ctx, currentPackageID)
		if pkgErr != nil {
			return scenarioExecutionPlan{}, pkgErr
		}
		activePackage = resolved
	}
	packageChanged := nodeChanged || currentPackageID != startPackageID
	transitionTrace := map[string]any{
		"status":      "no_transition",
		"fromNode":    firstNonEmpty(strings.TrimSpace(currentNodeID), strings.TrimSpace(gameScenario.InitialNodeID)),
		"toNode":      strings.TrimSpace(resolvedNode.ID),
		"fromPackage": strings.TrimSpace(startPackageID),
		"toPackage":   strings.TrimSpace(currentPackageID),
	}
	if nodeChanged {
		transitionTrace["status"] = "accepted"
		transitionTrace["reason"] = "game_scenario_transition_matched"
	}
	if terminal, ok, terminalErr := gameScenario.ResolveTerminalCondition(resolvedNode.ID, matchedTransitionID, previousState); terminalErr != nil {
		return scenarioExecutionPlan{}, terminalErr
	} else if ok {
		transitionTrace = map[string]any{
			"status":      "terminal_stop",
			"fromNode":    strings.TrimSpace(resolvedNode.ID),
			"fromPackage": strings.TrimSpace(activePackage.ID),
			"reason":      "game_scenario_terminal_condition_matched",
		}
		return scenarioExecutionPlan{
			StopTracking:      true,
			TerminalStateJSON: mergeJSONState(previousState, terminal.ResultStateJSON),
			TerminalLabel:     firstNonEmpty(strings.TrimSpace(terminal.ResultLabel), "tracking_stopped"),
			TransitionTrace:   transitionTrace,
			PreviousState:     previousState,
			GameScenarioID:    strings.TrimSpace(gameScenario.ID),
			StartPackageID:    startPackageID,
			CurrentPackageID:  strings.TrimSpace(activePackage.ID),
		}, nil
	}
	resolution, resolveErr := activePackage.ResolveNextPackage(previousState)
	if resolveErr == nil {
		if resolution.StopTracking {
			transitionTrace = map[string]any{
				"status":      "terminal_stop",
				"fromPackage": strings.TrimSpace(activePackage.ID),
				"reason":      "terminal_condition_matched",
			}
			return scenarioExecutionPlan{
				StopTracking:      true,
				TerminalStateJSON: mergeJSONState(previousState, resolution.FinalStateJSON),
				TerminalLabel:     firstNonEmpty(strings.TrimSpace(resolution.FinalLabel), "tracking_stopped"),
				TransitionTrace:   transitionTrace,
				PreviousState:     previousState,
				GameScenarioID:    strings.TrimSpace(gameScenario.ID),
				StartPackageID:    startPackageID,
				CurrentPackageID:  strings.TrimSpace(activePackage.ID),
			}, nil
		}
		if resolution.Changed {
			nextPackage, pkgErr := w.prompts.GetScenarioPackage(ctx, resolution.PackageID)
			if pkgErr == nil {
				canEnter, enterErr := nextPackage.CanEnter(previousState)
				if enterErr == nil && canEnter {
					transitionTrace = map[string]any{
						"status":      "accepted",
						"fromPackage": strings.TrimSpace(activePackage.ID),
						"toPackage":   strings.TrimSpace(nextPackage.ID),
						"reason":      "transition_condition_and_entry_guard_matched",
					}
					activePackage = nextPackage
					currentPackageID = strings.TrimSpace(nextPackage.ID)
					packageChanged = true
				} else {
					transitionTrace = map[string]any{
						"status":      "rejected",
						"fromPackage": strings.TrimSpace(activePackage.ID),
						"toPackage":   strings.TrimSpace(nextPackage.ID),
						"reason":      "target_initial_entry_condition_failed",
					}
				}
			} else {
				transitionTrace = map[string]any{
					"status":      "rejected",
					"fromPackage": strings.TrimSpace(activePackage.ID),
					"toPackage":   strings.TrimSpace(resolution.PackageID),
					"reason":      "target_package_not_found",
				}
			}
		}
	}
	currentStepID := strings.TrimSpace(latest.Stage)
	if packageChanged {
		currentStepID = ""
	}
	step, entering, err := activePackage.ResolveStep(currentStepID, previousState)
	if err != nil {
		if errors.Is(err, prompts.ErrScenarioStepNotFound) && strings.TrimSpace(latest.Stage) != "" {
			step, entering, err = activePackage.ResolveStep("", previousState)
		}
		if err != nil {
			return scenarioExecutionPlan{}, err
		}
	}
	if step.MaxRequests > 0 {
		requestCount := w.stepRequestCount(ctx, streamerID, step.ID)
		if requestCount >= step.MaxRequests {
			if step.Initial {
				return scenarioExecutionPlan{}, ErrTrackingStop
			}
			initial, initialErr := activePackage.InitialStep()
			if initialErr != nil {
				return scenarioExecutionPlan{}, initialErr
			}
			if initial.MaxRequests > 0 && w.stepRequestCount(ctx, streamerID, initial.ID) >= initial.MaxRequests {
				return scenarioExecutionPlan{}, ErrTrackingStop
			}
			step = initial
			entering = true
		}
	}
	return scenarioExecutionPlan{
		Step:                  step,
		Entering:              entering,
		StopTracking:          false,
		TransitionTrace:       transitionTrace,
		PreviousState:         previousState,
		GameScenarioID:        strings.TrimSpace(gameScenario.ID),
		StartPackageID:        startPackageID,
		CurrentPackageID:      strings.TrimSpace(activePackage.ID),
		CurrentPackageChanged: packageChanged,
	}, nil
}

func (w *Worker) stepRequestCount(ctx context.Context, streamerID, stepID string) int {
	if w == nil || w.decisions == nil {
		return 0
	}
	target := strings.TrimSpace(stepID)
	if target == "" {
		return 0
	}
	count := 0
	for _, item := range w.decisions.ListAllLLMDecisions(ctx, streamerID) {
		if strings.TrimSpace(item.Stage) == target {
			count++
		}
	}
	return count
}

func (w *Worker) processScenarioPackage(ctx context.Context, runID, streamerID string, chunk ChunkRef, pkg prompts.ScenarioPackage, execution scenarioExecutionPlan) (streamers.LLMDecision, error) {
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	step := execution.Step
	entering := execution.Entering
	previousState := execution.PreviousState
	logger.Info("scenario step selected", zap.String("streamerID", streamerID), zap.String("scenarioPackageID", pkg.ID), zap.String("stepID", step.ID), zap.Int("segmentSeconds", step.SegmentSeconds), zap.Int("maxRequests", step.MaxRequests))
	if strings.TrimSpace(pkg.LLMModelConfigID) == "" {
		return streamers.LLMDecision{}, prompts.ErrInvalidScenarioModelRef
	}
	config, err := w.prompts.GetLLMModelConfig(ctx, pkg.LLMModelConfigID)
	if err != nil {
		return streamers.LLMDecision{}, err
	}
	model := strings.TrimSpace(config.Model)
	activePrompt := prompts.PromptVersion{
		ID:       step.ID,
		Stage:    step.ID,
		Template: step.PromptTemplate,
		Model:    model,
		IsActive: true,
	}
	result, err := w.classifyWithRetry(ctx, StageRequest{
		StreamerID:      streamerID,
		Stage:           step.ID,
		Chunk:           chunk,
		Prompt:          activePrompt,
		ResponseSchema:  step.ResponseSchemaJSON,
		SendPrompt:      entering,
		ScenarioFolder:  step.Folder,
		ScenarioPackage: pkg.ID,
	}, activePrompt)
	if err != nil {
		return streamers.LLMDecision{}, err
	}
	finalPackageID, finalTransitionTrace := w.resolvePostStepScenarioTransition(ctx, execution, result.UpdatedStateJSON)
	if strings.TrimSpace(finalPackageID) == "" {
		finalPackageID = pkg.ID
	}
	result.UpdatedStateJSON = enrichScenarioState(result.UpdatedStateJSON, execution.PreviousState, execution.GameScenarioID, finalPackageID, step.ID, finalTransitionTrace)
	decision, err := w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, previousState)
	if err != nil {
		return streamers.LLMDecision{}, err
	}
	decision.TransitionToStep = step.ID
	return decision, nil
}

func (w *Worker) resolvePostStepScenarioTransition(ctx context.Context, execution scenarioExecutionPlan, currentState string) (string, map[string]any) {
	trace := execution.TransitionTrace
	currentPackageID := strings.TrimSpace(execution.CurrentPackageID)
	if currentPackageID == "" {
		currentPackageID = strings.TrimSpace(execution.StartPackageID)
	}
	if strings.TrimSpace(execution.GameScenarioID) == "" {
		return currentPackageID, trace
	}
	gameScenario, err := w.prompts.GetGameScenario(ctx, execution.GameScenarioID)
	if err != nil {
		return currentPackageID, trace
	}
	currentNodeID := strings.TrimSpace(fmt.Sprint(trace["toNode"]))
	if currentNodeID == "" {
		currentNodeID = scenarioStateNodeID(execution.PreviousState)
	}
	resolvedNode, _, nodeChanged, err := gameScenario.ResolveNode(currentNodeID, currentState)
	if err != nil || !nodeChanged {
		return currentPackageID, trace
	}
	nextPackageID := strings.TrimSpace(resolvedNode.ScenarioPackageID)
	if nextPackageID == "" {
		nextPackageID = currentPackageID
	}
	return nextPackageID, map[string]any{
		"status":      "accepted",
		"fromNode":    firstNonEmpty(currentNodeID, strings.TrimSpace(gameScenario.InitialNodeID)),
		"toNode":      strings.TrimSpace(resolvedNode.ID),
		"fromPackage": currentPackageID,
		"toPackage":   nextPackageID,
		"reason":      "game_scenario_transition_matched",
	}
}

func (w *Worker) latestDecisionByStreamer(ctx context.Context, streamerID string) streamers.LLMDecision {
	if w == nil || w.decisions == nil {
		return streamers.LLMDecision{}
	}
	items := w.decisions.ListAllLLMDecisions(ctx, streamerID)
	if len(items) == 0 {
		return streamers.LLMDecision{}
	}
	return items[len(items)-1]
}

func parseSimpleState(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	if nested, ok := out["state"].(map[string]any); ok {
		return nested
	}
	return out
}

func parseJSONMap(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func mergeJSONState(baseJSON, patchJSON string) string {
	base := parseJSONMap(baseJSON)
	patch := parseJSONMap(patchJSON)
	if len(base) == 0 && len(patch) == 0 {
		if strings.TrimSpace(baseJSON) != "" {
			return baseJSON
		}
		return patchJSON
	}
	for key, value := range patch {
		base[key] = value
	}
	body, err := json.Marshal(base)
	if err != nil {
		return firstNonEmpty(strings.TrimSpace(baseJSON), strings.TrimSpace(patchJSON))
	}
	return string(body)
}

func enrichScenarioState(currentState, previousState, gameScenarioID, packageID, stepID string, transitionTrace map[string]any) string {
	base := parseJSONMap(previousState)
	if existing, ok := base["_scenario"].(map[string]any); ok {
		baseScenario := map[string]any{}
		for k, v := range existing {
			baseScenario[k] = v
		}
		base["_scenario"] = baseScenario
	} else {
		base["_scenario"] = map[string]any{}
	}
	current := parseJSONMap(currentState)
	for key, value := range current {
		base[key] = value
	}
	scenarioMeta, _ := base["_scenario"].(map[string]any)
	scenarioMeta["gameScenarioId"] = strings.TrimSpace(gameScenarioID)
	scenarioMeta["packageId"] = strings.TrimSpace(packageID)
	scenarioMeta["stepId"] = strings.TrimSpace(stepID)
	if toNode, ok := transitionTrace["toNode"]; ok {
		nodeID := strings.TrimSpace(fmt.Sprint(toNode))
		if nodeID != "" {
			scenarioMeta["gameScenarioNodeId"] = nodeID
		}
	}
	if len(transitionTrace) > 0 {
		scenarioMeta["transition"] = transitionTrace
	}
	base["_scenario"] = scenarioMeta
	body, err := json.Marshal(base)
	if err != nil {
		return currentState
	}
	return string(body)
}

func scenarioStatePackageID(stateJSON string) string {
	state := parseJSONMap(stateJSON)
	raw, ok := state["_scenario"]
	if !ok {
		return ""
	}
	meta, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(meta["packageId"]))
}

func scenarioStateGameScenarioID(stateJSON string) string {
	state := parseJSONMap(stateJSON)
	raw, ok := state["_scenario"]
	if !ok {
		return ""
	}
	meta, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(meta["gameScenarioId"]))
}

func scenarioStateNodeID(stateJSON string) string {
	state := parseJSONMap(stateJSON)
	raw, ok := state["_scenario"]
	if !ok {
		return ""
	}
	meta, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(meta["gameScenarioNodeId"]))
}

func resolveGameScenarioSlug(stateJSON string) string {
	state := parseJSONMap(stateJSON)
	candidates := []string{
		valueAsNonNilString(state, "game"),
		valueAsNonNilString(state, "gameSlug"),
		valueAsNonNilString(state, "detectedGameKey"),
	}
	if nested, ok := state["state"].(map[string]any); ok {
		candidates = append(candidates,
			valueAsNonNilString(nested, "game"),
			valueAsNonNilString(nested, "gameSlug"),
			valueAsNonNilString(nested, "detectedGameKey"),
		)
	}
	if rawMeta, ok := state["_scenario"]; ok {
		if meta, ok := rawMeta.(map[string]any); ok {
			candidates = append(candidates, valueAsNonNilString(meta, "gameSlug"))
		}
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return strings.ToLower(candidate)
		}
	}
	return "global"
}

func valueAsNonNilString(payload map[string]any, key string) string {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}
