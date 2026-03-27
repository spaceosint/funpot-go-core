package prompts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const trackerConfigDDL = `
CREATE TABLE IF NOT EXISTS llm_prompt_versions (
    id TEXT PRIMARY KEY,
    stage TEXT NOT NULL,
    position INTEGER NOT NULL,
    version INTEGER NOT NULL,
    template TEXT NOT NULL,
    model TEXT NOT NULL,
    temperature DOUBLE PRECISION NOT NULL,
    max_tokens INTEGER NOT NULL,
    timeout_ms INTEGER NOT NULL,
    retry_count INTEGER NOT NULL,
    backoff_ms INTEGER NOT NULL,
    cooldown_ms INTEGER NOT NULL,
    min_confidence DOUBLE PRECISION NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(stage) > 0),
    CHECK (char_length(template) > 0),
    CHECK (char_length(model) > 0),
    CHECK (position > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_prompt_versions_active_stage
    ON llm_prompt_versions (stage) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_prompt_versions_order
    ON llm_prompt_versions (position ASC, stage ASC, version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS llm_state_schema_versions (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    fields_json JSONB NOT NULL,
    state_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    delta_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    initial_state_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_state_schema_versions_active_game
    ON llm_state_schema_versions (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_state_schema_versions_order
    ON llm_state_schema_versions (game_slug ASC, version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS llm_rule_set_versions (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    rule_items_json JSONB NOT NULL,
    finalization_rules_json JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_rule_set_versions_active_game
    ON llm_rule_set_versions (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_rule_set_versions_order
    ON llm_rule_set_versions (game_slug ASC, version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS llm_scenario_packages (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    steps_json JSONB NOT NULL,
    transitions_json JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_scenario_packages_active_game
    ON llm_scenario_packages (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_llm_scenario_packages_order
    ON llm_scenario_packages (game_slug ASC, version DESC, created_at DESC);
`

