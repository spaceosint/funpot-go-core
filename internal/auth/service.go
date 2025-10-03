package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/users"
)

const defaultInitDataMaxAge = 24 * time.Hour

// Service coordinates Telegram verification and JWT issuance.
type Service struct {
	logger         *zap.Logger
	botToken       string
	issuer         *JWTIssuer
	userService    *users.Service
	initDataMaxAge time.Duration
}

// TokenResponse describes the authentication payload returned to clients.
type TokenResponse struct {
	Token     string        `json:"token"`
	ExpiresAt time.Time     `json:"expiresAt"`
	User      users.Profile `json:"user"`
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
	return &Service{
		logger:         logger,
		botToken:       cfg.BotToken,
		issuer:         issuer,
		userService:    userService,
		initDataMaxAge: defaultInitDataMaxAge,
	}, nil
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

	token, expiresAt, err := s.issuer.Issue(profile.ID, payload.User.ID, now)
	if err != nil {
		return TokenResponse{}, err
	}

	return TokenResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      profile,
	}, nil
}

// ClaimsMiddleware exposes the HTTP middleware for authenticating API requests.
func (s *Service) ClaimsMiddleware() func(http.Handler) http.Handler {
	return Middleware(s.issuer)
}

// ParseToken exposes token parsing for other transports.
func (s *Service) ParseToken(token string) (*Claims, error) {
	return s.issuer.Parse(token)
}
