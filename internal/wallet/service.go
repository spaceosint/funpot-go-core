package wallet

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserIDRequired      = errors.New("user id is required")
	ErrInvalidAmount       = errors.New("amount must be a positive integer")
	ErrInvalidDelta        = errors.New("delta must not be zero")
	ErrIdempotencyRequired = errors.New("idempotency key is required")
	ErrInsufficientFunds   = errors.New("insufficient funds")
)

type EntryType string

const (
	EntryTypeCredit EntryType = "credit"
	EntryTypeDebit  EntryType = "debit"
	GameCurrency    string    = "FPC"
)

type Entry struct {
	ID             string    `json:"id"`
	UserID         string    `json:"-"`
	Type           EntryType `json:"type"`
	Amount         int64     `json:"amount"`
	Currency       string    `json:"currency"`
	Reason         string    `json:"reason"`
	IdempotencyKey string    `json:"-"`
	ActorID        string    `json:"-"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Wallet struct {
	Balance int64   `json:"balance"`
	History []Entry `json:"history"`
}

type PostRequest struct {
	UserID         string
	Type           EntryType
	Amount         int64
	Reason         string
	IdempotencyKey string
	ActorID        string
}

type AdjustRequest struct {
	UserID         string
	Delta          int64
	Reason         string
	IdempotencyKey string
	ActorID        string
}

type account struct {
	Balance           int64
	Entries           []Entry
	ProcessedByIdemID map[string]Entry
}

type Service struct {
	mu       sync.RWMutex
	now      func() time.Time
	accounts map[string]*account
	db       *sql.DB
}

func NewService() *Service {
	return &Service{
		now:      time.Now,
		accounts: make(map[string]*account),
	}
}

func NewPostgresService(db *sql.DB) *Service {
	svc := NewService()
	svc.db = db
	return svc
}

func (s *Service) Post(req PostRequest) (Entry, int64, error) {
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return Entry{}, 0, ErrUserIDRequired
	}
	if req.Amount <= 0 {
		return Entry{}, 0, ErrInvalidAmount
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return Entry{}, 0, ErrIdempotencyRequired
	}
	if req.Type != EntryTypeCredit && req.Type != EntryTypeDebit {
		return Entry{}, 0, errors.New("wallet entry type is invalid")
	}
	if s.db != nil {
		return s.postToDB(req, userID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acct := s.ensureAccountLocked(userID)
	if existing, ok := acct.ProcessedByIdemID[req.IdempotencyKey]; ok {
		return existing, acct.Balance, nil
	}

	if req.Type == EntryTypeDebit && acct.Balance < req.Amount {
		return Entry{}, acct.Balance, ErrInsufficientFunds
	}

	now := s.now().UTC()
	entry := Entry{
		ID:             uuid.NewString(),
		UserID:         userID,
		Type:           req.Type,
		Amount:         req.Amount,
		Currency:       GameCurrency,
		Reason:         strings.TrimSpace(req.Reason),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		ActorID:        strings.TrimSpace(req.ActorID),
		CreatedAt:      now,
	}

	if entry.Type == EntryTypeCredit {
		acct.Balance += entry.Amount
	} else {
		acct.Balance -= entry.Amount
	}

	acct.Entries = append(acct.Entries, entry)
	acct.ProcessedByIdemID[entry.IdempotencyKey] = entry
	return entry, acct.Balance, nil
}

func (s *Service) postToDB(req PostRequest, userID string) (Entry, int64, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	var existing Entry
	var existingBalance int64
	row := tx.QueryRowContext(ctx, `
SELECT id, user_id, tx_type, amount_int, currency, reason, idempotency_key, created_at
FROM wallet_ledger WHERE idempotency_key = $1
`, strings.TrimSpace(req.IdempotencyKey))
	if scanErr := row.Scan(&existing.ID, &existing.UserID, &existing.Type, &existing.Amount, &existing.Currency, &existing.Reason, &existing.IdempotencyKey, &existing.CreatedAt); scanErr == nil {
		if err = tx.QueryRowContext(ctx, `SELECT balance_int FROM wallet_accounts WHERE user_id = $1`, userID).Scan(&existingBalance); err != nil {
			return Entry{}, 0, err
		}
		if err = tx.Commit(); err != nil {
			return Entry{}, 0, err
		}
		return existing, existingBalance, nil
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		return Entry{}, 0, scanErr
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO wallet_accounts (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return Entry{}, 0, err
	}

	var balance int64
	if err = tx.QueryRowContext(ctx, `SELECT balance_int FROM wallet_accounts WHERE user_id = $1 FOR UPDATE`, userID).Scan(&balance); err != nil {
		return Entry{}, 0, err
	}
	if req.Type == EntryTypeDebit && balance < req.Amount {
		return Entry{}, balance, ErrInsufficientFunds
	}

	entry := Entry{ID: uuid.NewString(), UserID: userID, Type: req.Type, Amount: req.Amount, Currency: GameCurrency, Reason: strings.TrimSpace(req.Reason), IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), ActorID: strings.TrimSpace(req.ActorID), CreatedAt: s.now().UTC()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wallet_ledger (id, user_id, tx_type, amount_int, currency, reason, idempotency_key, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, entry.ID, entry.UserID, entry.Type, entry.Amount, balance, entry.Currency, entry.Reason, entry.IdempotencyKey, entry.CreatedAt); err != nil {
		return Entry{}, 0, err
	}
	if entry.Type == EntryTypeCredit {
		balance += entry.Amount
	} else {
		balance -= entry.Amount
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wallet_accounts SET balance_int=$2, updated_at=NOW(), version=version+1 WHERE user_id=$1`, userID, balance); err != nil {
		return Entry{}, 0, err
	}
	if err = tx.Commit(); err != nil {
		return Entry{}, 0, err
	}
	return entry, balance, nil
}

