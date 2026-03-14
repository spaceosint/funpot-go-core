package games

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidID     = errors.New("invalid game id")
	ErrInvalidSlug   = errors.New("slug is required")
	ErrInvalidTitle  = errors.New("title is required")
	ErrInvalidStatus = errors.New("status is invalid")
	ErrDuplicateSlug = errors.New("slug is already used")
	ErrNotFound      = errors.New("game not found")
)

// Service manages admin CRUD operations for games.
type Service struct {
	mu    sync.RWMutex
	games map[string]Game
}

func NewService() *Service {
	return &Service{games: make(map[string]Game)}
}

func (s *Service) List(_ context.Context) []Game {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Game, 0, len(s.games))
	for _, game := range s.games {
		items = append(items, game)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}

func (s *Service) Create(_ context.Context, req UpsertRequest) (Game, error) {
	now := time.Now().UTC()
	prepared, err := sanitizeUpsert(req)
	if err != nil {
		return Game{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slugExistsLocked(prepared.Slug, "") {
		return Game{}, fmt.Errorf("%w: %s", ErrDuplicateSlug, prepared.Slug)
	}
	game := Game{
		ID:          uuid.NewString(),
		Slug:        prepared.Slug,
		Title:       prepared.Title,
		Description: prepared.Description,
		Rules:       prepared.Rules,
		Status:      prepared.Status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.games[game.ID] = game
	return game, nil
}

func (s *Service) Update(_ context.Context, id string, req UpsertRequest) (Game, error) {
	if strings.TrimSpace(id) == "" {
		return Game{}, ErrInvalidID
	}
	prepared, err := sanitizeUpsert(req)
	if err != nil {
		return Game{}, err
	}
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.games[id]
	if !ok {
		return Game{}, ErrNotFound
	}
	if s.slugExistsLocked(prepared.Slug, id) {
		return Game{}, fmt.Errorf("%w: %s", ErrDuplicateSlug, prepared.Slug)
	}
	current.Slug = prepared.Slug
	current.Title = prepared.Title
	current.Description = prepared.Description
	current.Rules = prepared.Rules
	current.Status = prepared.Status
	current.UpdatedAt = now
	s.games[id] = current
	return current, nil
}

func (s *Service) Delete(_ context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.games[id]; !ok {
		return ErrNotFound
	}
	delete(s.games, id)
	return nil
}

func sanitizeUpsert(req UpsertRequest) (UpsertRequest, error) {
	prepared := UpsertRequest{
		Slug:        strings.ToLower(strings.TrimSpace(req.Slug)),
		Title:       strings.TrimSpace(req.Title),
		Description: strings.TrimSpace(req.Description),
		Status:      strings.ToLower(strings.TrimSpace(req.Status)),
	}
	for _, rule := range req.Rules {
		if trimmed := strings.TrimSpace(rule); trimmed != "" {
			prepared.Rules = append(prepared.Rules, trimmed)
		}
	}
	if prepared.Slug == "" {
		return UpsertRequest{}, ErrInvalidSlug
	}
	if prepared.Title == "" {
		return UpsertRequest{}, ErrInvalidTitle
	}
	if !IsSupportedStatus(prepared.Status) {
		return UpsertRequest{}, ErrInvalidStatus
	}
	return prepared, nil
}

func (s *Service) slugExistsLocked(slug string, exceptID string) bool {
	for _, game := range s.games {
		if game.ID != exceptID && game.Slug == slug {
			return true
		}
	}
	return false
}
