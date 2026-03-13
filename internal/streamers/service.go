package streamers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrInvalidUsername = errors.New("twitchUsername is required")

type Service struct {
	mu    sync.RWMutex
	items []Streamer
}

func NewService() *Service {
	return &Service{items: []Streamer{}}
}

func (s *Service) List(_ context.Context, query string, page int) []Streamer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if page < 1 {
		page = 1
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	matches := make([]Streamer, 0, len(s.items))
	for _, item := range s.items {
		if needle == "" || strings.Contains(strings.ToLower(item.Username), needle) || strings.Contains(strings.ToLower(item.DisplayName), needle) {
			matches = append(matches, item)
		}
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

func (s *Service) Submit(_ context.Context, twitchUsername, addedBy string) (Submission, error) {
	username := strings.TrimSpace(twitchUsername)
	if username == "" {
		return Submission{}, ErrInvalidUsername
	}

	now := time.Now().UTC().UnixNano()
	id := fmt.Sprintf("str_%d", now)
	streamer := Streamer{
		ID:          id,
		Platform:    "twitch",
		Username:    username,
		DisplayName: username,
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
