package streamers

import (
	"context"
	"strings"
	"sync"
)

// InMemoryDecisionRepository stores LLM decisions in process memory.
type InMemoryDecisionRepository struct {
	mu    sync.RWMutex
	items map[string][]LLMDecision
}

func NewInMemoryDecisionRepository() *InMemoryDecisionRepository {
	return &InMemoryDecisionRepository{items: make(map[string][]LLMDecision)}
}

func (r *InMemoryDecisionRepository) RecordLLMDecision(_ context.Context, item LLMDecision) error {
	key := strings.TrimSpace(item.StreamerID)
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[key] = append(r.items[key], item)
	return nil
}

func (r *InMemoryDecisionRepository) ListLLMDecisions(_ context.Context, streamerID string, limit int) ([]LLMDecision, error) {
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return []LLMDecision{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.items[key]
	if len(items) == 0 {
		return []LLMDecision{}, nil
	}
	if limit > len(items) {
		limit = len(items)
	}
	start := len(items) - limit
	out := make([]LLMDecision, 0, limit)
	for i := len(items) - 1; i >= start; i-- {
		out = append(out, items[i])
	}
	return out, nil
}

func (r *InMemoryDecisionRepository) ListAllLLMDecisions(_ context.Context, streamerID string) ([]LLMDecision, error) {
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return []LLMDecision{}, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.items[key]
	out := make([]LLMDecision, len(items))
	copy(out, items)
	return out, nil
}

func (r *InMemoryDecisionRepository) DeleteAllLLMDecisions(_ context.Context, streamerID string) (int, error) {
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	count := len(r.items[key])
	delete(r.items, key)
	return count, nil
}
