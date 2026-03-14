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
	t.Setenv("FUNPOT_REDIS_ENABLED", "true")
	t.Setenv("FUNPOT_REDIS_ADDR", "localhost:6379")
	t.Setenv("FUNPOT_REDIS_USERNAME", "redis-user")
	t.Setenv("FUNPOT_REDIS_PASSWORD", "redis-pass")
	t.Setenv("FUNPOT_REDIS_DB", "1")
	t.Setenv("FUNPOT_REDIS_CONNECT_TIMEOUT", "3s")
	t.Setenv("FUNPOT_REDIS_POOL_SIZE", "30")
	t.Setenv("FUNPOT_REDIS_MIN_IDLE_CONNS", "3")
	t.Setenv("FUNPOT_REDIS_DIAL_TIMEOUT", "4s")
	t.Setenv("FUNPOT_REDIS_READ_TIMEOUT", "5s")
	t.Setenv("FUNPOT_REDIS_WRITE_TIMEOUT", "6s")
	t.Setenv("FUNPOT_REDIS_HEALTHCHECK_TIMEOUT", "7s")

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
	if !cfg.Redis.Enabled {
		t.Fatalf("expected redis enabled")
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Fatalf("expected redis addr localhost:6379, got %q", cfg.Redis.Addr)
	}
	if cfg.Redis.DB != 1 {
		t.Fatalf("expected redis db 1, got %d", cfg.Redis.DB)
	}
	if cfg.Redis.Username != "redis-user" {
		t.Fatalf("expected redis username redis-user, got %q", cfg.Redis.Username)
	}
	if cfg.Redis.Password != "redis-pass" {
		t.Fatalf("expected redis password redis-pass, got %q", cfg.Redis.Password)
	}
	if cfg.Redis.ConnectTimeout != 3*time.Second {
		t.Fatalf("expected redis connect timeout 3s, got %s", cfg.Redis.ConnectTimeout)
	}
	if cfg.Redis.PoolSize != 30 {
		t.Fatalf("expected redis pool size 30, got %d", cfg.Redis.PoolSize)
	}
	if cfg.Redis.MinIdleConns != 3 {
		t.Fatalf("expected redis min idle conns 3, got %d", cfg.Redis.MinIdleConns)
	}
	if cfg.Redis.DialTimeout != 4*time.Second {
		t.Fatalf("expected redis dial timeout 4s, got %s", cfg.Redis.DialTimeout)
	}
	if cfg.Redis.ReadTimeout != 5*time.Second {
		t.Fatalf("expected redis read timeout 5s, got %s", cfg.Redis.ReadTimeout)
	}
	if cfg.Redis.WriteTimeout != 6*time.Second {
		t.Fatalf("expected redis write timeout 6s, got %s", cfg.Redis.WriteTimeout)
	}
	if cfg.Redis.HealthcheckPing != 7*time.Second {
		t.Fatalf("expected redis healthcheck timeout 7s, got %s", cfg.Redis.HealthcheckPing)
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
			name: "invalid redis db",
			env: map[string]string{
				"FUNPOT_REDIS_ENABLED": "true",
				"FUNPOT_REDIS_DB":      "-1",
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
		{
			name: "invalid redis pool bounds",
			env: map[string]string{
				"FUNPOT_REDIS_ENABLED":        "true",
				"FUNPOT_REDIS_POOL_SIZE":      "2",
				"FUNPOT_REDIS_MIN_IDLE_CONNS": "5",
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
