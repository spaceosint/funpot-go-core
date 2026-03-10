package streamers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Streamer struct {
	ID             string    `json:"id"`
	TwitchUsername string    `json:"twitchUsername"`
	DisplayName    string    `json:"displayName"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, twitchUsername string) (Streamer, error) {
	normalized := strings.ToLower(strings.TrimSpace(twitchUsername))
	item := Streamer{
		ID:             fmt.Sprintf("str_%d", time.Now().UTC().UnixNano()),
		TwitchUsername: normalized,
		DisplayName:    normalized,
		Status:         "pending",
		CreatedAt:      time.Now().UTC(),
	}
	const q = `INSERT INTO streamers (id, twitch_username, display_name, status, created_at) VALUES ($1,$2,$3,$4,$5)`
	if _, err := s.db.ExecContext(ctx, q, item.ID, item.TwitchUsername, item.DisplayName, item.Status, item.CreatedAt); err != nil {
		return Streamer{}, err
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, query string, page, pageSize int) ([]Streamer, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	needle := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	const q = `SELECT id, twitch_username, display_name, status, created_at
		FROM streamers
		WHERE ($1 = '%%' OR lower(twitch_username) LIKE $1)
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := s.db.QueryContext(ctx, q, needle, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]Streamer, 0)
	for rows.Next() {
		var item Streamer
		if err := rows.Scan(&item.ID, &item.TwitchUsername, &item.DisplayName, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
