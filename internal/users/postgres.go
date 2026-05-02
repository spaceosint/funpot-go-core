package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PostgresRepository persists user profiles in PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository constructs a repository backed by PostgreSQL.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// GetByID returns a profile identified by internal user ID.
func (r *PostgresRepository) GetByID(ctx context.Context, id string) (Profile, error) {
	const query = `SELECT id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at FROM users WHERE id = $1`

	var profile Profile
	var bannedAt sql.NullTime
	var bannedUntil sql.NullTime
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&profile.ID,
		&profile.TelegramID,
		&profile.Username,
		&profile.Nickname,
		&profile.FirstName,
		&profile.LastName,
		&profile.LanguageCode,
		&profile.ReferralCode,
		&profile.IsBanned,
		&profile.BanReason,
		&bannedAt,
		&bannedUntil,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("select user by id: %w", err)
	}
	applyBanTimes(&profile, bannedAt, bannedUntil)
	return profile, nil
}

// GetByTelegramID returns a profile identified by the Telegram ID.
func (r *PostgresRepository) GetByTelegramID(ctx context.Context, telegramID int64) (Profile, error) {
	const query = `SELECT id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at FROM users WHERE telegram_id = $1`

	var profile Profile
	var bannedAt sql.NullTime
	var bannedUntil sql.NullTime
	err := r.db.QueryRowContext(ctx, query, telegramID).Scan(
		&profile.ID,
		&profile.TelegramID,
		&profile.Username,
		&profile.Nickname,
		&profile.FirstName,
		&profile.LastName,
		&profile.LanguageCode,
		&profile.ReferralCode,
		&profile.IsBanned,
		&profile.BanReason,
		&bannedAt,
		&bannedUntil,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("select user by telegram id: %w", err)
	}
	applyBanTimes(&profile, bannedAt, bannedUntil)
	return profile, nil
}

// List returns paginated users matching an optional query.
func (r *PostgresRepository) List(ctx context.Context, query string, page, pageSize int) ([]Profile, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	needle := strings.TrimSpace(query)
	like := "%" + needle + "%"

	const totalQuery = `
SELECT COUNT(*)
FROM users
WHERE $1 = ''
   OR id ILIKE $2
   OR username ILIKE $2
   OR nickname ILIKE $2
   OR first_name ILIKE $2
   OR last_name ILIKE $2
   OR language_code ILIKE $2
   OR referral_code ILIKE $2`

	var total int
	if err := r.db.QueryRowContext(ctx, totalQuery, needle, like).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	const listQuery = `
SELECT id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at
FROM users
WHERE $1 = ''
   OR id ILIKE $2
   OR username ILIKE $2
   OR nickname ILIKE $2
   OR first_name ILIKE $2
   OR last_name ILIKE $2
   OR language_code ILIKE $2
   OR referral_code ILIKE $2
ORDER BY created_at DESC, id ASC
LIMIT $3 OFFSET $4`

	offset := (page - 1) * pageSize
	rows, err := r.db.QueryContext(ctx, listQuery, needle, like, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := make([]Profile, 0, pageSize)
	for rows.Next() {
		var profile Profile
		var bannedAt sql.NullTime
		var bannedUntil sql.NullTime
		if err := rows.Scan(
			&profile.ID,
			&profile.TelegramID,
			&profile.Username,
			&profile.Nickname,
			&profile.FirstName,
			&profile.LastName,
			&profile.LanguageCode,
			&profile.ReferralCode,
			&profile.IsBanned,
			&profile.BanReason,
			&bannedAt,
			&bannedUntil,
			&profile.CreatedAt,
			&profile.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		applyBanTimes(&profile, bannedAt, bannedUntil)
		items = append(items, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users: %w", err)
	}

	return items, total, nil
}

// Create inserts a new profile and initializes durable wallet/reward state.
func (r *PostgresRepository) Create(ctx context.Context, profile Profile) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	const query = `
INSERT INTO users (id, telegram_id, username, nickname, first_name, last_name, language_code, referral_code, is_banned, ban_reason, banned_at, banned_until, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (telegram_id) DO NOTHING`

	res, err := tx.ExecContext(ctx, query,
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
		nullableTime(profile.BannedAt),
		nullableTime(profile.BannedUntil),
		profile.CreatedAt,
		profile.UpdatedAt,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return nil
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO wallet_accounts (user_id, balance_int) VALUES ($1, 0) ON CONFLICT (user_id) DO NOTHING`, profile.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO weekly_reward_claims (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, profile.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// Update persists an existing profile.
func (r *PostgresRepository) Update(ctx context.Context, profile Profile) error {
	const query = `
		UPDATE users
		SET username = $2,
		    nickname = $3,
		    first_name = $4,
		    last_name = $5,
		    language_code = $6,
		    referral_code = $7,
		    is_banned = $8,
		    ban_reason = $9,
		    banned_at = $10,
		    banned_until = $11,
		    updated_at = $12
		WHERE telegram_id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		profile.TelegramID,
		profile.Username,
		profile.Nickname,
		profile.FirstName,
		profile.LastName,
		profile.LanguageCode,
		profile.ReferralCode,
		profile.IsBanned,
		profile.BanReason,
		nullableTime(profile.BannedAt),
		nullableTime(profile.BannedUntil),
		profile.UpdatedAt,
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func applyBanTimes(profile *Profile, bannedAt, bannedUntil sql.NullTime) {
	if profile == nil {
		return
	}
	if bannedAt.Valid {
		profile.BannedAt = bannedAt.Time.UTC()
	} else {
		profile.BannedAt = time.Time{}
	}
	if bannedUntil.Valid {
		profile.BannedUntil = bannedUntil.Time.UTC()
	} else {
		profile.BannedUntil = time.Time{}
	}
}

// DeleteByID deletes a user by internal ID.
func (r *PostgresRepository) DeleteByID(ctx context.Context, id string) error {
	const query = `DELETE FROM users WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete user by id: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete user by id rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
