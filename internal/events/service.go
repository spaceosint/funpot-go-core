package events

import (
	"context"
	"database/sql"
	"time"
)

type LiveEvent struct {
	ID          string         `json:"id"`
	GameID      *string        `json:"gameId"`
	Title       string         `json:"title"`
	Options     []EventOption  `json:"options"`
	ClosesAt    time.Time      `json:"closesAt"`
	Totals      map[string]int `json:"totals"`
	UserVote    userVote       `json:"userVote"`
	CostPerVote int            `json:"costPerVote"`
}

type EventOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type userVote struct {
	OptionID string `json:"optionId"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) ListLive(ctx context.Context, streamerID string, _ int64) ([]LiveEvent, error) {
	const q = `SELECT id, title, created_at FROM events WHERE streamer_id = $1 AND status = 'live' ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, streamerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]LiveEvent, 0)
	for rows.Next() {
		var createdAt time.Time
		var item LiveEvent
		if err := rows.Scan(&item.ID, &item.Title, &createdAt); err != nil {
			return nil, err
		}
		item.Options = []EventOption{}
		item.ClosesAt = createdAt.Add(5 * time.Minute)
		item.Totals = map[string]int{}
		item.UserVote = userVote{OptionID: ""}
		item.CostPerVote = 1
		items = append(items, item)
	}
	return items, rows.Err()
}
