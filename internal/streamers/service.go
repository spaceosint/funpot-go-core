package streamers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Streamer struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Online      bool   `json:"online"`
	Viewers     int    `json:"viewers"`
	AddedBy     string `json:"addedBy"`
	Status      string `json:"status"`
}

type Submission struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, twitchUsername string) (Submission, error) {
	normalized := strings.ToLower(strings.TrimSpace(twitchUsername))
	item := Streamer{
		ID:          fmt.Sprintf("str_%d", time.Now().UTC().UnixNano()),
		Platform:    "twitch",
		Username:    normalized,
		DisplayName: normalized,
		Online:      false,
		Viewers:     0,
		AddedBy:     "",
		Status:      "pending",
	}
	const q = `INSERT INTO streamers (id, twitch_username, display_name, status, created_at) VALUES ($1,$2,$3,$4,$5)`
	if _, err := s.db.ExecContext(ctx, q, item.ID, item.Username, item.DisplayName, item.Status, time.Now().UTC()); err != nil {
		return Submission{}, err
	}
	return Submission{ID: item.ID, Status: item.Status, Reason: nil}, nil
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
		var createdAt time.Time
		var item Streamer
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.Status, &createdAt); err != nil {
			return nil, err
		}
		item.Platform = "twitch"
		item.Online = false
		item.Viewers = 0
		item.AddedBy = ""
		items = append(items, item)
	}
	return items, rows.Err()
}
