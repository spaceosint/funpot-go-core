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
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS initial_state_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS state_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS delta_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
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

func TestPostgresServiceListStateSchemas(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresService(db)
	now := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(trackerConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS initial_state_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS state_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS delta_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, game_slug, name, description, version, fields_json, state_schema_json, delta_schema_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_state_schema_versions ORDER BY game_slug ASC, version DESC, created_at DESC`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_slug", "name", "description", "version", "fields_json", "state_schema_json", "delta_schema_json", "initial_state_json", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow("state-schema-1", "cs2", "Schema", "", 1, `[{"key":"score.ct","type":"number"}]`, `{"type":"object"}`, `{"type":"object","properties":{"chunk_time_range":{"type":"string"}}}`, `{"session_status":{"value":"unknown"}}`, true, "admin-1", "admin-1", now, now))

	items := svc.ListStateSchemas(context.Background())
	if len(items) != 1 || items[0].GameSlug != "cs2" {
		t.Fatalf("ListStateSchemas() = %#v", items)
	}
	if items[0].StateSchemaJSON == "" {
		t.Fatalf("expected state schema json to be set: %#v", items[0])
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
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS initial_state_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS state_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS delta_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 0))
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
