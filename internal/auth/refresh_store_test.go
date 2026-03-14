package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRefreshStore(t *testing.T) (*RedisRefreshSessionStore, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("run miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, err := NewRedisRefreshSessionStore(client, RefreshStoreConfig{KeyPrefix: "test", MaxSessionsPerUser: 2})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}
	return store, cleanup
}

func TestRedisRefreshSessionStoreCreateAndLimit(t *testing.T) {
	store, cleanup := newTestRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	cases := []RefreshSession{
		{SessionID: "s1", UserID: "u1", TokenHash: "h1", ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now},
		{SessionID: "s2", UserID: "u1", TokenHash: "h2", ExpiresAt: now.Add(6 * time.Minute), CreatedAt: now.Add(1 * time.Second)},
		{SessionID: "s3", UserID: "u1", TokenHash: "h3", ExpiresAt: now.Add(7 * time.Minute), CreatedAt: now.Add(2 * time.Second)},
	}
	for _, session := range cases {
		if err := store.Create(ctx, session); err != nil {
			t.Fatalf("Create(%s) error: %v", session.SessionID, err)
		}
	}

	if _, err := store.Get(ctx, "s1"); !errors.Is(err, ErrRefreshSessionNotFound) {
		t.Fatalf("expected oldest session to be evicted, got: %v", err)
	}
	if _, err := store.Get(ctx, "s2"); err != nil {
		t.Fatalf("expected s2 to exist: %v", err)
	}
	if _, err := store.Get(ctx, "s3"); err != nil {
		t.Fatalf("expected s3 to exist: %v", err)
	}
}

func TestRedisRefreshSessionStoreRotate(t *testing.T) {
	store, cleanup := newTestRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Create(ctx, RefreshSession{SessionID: "s1", UserID: "u1", TokenHash: "old", ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	err := store.Rotate(ctx, "s1", "old", "new", now.Add(10*time.Minute), now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Rotate error: %v", err)
	}

	session, err := store.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if session.TokenHash != "new" {
		t.Fatalf("expected token hash new, got %s", session.TokenHash)
	}

	err = store.Rotate(ctx, "s1", "old", "newer", now.Add(11*time.Minute), now.Add(time.Minute))
	if !errors.Is(err, ErrRefreshTokenMismatch) {
		t.Fatalf("expected ErrRefreshTokenMismatch, got %v", err)
	}
}

func TestRedisRefreshSessionStoreRevoke(t *testing.T) {
	store, cleanup := newTestRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	sessions := []RefreshSession{
		{SessionID: "s1", UserID: "u1", TokenHash: "h1", ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now},
		{SessionID: "s2", UserID: "u1", TokenHash: "h2", ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now},
	}
	for _, session := range sessions {
		if err := store.Create(ctx, session); err != nil {
			t.Fatalf("Create(%s) error: %v", session.SessionID, err)
		}
	}

	revokedAt := now.Add(time.Minute)
	if err := store.RevokeSession(ctx, "s1", revokedAt); err != nil {
		t.Fatalf("RevokeSession error: %v", err)
	}
	one, err := store.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get s1 error: %v", err)
	}
	if one.RevokedAt == nil {
		t.Fatalf("expected revoked_at for s1")
	}

	if err := store.RevokeUserSessions(ctx, "u1", revokedAt.Add(time.Minute)); err != nil {
		t.Fatalf("RevokeUserSessions error: %v", err)
	}
	two, err := store.Get(ctx, "s2")
	if err != nil {
		t.Fatalf("Get s2 error: %v", err)
	}
	if two.RevokedAt == nil {
		t.Fatalf("expected revoked_at for s2")
	}
}

func TestHashRefreshTokenDeterministic(t *testing.T) {
	first := HashRefreshToken("same-token")
	second := HashRefreshToken("same-token")
	if first != second {
		t.Fatalf("expected deterministic hash")
	}
	if first == HashRefreshToken("other-token") {
		t.Fatalf("expected different hash for different token")
	}
}
