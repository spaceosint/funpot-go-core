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
	PreviousState   string
	StateSchema     string
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

type activeScenarioPackageResolver interface {
	GetActiveScenarioPackage(ctx context.Context, gameSlug string) (prompts.ScenarioPackage, error)
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

func (w *Worker) processExecutionPlan(ctx context.Context, runID, streamerID string, chunk ChunkRef) (streamers.LLMDecision, error) {
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}

	resolver, ok := w.prompts.(activeScenarioPackageResolver)
	if !ok {
		logger.Warn("scenario package resolver is not configured", zap.String("streamerID", streamerID))
		return streamers.LLMDecision{}, prompts.ErrScenarioPackageNotFound
	}

	pkg, err := resolver.GetActiveScenarioPackage(ctx, "global")
	if err != nil {
		logger.Error("active scenario package lookup failed", zap.String("streamerID", streamerID), zap.String("gameSlug", "global"), zap.Error(err))
		return streamers.LLMDecision{}, err
	}
	return w.processScenarioPackage(ctx, runID, streamerID, chunk, pkg)
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
	return `{"session_type":"single_match_single_chat","game":"cs2","mode":"unknown","session_status":{"value":"unknown","confidence":0,"reason":null},"focus_player":{"name":null,"team_side":"unknown","team_label":"unknown","confidence":0},"score_state":{"ct_score":null,"t_score":null,"source":"unknown","confidence":0},"round_tracking":{"observed_round_wins_ct":0,"observed_round_wins_t":0,"observed_round_history":[],"confidence":0},"winner_state":{"winner_side":"unknown","winner_team_label":"unknown","source":"unknown","confidence":0},"player_result":{"outcome":"unknown","confidence":0,"reason":"match ending not confirmed","is_final":false},"terminal_evidence":{"final_banner_seen":false,"final_banner_text":null,"final_scoreboard_seen":false,"final_scoreboard_text":null,"post_match_ui_seen":false,"return_to_lobby_seen":false,"strong_terminal_signals":[],"weak_terminal_signals":[]},"supporting_evidence":[],"open_uncertainties":[],"hard_conflicts":[],"next_needed_evidence":["clear final screen","clear final scoreboard","clear player team confirmation"]}`
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
	stateSchema := w.resolveTrackerConfig(ctx)
	result, err := w.classifyWithRetry(ctx, StageRequest{StreamerID: streamerID, Stage: activePrompt.Stage, Chunk: chunk, Prompt: activePrompt, PreviousState: previousState, StateSchema: stateSchema}, activePrompt)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, activePrompt.Stage)
		return streamers.LLMDecision{}, err
	}
	return w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, previousState)
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
	if normalizeStateJSON(previousState) != normalizeStateJSON(updatedState) {
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
	if initial := strings.TrimSpace(w.resolveInitialTrackerState(ctx)); initial != "" {
		return initial
	}
	return defaultTrackerState()
}

func (w *Worker) resolveInitialTrackerState(ctx context.Context) string {
	const gameSlug = "cs2"
	if resolver, ok := w.prompts.(activeStateSchemaResolver); ok {
		if item, err := resolver.GetActiveStateSchema(ctx, gameSlug); err == nil {
			if state := strings.TrimSpace(item.InitialStateJSON); state != "" && state != "{}" {
				return normalizeInitialStateTemplateJSON(state)
			}
		}
	}
	return ""
}

func normalizeInitialStateTemplateJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	normalized := normalizeTemplateValue(parsed)
	body, err := json.Marshal(normalized)
	if err != nil {
		return trimmed
	}
	return strings.TrimSpace(string(body))
}

func normalizeTemplateValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			out[key] = normalizeTemplateValue(nested)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, normalizeTemplateValue(item))
		}
		return out
	case string:
		return resolveTemplateEnumString(typed)
	default:
		return value
	}
}

