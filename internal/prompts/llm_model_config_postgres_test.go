package prompts

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresModelConfigStoreListEnsuresMetadataColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresModelConfigStore(db)

	mock.ExpectExec("ALTER TABLE llm_model_configs").
		WillReturnResult(sqlmock.NewResult(0, 0))

	rows := sqlmock.NewRows([]string{
		"id", "name", "model", "metadata_json", "temperature", "max_tokens", "timeout_ms",
		"retry_count", "backoff_ms", "cooldown_ms", "min_confidence", "is_active",
		"created_by", "activated_by", "created_at", "activated_at",
	})
	mock.ExpectQuery("SELECT id, name, model, COALESCE\\(metadata_json, ''\\)").
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

func TestPostgresModelConfigStoreEnsureSchemaRunsOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresModelConfigStore(db)

	mock.ExpectExec("ALTER TABLE llm_model_configs").
		WillReturnResult(sqlmock.NewResult(0, 0))

	query := "SELECT id, name, model, COALESCE\\(metadata_json, ''\\)"
	rows := sqlmock.NewRows([]string{
		"id", "name", "model", "metadata_json", "temperature", "max_tokens", "timeout_ms",
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