func (s *Service) ensureSchema(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.schemaReady {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, trackerConfigDDL); err != nil {
		return fmt.Errorf("ensure tracker config schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS initial_state_json JSONB NOT NULL DEFAULT '{}'::jsonb`); err != nil {
		return fmt.Errorf("ensure llm_state_schema_versions.initial_state_json: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS state_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`); err != nil {
		return fmt.Errorf("ensure llm_state_schema_versions.state_schema_json: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE llm_state_schema_versions ADD COLUMN IF NOT EXISTS delta_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb`); err != nil {
		return fmt.Errorf("ensure llm_state_schema_versions.delta_schema_json: %w", err)
	}
	s.schemaReady = true
	return nil
}

func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

func scanPrompt(row scanner) (PromptVersion, error) {
	var item PromptVersion
	var activatedAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.Stage,
		&item.Position,
		&item.Version,
		&item.Template,
		&item.Model,
		&item.Temperature,
		&item.MaxTokens,
		&item.TimeoutMS,
		&item.RetryCount,
		&item.BackoffMS,
		&item.CooldownMS,
		&item.MinConfidence,
		&item.IsActive,
		&item.CreatedBy,
		&item.ActivatedBy,
		&item.CreatedAt,
		&activatedAt,
	); err != nil {
		return PromptVersion{}, err
	}
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

func scanStateSchema(row scanner) (StateSchemaVersion, error) {
	var item StateSchemaVersion
	var fields []byte
	var initialState []byte
	var activatedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.GameSlug, &item.Name, &item.Description, &item.Version, &fields, &initialState, &item.IsActive, &item.CreatedBy, &item.ActivatedBy, &item.CreatedAt, &activatedAt); err != nil {
		return StateSchemaVersion{}, err
	}
	if err := json.Unmarshal(fields, &item.Fields); err != nil {
		return StateSchemaVersion{}, err
	}
	item.InitialStateJSON = strings.TrimSpace(string(initialState))
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

func scanRuleSet(row scanner) (RuleSetVersion, error) {
	var item RuleSetVersion
	var ruleItems []byte
	var finalizationRules []byte
	var activatedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.GameSlug, &item.Name, &item.Description, &item.Version, &ruleItems, &finalizationRules, &item.IsActive, &item.CreatedBy, &item.ActivatedBy, &item.CreatedAt, &activatedAt); err != nil {
		return RuleSetVersion{}, err
	}
	if err := json.Unmarshal(ruleItems, &item.RuleItems); err != nil {
		return RuleSetVersion{}, err
	}
	if err := json.Unmarshal(finalizationRules, &item.FinalizationRules); err != nil {
		return RuleSetVersion{}, err
	}
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

func scanScenarioPackage(row scanner) (ScenarioPackage, error) {
	var item ScenarioPackage
	var steps []byte
	var transitions []byte
	var activatedAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.GameSlug,
		&item.Name,
		&item.Version,
		&steps,
		&transitions,
		&item.IsActive,
		&item.CreatedBy,
		&item.ActivatedBy,
		&item.CreatedAt,
		&activatedAt,
	); err != nil {
		return ScenarioPackage{}, err
	}
	if err := json.Unmarshal(steps, &item.Steps); err != nil {
		return ScenarioPackage{}, err
	}
	if err := json.Unmarshal(transitions, &item.Transitions); err != nil {
		return ScenarioPackage{}, err
	}
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

type scanner interface{ Scan(dest ...any) error }

func (s *Service) listPromptsDB(ctx context.Context) ([]PromptVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM llm_prompt_versions ORDER BY position ASC, stage ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PromptVersion
	for rows.Next() {
		item, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listActivePromptsDB(ctx context.Context) ([]PromptVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM llm_prompt_versions WHERE is_active = TRUE ORDER BY position ASC, stage ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PromptVersion
	for rows.Next() {
		item, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getActivePromptByStageDB(ctx context.Context, stage string) (PromptVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return PromptVersion{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM llm_prompt_versions WHERE stage = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`, strings.TrimSpace(stage))
	item, err := scanPrompt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return PromptVersion{}, ErrNotFound
		}
		return PromptVersion{}, err
	}
	return item, nil
}

func (s *Service) createPromptDB(ctx context.Context, req CreateRequest) (PromptVersion, error) {
	if err := ValidateCreateRequest(req); err != nil {
		return PromptVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return PromptVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PromptVersion{}, err
	}
	defer tx.Rollback()
	stage := strings.TrimSpace(req.Stage)
	position := req.Position
	if position <= 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM llm_prompt_versions`).Scan(&position); err != nil {
			return PromptVersion{}, err
		}
	}
	var version, existing int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM llm_prompt_versions WHERE stage = $1`, stage).Scan(&version); err != nil {
		return PromptVersion{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_prompt_versions WHERE stage = $1`, stage).Scan(&existing); err != nil {
		return PromptVersion{}, err
	}
	now := time.Now().UTC()
	item := PromptVersion{
		ID:            "prompt-" + uuid.NewString(),
		Stage:         stage,
		Position:      position,
		Version:       version,
		Template:      strings.TrimSpace(req.Template),
		Model:         strings.TrimSpace(req.Model),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		TimeoutMS:     req.TimeoutMS,
		RetryCount:    req.RetryCount,
		BackoffMS:     req.BackoffMS,
		CooldownMS:    req.CooldownMS,
		MinConfidence: req.MinConfidence,
		IsActive:      existing == 0,
		CreatedBy:     strings.TrimSpace(req.ActorID),
		ActivatedBy:   "",
		CreatedAt:     now,
	}
	if item.IsActive {
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO llm_prompt_versions (id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		item.ID, item.Stage, item.Position, item.Version, item.Template, item.Model, item.Temperature, item.MaxTokens, item.TimeoutMS, item.RetryCount, item.BackoffMS, item.CooldownMS, item.MinConfidence, item.IsActive, item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt))
	if err != nil {
		return PromptVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return PromptVersion{}, err
	}
	return item, nil
}

func (s *Service) activatePromptDB(ctx context.Context, id, actorID string) (PromptVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return PromptVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PromptVersion{}, err
	}
	defer tx.Rollback()
	var stage string
	if err := tx.QueryRowContext(ctx, `SELECT stage FROM llm_prompt_versions WHERE id = $1`, strings.TrimSpace(id)).Scan(&stage); err != nil {
		if err == sql.ErrNoRows {
			return PromptVersion{}, ErrNotFound
		}
		return PromptVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE llm_prompt_versions SET is_active = FALSE WHERE stage = $1`, stage); err != nil {
		return PromptVersion{}, err
	}
	row := tx.QueryRowContext(ctx, `UPDATE llm_prompt_versions SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1 RETURNING id, stage, position, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at`, strings.TrimSpace(id), strings.TrimSpace(actorID), time.Now().UTC())
	item, err := scanPrompt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return PromptVersion{}, ErrNotFound
		}
		return PromptVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return PromptVersion{}, err
	}
	return item, nil
}

func (s *Service) listStateSchemasDB(ctx context.Context) ([]StateSchemaVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, game_slug, name, description, version, fields_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_state_schema_versions ORDER BY game_slug ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StateSchemaVersion
	for rows.Next() {
		item, err := scanStateSchema(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) createStateSchemaDB(ctx context.Context, req StateSchemaCreateRequest) (StateSchemaVersion, error) {
	if err := ValidateStateSchemaCreateRequest(req); err != nil {
		return StateSchemaVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return StateSchemaVersion{}, err
	}
	fieldsJSON, err := marshalJSON(req.Fields)
	if err != nil {
		return StateSchemaVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StateSchemaVersion{}, err
	}
	defer tx.Rollback()
	gameSlug := strings.TrimSpace(req.GameSlug)
	var version, existing int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM llm_state_schema_versions WHERE game_slug = $1`, gameSlug).Scan(&version); err != nil {
		return StateSchemaVersion{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_state_schema_versions WHERE game_slug = $1`, gameSlug).Scan(&existing); err != nil {
		return StateSchemaVersion{}, err
	}
	now := time.Now().UTC()
	item := StateSchemaVersion{
		ID:               "state-schema-" + uuid.NewString(),
		GameSlug:         gameSlug,
		Name:             strings.TrimSpace(req.Name),
		Description:      strings.TrimSpace(req.Description),
		Version:          version,
		Fields:           append([]StateFieldDefinition(nil), req.Fields...),
		InitialStateJSON: buildInitialStateJSON(req.Fields),
		IsActive:         existing == 0,
		CreatedBy:        strings.TrimSpace(req.ActorID),
		CreatedAt:        now,
	}
	if item.IsActive {
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO llm_state_schema_versions (id, game_slug, name, description, version, fields_json, state_schema_json, delta_schema_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,'{}'::jsonb,'{}'::jsonb,$7::jsonb,$8,$9,$10,$11,$12)`,
		item.ID, item.GameSlug, item.Name, item.Description, item.Version, fieldsJSON, item.InitialStateJSON, item.IsActive, item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt))
	if err != nil {
		return StateSchemaVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return StateSchemaVersion{}, err
	}
	return item, nil
}

func (s *Service) getStateSchemaDB(ctx context.Context, id string) (StateSchemaVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return StateSchemaVersion{}, err
	}
	item, err := scanStateSchema(s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, description, version, fields_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_state_schema_versions WHERE id = $1`, strings.TrimSpace(id)))
	if err != nil {
		if err == sql.ErrNoRows {
			return StateSchemaVersion{}, ErrStateSchemaNotFound
		}
		return StateSchemaVersion{}, err
	}
	return item, nil
}

func (s *Service) updateStateSchemaDB(ctx context.Context, id string, req StateSchemaCreateRequest) (StateSchemaVersion, error) {
	if err := ValidateStateSchemaCreateRequest(req); err != nil {
		return StateSchemaVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return StateSchemaVersion{}, err
	}
	fieldsJSON, err := marshalJSON(req.Fields)
	if err != nil {
		return StateSchemaVersion{}, err
	}
	initialStateRaw := buildInitialStateJSON(req.Fields)
	row := s.db.QueryRowContext(ctx, `UPDATE llm_state_schema_versions SET game_slug = $2, name = $3, description = $4, fields_json = $5, state_schema_json = '{}'::jsonb, delta_schema_json = '{}'::jsonb, initial_state_json = $6::jsonb WHERE id = $1 RETURNING id, game_slug, name, description, version, fields_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at`,
		strings.TrimSpace(id), strings.TrimSpace(req.GameSlug), strings.TrimSpace(req.Name), strings.TrimSpace(req.Description), fieldsJSON, initialStateRaw)
	item, err := scanStateSchema(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return StateSchemaVersion{}, ErrStateSchemaNotFound
		}
		return StateSchemaVersion{}, err
	}
	return item, nil
}

func (s *Service) deleteStateSchemaDB(ctx context.Context, id string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var gameSlug string
	var wasActive bool
	if err := tx.QueryRowContext(ctx, `SELECT game_slug, is_active FROM llm_state_schema_versions WHERE id = $1`, strings.TrimSpace(id)).Scan(&gameSlug, &wasActive); err != nil {
		if err == sql.ErrNoRows {
			return ErrStateSchemaNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM llm_state_schema_versions WHERE id = $1`, strings.TrimSpace(id)); err != nil {
		return err
	}
	if wasActive {
		if _, err := tx.ExecContext(ctx, `UPDATE llm_state_schema_versions SET is_active = TRUE, activated_at = $2 WHERE id = (SELECT id FROM llm_state_schema_versions WHERE game_slug = $1 ORDER BY version DESC, created_at DESC LIMIT 1)`, gameSlug, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) activateStateSchemaDB(ctx context.Context, id, actorID string) (StateSchemaVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return StateSchemaVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StateSchemaVersion{}, err
	}
	defer tx.Rollback()
	var gameSlug string
	if err := tx.QueryRowContext(ctx, `SELECT game_slug FROM llm_state_schema_versions WHERE id = $1`, strings.TrimSpace(id)).Scan(&gameSlug); err != nil {
		if err == sql.ErrNoRows {
			return StateSchemaVersion{}, ErrStateSchemaNotFound
		}
		return StateSchemaVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE llm_state_schema_versions SET is_active = FALSE WHERE game_slug = $1`, gameSlug); err != nil {
		return StateSchemaVersion{}, err
	}
	item, err := scanStateSchema(tx.QueryRowContext(ctx, `UPDATE llm_state_schema_versions SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1 RETURNING id, game_slug, name, description, version, fields_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at`,
		strings.TrimSpace(id), strings.TrimSpace(actorID), time.Now().UTC()))
	if err != nil {
		if err == sql.ErrNoRows {
			return StateSchemaVersion{}, ErrStateSchemaNotFound
		}
		return StateSchemaVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return StateSchemaVersion{}, err
	}
	return item, nil
}

func (s *Service) getActiveStateSchemaDB(ctx context.Context, gameSlug string) (StateSchemaVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return StateSchemaVersion{}, err
	}
	item, err := scanStateSchema(s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, description, version, fields_json, initial_state_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_state_schema_versions WHERE game_slug = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`, strings.TrimSpace(gameSlug)))
	if err != nil {
		if err == sql.ErrNoRows {
			return StateSchemaVersion{}, ErrStateSchemaNotFound
		}
		return StateSchemaVersion{}, err
	}
	return item, nil
}

func (s *Service) listRuleSetsDB(ctx context.Context) ([]RuleSetVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_rule_set_versions ORDER BY game_slug ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RuleSetVersion
	for rows.Next() {
		item, err := scanRuleSet(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) createRuleSetDB(ctx context.Context, req RuleSetCreateRequest) (RuleSetVersion, error) {
	if err := ValidateRuleSetCreateRequest(req); err != nil {
		return RuleSetVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return RuleSetVersion{}, err
	}
	ruleItemsJSON, err := marshalJSON(req.RuleItems)
	if err != nil {
		return RuleSetVersion{}, err
	}
	finalizationJSON, err := marshalJSON(req.FinalizationRules)
	if err != nil {
		return RuleSetVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuleSetVersion{}, err
	}
	defer tx.Rollback()
	gameSlug := strings.TrimSpace(req.GameSlug)
	var version, existing int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM llm_rule_set_versions WHERE game_slug = $1`, gameSlug).Scan(&version); err != nil {
		return RuleSetVersion{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_rule_set_versions WHERE game_slug = $1`, gameSlug).Scan(&existing); err != nil {
		return RuleSetVersion{}, err
	}
	now := time.Now().UTC()
	item := RuleSetVersion{ID: "rule-set-" + uuid.NewString(), GameSlug: gameSlug, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Version: version, RuleItems: append([]RuleItem(nil), req.RuleItems...), FinalizationRules: append([]RuleCondition(nil), req.FinalizationRules...), IsActive: existing == 0, CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now}
	if item.IsActive {
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO llm_rule_set_versions (id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		item.ID, item.GameSlug, item.Name, item.Description, item.Version, ruleItemsJSON, finalizationJSON, item.IsActive, item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt))
	if err != nil {
		return RuleSetVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return RuleSetVersion{}, err
	}
	return item, nil
}

func (s *Service) getRuleSetDB(ctx context.Context, id string) (RuleSetVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return RuleSetVersion{}, err
	}
	item, err := scanRuleSet(s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_rule_set_versions WHERE id = $1`, strings.TrimSpace(id)))
	if err != nil {
		if err == sql.ErrNoRows {
			return RuleSetVersion{}, ErrRuleSetNotFound
		}
		return RuleSetVersion{}, err
	}
	return item, nil
}

func (s *Service) updateRuleSetDB(ctx context.Context, id string, req RuleSetCreateRequest) (RuleSetVersion, error) {
	if err := ValidateRuleSetCreateRequest(req); err != nil {
		return RuleSetVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return RuleSetVersion{}, err
	}
	ruleItemsJSON, err := marshalJSON(req.RuleItems)
	if err != nil {
		return RuleSetVersion{}, err
	}
	finalizationJSON, err := marshalJSON(req.FinalizationRules)
	if err != nil {
		return RuleSetVersion{}, err
	}
	item, err := scanRuleSet(s.db.QueryRowContext(ctx, `UPDATE llm_rule_set_versions SET game_slug = $2, name = $3, description = $4, rule_items_json = $5, finalization_rules_json = $6 WHERE id = $1 RETURNING id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at`,
		strings.TrimSpace(id), strings.TrimSpace(req.GameSlug), strings.TrimSpace(req.Name), strings.TrimSpace(req.Description), ruleItemsJSON, finalizationJSON))
	if err != nil {
		if err == sql.ErrNoRows {
			return RuleSetVersion{}, ErrRuleSetNotFound
		}
		return RuleSetVersion{}, err
	}
	return item, nil
}

func (s *Service) deleteRuleSetDB(ctx context.Context, id string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var gameSlug string
	var wasActive bool
	if err := tx.QueryRowContext(ctx, `SELECT game_slug, is_active FROM llm_rule_set_versions WHERE id = $1`, strings.TrimSpace(id)).Scan(&gameSlug, &wasActive); err != nil {
		if err == sql.ErrNoRows {
			return ErrRuleSetNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM llm_rule_set_versions WHERE id = $1`, strings.TrimSpace(id)); err != nil {
		return err
	}
	if wasActive {
		if _, err := tx.ExecContext(ctx, `UPDATE llm_rule_set_versions SET is_active = TRUE, activated_at = $2 WHERE id = (SELECT id FROM llm_rule_set_versions WHERE game_slug = $1 ORDER BY version DESC, created_at DESC LIMIT 1)`, gameSlug, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) activateRuleSetDB(ctx context.Context, id, actorID string) (RuleSetVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return RuleSetVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuleSetVersion{}, err
	}
	defer tx.Rollback()
	var gameSlug string
	if err := tx.QueryRowContext(ctx, `SELECT game_slug FROM llm_rule_set_versions WHERE id = $1`, strings.TrimSpace(id)).Scan(&gameSlug); err != nil {
		if err == sql.ErrNoRows {
			return RuleSetVersion{}, ErrRuleSetNotFound
		}
		return RuleSetVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE llm_rule_set_versions SET is_active = FALSE WHERE game_slug = $1`, gameSlug); err != nil {
		return RuleSetVersion{}, err
	}
	item, err := scanRuleSet(tx.QueryRowContext(ctx, `UPDATE llm_rule_set_versions SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1 RETURNING id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at`,
		strings.TrimSpace(id), strings.TrimSpace(actorID), time.Now().UTC()))
	if err != nil {
		if err == sql.ErrNoRows {
			return RuleSetVersion{}, ErrRuleSetNotFound
		}
		return RuleSetVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return RuleSetVersion{}, err
	}
	return item, nil
}

func (s *Service) getActiveRuleSetDB(ctx context.Context, gameSlug string) (RuleSetVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return RuleSetVersion{}, err
	}
	item, err := scanRuleSet(s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, description, version, rule_items_json, finalization_rules_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_rule_set_versions WHERE game_slug = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`, strings.TrimSpace(gameSlug)))
	if err != nil {
		if err == sql.ErrNoRows {
			return RuleSetVersion{}, ErrRuleSetNotFound
		}
		return RuleSetVersion{}, err
	}
	return item, nil
}

func (s *Service) listScenarioPackagesDB(ctx context.Context) ([]ScenarioPackage, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_scenario_packages ORDER BY game_slug ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]ScenarioPackage, 0)
	for rows.Next() {
		item, err := scanScenarioPackage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) createScenarioPackageDB(ctx context.Context, req ScenarioPackageCreateRequest) (ScenarioPackage, error) {
	if err := ValidateScenarioPackageCreateRequest(req); err != nil {
		return ScenarioPackage{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioPackage{}, err
	}
	gameSlug := strings.TrimSpace(req.GameSlug)
	if gameSlug == "" {
		gameSlug = "global"
	}
	stepsJSON, err := marshalJSON(req.Steps)
	if err != nil {
		return ScenarioPackage{}, err
	}
	transitionsJSON, err := marshalJSON(req.Transitions)
	if err != nil {
		return ScenarioPackage{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScenarioPackage{}, err
	}
	defer tx.Rollback()

	var version int
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM llm_scenario_packages WHERE game_slug = $1`, gameSlug).Scan(&version); err != nil {
		return ScenarioPackage{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_scenario_packages WHERE game_slug = $1`, gameSlug).Scan(&existing); err != nil {
		return ScenarioPackage{}, err
	}
	now := time.Now().UTC()
	item := ScenarioPackage{
		ID:          "scenario-pkg-" + uuid.NewString(),
		Name:        strings.TrimSpace(req.Name),
		GameSlug:    gameSlug,
		Version:     version,
		Steps:       append([]ScenarioStep(nil), req.Steps...),
		Transitions: append([]ScenarioTransition(nil), req.Transitions...),
		IsActive:    existing == 0,
		CreatedBy:   strings.TrimSpace(req.ActorID),
		CreatedAt:   now,
	}
	if item.IsActive {
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = now
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO llm_scenario_packages (id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		item.ID, item.GameSlug, item.Name, item.Version, stepsJSON, transitionsJSON, item.IsActive, item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt),
	); err != nil {
		return ScenarioPackage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScenarioPackage{}, err
	}
	return item, nil
}

func (s *Service) getActiveScenarioPackageDB(ctx context.Context, gameSlug string) (ScenarioPackage, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioPackage{}, err
	}
	key := strings.TrimSpace(gameSlug)
	if key == "" {
		key = "global"
	}
	item, err := scanScenarioPackage(s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_scenario_packages WHERE game_slug = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`, key))
	if err != nil {
		if err == sql.ErrNoRows {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		return ScenarioPackage{}, err
	}
	return item, nil
}

func (s *Service) getScenarioPackageDB(ctx context.Context, id string) (ScenarioPackage, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioPackage{}, err
	}
	item, err := scanScenarioPackage(s.db.QueryRowContext(
		ctx,
		`SELECT id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at FROM llm_scenario_packages WHERE id = $1`,
		strings.TrimSpace(id),
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		return ScenarioPackage{}, err
	}
	return item, nil
}

func (s *Service) updateScenarioPackageDB(ctx context.Context, id string, req ScenarioPackageCreateRequest) (ScenarioPackage, error) {
	if err := ValidateScenarioPackageCreateRequest(req); err != nil {
		return ScenarioPackage{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioPackage{}, err
	}
	gameSlug := strings.TrimSpace(req.GameSlug)
	if gameSlug == "" {
		gameSlug = "global"
	}
	stepsJSON, err := marshalJSON(req.Steps)
	if err != nil {
		return ScenarioPackage{}, err
	}
	transitionsJSON, err := marshalJSON(req.Transitions)
	if err != nil {
		return ScenarioPackage{}, err
	}
	item, err := scanScenarioPackage(s.db.QueryRowContext(
		ctx,
		`UPDATE llm_scenario_packages SET game_slug = $2, name = $3, steps_json = $4, transitions_json = $5 WHERE id = $1 RETURNING id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at`,
		strings.TrimSpace(id),
		gameSlug,
		strings.TrimSpace(req.Name),
		stepsJSON,
		transitionsJSON,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		return ScenarioPackage{}, err
	}
	return item, nil
}

func (s *Service) deleteScenarioPackageDB(ctx context.Context, id string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var gameSlug string
	var wasActive bool
	if err := tx.QueryRowContext(
		ctx,
		`DELETE FROM llm_scenario_packages WHERE id = $1 RETURNING game_slug, is_active`,
		strings.TrimSpace(id),
	).Scan(&gameSlug, &wasActive); err != nil {
		if err == sql.ErrNoRows {
			return ErrScenarioPackageNotFound
		}
		return err
	}

	if wasActive {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE llm_scenario_packages SET is_active = TRUE WHERE id = (
				SELECT id FROM llm_scenario_packages WHERE game_slug = $1 ORDER BY version DESC, created_at DESC LIMIT 1
			)`,
			gameSlug,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) activateScenarioPackageDB(ctx context.Context, id, actorID string) (ScenarioPackage, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioPackage{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScenarioPackage{}, err
	}
	defer tx.Rollback()

	var gameSlug string
	if err := tx.QueryRowContext(ctx, `SELECT game_slug FROM llm_scenario_packages WHERE id = $1`, strings.TrimSpace(id)).Scan(&gameSlug); err != nil {
		if err == sql.ErrNoRows {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		return ScenarioPackage{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE llm_scenario_packages SET is_active = FALSE WHERE game_slug = $1`, gameSlug); err != nil {
		return ScenarioPackage{}, err
	}
	item, err := scanScenarioPackage(tx.QueryRowContext(
		ctx,
		`UPDATE llm_scenario_packages SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1 RETURNING id, game_slug, name, version, steps_json, transitions_json, is_active, created_by, activated_by, created_at, activated_at`,
		strings.TrimSpace(id),
		strings.TrimSpace(actorID),
		time.Now().UTC(),
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		return ScenarioPackage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScenarioPackage{}, err
	}
	return item, nil
}
