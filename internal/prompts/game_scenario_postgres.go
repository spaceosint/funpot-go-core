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

type gameScenarioStore interface {
	List(context.Context) ([]GameScenario, error)
	Create(context.Context, GameScenario) (GameScenario, error)
	Update(context.Context, GameScenario) (GameScenario, error)
	Delete(context.Context, string) error
	SetActive(context.Context, string, string, time.Time) (GameScenario, error)
	GetByID(context.Context, string) (GameScenario, error)
	GetActiveByGameSlug(context.Context, string) (GameScenario, error)
}

type PostgresGameScenarioStore struct {
	db *sql.DB
}

func NewPostgresGameScenarioStore(db *sql.DB) *PostgresGameScenarioStore {
	return &PostgresGameScenarioStore{db: db}
}

func (s *PostgresGameScenarioStore) List(ctx context.Context) ([]GameScenario, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_game_scenarios
ORDER BY game_slug ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	items := make([]GameScenario, 0)
	for rows.Next() {
		item, err := scanGameScenario(rows)
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

func (s *PostgresGameScenarioStore) Create(ctx context.Context, item GameScenario) (GameScenario, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(item.GameSlug) == "" {
		item.GameSlug = "global"
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM llm_game_scenarios WHERE game_slug = $1`, item.GameSlug).Scan(&version); err != nil {
		return GameScenario{}, err
	}
	item.Version = version

	var hasActive bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM llm_game_scenarios WHERE is_active = TRUE)`).Scan(&hasActive); err != nil {
		return GameScenario{}, err
	}
	if !hasActive {
		item.IsActive = true
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = item.CreatedAt
	}

	nodesJSON, transitionsJSON, err := encodeGameScenarioPayload(item)
	if err != nil {
		return GameScenario{}, err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO llm_game_scenarios (
	id, game_slug, name, version, is_active, initial_node_id,
	nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, $12)`,
		item.ID, item.GameSlug, item.Name, item.Version, item.IsActive, item.InitialNodeID,
		nodesJSON, transitionsJSON, item.CreatedBy, item.ActivatedBy, item.CreatedAt, nullableTime(item.ActivatedAt),
	)
	if err != nil {
		return GameScenario{}, err
	}
	return item, nil
}

func (s *PostgresGameScenarioStore) Update(ctx context.Context, item GameScenario) (GameScenario, error) {
	nodesJSON, transitionsJSON, err := encodeGameScenarioPayload(item)
	if err != nil {
		return GameScenario{}, err
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE llm_game_scenarios
SET game_slug = $2,
	name = $3,
	is_active = $4,
	initial_node_id = $5,
	nodes_json = $6::jsonb,
	transitions_json = $7::jsonb,
	activated_by = $8,
	activated_at = $9
WHERE id = $1`,
		item.ID, item.GameSlug, item.Name, item.IsActive, item.InitialNodeID,
		nodesJSON, transitionsJSON, item.ActivatedBy, nullableTime(item.ActivatedAt),
	)
	if err != nil {
		return GameScenario{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	return s.GetByID(ctx, item.ID)
}

func (s *PostgresGameScenarioStore) Delete(ctx context.Context, id string) error {
	item, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `DELETE FROM llm_game_scenarios WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrGameScenarioNotFound
	}

	if item.IsActive {
		var replacementID string
		err = tx.QueryRowContext(ctx, `
SELECT id
FROM llm_game_scenarios
ORDER BY created_at DESC, id DESC
LIMIT 1`).Scan(&replacementID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if replacementID != "" {
			now := time.Now().UTC()
			if _, err := tx.ExecContext(ctx, `UPDATE llm_game_scenarios SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1`, replacementID, item.ActivatedBy, now); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *PostgresGameScenarioStore) SetActive(ctx context.Context, id string, actorID string, now time.Time) (GameScenario, error) {
	if _, err := s.GetByID(ctx, id); err != nil {
		return GameScenario{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GameScenario{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `UPDATE llm_game_scenarios SET is_active = FALSE, activated_by = '', activated_at = NULL WHERE is_active = TRUE`); err != nil {
		return GameScenario{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE llm_game_scenarios SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1`, id, actorID, now)
	if err != nil {
		return GameScenario{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return GameScenario{}, ErrGameScenarioNotFound
	}

	if err := tx.Commit(); err != nil {
		return GameScenario{}, err
	}
	return s.GetByID(ctx, id)
}

func (s *PostgresGameScenarioStore) GetByID(ctx context.Context, id string) (GameScenario, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_game_scenarios
WHERE id = $1`, id)
	item, err := scanGameScenario(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	return item, err
}

func (s *PostgresGameScenarioStore) GetActiveByGameSlug(ctx context.Context, gameSlug string) (GameScenario, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_game_scenarios
WHERE game_slug = $1 AND is_active = TRUE
LIMIT 1`, gameSlug)
	item, err := scanGameScenario(row)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GameScenario{}, err
	}
	fallback := s.db.QueryRowContext(ctx, `
SELECT id, name, version, game_slug, is_active, initial_node_id,
       nodes_json, transitions_json, created_by, activated_by, created_at, activated_at
FROM llm_game_scenarios
WHERE is_active = TRUE
ORDER BY created_at DESC, id DESC
LIMIT 1`)
	item, err = scanGameScenario(fallback)
	if errors.Is(err, sql.ErrNoRows) {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	return item, err
}

type gameScenarioScanner interface {
	Scan(dest ...any) error
}

func scanGameScenario(scanner gameScenarioScanner) (GameScenario, error) {
	var item GameScenario
	var activatedAt sql.NullTime
	var nodesRaw []byte
	var transitionsRaw []byte
	err := scanner.Scan(
		&item.ID,
		&item.Name,
		&item.Version,
		&item.GameSlug,
		&item.IsActive,
		&item.InitialNodeID,
		&nodesRaw,
		&transitionsRaw,
		&item.CreatedBy,
		&item.ActivatedBy,
		&item.CreatedAt,
		&activatedAt,
	)
	if err != nil {
		return GameScenario{}, err
	}
	if len(nodesRaw) > 0 {
		if err := json.Unmarshal(nodesRaw, &item.Nodes); err != nil {
			return GameScenario{}, fmt.Errorf("unmarshal nodes_json: %w", err)
		}
	}
	if len(transitionsRaw) > 0 {
		if err := json.Unmarshal(transitionsRaw, &item.Transitions); err != nil {
			return GameScenario{}, fmt.Errorf("unmarshal transitions_json: %w", err)
		}
	}
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	return item, nil
}

func encodeGameScenarioPayload(item GameScenario) ([]byte, []byte, error) {
	nodesJSON, err := json.Marshal(item.Nodes)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal nodes_json: %w", err)
	}
	transitionsJSON, err := json.Marshal(item.Transitions)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal transitions_json: %w", err)
	}
	return nodesJSON, transitionsJSON, nil
}
