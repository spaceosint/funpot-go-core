package streamers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	ErrInvalidUsername   = errors.New("twitchUsername is required")
	ErrInvalidStatus     = errors.New("status filter is invalid")
	ErrRateLimited       = errors.New("submission rate limit exceeded")
	ErrTwitchUnavailable = errors.New("failed to validate twitch username")
	ErrNotFound          = errors.New("streamer not found")
)

var twitchUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{4,25}$`)

type TwitchValidator interface {
	ValidateUsername(ctx context.Context, username string) (displayName string, err error)
}

type noopTwitchValidator struct{}

func (v noopTwitchValidator) ValidateUsername(_ context.Context, username string) (string, error) {
	if !twitchUsernamePattern.MatchString(username) {
		return "", ErrTwitchUnavailable
	}
	return strings.ToLower(username), nil
}

type submissionLimit struct {
	count      int
	windowEnds time.Time
}

type Service struct {
	logger         *zap.Logger
	mu             sync.RWMutex
	items          []Streamer
	decisions      map[string][]LLMDecision
	validator      TwitchValidator
	rateLimitMu    sync.Mutex
	rateLimitByKey map[string]submissionLimit
	nowFn          func() time.Time
	counterMu      sync.Mutex
	counter        int64
	onSubmittedMu  sync.RWMutex
	onSubmitted    func(context.Context, string) error
}

func NewService() *Service {
	return NewServiceWithValidator(noopTwitchValidator{})
}

func NewServiceWithValidator(validator TwitchValidator) *Service {
	if validator == nil {
		validator = noopTwitchValidator{}
	}
	return &Service{
		logger:         zap.NewNop(),
		items:          []Streamer{},
		decisions:      make(map[string][]LLMDecision),
		validator:      validator,
		rateLimitByKey: make(map[string]submissionLimit),
		nowFn: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (s *Service) SetLogger(logger *zap.Logger) {
	if s == nil {
		return
	}
	if logger == nil {
		s.logger = zap.NewNop()
		return
	}
	s.logger = logger
}

func (s *Service) SetSubmissionHook(hook func(context.Context, string) error) {
	s.onSubmittedMu.Lock()
	s.onSubmitted = hook
	s.onSubmittedMu.Unlock()
}

func (s *Service) submissionHook() func(context.Context, string) error {
	s.onSubmittedMu.RLock()
	defer s.onSubmittedMu.RUnlock()
	return s.onSubmitted
}

func (s *Service) List(_ context.Context, query, status string, page int) []Streamer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if page < 1 {
		page = 1
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	statusFilter := strings.ToLower(strings.TrimSpace(status))
	matches := make([]Streamer, 0, len(s.items))
	for _, item := range s.items {
		if needle != "" && !strings.Contains(strings.ToLower(item.Username), needle) && !strings.Contains(strings.ToLower(item.DisplayName), needle) {
			continue
		}
		if statusFilter != "" && strings.ToLower(item.Status) != statusFilter {
			continue
		}
		matches = append(matches, item)
	}

	const pageSize = 20
	start := (page - 1) * pageSize
	if start >= len(matches) {
		return []Streamer{}
	}
	end := start + pageSize
	if end > len(matches) {
		end = len(matches)
	}
	result := make([]Streamer, end-start)
	copy(result, matches[start:end])
	return result
}

func (s *Service) ResolveStreamlinkChannel(_ context.Context, streamerID string) (string, error) {
	id := strings.TrimSpace(streamerID)
	if id == "" {
		return "", ErrNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.ID == id {
			if username := strings.TrimSpace(item.Username); username != "" {
				return username, nil
			}
			break
		}
	}

	return "", ErrNotFound
}

func (s *Service) Submit(ctx context.Context, twitchUsername, addedBy string) (Submission, error) {
	username := strings.TrimSpace(twitchUsername)
	logger := s.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Info("streamer submission received",
		zap.String("twitchUsername", username),
		zap.String("addedBy", strings.TrimSpace(addedBy)),
	)
	if username == "" {
		logger.Warn("streamer submission rejected: missing username", zap.String("addedBy", strings.TrimSpace(addedBy)))
		return Submission{}, ErrInvalidUsername
	}
	if !IsSupportedStatus("pending") {
		return Submission{}, ErrInvalidStatus
	}

	if !s.allowSubmission(addedBy) {
		logger.Warn("streamer submission rate limited", zap.String("twitchUsername", username), zap.String("addedBy", strings.TrimSpace(addedBy)))
		return Submission{}, ErrRateLimited
	}

	displayName, err := s.validator.ValidateUsername(ctx, username)
	if err != nil {
		logger.Warn("streamer submission validation failed", zap.String("twitchUsername", username), zap.Error(err))
		return Submission{}, fmt.Errorf("%w: %v", ErrTwitchUnavailable, err)
	}
	logger.Info("streamer submission validated", zap.String("twitchUsername", username), zap.String("displayName", displayName))

	now := s.nowFn().UnixNano()
	id := fmt.Sprintf("str_%d", now)
	streamer := Streamer{
		ID:          id,
		Platform:    "twitch",
		Username:    strings.ToLower(username),
		DisplayName: displayName,
		Online:      false,
		Viewers:     0,
		AddedBy:     addedBy,
		Status:      "pending",
	}

	s.mu.Lock()
	s.items = append(s.items, streamer)
	s.mu.Unlock()

	logger.Info("streamer stored and awaiting worker scheduling", zap.String("streamerID", id), zap.String("status", streamer.Status))

	if hook := s.submissionHook(); hook != nil {
		logger.Info("starting streamer submission hook", zap.String("streamerID", id))
		if err := hook(ctx, id); err != nil {
			logger.Error("streamer submission hook failed", zap.String("streamerID", id), zap.Error(err))
			s.mu.Lock()
			if n := len(s.items); n > 0 && s.items[n-1].ID == id {
				s.items = s.items[:n-1]
			}
			s.mu.Unlock()
			return Submission{}, err
		}
		logger.Info("streamer submission hook completed", zap.String("streamerID", id))
	}

	logger.Info("streamer submission completed", zap.String("streamerID", id), zap.String("status", "pending"))
	return Submission{ID: id, Status: "pending", Reason: nil}, nil
}

func (s *Service) RecordLLMDecision(_ context.Context, req RecordDecisionRequest) (LLMDecision, error) {
	streamerID := strings.TrimSpace(req.StreamerID)
	if streamerID == "" {
		return LLMDecision{}, errors.New("streamerId is required")
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		return LLMDecision{}, errors.New("runId is required")
	}
	stage := strings.ToLower(strings.TrimSpace(req.Stage))
	if stage == "" {
		return LLMDecision{}, errors.New("stage is required")
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		return LLMDecision{}, errors.New("label is required")
	}
	if req.Confidence < 0 || req.Confidence > 1 {
		return LLMDecision{}, errors.New("confidence must be between 0 and 1")
	}

	s.counterMu.Lock()
	s.counter++
	id := fmt.Sprintf("llm_%d", s.counter)
	s.counterMu.Unlock()

	item := LLMDecision{
		ID:              id,
		RunID:           runID,
		StreamerID:      streamerID,
		Stage:           stage,
		Label:           label,
		Confidence:      req.Confidence,
		PromptVersionID: strings.TrimSpace(req.PromptVersionID),
		PromptText:      strings.TrimSpace(req.PromptText),
		Model:           strings.TrimSpace(req.Model),
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		TimeoutMS:       req.TimeoutMS,
		ChunkRef:        strings.TrimSpace(req.ChunkRef),
		RawResponse:     strings.TrimSpace(req.RawResponse),
		TokensIn:        req.TokensIn,
		TokensOut:       req.TokensOut,
		LatencyMS:       req.LatencyMS,
		CreatedAt:       s.nowFn().UTC().Format(time.RFC3339Nano),
	}

	s.mu.Lock()
	s.decisions[streamerID] = append(s.decisions[streamerID], item)
	s.mu.Unlock()

	return item, nil
}

func (s *Service) ListLLMDecisions(_ context.Context, streamerID string, limit int) []LLMDecision {
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return []LLMDecision{}
	}
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.decisions[key]
	if len(items) == 0 {
		return []LLMDecision{}
	}
	if limit > len(items) {
		limit = len(items)
	}

	start := len(items) - limit
	out := make([]LLMDecision, 0, limit)
	for i := len(items) - 1; i >= start; i-- {
		out = append(out, items[i])
	}
	return out
}

func (s *Service) GetLLMStatus(_ context.Context, streamerID string) LLMStatus {
	key := strings.TrimSpace(streamerID)
	status := LLMStatus{StreamerID: key, State: "idle", LatestByStage: []LLMDecision{}}
	if key == "" {
		return status
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.decisions[key]
	if len(items) == 0 {
		return status
	}

	latest := items[len(items)-1]
	status.CurrentRunID = latest.RunID
	status.CurrentStage = latest.Stage
	status.CurrentLabel = latest.Label
	status.CurrentConfidence = latest.Confidence
	status.UpdatedAt = latest.CreatedAt
	status.State = "active"
	status.DetectedGameKey = inferDetectedGameKey(items)

	orderedStages := make([]string, 0)
	seenStages := make(map[string]struct{})
	latestByStage := make(map[string]LLMDecision)
	for _, item := range items {
		if _, exists := seenStages[item.Stage]; !exists {
			orderedStages = append(orderedStages, item.Stage)
			seenStages[item.Stage] = struct{}{}
		}
		latestByStage[item.Stage] = item
	}

	ordered := make([]LLMDecision, 0, len(latestByStage))
	for _, stage := range orderedStages {
		ordered = append(ordered, latestByStage[stage])
	}
	status.LatestByStage = ordered
	return status
}

func inferDetectedGameKey(items []LLMDecision) string {
	for i := len(items) - 1; i >= 0; i-- {
		switch strings.ToLower(strings.TrimSpace(items[i].Label)) {
		case "cs_detected":
			return "counter_strike"
		case "dota_detected":
			return "dota_2"
		case "valorant_detected":
			return "valorant"
		}
	}
	return ""
}

func IsSupportedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "approved", "rejected":
		return true
	default:
		return false
	}
}

func (s *Service) allowSubmission(userID string) bool {
	const maxSubmissionsPerMinute = 3
	const window = time.Minute

	key := strings.TrimSpace(userID)
	if key == "" {
		key = "anonymous"
	}

	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	now := s.nowFn()
	state := s.rateLimitByKey[key]
	if state.windowEnds.IsZero() || now.After(state.windowEnds) {
		state = submissionLimit{count: 0, windowEnds: now.Add(window)}
	}
	if state.count >= maxSubmissionsPerMinute {
		s.rateLimitByKey[key] = state
		return false
	}
	state.count++
	s.rateLimitByKey[key] = state
	return true
}
