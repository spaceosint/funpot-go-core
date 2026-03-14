package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRefreshSessionNotFound = errors.New("refresh session not found")
	ErrRefreshSessionRevoked  = errors.New("refresh session revoked")
	ErrRefreshTokenMismatch   = errors.New("refresh token mismatch")
)

// RefreshSession represents the stored state of a refresh session.
type RefreshSession struct {
	SessionID   string
	UserID      string
	TelegramID  int64
	TokenHash   string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	LastRotated time.Time
	RevokedAt   *time.Time
}

// RefreshStoreConfig controls Redis key namespaces and session limits.
type RefreshStoreConfig struct {
	KeyPrefix          string
	MaxSessionsPerUser int
}

// RefreshSessionStore persists refresh sessions and supports rotation/revocation.
type RefreshSessionStore interface {
	Create(ctx context.Context, session RefreshSession) error
	Get(ctx context.Context, sessionID string) (RefreshSession, error)
	Rotate(ctx context.Context, sessionID, currentTokenHash, nextTokenHash string, nextExpiresAt, rotatedAt time.Time) error
	RevokeSession(ctx context.Context, sessionID string, revokedAt time.Time) error
	RevokeUserSessions(ctx context.Context, userID string, revokedAt time.Time) error
}

// RedisRefreshSessionStore is a Redis-backed refresh session store.
type RedisRefreshSessionStore struct {
	client             redis.UniversalClient
	keyPrefix          string
	maxSessionsPerUser int
}

func NewRedisRefreshSessionStore(client redis.UniversalClient, cfg RefreshStoreConfig) (*RedisRefreshSessionStore, error) {
	if client == nil {
		return nil, errors.New("redis client is required")
	}
	if cfg.MaxSessionsPerUser < 1 {
		cfg.MaxSessionsPerUser = 5
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "funpot:auth"
	}
	return &RedisRefreshSessionStore{client: client, keyPrefix: cfg.KeyPrefix, maxSessionsPerUser: cfg.MaxSessionsPerUser}, nil
}

func (s *RedisRefreshSessionStore) sessionKey(sessionID string) string {
	return fmt.Sprintf("%s:refresh:session:%s", s.keyPrefix, sessionID)
}

func (s *RedisRefreshSessionStore) userSessionsKey(userID string) string {
	return fmt.Sprintf("%s:refresh:user:%s", s.keyPrefix, userID)
}

func (s *RedisRefreshSessionStore) Create(ctx context.Context, session RefreshSession) error {
	if session.SessionID == "" || session.UserID == "" || session.TokenHash == "" {
		return errors.New("session id, user id and token hash are required")
	}

	ttl := time.Until(session.ExpiresAt)
	if ttl <= 0 {
		return errors.New("session expiration must be in the future")
	}

	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.LastRotated.IsZero() {
		session.LastRotated = session.CreatedAt
	}

	if err := s.cleanupExpiredUserSessions(ctx, session.UserID); err != nil {
		return err
	}

	userKey := s.userSessionsKey(session.UserID)
	for {
		count, err := s.client.ZCard(ctx, userKey).Result()
		if err != nil {
			return err
		}
		if int(count) < s.maxSessionsPerUser {
			break
		}
		oldestIDs, err := s.client.ZRange(ctx, userKey, 0, 0).Result()
		if err != nil {
			return err
		}
		if len(oldestIDs) == 0 {
			break
		}
		if err := s.client.Del(ctx, s.sessionKey(oldestIDs[0])).Err(); err != nil {
			return err
		}
		if err := s.client.ZRem(ctx, userKey, oldestIDs[0]).Err(); err != nil {
			return err
		}
	}

	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, s.sessionKey(session.SessionID), map[string]any{
		"user_id":      session.UserID,
		"telegram_id":  session.TelegramID,
		"token_hash":   session.TokenHash,
		"expires_at":   session.ExpiresAt.UTC().Unix(),
		"created_at":   session.CreatedAt.UTC().Unix(),
		"last_rotated": session.LastRotated.UTC().Unix(),
	})
	pipe.Expire(ctx, s.sessionKey(session.SessionID), ttl)
	pipe.ZAdd(ctx, userKey, redis.Z{Score: float64(session.ExpiresAt.UTC().Unix()), Member: session.SessionID})
	pipe.Expire(ctx, userKey, ttl+24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisRefreshSessionStore) Get(ctx context.Context, sessionID string) (RefreshSession, error) {
	fields, err := s.client.HGetAll(ctx, s.sessionKey(sessionID)).Result()
	if err != nil {
		return RefreshSession{}, err
	}
	if len(fields) == 0 {
		return RefreshSession{}, ErrRefreshSessionNotFound
	}
	return parseRefreshSession(sessionID, fields)
}

