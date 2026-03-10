package games

import (
	"context"
	"database/sql"
)

type Game struct {
	ID         string `json:"id"`
	StreamerID string `json:"streamerId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) ListByStreamer(ctx context.Context, streamerID string) ([]Game, error) {
	const q = `SELECT id, streamer_id, name, status FROM games WHERE streamer_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, streamerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]Game, 0)
	for rows.Next() {
		var item Game
		if err := rows.Scan(&item.ID, &item.StreamerID, &item.Name, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
