package users

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// InMemoryRepository stores users in memory for development and tests.
type InMemoryRepository struct {
	mu           sync.RWMutex
	byTelegramID map[int64]Profile
	byID         map[string]int64
}

// NewInMemoryRepository constructs an empty in-memory repository.
func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		byTelegramID: make(map[int64]Profile),
		byID:         make(map[string]int64),
	}
}

// GetByID returns a profile by internal ID.
func (r *InMemoryRepository) GetByID(_ context.Context, id string) (Profile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	telegramID, ok := r.byID[id]
	if !ok {
		return Profile{}, ErrNotFound
	}
	profile, ok := r.byTelegramID[telegramID]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return profile, nil
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

// List returns paginated users matching query across username/name fields.
func (r *InMemoryRepository) List(_ context.Context, query string, page, pageSize int) ([]Profile, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	normalized := strings.ToLower(strings.TrimSpace(query))
	matched := make([]Profile, 0, len(r.byTelegramID))
	for _, profile := range r.byTelegramID {
		if normalized == "" || profileMatches(profile, normalized) {
			matched = append(matched, profile)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	start := (page - 1) * pageSize
	if start >= total {
		return []Profile{}, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]Profile, end-start)
	copy(out, matched[start:end])
	return out, total, nil
}

func profileMatches(profile Profile, query string) bool {
	values := []string{
		profile.ID,
		profile.Username,
		profile.FirstName,
		profile.LastName,
		profile.LanguageCode,
		profile.ReferralCode,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

// Create stores a new profile.
func (r *InMemoryRepository) Create(_ context.Context, profile Profile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byTelegramID[profile.TelegramID]; exists {
		return nil
	}
	r.byTelegramID[profile.TelegramID] = profile
	r.byID[profile.ID] = profile.TelegramID
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
	r.byID[profile.ID] = profile.TelegramID
	return nil
}

// DeleteByID removes a profile by internal ID.
func (r *InMemoryRepository) DeleteByID(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	telegramID, ok := r.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(r.byID, id)
	delete(r.byTelegramID, telegramID)
	return nil
}
