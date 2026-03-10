package media

import (
	"context"
	"database/sql"
)

type Clip struct {
	ID         string `json:"id"`
	StreamerID string `json:"streamerId"`
	URL        string `json:"url"`
	DurationS  int    `json:"durationS"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) ListClips(ctx context.Context, streamerID string) ([]Clip, error) {
	const q = `SELECT id, streamer_id, url, duration_s FROM media_clips WHERE streamer_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, streamerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]Clip, 0)
	for rows.Next() {
		var item Clip
		if err := rows.Scan(&item.ID, &item.StreamerID, &item.URL, &item.DurationS); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
