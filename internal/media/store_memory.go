package media

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type InMemoryRunStore struct {
	counter atomic.Int64
}

func (s *InMemoryRunStore) CreateRun(_ context.Context, streamerID string) (string, error) {
	id := s.counter.Add(1)
	return fmt.Sprintf("run_%s_%d", streamerID, id), nil
}

type InMemoryLocker struct {
	mu    sync.Mutex
	locks map[string]time.Time
	nowFn func() time.Time
}

func NewInMemoryLocker() *InMemoryLocker {
	return &InMemoryLocker{
		locks: make(map[string]time.Time),
		nowFn: func() time.Time { return time.Now().UTC() },
	}
}

func (l *InMemoryLocker) TryLock(key string, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.nowFn()
	expiresAt, ok := l.locks[key]
	if ok && now.Before(expiresAt) {
		return false
	}
	l.locks[key] = now.Add(ttl)
	return true
}

func (l *InMemoryLocker) Unlock(key string) {
	l.mu.Lock()
	delete(l.locks, key)
	l.mu.Unlock()
}
