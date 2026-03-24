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
	StreamerID    string
	Stage         string
	Chunk         ChunkRef
	Prompt        prompts.PromptVersion
	PreviousState string
	StateSchema   string
	RuleSet       string
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
			decision, closeErr := w.processCloseCurrentSession(ctx, id)
			if closeErr != nil {
				logger.Error("close_current_session processing failed", zap.String("streamerID", id), zap.Error(closeErr))
				w.metrics.recordFailure(ctx, id, trackerStageClose)
				w.metrics.recordCycle(ctx, id, "failed")
				return streamers.LLMDecision{}, closeErr
			}
			w.metrics.recordCycle(ctx, id, "stream_unavailable")
			return decision, nil
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

func (w *Worker) processCloseCurrentSession(ctx context.Context, streamerID string) (streamers.LLMDecision, error) {
	activePrompts := filterTrackerPrompts(w.prompts.ListActive(ctx))
	var closePrompt prompts.PromptVersion
	for _, item := range activePrompts {
		if strings.EqualFold(strings.TrimSpace(item.Stage), trackerStageClose) {
			closePrompt = item
			break
		}
	}
	if strings.TrimSpace(closePrompt.Stage) == "" {
		return streamers.LLMDecision{}, nil
	}
	runID, err := w.runs.CreateRun(ctx, streamerID)
	if err != nil {
		return streamers.LLMDecision{}, err
	}
	return w.processStage(ctx, runID, streamerID, ChunkRef{CapturedAt: time.Now().UTC()}, closePrompt)
}

func (w *Worker) processExecutionPlan(ctx context.Context, runID, streamerID string, chunk ChunkRef) (streamers.LLMDecision, error) {
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}

	activePrompts := filterTrackerPrompts(w.prompts.ListActive(ctx))
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

func filterTrackerPrompts(items []prompts.PromptVersion) []prompts.PromptVersion {
	if len(items) == 0 {
		return nil
	}
	trackerOnly := make([]prompts.PromptVersion, 0, len(items))
	for _, item := range items {
		if isTrackerStage(item.Stage) {
			trackerOnly = append(trackerOnly, item)
		}
	}
	if len(trackerOnly) > 0 {
		return trackerOnly
	}
	return items
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
	stateSchema, ruleSet := w.resolveTrackerConfig(ctx)
	result, err := w.classifyWithRetry(ctx, StageRequest{StreamerID: streamerID, Stage: activePrompt.Stage, Chunk: chunk, Prompt: activePrompt, PreviousState: previousState, StateSchema: stateSchema, RuleSet: ruleSet}, activePrompt)
	if err != nil {
		w.metrics.recordFailure(ctx, streamerID, activePrompt.Stage)
		return streamers.LLMDecision{}, err
	}
	return w.processStageResult(ctx, activePrompt, result, chunk, runID, streamerID, previousState)
}

func (w *Worker) processStageResult(ctx context.Context, activePrompt prompts.PromptVersion, result StageClassification, chunk ChunkRef, runID, streamerID, previousState string) (streamers.LLMDecision, error) {
	updatedStateJSON := normalizeStateSnapshot(previousState, result)
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
				return state
			}
		}
	}
	return ""
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

type activeStateSchemaResolver interface {
	GetActiveStateSchema(ctx context.Context, gameSlug string) (prompts.StateSchemaVersion, error)
}

type activeRuleSetResolver interface {
	GetActiveRuleSet(ctx context.Context, gameSlug string) (prompts.RuleSetVersion, error)
}

func (w *Worker) resolveTrackerConfig(ctx context.Context) (string, string) {
	const gameSlug = "cs2"
	var stateSchema string
	if resolver, ok := w.prompts.(activeStateSchemaResolver); ok {
		if item, err := resolver.GetActiveStateSchema(ctx, gameSlug); err == nil {
			stateSchema = fmt.Sprintf("state_schema[%s v%d]: %s", item.Name, item.Version, compactJSON(item.Fields))
		}
	}
	var ruleSet string
	if resolver, ok := w.prompts.(activeRuleSetResolver); ok {
		if item, err := resolver.GetActiveRuleSet(ctx, gameSlug); err == nil {
			ruleSet = fmt.Sprintf("rule_set[%s v%d]: rule_items=%s finalization_rules=%s", item.Name, item.Version, compactJSON(item.RuleItems), compactJSON(item.FinalizationRules))
		}
	}
	return stateSchema, ruleSet
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
