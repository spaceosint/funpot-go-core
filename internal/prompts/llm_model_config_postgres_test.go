package prompts

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresModelConfigStoreList(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresModelConfigStore(db)

	rows := sqlmock.NewRows([]string{
		"id", "name", "model", "metadata", "temperature", "max_tokens", "timeout_ms",
		"retry_count", "backoff_ms", "cooldown_ms", "min_confidence", "is_active",
		"created_by", "activated_by", "created_at", "activated_at",
	})
	mock.ExpectQuery("SELECT id, name, model, COALESCE\\(metadata::text, '\\{\\}'\\)").
		WillReturnRows(rows)

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %d", len(items))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected mock expectations: %v", err)
	}
}

func TestPostgresModelConfigStoreListTwice(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresModelConfigStore(db)

	query := "SELECT id, name, model, COALESCE\\(metadata::text, '\\{\\}'\\)"
	rows := sqlmock.NewRows([]string{
		"id", "name", "model", "metadata", "temperature", "max_tokens", "timeout_ms",
		"retry_count", "backoff_ms", "cooldown_ms", "min_confidence", "is_active",
		"created_by", "activated_by", "created_at", "activated_at",
	})
	mock.ExpectQuery(query).WillReturnRows(rows)
	mock.ExpectQuery(query).WillReturnRows(rows)

	if _, err := store.List(context.Background()); err != nil {
		t.Fatalf("first list failed: %v", err)
	}
	if _, err := store.List(context.Background()); err != nil {
		t.Fatalf("second list failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected mock expectations: %v", err)
	}
}
