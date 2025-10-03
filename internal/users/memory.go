package users

import (
	"context"
	"sync"
)

// InMemoryRepository stores users in memory for development and tests.
type InMemoryRepository struct {
	mu           sync.RWMutex
	byTelegramID map[int64]Profile
}

// NewInMemoryRepository constructs an empty in-memory repository.
func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		byTelegramID: make(map[int64]Profile),
	}
}

// GetByTelegramID returns a profile by Telegram identifier.
func (r *InMemoryRepository) GetByTelegramID(_ context.Context, telegramID int64) (Profile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	profile, ok := r.byTelegramID[telegramID]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return profile, nil
}

// Create stores a new profile.
func (r *InMemoryRepository) Create(_ context.Context, profile Profile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byTelegramID[profile.TelegramID]; exists {
		return nil
	}
	r.byTelegramID[profile.TelegramID] = profile
	return nil
}

// Update persists an existing profile.
func (r *InMemoryRepository) Update(_ context.Context, profile Profile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byTelegramID[profile.TelegramID]; !exists {
		return ErrNotFound
	}
	r.byTelegramID[profile.TelegramID] = profile
	return nil
}
