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

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(version), 0) + 1 FROM llm_scenarios WHERE COALESCE(metadata->>'gameSlug', game_slug) = $1 AND metadata->>'kind' = 'game_scenario'`)).
		WithArgs("cs2").
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (SELECT 1 FROM llm_scenarios WHERE is_active = TRUE AND metadata->>'kind' = 'game_scenario')`)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta(`
INSERT INTO llm_scenarios (
	id, game_slug, name, version, is_active, initial_node_id,
	nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13)`)).
		WithArgs(
			sqlmock.AnyArg(),
			"game_scenario:cs2",
			"scenario",
			1,
			true,
			"n1",
			sqlmock.AnyArg(),
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

func TestPostgresGameScenarioStoreListReturnsAdminGameSlug(t *testing.T) {
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
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE metadata->>'kind' = 'game_scenario'
ORDER BY COALESCE(metadata->>'gameSlug', game_slug) ASC, version DESC, created_at DESC`)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "metadata", "created_by", "activated_by", "created_at", "activated_at",
		}).AddRow("game-scenario-1", "scenario", 1, "game_scenario:cs2", true, "n1", []byte(nodes), []byte(transitions), []byte(`{"gameSlug":"cs2","kind":"game_scenario"}`), "admin", "admin", now, now))

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	if items[0].GameSlug != "cs2" {
		t.Fatalf("expected admin game slug cs2, got %s", items[0].GameSlug)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresGameScenarioStoreListDecodesSnakeCaseTerminalConditions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresGameScenarioStore(db)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	nodes := `[{"id":"n1","scenario_package_id":"pkg-1"}]`
	transitions := `[{"id":"tr-1","from_node_id":"n1","to_node_id":"n1","condition":"winner == \"ct\"","priority":1,"terminal_conditions":[{"id":"tm-1","game_title":{"ru":"Победа"},"default_language":"ru","outcome_templates":[{"id":"ct","title":{"ru":"CT"},"condition":"winner == \"ct\"","priority":10}],"priority":20}]}]`

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE metadata->>'kind' = 'game_scenario'
ORDER BY COALESCE(metadata->>'gameSlug', game_slug) ASC, version DESC, created_at DESC`)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "metadata", "created_by", "activated_by", "created_at", "activated_at",
		}).AddRow("game-scenario-1", "scenario", 1, "game_scenario:cs2", true, "n1", []byte(nodes), []byte(transitions), []byte(`{"gameSlug":"cs2","kind":"game_scenario"}`), "admin", "admin", now, now))

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 1 || len(items[0].Transitions) != 1 {
		t.Fatalf("expected one scenario transition, got %#v", items)
	}
	transition := items[0].Transitions[0]
	if transition.FromNodeID != "n1" || transition.ToNodeID != "n1" {
		t.Fatalf("expected snake_case node ids to decode, got %#v", transition)
	}
	if len(transition.TerminalConditions) != 1 {
		t.Fatalf("expected snake_case terminal conditions to decode, got %#v", transition.TerminalConditions)
	}
	terminal := transition.TerminalConditions[0]
	if terminal.DefaultLanguage != "ru" || terminal.GameTitle["ru"] != "Победа" || len(terminal.OutcomeTemplates) != 1 {
		t.Fatalf("unexpected terminal condition: %#v", terminal)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresGameScenarioStoreUpdateUsesStorageSlugAndReloads(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck

	store := NewPostgresGameScenarioStore(db)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	nodes := `[{"id":"n1","scenarioPackageId":"pkg-1"}]`
	transitions := `[]`

	mock.ExpectExec(regexp.QuoteMeta(`
UPDATE llm_scenarios
SET game_slug = $2,
	name = $3,
	is_active = $4,
	initial_node_id = $5,
	nodes_json = $6::jsonb,
	transitions_json = $7::jsonb,
	metadata = $8::jsonb,
	activated_by = $9,
	activated_at = $10
WHERE id = $1 AND metadata->>'kind' = 'game_scenario'`)).
		WithArgs("game-scenario-1", "game_scenario:cs2", "updated", true, "n1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "admin", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE id = $1 AND metadata->>'kind' = 'game_scenario'`)).
		WithArgs("game-scenario-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "metadata", "created_by", "activated_by", "created_at", "activated_at",
		}).AddRow("game-scenario-1", "updated", 2, "game_scenario:cs2", true, "n1", []byte(nodes), []byte(transitions), []byte(`{"gameSlug":"cs2","kind":"game_scenario"}`), "admin", "admin", now, now))

	item, err := store.Update(context.Background(), GameScenario{
		ID:            "game-scenario-1",
		Name:          "updated",
		GameSlug:      "cs2",
		Version:       2,
		IsActive:      true,
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: "pkg-1"}},
		ActivatedBy:   "admin",
		ActivatedAt:   now,
	})
	if err != nil {
		t.Fatalf("store.Update: %v", err)
	}
	if item.GameSlug != "cs2" || item.Name != "updated" {
		t.Fatalf("unexpected updated item: %#v", item)
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
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE COALESCE(metadata->>'gameSlug', game_slug) = $1 AND is_active = TRUE AND metadata->>'kind' = 'game_scenario'
LIMIT 1`)).
		WithArgs("dota2").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "metadata", "created_by", "activated_by", "created_at", "activated_at",
		}))

	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE is_active = TRUE AND metadata->>'kind' = 'game_scenario'
ORDER BY created_at DESC, id DESC
LIMIT 1`)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "game_slug", "is_active", "initial_node_id",
			"nodes_json", "transitions_json", "metadata", "created_by", "activated_by", "created_at", "activated_at",
		}).AddRow("game-scenario-1", "scenario", 1, "cs2", true, "n1", []byte(nodes), []byte(transitions), []byte(`{"gameSlug":"cs2","kind":"game_scenario"}`), "admin", "admin", now, now))

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
