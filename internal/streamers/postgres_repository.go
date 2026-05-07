package streamers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// PostgresStreamerRepository persists user-submitted streamers in the streamers table.
// The database status column models operational availability (active/inactive/disabled),
// while the user-facing submission moderation status is kept in metadata.submissionStatus.
type PostgresStreamerRepository struct {
	db *sql.DB
}

func NewPostgresStreamerRepository(db *sql.DB) *PostgresStreamerRepository {
	return &PostgresStreamerRepository{db: db}
}

func (r *PostgresStreamerRepository) List(ctx context.Context, query, status string, page int) ([]Streamer, error) {
	if r == nil || r.db == nil {
		return []Streamer{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id::text, twitch_username, display_name, status, metadata
FROM streamers
ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]Streamer, 0)
	for rows.Next() {
		item, scanErr := scanStreamer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return filterStreamers(items, query, status, page), nil
}

func (r *PostgresStreamerRepository) GetByID(ctx context.Context, id string) (Streamer, bool, error) {
	if r == nil || r.db == nil || strings.TrimSpace(id) == "" {
		return Streamer{}, false, nil
	}
	row := r.db.QueryRowContext(ctx, `
SELECT id::text, twitch_username, display_name, status, metadata
FROM streamers
WHERE id = $1`, strings.TrimSpace(id))
	item, err := scanStreamer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Streamer{}, false, nil
	}
	if err != nil {
		return Streamer{}, false, err
	}
	return item, true, nil
}

func (r *PostgresStreamerRepository) Upsert(ctx context.Context, item Streamer) (Streamer, error) {
	if r == nil || r.db == nil {
		return item, nil
	}
	item = normalizeStreamerForStorage(item)
	metadata, err := marshalStreamerMetadata(item)
	if err != nil {
		return Streamer{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Streamer{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var existingID string
	err = tx.QueryRowContext(ctx, `
SELECT id::text
FROM streamers
WHERE lower(twitch_username) = lower($1)
FOR UPDATE`, item.TwitchNickname).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Streamer{}, err
	}
	if existingID != "" {
		item.ID = existingID
		_, err = tx.ExecContext(ctx, `
UPDATE streamers
SET twitch_username = $2,
    display_name = $3,
    status = $4,
    metadata = $5::jsonb
WHERE id = $1`, item.ID, item.TwitchNickname, item.DisplayName, dbStreamerStatus(item.Status), string(metadata))
		if err != nil {
			return Streamer{}, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `
INSERT INTO streamers (id, twitch_username, display_name, status, metadata)
VALUES ($1, $2, $3, $4, $5::jsonb)`, item.ID, item.TwitchNickname, item.DisplayName, dbStreamerStatus(item.Status), string(metadata))
		if err != nil {
			return Streamer{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Streamer{}, err
	}
	return item, nil
}

func (r *PostgresStreamerRepository) Delete(ctx context.Context, id string) error {
	if r == nil || r.db == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM streamers WHERE id = $1`, strings.TrimSpace(id))
	return err
}

type streamerScanner interface {
	Scan(dest ...any) error
}

type streamerMetadata struct {
	Platform         string `json:"platform,omitempty"`
	MiniIconURL      string `json:"miniIconUrl,omitempty"`
	Online           bool   `json:"online,omitempty"`
	Viewers          int    `json:"viewers,omitempty"`
	AddedBy          string `json:"addedBy,omitempty"`
	SubmissionStatus string `json:"submissionStatus,omitempty"`
}

func scanStreamer(scanner streamerScanner) (Streamer, error) {
	var item Streamer
	var dbStatus string
	var metadataRaw []byte
	if err := scanner.Scan(&item.ID, &item.TwitchNickname, &item.DisplayName, &dbStatus, &metadataRaw); err != nil {
		return Streamer{}, err
	}
	metadata := streamerMetadata{}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
			return Streamer{}, err
		}
	}
	item.Platform = firstNonEmptyStreamer(metadata.Platform, "twitch")
	item.MiniIconURL = strings.TrimSpace(metadata.MiniIconURL)
	item.Online = metadata.Online
	item.Viewers = metadata.Viewers
	item.AddedBy = strings.TrimSpace(metadata.AddedBy)
	item.Status = firstNonEmptyStreamer(strings.TrimSpace(metadata.SubmissionStatus), appStreamerStatus(dbStatus))
	return item, nil
}

func normalizeStreamerForStorage(item Streamer) Streamer {
	item.ID = strings.TrimSpace(item.ID)
	item.Platform = firstNonEmptyStreamer(strings.TrimSpace(item.Platform), "twitch")
	item.TwitchNickname = strings.ToLower(strings.TrimSpace(item.TwitchNickname))
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.MiniIconURL = strings.TrimSpace(item.MiniIconURL)
	item.AddedBy = strings.TrimSpace(item.AddedBy)
	item.Status = firstNonEmptyStreamer(strings.ToLower(strings.TrimSpace(item.Status)), "pending")
	return item
}

func marshalStreamerMetadata(item Streamer) ([]byte, error) {
	return json.Marshal(streamerMetadata{
		Platform:         item.Platform,
		MiniIconURL:      item.MiniIconURL,
		Online:           item.Online,
		Viewers:          item.Viewers,
		AddedBy:          item.AddedBy,
		SubmissionStatus: item.Status,
	})
}

func dbStreamerStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "rejected":
		return "inactive"
	default:
		return "active"
	}
}

func appStreamerStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "inactive", "disabled":
		return "rejected"
	default:
		return "approved"
	}
}

func firstNonEmptyStreamer(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
