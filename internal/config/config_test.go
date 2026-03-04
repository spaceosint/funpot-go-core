package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDatabaseConfig(t *testing.T) {
	t.Setenv("FUNPOT_DATABASE_ENABLED", "true")
	t.Setenv("FUNPOT_DATABASE_URL", "postgres://funpot:funpot@localhost:5432/funpot?sslmode=disable")
	t.Setenv("FUNPOT_DATABASE_MAX_OPEN_CONNS", "20")
	t.Setenv("FUNPOT_DATABASE_MIN_OPEN_CONNS", "2")
	t.Setenv("FUNPOT_DATABASE_CONNECT_TIMEOUT", "7s")
	t.Setenv("FUNPOT_DATABASE_HEALTHCHECK_TIMEOUT", "2s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.Database.Enabled {
		t.Fatalf("expected database enabled")
	}
	if cfg.Database.URL == "" {
		t.Fatalf("expected database url to be set")
	}
	if cfg.Database.MaxOpenConns != 20 {
		t.Fatalf("expected max open conns 20, got %d", cfg.Database.MaxOpenConns)
	}
	if cfg.Database.MinOpenConns != 2 {
		t.Fatalf("expected min open conns 2, got %d", cfg.Database.MinOpenConns)
	}
	if cfg.Database.ConnectTimeout != 7*time.Second {
		t.Fatalf("expected connect timeout 7s, got %s", cfg.Database.ConnectTimeout)
	}
	if cfg.Database.HealthcheckPing != 2*time.Second {
		t.Fatalf("expected healthcheck timeout 2s, got %s", cfg.Database.HealthcheckPing)
	}
}

func TestLoadDatabaseValidation(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		unsets []string
	}{
		{
			name: "missing url when enabled",
			env: map[string]string{
				"FUNPOT_DATABASE_ENABLED": "true",
			},
			unsets: []string{"FUNPOT_DATABASE_URL"},
		},
		{
			name: "invalid pool bounds",
			env: map[string]string{
				"FUNPOT_DATABASE_ENABLED":        "true",
				"FUNPOT_DATABASE_URL":            "postgres://funpot:funpot@localhost:5432/funpot?sslmode=disable",
				"FUNPOT_DATABASE_MAX_OPEN_CONNS": "2",
				"FUNPOT_DATABASE_MIN_OPEN_CONNS": "5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range tt.unsets {
				if err := os.Unsetenv(key); err != nil {
					t.Fatalf("unset %s: %v", key, err)
				}
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			if _, err := Load(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}
