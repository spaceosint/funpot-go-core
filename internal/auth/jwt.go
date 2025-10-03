package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the JWT payload issued to authenticated users.
type Claims struct {
	UserID     string `json:"uid"`
	TelegramID int64  `json:"tid"`
	jwt.RegisteredClaims
}

// JWTIssuer creates and validates JWT tokens.
type JWTIssuer struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTIssuer constructs a JWT issuer with the provided secret and TTL.
func NewJWTIssuer(secret string, ttl time.Duration) (*JWTIssuer, error) {
	if secret == "" {
		return nil, errors.New("jwt secret must be provided")
	}
	if ttl <= 0 {
		return nil, errors.New("jwt ttl must be positive")
	}
	return &JWTIssuer{secret: []byte(secret), ttl: ttl}, nil
}

// Issue generates a signed token for the provided user identity.
func (i *JWTIssuer) Issue(userID string, telegramID int64, now time.Time) (string, time.Time, error) {
	if i == nil {
		return "", time.Time{}, errors.New("jwt issuer is not configured")
	}
	expiresAt := now.Add(i.ttl)
	claims := Claims{
		UserID:     userID,
		TelegramID: telegramID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return signed, expiresAt, nil
}

// Parse validates the token and extracts claims.
func (i *JWTIssuer) Parse(token string) (*Claims, error) {
	if i == nil {
		return nil, errors.New("jwt issuer is not configured")
	}
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid jwt claims")
	}
	return claims, nil
}
