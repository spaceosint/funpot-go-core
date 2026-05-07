package streamers

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

func TestServiceSubmitPersistsPostgresStreamerWithUUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close() //nolint:errcheck

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id::text
FROM streamers
WHERE lower(twitch_username) = lower($1)
FOR UPDATE`)).
		WithArgs("best_streamer").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(regexp.QuoteMeta(`
INSERT INTO streamers (id, twitch_username, display_name, status, metadata)
VALUES ($1, $2, $3, $4, $5::jsonb)`)).
		WithArgs(sqlmock.AnyArg(), "best_streamer", "Best Streamer", "active", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	svc := NewServiceWithValidator(validatorStub{displayName: "Best Streamer", miniIconURL: "https://cdn.twitch.tv/icon.png"})
	svc.SetStreamerRepository(NewPostgresStreamerRepository(db))
	var hookStreamerID string
	svc.SetSubmissionHook(func(_ context.Context, streamerID string) error {
		hookStreamerID = streamerID
		return nil
	})

	sub, err := svc.Submit(context.Background(), "Best_Streamer", "user-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if _, err := uuid.Parse(sub.ID); err != nil {
		t.Fatalf("expected UUID streamer id, got %q: %v", sub.ID, err)
	}
	if hookStreamerID != sub.ID {
		t.Fatalf("submission hook streamerID = %q, want %q", hookStreamerID, sub.ID)
	}
	items := svc.List(context.Background(), "best", "pending", 1)
	if len(items) != 1 || items[0].ID != sub.ID {
		t.Fatalf("expected cached submitted streamer after db insert, got %#v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestPostgresStreamerRepositoryMapsMetadataSubmissionStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close() //nolint:errcheck

	id := uuid.NewString()
	metadata, err := json.Marshal(streamerMetadata{
		Platform:         "twitch",
		MiniIconURL:      "https://cdn.twitch.tv/icon.png",
		Online:           true,
		Viewers:          321,
		AddedBy:          "user-1",
		SubmissionStatus: "pending",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id::text, twitch_username, display_name, status, metadata
FROM streamers
WHERE id = $1`)).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"id", "twitch_username", "display_name", "status", "metadata"}).
			AddRow(id, "best_streamer", "Best Streamer", "active", metadata))

	repo := NewPostgresStreamerRepository(db)
	item, ok, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if !ok {
		t.Fatalf("expected streamer to be found")
	}
	if item.Status != "pending" || item.Platform != "twitch" || !item.Online || item.Viewers != 321 || item.AddedBy != "user-1" || item.MiniIconURL == "" {
		t.Fatalf("unexpected mapped streamer: %#v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
