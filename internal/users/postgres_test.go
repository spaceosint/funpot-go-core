package users

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresRepository_GetByTelegramID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewPostgresRepository(db)
	now := time.Now().UTC()

	rows := sqlmock.NewRows([]string{"id", "telegram_id", "username", "nickname", "first_name", "last_name", "language_code", "referral_code", "is_banned", "ban_reason", "banned_at", "banned_until", "created_at", "updated_at"}).
		AddRow("tg_1", int64(1), "user", "BraveFox101", "First", "Last", "en", "ABC123", false, "", nil, nil, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at FROM users WHERE telegram_id = $1")).
		WithArgs(int64(1)).
		WillReturnRows(rows)

	profile, err := repo.GetByTelegramID(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.TelegramID != 1 || profile.Username != "user" {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepository_GetByTelegramID_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewPostgresRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at FROM users WHERE telegram_id = $1")).
		WithArgs(int64(99)).
		WillReturnError(sql.ErrNoRows)

	_, err = repo.GetByTelegramID(context.Background(), 99)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepository_Create(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewPostgresRepository(db)
	now := time.Now().UTC()
	profile := Profile{
		ID:           "tg_1",
		TelegramID:   1,
		Username:     "user",
		Nickname:     "BraveFox101",
		FirstName:    "First",
		LastName:     "Last",
		LanguageCode: "en",
		ReferralCode: "ABC123",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO users (id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at)\nVALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)\nON CONFLICT (telegram_id) DO NOTHING")).
		WithArgs(
			profile.ID,
			profile.TelegramID,
			profile.Username,
			profile.Nickname,
			profile.FirstName,
			profile.LastName,
			profile.LanguageCode,
			profile.ReferralCode,
			profile.IsBanned,
			profile.BanReason,
			nil,
			nil,
			profile.CreatedAt,
			profile.UpdatedAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO wallet_accounts (user_id, balance_int) VALUES ($1, 0) ON CONFLICT (user_id) DO NOTHING`)).
		WithArgs(profile.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO weekly_reward_claims (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`)).
		WithArgs(profile.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.Create(context.Background(), profile); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepository_Update(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewPostgresRepository(db)
	now := time.Now().UTC()
	profile := Profile{
		ID:           "tg_1",
		TelegramID:   1,
		Username:     "user",
		Nickname:     "BraveFox101",
		FirstName:    "First",
		LastName:     "Last",
		LanguageCode: "en",
		ReferralCode: "ABC123",
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now,
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users\nSET username = $2,\n    nickname = $3,\n    first_name = $4,\n    last_name = $5,\n    language_code = $6,\n    referral_code = $7,\n    is_banned = $8,\n    ban_reason = $9,\n    banned_at = $10,\n    banned_until = $11,\n    updated_at = $12\nWHERE telegram_id = $1")).
		WithArgs(
			profile.TelegramID,
			profile.Username,
			profile.Nickname,
			profile.FirstName,
			profile.LastName,
			profile.LanguageCode,
			profile.ReferralCode,
			profile.IsBanned,
			profile.BanReason,
			nil,
			nil,
			profile.UpdatedAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.Update(context.Background(), profile); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepository_UpdateNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewPostgresRepository(db)
	now := time.Now().UTC()
	profile := Profile{TelegramID: 42, UpdatedAt: now}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users\nSET username = $2,\n    nickname = $3,\n    first_name = $4,\n    last_name = $5,\n    language_code = $6,\n    referral_code = $7,\n    is_banned = $8,\n    ban_reason = $9,\n    banned_at = $10,\n    banned_until = $11,\n    updated_at = $12\nWHERE telegram_id = $1")).
		WithArgs(profile.TelegramID, "", "", "", "", "", "", false, "", nil, nil, profile.UpdatedAt).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.Update(context.Background(), profile)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
