package media

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresUploadedVideoStoreSave(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresUploadedVideoStore(db)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO streamer_uploaded_videos")).
		WithArgs("str-1", "vid-1", "title", "https://video", "2026-01-01T00:00:00Z").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Save(context.Background(), "str-1", UploadedVideo{ID: "vid-1", Title: "title", URL: "https://video", CreatedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresUploadedVideoStoreListByStreamer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresUploadedVideoStore(db)
	rows := sqlmock.NewRows([]string{"video_id", "title", "url", "created_at"}).
		AddRow("vid-2", "title-2", "https://video/2", "2026-01-02T00:00:00Z").
		AddRow("vid-1", "title-1", "https://video/1", "2026-01-01T00:00:00Z")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT video_id, title, url, created_at")).WithArgs("str-1").WillReturnRows(rows)

	items, err := store.ListByStreamer(context.Background(), "str-1")
	if err != nil {
		t.Fatalf("ListByStreamer error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items)=%d, want 2", len(items))
	}
	if items[0].ID != "vid-2" || items[1].ID != "vid-1" {
		t.Fatalf("unexpected items order: %#v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresUploadedVideoStoreDeleteByStreamer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresUploadedVideoStore(db)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM streamer_uploaded_videos WHERE streamer_id = $1")).WithArgs("str-1").WillReturnResult(sqlmock.NewResult(0, 2))

	if err := store.DeleteByStreamer(context.Background(), "str-1"); err != nil {
		t.Fatalf("DeleteByStreamer error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
