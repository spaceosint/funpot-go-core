package events

import (
	"context"
	"sync"
)

type Service struct {
	mu    sync.RWMutex
	items []LiveEvent
}

func NewService(seed []LiveEvent) *Service {
	items := make([]LiveEvent, len(seed))
	copy(items, seed)
	return &Service{items: items}
}

func (s *Service) ListLiveByStreamer(_ context.Context, streamerID string) []LiveEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]LiveEvent, 0)
	for _, item := range s.items {
		if item.StreamerID == streamerID {
			result = append(result, item)
		}
	}
	return result
}
