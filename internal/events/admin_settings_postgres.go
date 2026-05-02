package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

const adminGeneralSettingsKey = "admin_general_settings"

type PostgresSettingsStore struct {
	db *sql.DB
}

func NewPostgresSettingsStore(db *sql.DB) *PostgresSettingsStore {
	return &PostgresSettingsStore{db: db}
}

func (s *PostgresSettingsStore) Load(ctx context.Context) (Settings, bool, error) {
	if s == nil || s.db == nil {
		return Settings{}, false, nil
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM config WHERE key = $1`, adminGeneralSettingsKey).Scan(&payload)
	if err != nil {
		if err == sql.ErrNoRows {
			return Settings{}, false, nil
		}
		return Settings{}, false, fmt.Errorf("load admin general settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(payload, &settings); err != nil {
		return Settings{}, false, fmt.Errorf("decode admin general settings: %w", err)
	}
	return settings, true, nil
}

func (s *PostgresSettingsStore) Save(ctx context.Context, settings Settings) error {
	if s == nil || s.db == nil {
		return nil
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode admin general settings: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO config (key, value_json, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE
SET value_json = EXCLUDED.value_json,
    updated_at = NOW()`, adminGeneralSettingsKey, payload)
	if err != nil {
		return fmt.Errorf("save admin general settings: %w", err)
	}
	return nil
}
