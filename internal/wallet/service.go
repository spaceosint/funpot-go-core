package wallet

import (
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
	Currency       string
	Reason         string
	IdempotencyKey string
	ActorID        string
}

type AdjustRequest struct {
	UserID         string
	Delta          int64
	Currency       string
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
}

func NewService() *Service {
	return &Service{
		now:      time.Now,
		accounts: make(map[string]*account),
	}
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
		Currency:       normalizeCurrency(req.Currency),
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
		Currency:       req.Currency,
		Reason:         req.Reason,
		IdempotencyKey: req.IdempotencyKey,
		ActorID:        req.ActorID,
	})
}

func (s *Service) Get(userID string) Wallet {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acct, ok := s.accounts[strings.TrimSpace(userID)]
	if !ok {
		return Wallet{Balance: 0, History: []Entry{}}
	}

	history := append([]Entry(nil), acct.Entries...)
	sort.Slice(history, func(i, j int) bool {
		return history[i].CreatedAt.After(history[j].CreatedAt)
	})

	return Wallet{Balance: acct.Balance, History: history}
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

func normalizeCurrency(currency string) string {
	trimmed := strings.TrimSpace(currency)
	if trimmed == "" {
		return "FPC"
	}
	return strings.ToUpper(trimmed)
}
