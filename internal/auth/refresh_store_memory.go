package auth

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// InMemoryRefreshSessionStore is a thread-safe test-friendly refresh session store.
type InMemoryRefreshSessionStore struct {
	mu                 sync.RWMutex
	sessions           map[string]RefreshSession
	maxSessionsPerUser int
}

func NewInMemoryRefreshSessionStore(maxSessionsPerUser int) *InMemoryRefreshSessionStore {
	if maxSessionsPerUser < 1 {
		maxSessionsPerUser = 5
	}
	return &InMemoryRefreshSessionStore{sessions: make(map[string]RefreshSession), maxSessionsPerUser: maxSessionsPerUser}
}

func (s *InMemoryRefreshSessionStore) Create(_ context.Context, session RefreshSession) error {
	if session.SessionID == "" || session.UserID == "" || session.TokenHash == "" {
		return errors.New("session id, user id and token hash are required")
	}
	if session.ExpiresAt.IsZero() || !session.ExpiresAt.After(time.Now().UTC()) {
		return errors.New("session expiration must be in the future")
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.LastRotated.IsZero() {
		session.LastRotated = session.CreatedAt
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupExpiredLocked(time.Now().UTC())

	userSessions := s.userSessionsLocked(session.UserID)
	if len(userSessions) >= s.maxSessionsPerUser {
		sort.Slice(userSessions, func(i, j int) bool {
			return userSessions[i].ExpiresAt.Before(userSessions[j].ExpiresAt)
		})
		delete(s.sessions, userSessions[0].SessionID)
	}

	s.sessions[session.SessionID] = session
	return nil
}

func (s *InMemoryRefreshSessionStore) Get(_ context.Context, sessionID string) (RefreshSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return RefreshSession{}, ErrRefreshSessionNotFound
	}
	if session.ExpiresAt.Before(time.Now().UTC()) {
		return RefreshSession{}, ErrRefreshSessionNotFound
	}
	return session, nil
}

func (s *InMemoryRefreshSessionStore) Rotate(_ context.Context, sessionID, currentTokenHash, nextTokenHash string, nextExpiresAt, rotatedAt time.Time) error {
	if nextTokenHash == "" {
		return errors.New("next token hash is required")
	}
	if nextExpiresAt.Before(rotatedAt) {
		return errors.New("next expiration must be in the future")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return ErrRefreshSessionNotFound
	}
	if session.RevokedAt != nil {
		return ErrRefreshSessionRevoked
	}
	if session.TokenHash != currentTokenHash {
		return ErrRefreshTokenMismatch
	}
	session.TokenHash = nextTokenHash
	session.ExpiresAt = nextExpiresAt.UTC()
	session.LastRotated = rotatedAt.UTC()
	s.sessions[sessionID] = session
	return nil
}

func (s *InMemoryRefreshSessionStore) RevokeSession(_ context.Context, sessionID string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return ErrRefreshSessionNotFound
	}
	revoked := revokedAt.UTC()
	session.RevokedAt = &revoked
	s.sessions[sessionID] = session
	return nil
}

func (s *InMemoryRefreshSessionStore) RevokeUserSessions(_ context.Context, userID string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	revoked := revokedAt.UTC()
	for id, session := range s.sessions {
		if session.UserID != userID {
			continue
		}
		session.RevokedAt = &revoked
		s.sessions[id] = session
	}
	return nil
}

func (s *InMemoryRefreshSessionStore) cleanupExpiredLocked(now time.Time) {
	for id, session := range s.sessions {
		if session.ExpiresAt.Before(now) {
			delete(s.sessions, id)
		}
	}
}

func (s *InMemoryRefreshSessionStore) userSessionsLocked(userID string) []RefreshSession {
	out := make([]RefreshSession, 0)
	for _, session := range s.sessions {
		if session.UserID == userID {
			out = append(out, session)
		}
	}
	return out
}
