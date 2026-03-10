package payments

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Invoice struct {
	InvoiceID string `json:"invoiceId"`
	AmountINT int    `json:"amountINT"`
	Currency  string `json:"currency"`
	URL       string `json:"url"`
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) CreateStarsInvoice(ctx context.Context, telegramID int64, amount int) (Invoice, error) {
	invoice := Invoice{
		InvoiceID: fmt.Sprintf("inv_%d", time.Now().UTC().UnixNano()),
		AmountINT: amount,
		Currency:  "XTR",
		URL:       "https://t.me/invoice/mock",
	}
	const q = `INSERT INTO payments (id, user_telegram_id, amount_int, currency, status, provider_payload, created_at)
		VALUES ($1,$2,$3,$4,'pending',$5,$6)`
	if _, err := s.db.ExecContext(ctx, q, invoice.InvoiceID, telegramID, amount, invoice.Currency, invoice.URL, time.Now().UTC()); err != nil {
		return Invoice{}, err
	}
	return invoice, nil
}
