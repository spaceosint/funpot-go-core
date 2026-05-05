package prompts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type scenarioPackageStore interface {
	List(context.Context) ([]ScenarioPackage, error)
	Create(context.Context, ScenarioPackage) (ScenarioPackage, error)
	Update(context.Context, ScenarioPackage) (ScenarioPackage, error)
	Delete(context.Context, string) error
	SetActive(context.Context, string, string, time.Time) (ScenarioPackage, error)
	GetByID(context.Context, string) (ScenarioPackage, error)
	GetActiveByGameSlug(context.Context, string) (ScenarioPackage, error)
}

type PostgresScenarioPackageStore struct {
	db *sql.DB
}

func NewPostgresScenarioPackageStore(db *sql.DB) *PostgresScenarioPackageStore {
	return &PostgresScenarioPackageStore{db: db}
}

func (s *PostgresScenarioPackageStore) List(ctx context.Context) ([]ScenarioPackage, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, version, game_slug, llm_model_config_id, is_active,
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
ORDER BY game_slug ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]ScenarioPackage, 0)
	for rows.Next() {
		item, err := scanScenarioPackage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresScenarioPackageStore) Create(ctx context.Context, item ScenarioPackage) (ScenarioPackage, error) {
	if item.ID == "" {
		item.ID = "scenario-pkg-" + uuid.NewString()
	}
	if strings.TrimSpace(item.GameSlug) == "" {
		item.GameSlug = "global"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM llm_scenarios WHERE game_slug = $1`, item.GameSlug).Scan(&version); err != nil {
		return ScenarioPackage{}, err
	}
	item.Version = version

	var hasAny bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM llm_scenarios WHERE game_slug = $1)`, item.GameSlug).Scan(&hasAny); err != nil {
		return ScenarioPackage{}, err
	}
	if !hasAny {
		item.IsActive = true
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = item.CreatedAt
	}

	stepsJSON, _, err := encodeScenarioPackagePayload(item)
	if err != nil {
		return ScenarioPackage{}, err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO llm_scenarios (
	id, game_slug, name, version, model_config_id, initial_node_id,
	nodes_json, transitions_json, metadata, is_active,
	created_by, activated_by, created_at, activated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13, $14)`,
		item.ID, item.GameSlug, item.Name, item.Version, item.LLMModelConfigID,
		initialStepID(item.Steps), stepsJSON, legacyStepTransitionsJSON(item.Transitions), scenarioMetadataJSON(item),
		item.IsActive,
		item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt),
	)
	if err != nil {
		return ScenarioPackage{}, err
	}
	return item, nil
}

func (s *PostgresScenarioPackageStore) Update(ctx context.Context, item ScenarioPackage) (ScenarioPackage, error) {
	stepsJSON, _, err := encodeScenarioPackagePayload(item)
	if err != nil {
		return ScenarioPackage{}, err
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE llm_scenarios
SET game_slug = $2,
	name = $3,
	model_config_id = $4,
	initial_node_id = $5,
	nodes_json = $6::jsonb,
	transitions_json = $7::jsonb,
	metadata = $8::jsonb,
	is_active = $9,
	activated_by = $10,
	activated_at = $11
WHERE id = $1`,
		item.ID, item.GameSlug, item.Name, item.LLMModelConfigID, initialStepID(item.Steps), stepsJSON, legacyStepTransitionsJSON(item.Transitions), scenarioMetadataJSON(item),
		item.IsActive, item.ActivatedBy, nullableTime(item.ActivatedAt),
	)
	if err != nil {
		return ScenarioPackage{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ScenarioPackage{}, ErrScenarioPackageNotFound
	}
	return s.GetByID(ctx, item.ID)
}

func (s *PostgresScenarioPackageStore) Delete(ctx context.Context, id string) error {
	item, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `DELETE FROM llm_scenarios WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrScenarioPackageNotFound
	}

	if item.IsActive {
		var replacementID string
		err = tx.QueryRowContext(ctx, `
SELECT id
FROM llm_scenarios
WHERE game_slug = $1
ORDER BY version DESC, created_at DESC, id DESC
LIMIT 1`, item.GameSlug).Scan(&replacementID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if replacementID != "" {
			now := time.Now().UTC()
			if _, err := tx.ExecContext(ctx, `UPDATE llm_scenarios SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1`, replacementID, item.ActivatedBy, now); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *PostgresScenarioPackageStore) SetActive(ctx context.Context, id string, actorID string, now time.Time) (ScenarioPackage, error) {
	item, err := s.GetByID(ctx, id)
	if err != nil {
		return ScenarioPackage{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScenarioPackage{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `UPDATE llm_scenarios SET is_active = FALSE WHERE game_slug = $1 AND is_active = TRUE`, item.GameSlug); err != nil {
		return ScenarioPackage{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE llm_scenarios SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1`, id, actorID, now)
	if err != nil {
		return ScenarioPackage{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ScenarioPackage{}, ErrScenarioPackageNotFound
	}

	if err := tx.Commit(); err != nil {
		return ScenarioPackage{}, err
	}
	return s.GetByID(ctx, id)
}

func (s *PostgresScenarioPackageStore) GetByID(ctx context.Context, id string) (ScenarioPackage, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, version, game_slug, model_config_id, is_active,
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE id = $1`, id)
	item, err := scanScenarioPackage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioPackage{}, ErrScenarioPackageNotFound
	}
	return item, err
}

func (s *PostgresScenarioPackageStore) GetActiveByGameSlug(ctx context.Context, gameSlug string) (ScenarioPackage, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, version, game_slug, model_config_id, is_active,
       nodes_json, transitions_json, metadata, created_by, activated_by, created_at, activated_at
FROM llm_scenarios
WHERE game_slug = $1 AND is_active = TRUE
LIMIT 1`, gameSlug)
	item, err := scanScenarioPackage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioPackage{}, ErrScenarioPackageNotFound
	}
	return item, err
}

