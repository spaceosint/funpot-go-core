package wallet

import "testing"

func TestServicePostIdempotentAndInsufficientFunds(t *testing.T) {
	svc := NewService()

	entry, balance, err := svc.Post(PostRequest{UserID: "u1", Type: EntryTypeCredit, Amount: 100, IdempotencyKey: "k1", Reason: "init"})
	if err != nil {
		t.Fatalf("Post(credit) error = %v", err)
	}
	if balance != 100 {
		t.Fatalf("expected balance 100, got %d", balance)
	}

	again, againBalance, err := svc.Post(PostRequest{UserID: "u1", Type: EntryTypeCredit, Amount: 100, IdempotencyKey: "k1", Reason: "init"})
	if err != nil {
		t.Fatalf("Post(credit idempotent) error = %v", err)
	}
	if againBalance != 100 {
		t.Fatalf("expected idempotent balance 100, got %d", againBalance)
	}
	if again.ID != entry.ID {
		t.Fatalf("expected same entry id for idempotent replay")
	}

	_, _, err = svc.Post(PostRequest{UserID: "u1", Type: EntryTypeDebit, Amount: 200, IdempotencyKey: "k2", Reason: "withdraw"})
	if err == nil {
		t.Fatalf("expected insufficient funds error")
	}
}

func TestServiceAdjustDebitAndCredit(t *testing.T) {
	svc := NewService()
	if _, _, err := svc.Adjust(AdjustRequest{UserID: "u1", Delta: 50, IdempotencyKey: "adj-1", Reason: "grant"}); err != nil {
		t.Fatalf("Adjust(credit) error = %v", err)
	}
	if _, _, err := svc.Adjust(AdjustRequest{UserID: "u1", Delta: -20, IdempotencyKey: "adj-2", Reason: "penalty"}); err != nil {
		t.Fatalf("Adjust(debit) error = %v", err)
	}
	wallet := svc.Get("u1")
	if wallet.Balance != 30 {
		t.Fatalf("expected balance 30, got %d", wallet.Balance)
	}
	if len(wallet.History) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(wallet.History))
	}
}
