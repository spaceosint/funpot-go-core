package votes

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type CastRequest struct {
	EventID string `json:"eventId"`
	Option  string `json:"optionId"`
	Cost    int    `json:"cost"`
}

type Response struct {
	VoteID     string `json:"voteId"`
	EventID    string `json:"eventId"`
	OptionID   string `json:"optionId"`
	AcceptedAt string `json:"acceptedAt"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) Cast(ctx context.Context, telegramID int64, idempotencyKey string, req CastRequest) (Response, error) {
	voteID := fmt.Sprintf("vote_%d", time.Now().UTC().UnixNano())
	acceptedAt := time.Now().UTC()
	const q = `INSERT INTO votes (id, event_id, option_id, user_telegram_id, cost_int, idempotency_key, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	if _, err := s.db.ExecContext(ctx, q, voteID, req.EventID, req.Option, telegramID, req.Cost, idempotencyKey, acceptedAt); err != nil {
		return Response{}, err
	}
	return Response{VoteID: voteID, EventID: req.EventID, OptionID: req.Option, AcceptedAt: acceptedAt.Format(time.RFC3339Nano)}, nil
}