type scenarioPackageScanner interface {
	Scan(dest ...any) error
}

type transitionsPayload struct {
	StepTransitions    []ScenarioTransition        `json:"stepTransitions"`
	PackageTransitions []ScenarioPackageTransition `json:"packageTransitions"`
	FinalStateOptions  []ScenarioFinalStateOption  `json:"finalStateOptions"`
	FinalCondition     string                      `json:"finalCondition,omitempty"`
}

func scanScenarioPackage(scanner scenarioPackageScanner) (ScenarioPackage, error) {
	var item ScenarioPackage
	var activatedAt sql.NullTime
	var stepsRaw []byte
	var transitionsRaw []byte
	var metadataRaw []byte
	err := scanner.Scan(
		&item.ID,
		&item.Name,
		&item.Version,
		&item.GameSlug,
		&item.LLMModelConfigID,
		&item.IsActive,
		&stepsRaw,
		&transitionsRaw,
		&metadataRaw,
		&item.CreatedBy,
		&item.ActivatedBy,
		&item.CreatedAt,
		&activatedAt,
	)
	if err != nil {
		return ScenarioPackage{}, err
	}
	if len(stepsRaw) > 0 {
		if err := json.Unmarshal(stepsRaw, &item.Steps); err != nil {
			return ScenarioPackage{}, fmt.Errorf("unmarshal steps_json: %w", err)
		}
	}
	if len(transitionsRaw) > 0 {
		var payload transitionsPayload
		if err := json.Unmarshal(transitionsRaw, &payload); err == nil && (payload.StepTransitions != nil || payload.PackageTransitions != nil) {
			item.Transitions = payload.StepTransitions
			item.PackageTransitions = payload.PackageTransitions
			item.FinalStateOptions = payload.FinalStateOptions
			item.FinalCondition = strings.TrimSpace(payload.FinalCondition)
		} else if err := json.Unmarshal(transitionsRaw, &item.Transitions); err != nil {
			return ScenarioPackage{}, fmt.Errorf("unmarshal transitions_json: %w", err)
		}
	}
	if len(metadataRaw) > 0 {
		var payload transitionsPayload
		if err := json.Unmarshal(metadataRaw, &payload); err == nil {
			item.PackageTransitions = payload.PackageTransitions
			item.FinalStateOptions = payload.FinalStateOptions
			item.FinalCondition = strings.TrimSpace(payload.FinalCondition)
		}
	}
	item.Transitions = cloneScenarioTransitions(item.Transitions)
	item.PackageTransitions = cloneScenarioPackageTransitions(item.PackageTransitions)
	item.FinalStateOptions = cloneFinalStateOptions(item.FinalStateOptions)
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

func initialStepID(steps []ScenarioStep) string {
	for _, step := range steps {
		if step.Initial {
			return step.ID
		}
	}
	if len(steps) > 0 {
		return steps[0].ID
	}
	return "initial"
}

func legacyStepTransitionsJSON(transitions []ScenarioTransition) []byte {
	raw, _ := json.Marshal(transitions)
	return raw
}

func scenarioMetadataJSON(item ScenarioPackage) []byte {
	raw, _ := json.Marshal(transitionsPayload{
		PackageTransitions: item.PackageTransitions,
		FinalStateOptions:  item.FinalStateOptions,
		FinalCondition:     strings.TrimSpace(item.FinalCondition),
	})
	return raw
}

func encodeScenarioPackagePayload(item ScenarioPackage) ([]byte, []byte, error) {
	stepsJSON, err := json.Marshal(item.Steps)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal steps: %w", err)
	}
	transitionsJSON, err := json.Marshal(transitionsPayload{
		StepTransitions:    item.Transitions,
		PackageTransitions: item.PackageTransitions,
		FinalStateOptions:  item.FinalStateOptions,
		FinalCondition:     strings.TrimSpace(item.FinalCondition),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal transitions: %w", err)
	}
	return stepsJSON, transitionsJSON, nil
}