func resolveTemplateEnumString(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.Contains(trimmed, "|") {
		return trimmed
	}
	parts := strings.Split(trimmed, "|")
	candidate := ""
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		if strings.EqualFold(token, "unknown") {
			return "unknown"
		}
		if candidate == "" {
			candidate = token
		}
	}
	return candidate
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
	current := firstNonEmpty(strings.TrimSpace(result.UpdatedStateJSON), strings.TrimSpace(result.RawResponse))
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
	case []any:
		if len(currentTyped) == 0 {
			if previousTyped, ok := previous.([]any); ok && len(previousTyped) > 0 {
				return previousTyped
			}
		}
		return currentTyped
	case string:
		if isPlaceholderString(currentTyped) {
			if previousTyped, ok := previous.(string); ok && strings.TrimSpace(previousTyped) != "" {
				return previousTyped
			}
		}
		return currentTyped
	case float64:
		if currentTyped == 0 {
			if previousTyped, ok := previous.(float64); ok && previousTyped != 0 {
				return previousTyped
			}
		}
		return currentTyped
	case bool:
		if !currentTyped {
			if previousTyped, ok := previous.(bool); ok && previousTyped {
				return previousTyped
			}
		}
		return currentTyped
	case nil:
		if previous != nil {
			return previous
		}
		return nil
	default:
		return currentTyped
	}
}

func isPlaceholderString(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "unknown", "null", "nil", "n/a", "none", "unset":
		return true
	default:
		return false
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

type activeStateSchemaResolver interface {
	GetActiveStateSchema(ctx context.Context, gameSlug string) (prompts.StateSchemaVersion, error)
}

func (w *Worker) resolveTrackerConfig(ctx context.Context) string {
	const gameSlug = "cs2"
	var stateSchema string
	if resolver, ok := w.prompts.(activeStateSchemaResolver); ok {
		if item, err := resolver.GetActiveStateSchema(ctx, gameSlug); err == nil {
			schemaPayload := compactJSON(item.Fields)
			stateSchema = fmt.Sprintf("state_schema[%s v%d]: %s", item.Name, item.Version, schemaPayload)
		}
	}
	return stateSchema
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func (w *Worker) processScenarioPackage(ctx context.Context, runID, streamerID string, chunk ChunkRef, pkg prompts.ScenarioPackage) (streamers.LLMDecision, error) {
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	latest := w.latestDecisionByStreamer(ctx, streamerID)
	previousState := w.resolvePreviousState(ctx, streamerID)
	step, entering, err := pkg.ResolveStep(latest.Stage, previousState)
	if err != nil {
		if errors.Is(err, prompts.ErrScenarioStepNotFound) && strings.TrimSpace(latest.Stage) != "" {
			logger.Warn("current scenario step is missing in active package, restarting from initial step",
				zap.String("streamerID", streamerID),
				zap.String("missingStepID", latest.Stage),
				zap.String("scenarioPackageID", pkg.ID),
			)
			step, entering, err = pkg.ResolveStep("", previousState)
		}
		if err != nil {
			return streamers.LLMDecision{}, err
		}
	}
	activePrompt := prompts.PromptVersion{
		ID:       step.ID,
		Stage:    step.ID,
		Template: step.PromptTemplate,
		Model:    "scenario-graph",
		IsActive: true,
	}
	stateSchema := w.resolveTrackerConfig(ctx)
	result, err := w.classifyWithRetry(ctx, StageRequest{
		StreamerID:      streamerID,
		Stage:           step.ID,
		Chunk:           chunk,
		Prompt:          activePrompt,
		PreviousState:   previousState,
		StateSchema:     stateSchema,
		ResponseSchema:  step.ResponseSchemaJSON,
		SendPrompt:      entering,
		ScenarioFolder:  step.Folder,
		ScenarioPackage: pkg.ID,
	}, activePrompt)
	if err != nil {
		return streamers.LLMDecision{}, err
	}
	decision, err := w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, previousState)
	if err != nil {
		return streamers.LLMDecision{}, err
	}
	decision.TransitionToStep = step.ID
	return decision, nil
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
