package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestVerifyInitData_Success(t *testing.T) {
	botToken := "12345:ABCDEF"
	now := time.Unix(1_700_000_000, 0)

	values := url.Values{}
	values.Set("auth_date", "1700000000")
	values.Set("query_id", "AAEWpqlDAAAAANz")

	userPayload := map[string]any{
		"id":         float64(123456789),
		"is_bot":     false,
		"first_name": "Alice",
		"username":   "alice",
	}
	rawUser, err := json.Marshal(userPayload)
	if err != nil {
		t.Fatalf("marshal user payload: %v", err)
	}
	values.Set("user", string(rawUser))

	hash := computeHash(values, botToken)
	values.Set("hash", hash)

	initData := values.Encode()

	result, err := VerifyInitData(initData, botToken, time.Hour, now)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if result.User.ID != 123456789 {
		t.Fatalf("unexpected user id: %d", result.User.ID)
	}
	if result.User.Username != "alice" {
		t.Fatalf("unexpected username: %s", result.User.Username)
	}
	if result.AuthDate.Unix() != 1_700_000_000 {
		t.Fatalf("unexpected auth date: %d", result.AuthDate.Unix())
	}
}

func TestVerifyInitData_InvalidHash(t *testing.T) {
	botToken := "12345:ABCDEF"
	now := time.Unix(1_700_000_000, 0)

	values := url.Values{}
	values.Set("auth_date", "1700000000")
	values.Set("query_id", "AAEWpqlDAAAAANz")
	values.Set("user", `{"id":123}`)
	values.Set("hash", "deadbeef")

	initData := values.Encode()

	_, err := VerifyInitData(initData, botToken, time.Hour, now)
	if err == nil {
		t.Fatal("expected error for invalid hash")
	}
	if !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("expected ErrInvalidHash, got %v", err)
	}
}

func TestVerifyInitData_Expired(t *testing.T) {
	botToken := "12345:ABCDEF"
	now := time.Unix(1_700_000_000, 0)

	values := url.Values{}
	values.Set("auth_date", "1699990000")
	values.Set("query_id", "AAEWpqlDAAAAANz")
	values.Set("user", `{"id":123}`)
	hash := computeHash(values, botToken)
	values.Set("hash", hash)

	initData := values.Encode()

	_, err := VerifyInitData(initData, botToken, time.Minute, now)
	if err == nil {
		t.Fatal("expected error for expired init data")
	}
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func computeHash(values url.Values, botToken string) string {
	dataCheckString := buildDataCheckString(values)
	secretHasher := hmac.New(sha256.New, []byte("WebAppData"))
	secretHasher.Write([]byte(botToken))
	secretKey := secretHasher.Sum(nil)

	dataHasher := hmac.New(sha256.New, secretKey)
	dataHasher.Write([]byte(dataCheckString))
	return hex.EncodeToString(dataHasher.Sum(nil))
}
