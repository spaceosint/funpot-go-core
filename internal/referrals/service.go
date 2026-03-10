package referrals

import (
	"context"
	"database/sql"
	"time"
)

type Summary struct {
	Code           string `json:"code"`
	InvitedCount   int    `json:"invitedCount"`
	RewardTotalINT int    `json:"rewardTotalINT"`
}

type Payout struct {
	ID        string `json:"id"`
	AmountINT int    `json:"amountINT"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) GetSummary(ctx context.Context, telegramID int64) (Summary, error) {
	summary := Summary{}
	if err := s.db.QueryRowContext(ctx, `SELECT referral_code FROM users WHERE telegram_id = $1`, telegramID).Scan(&summary.Code); err != nil {
		if err != sql.ErrNoRows {
			return Summary{}, err
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM referral_invites WHERE referrer_telegram_id = $1`, telegramID).Scan(&summary.InvitedCount); err != nil {
		return Summary{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_int),0) FROM referral_payouts WHERE user_telegram_id = $1 AND status = 'paid'`, telegramID).Scan(&summary.RewardTotalINT); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

func (s *Service) ListPayouts(ctx context.Context, telegramID int64) ([]Payout, error) {
	const q = `SELECT id, amount_int, status, created_at FROM referral_payouts WHERE user_telegram_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, telegramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]Payout, 0)
	for rows.Next() {
		var item Payout
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.AmountINT, &item.Status, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		items = append(items, item)
	}
	return items, rows.Err()
}
