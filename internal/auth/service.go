package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/users"
)

const (
	defaultInitDataMaxAge = 24 * time.Hour
	defaultRefreshTTL     = 30 * 24 * time.Hour
)

var (
	ErrRefreshTokenRequired = errors.New("refresh token is required")
	ErrInvalidRefreshToken  = errors.New("invalid refresh token")
	ErrUserBanned           = errors.New("user is banned")
)

// Service coordinates Telegram verification and JWT issuance.
type Service struct {
	logger         *zap.Logger
	botToken       string
	issuer         *JWTIssuer
	userService    *users.Service
	initDataMaxAge time.Duration
	refreshStore   RefreshSessionStore
	refreshTTL     time.Duration
}

// TokenResponse describes the authentication payload returned to clients.
type TokenResponse struct {
	Token            string        `json:"token"`
	ExpiresAt        time.Time     `json:"expiresAt"`
	User             users.Profile `json:"user"`
	RefreshToken     string        `json:"refreshToken,omitempty"`
	RefreshExpiresAt *time.Time    `json:"refreshExpiresAt,omitempty"`
	SessionID        string        `json:"sessionId,omitempty"`
}

// RefreshResponse describes the payload returned for token rotation.
type RefreshResponse struct {
	Token            string    `json:"token"`
	ExpiresAt        time.Time `json:"expiresAt"`
	RefreshToken     string    `json:"refreshToken"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
	SessionID        string    `json:"sessionId"`
}

// NewService constructs the authentication service.
func NewService(logger *zap.Logger, cfg config.AuthConfig, userService *users.Service) (*Service, error) {
	if userService == nil {
		return nil, errors.New("user service is required")
	}
	if cfg.BotToken == "" {
		return nil, errors.New("telegram bot token is required")
	}
	issuer, err := NewJWTIssuer(cfg.JWT.Secret, cfg.JWT.TTL)
	if err != nil {
		return nil, err
	}
	refreshTTL := cfg.Refresh.TTL
	if refreshTTL <= 0 {
		refreshTTL = defaultRefreshTTL
	}
	return &Service{
		logger:         logger,
		botToken:       cfg.BotToken,
		issuer:         issuer,
		userService:    userService,
		initDataMaxAge: defaultInitDataMaxAge,
		refreshTTL:     refreshTTL,
	}, nil
}

func (s *Service) WithRefreshSessionStore(store RefreshSessionStore) {
	s.refreshStore = store
}

// Authenticate validates Telegram init data, syncs the user profile, and issues a JWT.
func (s *Service) Authenticate(ctx context.Context, initData string, now time.Time) (TokenResponse, error) {
	payload, err := VerifyInitData(initData, s.botToken, s.initDataMaxAge, now)
	if err != nil {
		return TokenResponse{}, err
	}
	profile, err := s.userService.SyncTelegramProfile(ctx, users.TelegramProfile{
		ID:           payload.User.ID,
		Username:     payload.User.Username,
		FirstName:    payload.User.FirstName,
		LastName:     payload.User.LastName,
		LanguageCode: payload.User.LanguageCode,
	})
	if err != nil {
		return TokenResponse{}, err
	}
	if profile.IsAccessBlocked(now.UTC()) {
		return TokenResponse{}, ErrUserBanned
	}

	token, expiresAt, err := s.issuer.Issue(profile.ID, payload.User.ID, now)
	if err != nil {
		return TokenResponse{}, err
	}

	resp := TokenResponse{Token: token, ExpiresAt: expiresAt, User: profile}
	if s.refreshStore == nil {
		return resp, nil
	}
	refreshToken, sessionID, refreshExpiresAt, err := s.createRefreshSession(ctx, profile.ID, payload.User.ID, now)
	if err != nil {
		s.logger.Error("failed to create refresh session", zap.Error(err))
		return TokenResponse{}, err
	}
	resp.RefreshToken = refreshToken
	resp.SessionID = sessionID
	resp.RefreshExpiresAt = &refreshExpiresAt
	return resp, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string, now time.Time) (RefreshResponse, error) {
	if s.refreshStore == nil {
		return RefreshResponse{}, errors.New("refresh session store is not configured")
	}
	if strings.TrimSpace(refreshToken) == "" {
		return RefreshResponse{}, ErrRefreshTokenRequired
	}
	sessionID, err := parseRefreshSessionID(refreshToken)
	if err != nil {
		return RefreshResponse{}, err
	}
	session, err := s.refreshStore.Get(ctx, sessionID)
	if err != nil {
		return RefreshResponse{}, err
	}
	if session.RevokedAt != nil {
		return RefreshResponse{}, ErrRefreshSessionRevoked
	}
	if session.ExpiresAt.Before(now) {
		return RefreshResponse{}, ErrRefreshSessionNotFound
	}
	currentHash := HashRefreshToken(refreshToken)
	if session.TokenHash != currentHash {
		return RefreshResponse{}, ErrRefreshTokenMismatch
	}

	accessToken, accessExpiresAt, err := s.issuer.Issue(session.UserID, session.TelegramID, now)
	if err != nil {
		return RefreshResponse{}, err
	}
	nextRefreshToken, err := buildRefreshToken(sessionID)
	if err != nil {
		return RefreshResponse{}, err
	}
	nextRefreshExpiresAt := now.Add(s.refreshTTL)
	if err := s.refreshStore.Rotate(ctx, sessionID, currentHash, HashRefreshToken(nextRefreshToken), nextRefreshExpiresAt, now); err != nil {
		return RefreshResponse{}, err
	}
	return RefreshResponse{
		Token:            accessToken,
		ExpiresAt:        accessExpiresAt,
		RefreshToken:     nextRefreshToken,
		RefreshExpiresAt: nextRefreshExpiresAt,
		SessionID:        sessionID,
	}, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string, revokedAt time.Time) error {
	if strings.TrimSpace(refreshToken) == "" {
		return ErrRefreshTokenRequired
	}
	if s.refreshStore == nil {
		return errors.New("refresh session store is not configured")
	}
	sessionID, err := parseRefreshSessionID(refreshToken)
	if err != nil {
		return err
	}
	session, err := s.refreshStore.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.TokenHash != HashRefreshToken(refreshToken) {
		return ErrRefreshTokenMismatch
	}
	return s.refreshStore.RevokeSession(ctx, sessionID, revokedAt)
}

func (s *Service) LogoutAll(ctx context.Context, userID string, revokedAt time.Time) error {
	if s.refreshStore == nil {
		return errors.New("refresh session store is not configured")
	}
	if strings.TrimSpace(userID) == "" {
		return errors.New("user id is required")
	}
	return s.refreshStore.RevokeUserSessions(ctx, userID, revokedAt)
}

func (s *Service) createRefreshSession(ctx context.Context, userID string, telegramID int64, now time.Time) (refreshToken, sessionID string, expiresAt time.Time, err error) {
	sessionID, err = randomToken(18)
	if err != nil {
		return "", "", time.Time{}, err
	}
	refreshToken, err = buildRefreshToken(sessionID)
	if err != nil {
		return "", "", time.Time{}, err
	}
	expiresAt = now.Add(s.refreshTTL)
	err = s.refreshStore.Create(ctx, RefreshSession{
		SessionID:   sessionID,
		UserID:      userID,
		TelegramID:  telegramID,
		TokenHash:   HashRefreshToken(refreshToken),
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
		LastRotated: now,
	})
	if err != nil {
		return "", "", time.Time{}, err
	}
	return refreshToken, sessionID, expiresAt, nil
}

func buildRefreshToken(sessionID string) (string, error) {
	secret, err := randomToken(32)
	if err != nil {
		return "", err
	}
	return sessionID + "." + secret, nil
}

func parseRefreshSessionID(refreshToken string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(refreshToken), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ErrInvalidRefreshToken
	}
	return parts[0], nil
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ClaimsMiddleware exposes the HTTP middleware for authenticating API requests.
func (s *Service) ClaimsMiddleware() func(http.Handler) http.Handler {
	return Middleware(s.issuer)
}

// ParseToken exposes token parsing for other transports.
func (s *Service) ParseToken(token string) (*Claims, error) {
	return s.issuer.Parse(token)
}
