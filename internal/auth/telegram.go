package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrMissingHash     = errors.New("init data missing hash")
	ErrMissingAuthDate = errors.New("init data missing auth_date")
	ErrMissingUser     = errors.New("init data missing user")
	ErrInvalidHash     = errors.New("invalid init data hash")
	ErrExpired         = errors.New("init data expired")
)

// TelegramUser captures the subset of user fields provided by Telegram initData.
type TelegramUser struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
	PhotoURL     string `json:"photo_url"`
}

// TelegramInitData represents the validated initData payload.
type TelegramInitData struct {
	User     TelegramUser
	AuthDate time.Time
	Raw      url.Values
}

// VerifyInitData parses and validates Telegram init data according to the spec.
func VerifyInitData(raw string, botToken string, maxAge time.Duration, now time.Time) (TelegramInitData, error) {
	if botToken == "" {
		return TelegramInitData{}, errors.New("telegram bot token is required")
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return TelegramInitData{}, fmt.Errorf("parse init data: %w", err)
	}

	hash := values.Get("hash")
	if hash == "" {
		return TelegramInitData{}, ErrMissingHash
	}

	authDateStr := values.Get("auth_date")
	if authDateStr == "" {
		return TelegramInitData{}, ErrMissingAuthDate
	}

	authUnix, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil {
		return TelegramInitData{}, fmt.Errorf("parse auth_date: %w", err)
	}
	authTime := time.Unix(authUnix, 0).UTC()

	if maxAge > 0 && now.Sub(authTime) > maxAge {
		return TelegramInitData{}, ErrExpired
	}

	dataCheckString := buildDataCheckString(values)

	if !validateHash(hash, dataCheckString, botToken) {
		return TelegramInitData{}, ErrInvalidHash
	}

	userStr := values.Get("user")
	if userStr == "" {
		return TelegramInitData{}, ErrMissingUser
	}

	var user TelegramUser
	if err := json.Unmarshal([]byte(userStr), &user); err != nil {
		return TelegramInitData{}, fmt.Errorf("decode user: %w", err)
	}

	return TelegramInitData{
		User:     user,
		AuthDate: authTime,
		Raw:      values,
	}, nil
}

func buildDataCheckString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "hash" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, strings.Join(values[key], "\n")))
	}
	return strings.Join(pairs, "\n")
}

func validateHash(expectedHash string, dataCheckString string, botToken string) bool {
	secretHasher := hmac.New(sha256.New, []byte("WebAppData"))
	secretHasher.Write([]byte(botToken))
	secretKey := secretHasher.Sum(nil)

	dataHasher := hmac.New(sha256.New, secretKey)
	dataHasher.Write([]byte(dataCheckString))
	computed := dataHasher.Sum(nil)

	provided, err := hex.DecodeString(expectedHash)
	if err != nil {
		return false
	}
	return hmac.Equal(computed, provided)
}
