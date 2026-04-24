package prompts

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresGameScenarioStoreCreate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresGameScenarioStore(db)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(version), 0) + 1 FROM llm_game_scenarios WHERE game_slug = $1`)).
		WithArgs("cs2").
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (SELECT 1 FROM llm_game_scenarios WHERE is_active = TRUE)`)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta(`
INSERT INTO llm_game_scenarios (
	id, game_slug, name, version, is_active, initial_node_id,
	nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, $12)`)).
		WithArgs(
			sqlmock.AnyArg(),
			"cs2",
			"scenario",
			1,
			true,
			"n1",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			"admin",
			"admin",
			now,
			now,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	item, err := store.Create(context.Background(), GameScenario{
		Name:          "scenario",
		GameSlug:      "cs2",
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: "pkg-1"}},
		CreatedBy:     "admin",
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	if !item.IsActive {
		t.Fatalf("expected first scenario to be active")
	}
	if item.Version != 1 {
		t.Fatalf("expected version 1, got %d", item.Version)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresGameScenarioStoreGetActiveByGameSlugFallsBackToGlobal(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresGameScenarioStore(db)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	nodes := `[{"id":"n1","scenarioPackageId":"pkg-1"}]`
	transitions := `[]`

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_game_scenarios
WHERE game_slug = $1 AND is_active = TRUE
LIMIT 1`)).
		WithArgs("dota2").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "created_by", "activated_by", "created_at", "activated_at",
		}))

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_game_scenarios
WHERE is_active = TRUE
ORDER BY created_at DESC, id DESC
LIMIT 1`)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "created_by", "activated_by", "created_at", "activated_at",
		}).AddRow("game-scenario-1", "scenario", 1, "cs2", true, "n1", []byte(nodes), []byte(transitions), "admin", "admin", now, now))

	item, err := store.GetActiveByGameSlug(context.Background(), "dota2")
	if err != nil {
		t.Fatalf("store.GetActiveByGameSlug: %v", err)
	}
	if item.ID != "game-scenario-1" {
		t.Fatalf("unexpected id: %s", item.ID)
	}
	if item.GameSlug != "cs2" {
		t.Fatalf("unexpected game slug: %s", item.GameSlug)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
