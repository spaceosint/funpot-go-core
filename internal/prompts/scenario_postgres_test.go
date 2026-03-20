package prompts

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresScenarioServiceCreateAndReadGlobalDetector(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresScenarioService(db)
	mock.ExpectExec(regexp.QuoteMeta(scenarioConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(version), 0) + 1 FROM prompt_global_detectors`)).WillReturnRows(sqlmock.NewRows([]string{"next_version"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE prompt_global_detectors SET is_active = FALSE WHERE is_active = TRUE`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO prompt_global_detectors (id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,TRUE,$13,$14,$15,$16)`)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	created, err := svc.CreateGlobalDetector(context.Background(), CreateRequest{
		Stage:         "global_detector",
		Template:      "detect current game",
		Model:         "gemini-2.0-flash",
		Temperature:   0.1,
		MaxTokens:     256,
		TimeoutMS:     1000,
		RetryCount:    1,
		BackoffMS:     200,
		CooldownMS:    100,
		MinConfidence: 0.6,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateGlobalDetector() error = %v", err)
	}
	if created.ID == "" || !created.IsActive {
		t.Fatalf("unexpected created detector: %#v", created)
	}

	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM prompt_global_detectors WHERE is_active = TRUE ORDER BY version DESC LIMIT 1`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "stage", "version", "template", "model", "temperature", "max_tokens", "timeout_ms", "retry_count", "backoff_ms", "cooldown_ms", "min_confidence", "is_active", "created_by", "activated_by", "created_at", "activated_at"}).
			AddRow(created.ID, created.Stage, created.Version, created.Template, created.Model, created.Temperature, created.MaxTokens, created.TimeoutMS, created.RetryCount, created.BackoffMS, created.CooldownMS, created.MinConfidence, true, created.CreatedBy, created.ActivatedBy, now, now))

	active, err := svc.GetActiveGlobalDetector(context.Background())
	if err != nil {
		t.Fatalf("GetActiveGlobalDetector() error = %v", err)
	}
	if active.ID != created.ID {
		t.Fatalf("active.ID = %q, want %q", active.ID, created.ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresScenarioServiceListScenarios(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	svc := NewPostgresScenarioService(db)
	steps := `[{
		"id":"scenario-step-1",
		"code":"match_start",
		"title":"Match start",
		"position":1,
		"prompt":{
			"id":"prompt-template-1",
			"stage":"match_start",
			"template":"Has a match started?",
			"model":"gemini-2.0-flash",
			"temperature":0.1,
			"maxTokens":256,
			"timeoutMs":2000,
			"retryCount":1,
			"backoffMs":250,
			"cooldownMs":1000,
			"minConfidence":0.7,
			"createdBy":"admin-1",
			"createdAt":"2026-03-20T00:00:00Z"
		}
	}]`
	transitions := `[{"id":"transition-1","fromStepCode":"match_start","outcome":"match_started","terminal":true}]`
	mock.ExpectExec(regexp.QuoteMeta(scenarioConfigDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json FROM prompt_scenarios ORDER BY game_slug ASC, version DESC, created_at DESC`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_slug", "name", "description", "version", "is_active", "created_by", "activated_by", "created_at", "activated_at", "steps_json", "transitions_json"}).
			AddRow("scenario-1", "counter_strike", "CS flow", "desc", 1, true, "admin-1", "admin-1", time.Now().UTC(), time.Now().UTC(), []byte(steps), []byte(transitions)))

	items := svc.ListScenarios(context.Background())
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].GameSlug != "counter_strike" || len(items[0].Steps) != 1 {
		t.Fatalf("unexpected scenario payload: %#v", items[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
