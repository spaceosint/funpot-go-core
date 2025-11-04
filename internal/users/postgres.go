package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PostgresRepository persists user profiles in PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository constructs a repository backed by PostgreSQL.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// GetByTelegramID returns a profile identified by the Telegram ID.
func (r *PostgresRepository) GetByTelegramID(ctx context.Context, telegramID int64) (Profile, error) {
	const query = `SELECT id, telegram_id, username, first_name, last_name, language_code, referral_code, created_at, updated_at FROM users WHERE telegram_id = $1`

	var profile Profile
	err := r.db.QueryRowContext(ctx, query, telegramID).Scan(
		&profile.ID,
		&profile.TelegramID,
		&profile.Username,
		&profile.FirstName,
		&profile.LastName,
		&profile.LanguageCode,
		&profile.ReferralCode,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("select user by telegram id: %w", err)
	}
	return profile, nil
}

// Create inserts a new profile. Existing records are left untouched.
func (r *PostgresRepository) Create(ctx context.Context, profile Profile) error {
	const query = `
INSERT INTO users (id, telegram_id, username, first_name, last_name, language_code, referral_code, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (telegram_id) DO NOTHING`

	if _, err := r.db.ExecContext(ctx, query,
		profile.ID,
		profile.TelegramID,
		profile.Username,
		profile.FirstName,
		profile.LastName,
		profile.LanguageCode,
		profile.ReferralCode,
		profile.CreatedAt,
		profile.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// Update persists profile changes for an existing user.
func (r *PostgresRepository) Update(ctx context.Context, profile Profile) error {
	const query = `
UPDATE users
SET username = $1,
    first_name = $2,
    last_name = $3,
    language_code = $4,
    updated_at = $5
WHERE telegram_id = $6`

	res, err := r.db.ExecContext(ctx, query,
		profile.Username,
		profile.FirstName,
		profile.LastName,
		profile.LanguageCode,
		profile.UpdatedAt,
		profile.TelegramID,
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update user rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
