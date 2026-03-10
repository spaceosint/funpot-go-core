package wallet

import (
	"context"
	"database/sql"
)

type Transaction struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Amount  int    `json:"amountINT"`
	Comment string `json:"comment"`
}

type Snapshot struct {
	BalanceINT int           `json:"balanceINT"`
	History    []Transaction `json:"history"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (Snapshot, error) {
	snap := Snapshot{BalanceINT: 0, History: []Transaction{}}
	const balQ = `SELECT balance_int FROM wallet_accounts WHERE user_telegram_id = $1`
	if err := s.db.QueryRowContext(ctx, balQ, telegramID).Scan(&snap.BalanceINT); err != nil && err != sql.ErrNoRows {
		return Snapshot{}, err
	}

	const hQ = `SELECT id, tx_type, amount_int, comment FROM wallet_ledger WHERE user_telegram_id = $1 ORDER BY created_at DESC LIMIT 50`
	rows, err := s.db.QueryContext(ctx, hQ, telegramID)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var tx Transaction
		if err := rows.Scan(&tx.ID, &tx.Type, &tx.Amount, &tx.Comment); err != nil {
			return Snapshot{}, err
		}
		snap.History = append(snap.History, tx)
	}
	return snap, rows.Err()
}
