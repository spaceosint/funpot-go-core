package events

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidEvent  = errors.New("event payload is invalid")
	ErrEventNotFound = errors.New("event not found")
	ErrEventClosed   = errors.New("event is closed")
	ErrInvalidVote   = errors.New("vote payload is invalid")
	ErrAlreadyActive = errors.New("active event already exists for template")
)

type voteRecord struct {
	OptionID string
	Amount   int64
}

type liveEventState struct {
	event          LiveEvent
	processedVotes map[string]voteRecord
	userVotes      map[string]voteRecord
}

type Service struct {
	mu                 sync.RWMutex
	items              map[string]*liveEventState
	votePlatformFeeBPS int64
}

type Settings struct {
	VotePlatformFeePercent float64 `json:"votePlatformFeePercent"`
}

func NewService(seed []LiveEvent) *Service {
	items := make(map[string]*liveEventState, len(seed))
	for _, item := range seed {
		copyItem := item
		if copyItem.Totals == nil {
			copyItem.Totals = map[string]int64{}
		}
		items[copyItem.ID] = &liveEventState{
			event:          copyItem,
			processedVotes: map[string]voteRecord{},
			userVotes:      map[string]voteRecord{},
		}
	}
	return &Service{items: items}
}

func (s *Service) CreateLiveEvent(_ context.Context, req CreateLiveEventRequest) (LiveEvent, error) {
	if strings.TrimSpace(req.StreamerID) == "" || strings.TrimSpace(req.ScenarioID) == "" || strings.TrimSpace(req.TerminalID) == "" {
		return LiveEvent{}, ErrInvalidEvent
	}
	if strings.TrimSpace(req.DefaultLanguage) == "" || strings.TrimSpace(req.Title[req.DefaultLanguage]) == "" {
		return LiveEvent{}, ErrInvalidEvent
	}
	if len(req.Options) == 0 {
		return LiveEvent{}, ErrInvalidEvent
	}
	now := time.Now().UTC()
	if req.Duration <= 0 {
		req.Duration = 5 * time.Minute
	}
	templateID := strings.TrimSpace(req.StreamerID) + ":" + strings.TrimSpace(req.TerminalID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.event.StreamerID != strings.TrimSpace(req.StreamerID) || item.event.TemplateID != templateID {
			continue
		}
		if isOpen(item.event, now) {
			return item.event, ErrAlreadyActive
		}
	}
	event := LiveEvent{
		ID:              uuid.NewString(),
		TemplateID:      templateID,
		StreamerID:      strings.TrimSpace(req.StreamerID),
		ScenarioID:      strings.TrimSpace(req.ScenarioID),
		TransitionID:    strings.TrimSpace(req.TransitionID),
		TerminalID:      strings.TrimSpace(req.TerminalID),
		Title:           req.Title,
		DefaultLanguage: strings.TrimSpace(req.DefaultLanguage),
		Options:         append([]Option(nil), req.Options...),
		CreatedAt:       now.Format(time.RFC3339Nano),
		ClosesAt:        now.Add(req.Duration).Format(time.RFC3339Nano),
		Status:          "open",
		Totals:          map[string]int64{},
	}
	for _, option := range event.Options {
		event.Totals[strings.TrimSpace(option.ID)] = 0
	}
	state := &liveEventState{
		event:          event,
		processedVotes: map[string]voteRecord{},
		userVotes:      map[string]voteRecord{},
	}
	s.items[event.ID] = state
	return event, nil
}

func (s *Service) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Settings{
		VotePlatformFeePercent: float64(s.votePlatformFeeBPS) / 100.0,
	}
}

func (s *Service) UpdateSettings(settings Settings) (Settings, error) {
	if settings.VotePlatformFeePercent < 0 || settings.VotePlatformFeePercent > 100 {
		return Settings{}, ErrInvalidVote
	}
	feeBPS := int64(math.Round(settings.VotePlatformFeePercent * 100))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.votePlatformFeeBPS = feeBPS
	return Settings{VotePlatformFeePercent: float64(s.votePlatformFeeBPS) / 100.0}, nil
}

func (s *Service) ListLiveByStreamer(_ context.Context, streamerID string) []LiveEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	result := make([]LiveEvent, 0)
	for _, item := range s.items {
		if item.event.StreamerID != streamerID {
			continue
		}
		event := item.event
		if !isOpen(event, now) {
			event.Status = "closed"
			continue
		}
		event.Status = "open"
		result = append(result, event)
	}
	return result
}

func (s *Service) Vote(_ context.Context, req VoteRequest) (LiveEvent, error) {
	if strings.TrimSpace(req.EventID) == "" || strings.TrimSpace(req.StreamerID) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.OptionID) == "" || req.Amount <= 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		return LiveEvent{}, ErrInvalidVote
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[strings.TrimSpace(req.EventID)]
	if !ok || item.event.StreamerID != strings.TrimSpace(req.StreamerID) {
		return LiveEvent{}, ErrEventNotFound
	}
	now := time.Now().UTC()
	if !isOpen(item.event, now) {
		item.event.Status = "closed"
		return LiveEvent{}, ErrEventClosed
	}
	if existing, ok := item.processedVotes[req.IdempotencyKey]; ok {
		_ = existing
		event := item.event
		if user, ok := item.userVotes[strings.TrimSpace(req.UserID)]; ok {
			event.UserVote = &UserVote{OptionID: user.OptionID, TotalAmount: user.Amount}
		}
		return event, nil
	}
	optionID := strings.TrimSpace(req.OptionID)
	if _, ok := item.event.Totals[optionID]; !ok {
		return LiveEvent{}, ErrInvalidVote
	}
	fee := calculateFee(req.Amount, s.votePlatformFeeBPS)
	netAmount := req.Amount - fee
	item.event.Totals[optionID] += netAmount
	item.event.TotalContributed += req.Amount
	item.event.PlatformFeeINT += fee
	item.event.DistributableINT = item.event.TotalContributed - item.event.PlatformFeeINT
	userVote := item.userVotes[strings.TrimSpace(req.UserID)]
	if userVote.OptionID == "" {
		userVote.OptionID = optionID
	}
	if userVote.OptionID != optionID {
		userVote.OptionID = optionID
	}
	userVote.Amount += req.Amount
	item.userVotes[strings.TrimSpace(req.UserID)] = userVote
	item.processedVotes[strings.TrimSpace(req.IdempotencyKey)] = voteRecord{OptionID: optionID, Amount: req.Amount}
	event := item.event
	event.UserVote = &UserVote{OptionID: userVote.OptionID, TotalAmount: userVote.Amount}
	return event, nil
}

func calculateFee(amount int64, feeBPS int64) int64 {
	if amount <= 0 || feeBPS <= 0 {
		return 0
	}
	if feeBPS >= 10000 {
		return amount
	}
	return (amount * feeBPS) / 10000
}

// CalculateAccrualINT calculates user's accrual from the distributable event pool.
// Formula: (totalContributed - platformFee) * userContribution / totalContributionForWinningOption.
func CalculateAccrualINT(totalContributed, platformFeeINT, totalContributionForWinningOption, userContribution int64) int64 {
	if totalContributed <= 0 || userContribution <= 0 || totalContributionForWinningOption <= 0 {
		return 0
	}
	distributable := totalContributed - platformFeeINT
	if distributable <= 0 {
		return 0
	}
	return (distributable * userContribution) / totalContributionForWinningOption
}

func isOpen(event LiveEvent, now time.Time) bool {
	closesAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.ClosesAt))
	if err != nil {
		return false
	}
	return now.Before(closesAt)
}