func (s *RedisRefreshSessionStore) Rotate(ctx context.Context, sessionID, currentTokenHash, nextTokenHash string, nextExpiresAt, rotatedAt time.Time) error {
	if nextTokenHash == "" {
		return errors.New("next token hash is required")
	}
	if rotatedAt.IsZero() {
		rotatedAt = time.Now().UTC()
	}

	key := s.sessionKey(sessionID)
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		fields, err := tx.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			return ErrRefreshSessionNotFound
		}
		session, err := parseRefreshSession(sessionID, fields)
		if err != nil {
			return err
		}
		if session.RevokedAt != nil {
			return ErrRefreshSessionRevoked
		}
		if session.TokenHash != currentTokenHash {
			return ErrRefreshTokenMismatch
		}
		if nextExpiresAt.Before(rotatedAt) {
			return errors.New("next expiration must be in the future")
		}
		pipe := tx.TxPipeline()
		pipe.HSet(ctx, key, map[string]any{
			"token_hash":   nextTokenHash,
			"expires_at":   nextExpiresAt.UTC().Unix(),
			"last_rotated": rotatedAt.UTC().Unix(),
		})
		pipe.Expire(ctx, key, time.Until(nextExpiresAt))
		pipe.ZAdd(ctx, s.userSessionsKey(session.UserID), redis.Z{Score: float64(nextExpiresAt.UTC().Unix()), Member: sessionID})
		_, err = pipe.Exec(ctx)
		return err
	}, key)
	if err == redis.TxFailedErr {
		return errors.New("refresh session concurrent update")
	}
	return err
}

func (s *RedisRefreshSessionStore) RevokeSession(ctx context.Context, sessionID string, revokedAt time.Time) error {
	session, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if revokedAt.IsZero() {
		revokedAt = time.Now().UTC()
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, s.sessionKey(sessionID), "revoked_at", revokedAt.UTC().Unix())
	pipe.ZRem(ctx, s.userSessionsKey(session.UserID), sessionID)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisRefreshSessionStore) RevokeUserSessions(ctx context.Context, userID string, revokedAt time.Time) error {
	if revokedAt.IsZero() {
		revokedAt = time.Now().UTC()
	}
	key := s.userSessionsKey(userID)
	sessionIDs, err := s.client.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		return err
	}
	if len(sessionIDs) == 0 {
		return nil
	}
	pipe := s.client.TxPipeline()
	for _, sessionID := range sessionIDs {
		pipe.HSet(ctx, s.sessionKey(sessionID), "revoked_at", revokedAt.UTC().Unix())
		pipe.ZRem(ctx, key, sessionID)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisRefreshSessionStore) cleanupExpiredUserSessions(ctx context.Context, userID string) error {
	key := s.userSessionsKey(userID)
	now := time.Now().UTC().Unix()
	expiredIDs, err := s.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(now-1, 10)}).Result()
	if err != nil {
		return err
	}
	if len(expiredIDs) == 0 {
		return nil
	}
	pipe := s.client.TxPipeline()
	for _, sessionID := range expiredIDs {
		pipe.Del(ctx, s.sessionKey(sessionID))
		pipe.ZRem(ctx, key, sessionID)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func parseRefreshSession(sessionID string, fields map[string]string) (RefreshSession, error) {
	expiresAtUnix, err := strconv.ParseInt(fields["expires_at"], 10, 64)
	if err != nil {
		return RefreshSession{}, fmt.Errorf("parse expires_at: %w", err)
	}
	createdAtUnix, err := strconv.ParseInt(fields["created_at"], 10, 64)
	if err != nil {
		return RefreshSession{}, fmt.Errorf("parse created_at: %w", err)
	}
	lastRotatedUnix, err := strconv.ParseInt(fields["last_rotated"], 10, 64)
	if err != nil {
		return RefreshSession{}, fmt.Errorf("parse last_rotated: %w", err)
	}
	telegramIDUnix, err := strconv.ParseInt(fields["telegram_id"], 10, 64)
	if err != nil {
		return RefreshSession{}, fmt.Errorf("parse telegram_id: %w", err)
	}
	var revokedAt *time.Time
	if rawRevokedAt, ok := fields["revoked_at"]; ok && rawRevokedAt != "" {
		revokedAtUnix, err := strconv.ParseInt(rawRevokedAt, 10, 64)
		if err != nil {
			return RefreshSession{}, fmt.Errorf("parse revoked_at: %w", err)
		}
		t := time.Unix(revokedAtUnix, 0).UTC()
		revokedAt = &t
	}
	return RefreshSession{
		SessionID:   sessionID,
		UserID:      fields["user_id"],
		TelegramID:  telegramIDUnix,
		TokenHash:   fields["token_hash"],
		ExpiresAt:   time.Unix(expiresAtUnix, 0).UTC(),
		CreatedAt:   time.Unix(createdAtUnix, 0).UTC(),
		LastRotated: time.Unix(lastRotatedUnix, 0).UTC(),
		RevokedAt:   revokedAt,
	}, nil
}

func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
