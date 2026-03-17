package prompts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service manages versioned LLM prompt templates for each stage.
type Service struct {
	mu       sync.RWMutex
	counter  int
	versions map[string][]PromptVersion
}

func NewService() *Service {
	return &Service{versions: map[string][]PromptVersion{}}
}

func (s *Service) List(_ context.Context) []PromptVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]PromptVersion, 0)
	for _, byStage := range s.versions {
		items = append(items, byStage...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Stage == items[j].Stage {
			return items[i].Version > items[j].Version
		}
		return items[i].Stage < items[j].Stage
	})
	return items
}

func (s *Service) GetActiveByStage(_ context.Context, stage string) (PromptVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stage = strings.TrimSpace(stage)
	versions := s.versions[stage]
	for _, item := range versions {
		if item.IsActive {
			return item, nil
		}
	}
	return PromptVersion{}, ErrNotFound
}

func (s *Service) Create(_ context.Context, req CreateRequest) (PromptVersion, error) {
	if err := ValidateCreateRequest(req); err != nil {
		return PromptVersion{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stage := strings.TrimSpace(req.Stage)
	nextVersion := len(s.versions[stage]) + 1
	s.counter++
	now := time.Now().UTC()
	item := PromptVersion{
		ID:            fmt.Sprintf("prompt-%d", s.counter),
		Stage:         stage,
		Version:       nextVersion,
		Template:      strings.TrimSpace(req.Template),
		Model:         strings.TrimSpace(req.Model),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		TimeoutMS:     req.TimeoutMS,
		RetryCount:    req.RetryCount,
		BackoffMS:     req.BackoffMS,
		CooldownMS:    req.CooldownMS,
		MinConfidence: req.MinConfidence,
		CreatedBy:     strings.TrimSpace(req.ActorID),
		CreatedAt:     now,
	}
	s.versions[stage] = append(s.versions[stage], item)
	return item, nil
}

func (s *Service) Activate(_ context.Context, id, actorID string) (PromptVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for stage, byStage := range s.versions {
		activeIndex := -1
		for i := range byStage {
			if byStage[i].ID == id {
				activeIndex = i
				break
			}
		}
		if activeIndex == -1 {
			continue
		}
		now := time.Now().UTC()
		for i := range byStage {
			byStage[i].IsActive = i == activeIndex
			if i == activeIndex {
				byStage[i].ActivatedAt = now
				byStage[i].ActivatedBy = strings.TrimSpace(actorID)
			}
		}
		s.versions[stage] = byStage
		return byStage[activeIndex], nil
	}

	return PromptVersion{}, ErrNotFound
}
