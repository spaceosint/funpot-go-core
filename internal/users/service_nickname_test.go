package users

import (
	"context"
	"testing"
)

func TestService_UpdateNicknameByID(t *testing.T) {
	repo := NewInMemoryRepository()
	svc := NewService(repo)

	created, err := svc.SyncTelegramProfile(context.Background(), TelegramProfile{ID: 100})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}

	updated, err := svc.UpdateNicknameByID(context.Background(), created.ID, "  NewNick  ")
	if err != nil {
		t.Fatalf("UpdateNicknameByID() error = %v", err)
	}
	if updated.Nickname != "NewNick" {
		t.Fatalf("expected nickname to be trimmed and updated, got %q", updated.Nickname)
	}
}

func TestService_UpdateNicknameByID_EmptyNickname(t *testing.T) {
	repo := NewInMemoryRepository()
	svc := NewService(repo)

	created, err := svc.SyncTelegramProfile(context.Background(), TelegramProfile{ID: 200})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}

	if _, err := svc.UpdateNicknameByID(context.Background(), created.ID, "   "); err != ErrInvalidNickname {
		t.Fatalf("expected ErrInvalidNickname, got %v", err)
	}
}