func (s *Service) Adjust(req AdjustRequest) (Entry, int64, error) {
	if req.Delta == 0 {
		return Entry{}, 0, ErrInvalidDelta
	}
	t := EntryTypeCredit
	amount := req.Delta
	if req.Delta < 0 {
		t = EntryTypeDebit
		amount = -req.Delta
	}
	return s.Post(PostRequest{
		UserID:         req.UserID,
		Type:           t,
		Amount:         amount,
		Reason:         req.Reason,
		IdempotencyKey: req.IdempotencyKey,
		ActorID:        req.ActorID,
	})
}

func (s *Service) Get(userID string) Wallet {
	if s.db != nil {
		return s.getFromDB(userID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	acct, ok := s.accounts[strings.TrimSpace(userID)]
	if !ok {
		return Wallet{Balance: 0, History: []Entry{}}
	}

	history := make([]Entry, 0, len(acct.Entries))
	for _, entry := range acct.Entries {
		if isGameRelatedReason(entry.Reason) {
			continue
		}
		history = append(history, entry)
	}
	sort.Slice(history, func(i, j int) bool {
		return history[i].CreatedAt.After(history[j].CreatedAt)
	})

	return Wallet{Balance: acct.Balance, History: history}
}

func (s *Service) getFromDB(userID string) Wallet {
	lookup := strings.TrimSpace(userID)
	if lookup == "" {
		return Wallet{Balance: 0, History: []Entry{}}
	}
	ctx := context.Background()
	balance := int64(0)
	_ = s.db.QueryRowContext(ctx, `SELECT balance_int FROM wallet_accounts WHERE user_id = $1`, lookup).Scan(&balance)
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, tx_type, amount_int, currency, reason, idempotency_key, created_at FROM wallet_ledger WHERE user_id = $1 ORDER BY created_at DESC`, lookup)
	if err != nil {
		return Wallet{Balance: balance, History: []Entry{}}
	}
	defer rows.Close() //nolint:errcheck
	history := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		if scanErr := rows.Scan(&e.ID, &e.UserID, &e.Type, &e.Amount, &e.Currency, &e.Reason, &e.IdempotencyKey, &e.CreatedAt); scanErr == nil {
			history = append(history, e)
		}
	}
	return Wallet{Balance: balance, History: history}
}

func (s *Service) ensureAccountLocked(userID string) *account {
	acct, ok := s.accounts[userID]
	if ok {
		return acct
	}
	acct = &account{ProcessedByIdemID: make(map[string]Entry)}
	s.accounts[userID] = acct
	return acct
}

func isGameRelatedReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	return reason == "event_vote"
}
