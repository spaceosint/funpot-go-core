package streamers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidUsername   = errors.New("twitchUsername is required")
	ErrInvalidStatus     = errors.New("status filter is invalid")
	ErrRateLimited       = errors.New("submission rate limit exceeded")
	ErrTwitchUnavailable = errors.New("failed to validate twitch username")
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
	mu             sync.RWMutex
	items          []Streamer
	validator      TwitchValidator
	rateLimitMu    sync.Mutex
	rateLimitByKey map[string]submissionLimit
	nowFn          func() time.Time
}

func NewService() *Service {
	return NewServiceWithValidator(noopTwitchValidator{})
}

func NewServiceWithValidator(validator TwitchValidator) *Service {
	if validator == nil {
		validator = noopTwitchValidator{}
	}
	return &Service{
		items:          []Streamer{},
		validator:      validator,
		rateLimitByKey: make(map[string]submissionLimit),
		nowFn: func() time.Time {
			return time.Now().UTC()
		},
	}
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

func (s *Service) Submit(ctx context.Context, twitchUsername, addedBy string) (Submission, error) {
	username := strings.TrimSpace(twitchUsername)
	if username == "" {
		return Submission{}, ErrInvalidUsername
	}
	if !IsSupportedStatus("pending") {
		return Submission{}, ErrInvalidStatus
	}

	if !s.allowSubmission(addedBy) {
		return Submission{}, ErrRateLimited
	}

	displayName, err := s.validator.ValidateUsername(ctx, username)
	if err != nil {
		return Submission{}, fmt.Errorf("%w: %v", ErrTwitchUnavailable, err)
	}

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

	return Submission{ID: id, Status: "pending", Reason: nil}, nil
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
