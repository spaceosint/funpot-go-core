package prompts

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type PostgresModelConfigStore struct {
	db *sql.DB
}

func NewPostgresModelConfigStore(db *sql.DB) *PostgresModelConfigStore {
	return &PostgresModelConfigStore{db: db}
}

func (s *PostgresModelConfigStore) List(ctx context.Context) ([]LLMModelConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, model, COALESCE(metadata_json, ''), temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at
FROM llm_model_configs
ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]LLMModelConfig, 0)
	for rows.Next() {
		item, err := scanLLMModelConfig(rows)
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

func (s *PostgresModelConfigStore) Create(ctx context.Context, item LLMModelConfig) (LLMModelConfig, error) {
	if item.ID == "" {
		item.ID = "llm-model-cfg-" + uuid.NewString()
	}
	var hasAny bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM llm_model_configs)`).Scan(&hasAny); err != nil {
		return LLMModelConfig{}, err
	}
	if !hasAny {
		item.IsActive = true
		item.ActivatedAt = item.CreatedAt
		item.ActivatedBy = item.CreatedBy
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO llm_model_configs (
	id, name, model, metadata_json, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms,
	min_confidence, is_active, created_by, activated_by, created_at, activated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		item.ID, item.Name, item.Model, item.MetadataJSON, item.Temperature, item.MaxTokens, item.TimeoutMS, item.RetryCount,
		item.BackoffMS, item.CooldownMS, item.MinConfidence, item.IsActive, item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt),
	)
	if err != nil {
		return LLMModelConfig{}, err
	}
	return item, nil
}

func (s *PostgresModelConfigStore) Update(ctx context.Context, item LLMModelConfig) (LLMModelConfig, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE llm_model_configs
SET name = $2, model = $3, metadata_json = $4, temperature = $5, max_tokens = $6, timeout_ms = $7,
	retry_count = $8, backoff_ms = $9, cooldown_ms = $10, min_confidence = $11
WHERE id = $1`,
		item.ID, item.Name, item.Model, item.MetadataJSON, item.Temperature, item.MaxTokens, item.TimeoutMS,
		item.RetryCount, item.BackoffMS, item.CooldownMS, item.MinConfidence,
	)
	if err != nil {
		return LLMModelConfig{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	return s.GetByID(ctx, item.ID)
}

func (s *PostgresModelConfigStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM llm_model_configs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrLLMModelConfigNotFound
	}
	return nil
}

func (s *PostgresModelConfigStore) SetActive(ctx context.Context, id string, actorID string, now time.Time) (LLMModelConfig, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LLMModelConfig{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `UPDATE llm_model_configs SET is_active = FALSE WHERE is_active = TRUE`)
	if err != nil {
		return LLMModelConfig{}, err
	}
	_ = res
	res, err = tx.ExecContext(ctx, `UPDATE llm_model_configs SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1`, id, actorID, now)
	if err != nil {
		return LLMModelConfig{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	if err := tx.Commit(); err != nil {
		return LLMModelConfig{}, err
	}
	return s.GetByID(ctx, id)
}

func (s *PostgresModelConfigStore) GetByID(ctx context.Context, id string) (LLMModelConfig, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, model, COALESCE(metadata_json, ''), temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at
FROM llm_model_configs
WHERE id = $1`, id)
	item, err := scanLLMModelConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	return item, err
}

type llmModelConfigScanner interface {
	Scan(dest ...any) error
}

func scanLLMModelConfig(scanner llmModelConfigScanner) (LLMModelConfig, error) {
	var item LLMModelConfig
	var activatedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.Name, &item.Model, &item.MetadataJSON, &item.Temperature, &item.MaxTokens, &item.TimeoutMS, &item.RetryCount,
		&item.BackoffMS, &item.CooldownMS, &item.MinConfidence, &item.IsActive, &item.CreatedBy, &item.ActivatedBy, &item.CreatedAt, &activatedAt,
	)
	if err != nil {
		return LLMModelConfig{}, err
	}
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
