package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDatabaseConfig(t *testing.T) {
	t.Setenv("FUNPOT_DATABASE_ENABLED", "true")
	t.Setenv("FUNPOT_DATABASE_HOST", "localhost")
	t.Setenv("FUNPOT_DATABASE_PORT", "5432")
	t.Setenv("FUNPOT_DATABASE_NAME", "funpot")
	t.Setenv("FUNPOT_DATABASE_USER", "funpot")
	t.Setenv("FUNPOT_DATABASE_PASSWORD", "funpot")
	t.Setenv("FUNPOT_DATABASE_SSLMODE", "disable")
	t.Setenv("FUNPOT_DATABASE_MAX_OPEN_CONNS", "20")
	t.Setenv("FUNPOT_DATABASE_MIN_OPEN_CONNS", "2")
	t.Setenv("FUNPOT_DATABASE_CONNECT_TIMEOUT", "7s")
	t.Setenv("FUNPOT_DATABASE_HEALTHCHECK_TIMEOUT", "2s")
	t.Setenv("FUNPOT_CLIENT_STARS_RATE", "1.5")
	t.Setenv("FUNPOT_CLIENT_MIN_VIEWERS", "150")
	t.Setenv("FUNPOT_CLIENT_CURRENCIES", "INT,USD")
	t.Setenv("FUNPOT_CLIENT_LIMIT_VOTE_PER_MIN", "40")
	t.Setenv("FUNPOT_AUTH_REFRESH_ENABLED", "true")
	t.Setenv("FUNPOT_AUTH_REFRESH_TTL", "240h")
	t.Setenv("FUNPOT_AUTH_REFRESH_MAX_SESSIONS", "3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.Database.Enabled {
		t.Fatalf("expected database enabled")
	}
	if cfg.Database.DSN() == "" {
		t.Fatalf("expected database connection settings to be set")
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
	if cfg.Client.StarsRate != 1.5 {
		t.Fatalf("expected stars rate 1.5, got %v", cfg.Client.StarsRate)
	}
	if cfg.Client.MinViewers != 150 {
		t.Fatalf("expected min viewers 150, got %d", cfg.Client.MinViewers)
	}
	if len(cfg.Client.Currencies) != 2 {
		t.Fatalf("expected 2 currencies, got %d", len(cfg.Client.Currencies))
	}
	if cfg.Client.VotePerMin != 40 {
		t.Fatalf("expected vote per min 40, got %d", cfg.Client.VotePerMin)
	}
	if !cfg.Auth.Refresh.Enabled {
		t.Fatalf("expected refresh auth enabled")
	}
	if cfg.Auth.Refresh.TTL != 240*time.Hour {
		t.Fatalf("expected refresh ttl 240h, got %s", cfg.Auth.Refresh.TTL)
	}
	if cfg.Auth.Refresh.MaxSessionsPerUser != 3 {
		t.Fatalf("expected refresh max sessions 3, got %d", cfg.Auth.Refresh.MaxSessionsPerUser)
	}
}

func TestLoadDatabaseValidation(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		unsets []string
	}{
		{
			name: "missing host when enabled",
			env: map[string]string{
				"FUNPOT_DATABASE_ENABLED": "true",
				"FUNPOT_DATABASE_NAME":    "funpot",
				"FUNPOT_DATABASE_USER":    "funpot",
			},
			unsets: []string{"FUNPOT_DATABASE_HOST"},
		},
		{
			name: "invalid pool bounds",
			env: map[string]string{
				"FUNPOT_DATABASE_ENABLED":        "true",
				"FUNPOT_DATABASE_HOST":           "localhost",
				"FUNPOT_DATABASE_NAME":           "funpot",
				"FUNPOT_DATABASE_USER":           "funpot",
				"FUNPOT_DATABASE_MAX_OPEN_CONNS": "2",
				"FUNPOT_DATABASE_MIN_OPEN_CONNS": "5",
			},
		},
		{
			name: "invalid port",
			env: map[string]string{
				"FUNPOT_DATABASE_ENABLED": "true",
				"FUNPOT_DATABASE_HOST":    "localhost",
				"FUNPOT_DATABASE_PORT":    "70000",
				"FUNPOT_DATABASE_NAME":    "funpot",
				"FUNPOT_DATABASE_USER":    "funpot",
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
