package events

import (
	"context"
	"database/sql"
)

type LiveEvent struct {
	ID         string   `json:"id"`
	StreamerID string   `json:"streamerId"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Options    []string `json:"options"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) ListLive(ctx context.Context, streamerID string) ([]LiveEvent, error) {
	const q = `SELECT id, streamer_id, title, status FROM events WHERE streamer_id = $1 AND status = 'live' ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, streamerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]LiveEvent, 0)
	for rows.Next() {
		var item LiveEvent
		if err := rows.Scan(&item.ID, &item.StreamerID, &item.Title, &item.Status); err != nil {
			return nil, err
		}
		item.Options = []string{}
		items = append(items, item)
	}
	return items, rows.Err()
}
