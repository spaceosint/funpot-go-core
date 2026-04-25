package users

import (
	"context"
	"testing"
	"time"
)

func TestService_BanAndUnbanByID(t *testing.T) {
	repo := NewInMemoryRepository()
	svc := NewService(repo)
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	svc.WithNow(func() time.Time { return now })

	created, err := svc.SyncTelegramProfile(context.Background(), TelegramProfile{
		ID:           1,
		Username:     "user",
		FirstName:    "First",
		LastName:     "Last",
		LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}

	banned, err := svc.BanByID(context.Background(), created.ID, "manual", now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("BanByID() error = %v", err)
	}
	if !banned.IsBanned {
		t.Fatalf("expected IsBanned=true")
	}
	if banned.BanReason != "manual" {
		t.Fatalf("unexpected reason: %q", banned.BanReason)
	}
	if !banned.IsAccessBlocked(now.Add(10 * time.Minute)) {
		t.Fatalf("expected user to be blocked during temporary ban")
	}
	if banned.IsAccessBlocked(now.Add(time.Hour)) {
		t.Fatalf("expected temporary ban to expire")
	}

	unbanned, err := svc.UnbanByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("UnbanByID() error = %v", err)
	}
	if unbanned.IsBanned {
		t.Fatalf("expected IsBanned=false")
	}
	if unbanned.IsAccessBlocked(now.Add(time.Minute)) {
		t.Fatalf("expected user to have access")
	}
}

func TestService_BanByID_InvalidBanUntil(t *testing.T) {
	repo := NewInMemoryRepository()
	svc := NewService(repo)
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	svc.WithNow(func() time.Time { return now })

	created, err := svc.SyncTelegramProfile(context.Background(), TelegramProfile{ID: 2})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}

	if _, err := svc.BanByID(context.Background(), created.ID, "manual", now); err != ErrBanUntilBeforeNow {
		t.Fatalf("expected ErrBanUntilBeforeNow, got %v", err)
	}
}
