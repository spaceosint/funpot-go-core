package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// PostgresUploadedVideoStore stores uploaded Bunny video metadata in PostgreSQL.
type PostgresUploadedVideoStore struct {
	db *sql.DB
}

func NewPostgresUploadedVideoStore(db *sql.DB) *PostgresUploadedVideoStore {
	return &PostgresUploadedVideoStore{db: db}
}

func (s *PostgresUploadedVideoStore) Save(ctx context.Context, streamerID string, item UploadedVideo) error {
	if s == nil || s.db == nil {
		return nil
	}
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return nil
	}
	const query = `
INSERT INTO streamer_uploaded_videos (streamer_id, provider, video_id, title, url, status, created_at)
VALUES ($1, 'bunny', $2, $3, $4, 'ready', $5)
ON CONFLICT (provider, video_id)
DO UPDATE SET streamer_id = EXCLUDED.streamer_id,
              title = EXCLUDED.title,
              url = EXCLUDED.url,
              status = EXCLUDED.status,
              created_at = EXCLUDED.created_at`
	if _, err := s.db.ExecContext(ctx, query, key, strings.TrimSpace(item.ID), strings.TrimSpace(item.Title), strings.TrimSpace(item.URL), strings.TrimSpace(item.CreatedAt)); err != nil {
		return fmt.Errorf("save uploaded video metadata: %w", err)
	}
	return nil
}

func (s *PostgresUploadedVideoStore) ListByStreamer(ctx context.Context, streamerID string) ([]UploadedVideo, error) {
	if s == nil || s.db == nil {
		return []UploadedVideo{}, nil
	}
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return []UploadedVideo{}, nil
	}
	const query = `
SELECT video_id, title, url, created_at
FROM streamer_uploaded_videos
WHERE streamer_id = $1
ORDER BY created_at DESC, video_id DESC`
	rows, err := s.db.QueryContext(ctx, query, key)
	if err != nil {
		return nil, fmt.Errorf("list uploaded videos by streamer: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := make([]UploadedVideo, 0)
	for rows.Next() {
		var item UploadedVideo
		if err := rows.Scan(&item.ID, &item.Title, &item.URL, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan uploaded video metadata: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate uploaded video metadata: %w", err)
	}
	return items, nil
}

func (s *PostgresUploadedVideoStore) DeleteByStreamer(ctx context.Context, streamerID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return nil
	}
	const query = `DELETE FROM streamer_uploaded_videos WHERE streamer_id = $1`
	if _, err := s.db.ExecContext(ctx, query, key); err != nil {
		return fmt.Errorf("delete uploaded videos by streamer: %w", err)
	}
	return nil
}
