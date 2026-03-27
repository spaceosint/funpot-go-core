package prompts

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresServiceCreatePrompt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(version), 0) + 1 FROM llm_prompt_versions WHERE stage = $1`)).
		WithArgs("match_update").
		WillReturnRows(sqlmock.NewRows([]string{"next_version"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM llm_prompt_versions WHERE stage = $1`)).
		WithArgs("match_update").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO llm_prompt_versions (id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	item, err := svc.Create(context.Background(), CreateRequest{
		Stage:         "match_update",
		Position:      1,
		Template:      "update state",
		Model:         "gpt",
		Temperature:   0.2,
		MaxTokens:     128,
		TimeoutMS:     1000,
		RetryCount:    1,
		BackoffMS:     10,
		CooldownMS:    20,
		MinConfidence: 0.5,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !item.IsActive {
		t.Fatal("expected first prompt to be active")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}

func TestPostgresServicePromptCRUD(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	now := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM llm_prompt_versions WHERE id = $1`)).
		WithArgs("prompt-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "stage", "position", "version", "template", "model", "temperature", "max_tokens", "timeout_ms", "retry_count", "backoff_ms", "cooldown_ms", "min_confidence", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("prompt-1", "detector", 1, 1, "detect", "gpt", 0.2, 256, 1000, 1, 10, 10, 0.5, true, "admin-1", "admin-1", now, now))
	item, err := svc.Get(context.Background(), "prompt-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if item.ID != "prompt-1" {
		t.Fatalf("Get() = %#v", item)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE llm_prompt_versions SET stage = $2, position = $3, template = $4, model = $5, temperature = $6, max_tokens = $7, timeout_ms = $8, retry_count = $9, backoff_ms = $10, cooldown_ms = $11, min_confidence = $12 WHERE id = $1 RETURNING id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at`)).
		WithArgs("prompt-1", "detector", 2, "updated", "gpt-2", 0.3, 128, 900, 0, 0, 0, 0.6).
		WillReturnRows(sqlmock.NewRows([]string{"id", "stage", "position", "version", "template", "model", "temperature", "max_tokens", "timeout_ms", "retry_count", "backoff_ms", "cooldown_ms", "min_confidence", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("prompt-1", "detector", 2, 1, "updated", "gpt-2", 0.3, 128, 900, 0, 0, 0, 0.6, true, "admin-1", "admin-1", now, now))
	updated, err := svc.Update(context.Background(), "prompt-1", CreateRequest{
		Stage:         "detector",
		Position:      2,
		Template:      "updated",
		Model:         "gpt-2",
		Temperature:   0.3,
		MaxTokens:     128,
		TimeoutMS:     900,
		RetryCount:    0,
		BackoffMS:     0,
		CooldownMS:    0,
		MinConfidence: 0.6,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Template != "updated" {
		t.Fatalf("Update() = %#v", updated)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT stage, is_active FROM llm_prompt_versions WHERE id = $1`)).
		WithArgs("prompt-1").
		WillReturnRows(sqlmock.NewRows([]string{"stage", "is_active"}).AddRow("detector", true))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM llm_prompt_versions WHERE id = $1`)).
		WithArgs("prompt-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE llm_prompt_versions SET is_active = FALSE WHERE stage = $1`)).
		WithArgs("detector").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE llm_prompt_versions SET is_active = TRUE WHERE id = (SELECT id FROM llm_prompt_versions WHERE stage = $1 ORDER BY version DESC, created_at DESC LIMIT 1)`)).
		WithArgs("detector").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := svc.Delete(context.Background(), "prompt-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}

func TestPostgresServiceListStateSchemas(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	now := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, game_slug, name, description, version, fields_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_state_schema_versions ORDER BY game_slug ASC, version DESC, created_at DESC`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_slug", "name", "description", "version", "fields_json", "initial_state_json", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("state-schema-1", "cs2", "Schema", "", 1, `[{"key":"score.ct","type":"number"}]`, `{"session_status":{"value":"unknown"}}`, true, "admin-1", "admin-1", now, now))

	items := svc.ListStateSchemas(context.Background())
	if len(items) != 1 || items[0].GameSlug != "cs2" {
		t.Fatalf("ListStateSchemas() = %#v", items)
	}
	if items[0].InitialStateJSON == "" {
		t.Fatalf("expected generated initial state to be set: %#v", items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}

func TestPostgresServiceGetActiveRuleSet(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	now := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_rule_set_versions WHERE game_slug = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`)).
		WithArgs("cs2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_slug", "name", "description", "version", "rule_items_json", "finalization_rules_json", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("rule-set-1", "cs2", "Rules", "", 1, `[{"id":"rule-item-1","fieldKey":"score.ct","operation":"set","confidenceMode":"strict","finalOnly":false}]`, `[{"id":"rule-condition-1","priority":1,"condition":"final_seen","action":"finalize_win","targetField":""}]`, true, "admin-1", "admin-1", now, now))

	item, err := svc.GetActiveRuleSet(context.Background(), "cs2")
	if err != nil {
		t.Fatalf("GetActiveRuleSet() error = %v", err)
	}
	if item.ID != "rule-set-1" {
		t.Fatalf("GetActiveRuleSet() = %#v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}

func TestPostgresServiceCreateScenarioPackage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(version), 0) + 1 FROM llm_scenario_packages WHERE game_slug = $1`)).
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"next_version"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM llm_scenario_packages WHERE game_slug = $1`)).
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO llm_scenario_packages (id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	item, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:     "global-flow",
		GameSlug: "global",
		ActorID:  "admin-1",
		Steps: []ScenarioStep{
			{ID: "game_detect", Name: "Game detect", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true},
		},
		Transitions: []ScenarioTransition{},
	})
	if err != nil {
		t.Fatalf("CreateScenarioPackage() error = %v", err)
	}
	if !item.IsActive {
		t.Fatal("expected first scenario package to be active")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}

func TestPostgresServiceGetActiveScenarioPackage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	now := time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_scenario_packages WHERE game_slug = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`)).
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_slug", "name", "version", "steps_json", "transitions_json", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("scenario-pkg-1", "global", "global-flow", 1, `[{"id":"game_detect","name":"Game detect","promptTemplate":"detect","responseSchemaJson":"{}","initial":true,"order":1}]`, `[]`, true, "admin-1", "admin-1", now, now))

	item, err := svc.GetActiveScenarioPackage(context.Background(), "global")
	if err != nil {
		t.Fatalf("GetActiveScenarioPackage() error = %v", err)
	}
	if item.ID != "scenario-pkg-1" {
		t.Fatalf("GetActiveScenarioPackage() = %#v", item)
	}
	if len(item.Steps) != 1 || item.Steps[0].ID != "game_detect" {
		t.Fatalf("unexpected steps: %#v", item.Steps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}

func TestPostgresServiceUpdateScenarioPackageCrossGameDeactivates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	now := time.Date(2026, 3, 27, 13, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT game_slug, is_active, COALESCE(activated_by, ''), activated_at FROM llm_scenario_packages WHERE id = $1`)).
		WithArgs("scenario-pkg-1").
		WillReturnRows(sqlmock.NewRows([]string{"game_slug", "is_active", "activated_by", "activated_at"}).
			AddRow("global", true, "admin-1", now))
	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE llm_scenario_packages
			SET game_slug = $2,
			    name = $3,
			    steps_json = $4,
			    transitions_json = $5,
			    is_active = $6,
			    activated_by = $7,
			    activated_at = $8
		 WHERE id = $1
		 RETURNING id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at`)).
		WithArgs("scenario-pkg-1", "cs2", "cs2-flow", sqlmock.AnyArg(), sqlmock.AnyArg(), false, "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_slug", "name", "version", "steps_json", "transitions_json", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("scenario-pkg-1", "cs2", "cs2-flow", 1, `[{"id":"cs2_mode","name":"CS2 mode","gameSlug":"cs2","promptTemplate":"mode","responseSchemaJson":"{}","initial":true,"order":1}]`, `[]`, false, "admin-1", "", now, nil))
	mock.ExpectCommit()

	item, err := svc.UpdateScenarioPackage(context.Background(), "scenario-pkg-1", ScenarioPackageCreateRequest{
		Name:     "cs2-flow",
		GameSlug: "cs2",
		ActorID:  "admin-2",
		Steps: []ScenarioStep{
			{ID: "cs2_mode", Name: "CS2 mode", PromptTemplate: "mode", ResponseSchemaJSON: "{}", Initial: true},
		},
	})
	if err != nil {
		t.Fatalf("UpdateScenarioPackage() error = %v", err)
	}
	if item.IsActive {
		t.Fatalf("expected moved package to be inactive: %#v", item)
	}
	if len(item.Steps) != 1 || item.Steps[0].GameSlug != "cs2" || item.Steps[0].Order != 1 {
		t.Fatalf("expected normalized steps in updated package, got %#v", item.Steps)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("ExpectationsWereMet() error = %v", err)
	}
}
