package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/users"
)

func TestSetupRefreshSessionStoreUsesRedisWhenEnabled(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	defer mr.Close()

	authService := buildAuthServiceForSetupStore(t)
	cfg := config.Config{
		Auth: config.AuthConfig{
			Refresh: config.RefreshConfig{
				Enabled:            true,
				KeyPrefix:          "funpot:test",
				MaxSessionsPerUser: 2,
			},
		},
		Redis: config.RedisConfig{
			Enabled:        true,
			Addr:           mr.Addr(),
			ConnectTimeout: time.Second,
		},
	}

	cleanup, err := setupRefreshSessionStore(context.Background(), zap.NewNop(), cfg, authService)
	if err != nil {
		t.Fatalf("setupRefreshSessionStore() error = %v", err)
	}
	defer cleanup()

	if err := authService.LogoutAll(context.Background(), "user-1", time.Now().UTC()); err != nil {
		t.Fatalf("LogoutAll() error = %v", err)
	}
}

func TestSetupRefreshSessionStoreUsesMemoryFallbackWhenRedisDisabled(t *testing.T) {
	authService := buildAuthServiceForSetupStore(t)
	cfg := config.Config{
		Auth: config.AuthConfig{
			Refresh: config.RefreshConfig{
				Enabled:            true,
				MaxSessionsPerUser: 2,
			},
		},
		Redis: config.RedisConfig{Enabled: false},
	}

	cleanup, err := setupRefreshSessionStore(context.Background(), zap.NewNop(), cfg, authService)
	if err != nil {
		t.Fatalf("setupRefreshSessionStore() error = %v", err)
	}
	defer cleanup()

	if err := authService.LogoutAll(context.Background(), "user-1", time.Now().UTC()); err != nil {
		t.Fatalf("LogoutAll() error = %v", err)
	}
}

func TestSetupRefreshSessionStoreNoopWhenRefreshDisabled(t *testing.T) {
	authService := buildAuthServiceForSetupStore(t)
	cfg := config.Config{
		Auth: config.AuthConfig{Refresh: config.RefreshConfig{Enabled: false}},
	}

	cleanup, err := setupRefreshSessionStore(context.Background(), zap.NewNop(), cfg, authService)
	if err != nil {
		t.Fatalf("setupRefreshSessionStore() error = %v", err)
	}
	defer cleanup()

	err = authService.LogoutAll(context.Background(), "user-1", time.Now().UTC())
	if err == nil {
		t.Fatal("expected error when refresh sessions are disabled")
	}
	if err.Error() != "refresh session store is not configured" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func buildAuthServiceForSetupStore(t *testing.T) *auth.Service {
	t.Helper()
	repo := users.NewInMemoryRepository()
	userService := users.NewService(repo)
	svc, err := auth.NewService(zap.NewNop(), config.AuthConfig{
		BotToken: "test-bot-token",
		JWT: config.JWTConfig{
			Secret: "test-secret",
			TTL:    15 * time.Minute,
		},
		Refresh: config.RefreshConfig{
			TTL: 24 * time.Hour,
		},
	}, userService)
	if err != nil {
		t.Fatalf("auth.NewService() error = %v", err)
	}
	return svc
}
