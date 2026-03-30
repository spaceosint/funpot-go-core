package prompts

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresScenarioPackageStoreCreate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresScenarioPackageStore(db)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(version), 0) + 1 FROM llm_scenario_packages WHERE game_slug = $1`)).
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (SELECT 1 FROM llm_scenario_packages WHERE game_slug = $1)`)).
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta(`
INSERT INTO llm_scenario_packages (
	id, game_slug, name, version, llm_model_config_id,
	steps_json, transitions_json, is_active,
	created_by, activated_by, created_at, activated_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12)`)).
		WithArgs(
			sqlmock.AnyArg(),
			"global",
			"pkg",
			1,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			true,
			"admin",
			"admin",
			now,
			now,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	item, err := store.Create(context.Background(), ScenarioPackage{
		Name:      "pkg",
		GameSlug:  "global",
		CreatedBy: "admin",
		CreatedAt: now,
		Steps: []ScenarioStep{{
			ID:    "s1",
			Order: 1,
		}},
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	if !item.IsActive {
		t.Fatalf("expected first package to be active")
	}
	if item.Version != 1 {
		t.Fatalf("expected version 1, got %d", item.Version)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresScenarioPackageStoreGetActiveByGameSlug(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresScenarioPackageStore(db)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	steps := `[{"id":"s1","name":"step-1","order":1}]`
	transitions := `[]`

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, name, version, game_slug, llm_model_config_id, is_active,
       steps_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_scenario_packages
WHERE game_slug = $1 AND is_active = TRUE
LIMIT 1`)).
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "llm_model_config_id", "is_active",
			"steps_json", "transitions_json", "created_by", "activated_by", "created_at", "activated_at",
		}).AddRow("scenario-pkg-1", "pkg", 2, "global", "", true, []byte(steps), []byte(transitions), "admin", "admin", now, now))

	item, err := store.GetActiveByGameSlug(context.Background(), "global")
	if err != nil {
		t.Fatalf("store.GetActiveByGameSlug: %v", err)
	}
	if item.ID != "scenario-pkg-1" {
		t.Fatalf("unexpected id: %s", item.ID)
	}
	if len(item.Steps) != 1 || item.Steps[0].ID != "s1" {
		t.Fatalf("unexpected decoded steps: %#v", item.Steps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
