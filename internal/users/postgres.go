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
	)
	return err
}

// Update persists an existing profile.
func (r *PostgresRepository) Update(ctx context.Context, profile Profile) error {
	const query = `
		UPDATE users
		SET username = $2,
		    first_name = $3,
		    last_name = $4,
		    language_code = $5,
		    referral_code = $6,
		    updated_at = $7
		WHERE telegram_id = $1
	`

	result, err := r.pool.Exec(ctx, query,
		profile.TelegramID,
		profile.Username,
		profile.FirstName,
		profile.LastName,
		profile.LanguageCode,
		profile.ReferralCode,
		profile.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
