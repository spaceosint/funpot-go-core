package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/app"
	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/events"
	"github.com/funpot/funpot-go-core/internal/games"
	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
	"github.com/funpot/funpot-go-core/internal/users"
	dbpkg "github.com/funpot/funpot-go-core/pkg/database"
	"github.com/funpot/funpot-go-core/pkg/telemetry"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := newLogger(cfg.Logging.Level)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	telemetryProvider, err := telemetry.Setup(logger, cfg.Telemetry.ServiceName, cfg.Environment, cfg.Telemetry.MetricsEnabled)
	if err != nil {
		logger.Fatal("failed to setup telemetry", zap.Error(err))
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetryProvider.Shutdown(shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", zap.Error(err))
		}
	}()

	if err := telemetry.InitSentry(cfg.Sentry, logger); err != nil {
		logger.Fatal("failed to initialize sentry", zap.Error(err))
	}
	defer telemetry.FlushSentry(2 * time.Second)

	var (
		db       *sql.DB
		userRepo users.Repository
	)

	if cfg.Database.DSN() != "" {
		db, err = dbpkg.OpenPostgres(dbpkg.PostgresSettings{
			DSN:             cfg.Database.DSN(),
			MaxOpenConns:    cfg.Database.MaxOpenConns,
			MaxIdleConns:    cfg.Database.MaxIdleConns,
			ConnMaxIdleTime: cfg.Database.ConnMaxIdleTime,
			ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		})
		if err != nil {
			logger.Fatal("failed to connect to postgres", zap.Error(err))
		}
		defer func() {
			if err := db.Close(); err != nil {
				logger.Error("failed to close database", zap.Error(err))
			}
		}()

		userRepo = users.NewPostgresRepository(db)
	} else {
		logger.Warn("database connection parameters not provided; using in-memory users repository")
		userRepo = users.NewInMemoryRepository()
	}

	userService := users.NewService(userRepo)
	adminService := admin.NewService(cfg.Admin.UserIDs)
	streamersService := streamers.NewService()
	gamesService := games.NewService()
	promptsService := prompts.NewService()
	eventsService := events.NewService(nil)

	authService, err := auth.NewService(logger, cfg.Auth, userService)
	if err != nil {
		logger.Fatal("failed to create auth service", zap.Error(err))
	}

	cleanupRefreshStore, err := setupRefreshSessionStore(ctx, logger, cfg, authService)
	if err != nil {
		logger.Fatal("failed to configure refresh sessions", zap.Error(err))
	}
	defer cleanupRefreshStore()

	readyFn := func() bool {
		if db == nil {
			return true
		}

		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		return db.PingContext(pingCtx) == nil
	}

	handler := app.NewHandler(
		logger,
		readyFn,
		telemetryProvider.MetricsHandler(),
		authService,
		adminService,
		userService,
		streamersService,
		gamesService,
		promptsService,
		eventsService,
		app.ConfigResponseFromConfig(cfg),
	)

	application, err := app.New(cfg, logger, handler)
	if err != nil {
		logger.Fatal("failed to create app", zap.Error(err))
	}

	if err := application.Run(ctx); err != nil {
		logger.Fatal("application exited with error", zap.Error(err))
	}
}

func newLogger(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if level != "" {
		if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
			return nil, err
		}
	}
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return cfg.Build()
}

func setupRefreshSessionStore(ctx context.Context, logger *zap.Logger, cfg config.Config, authService *auth.Service) (func(), error) {
	if !cfg.Auth.Refresh.Enabled {
		return func() {}, nil
	}

	if !cfg.Redis.Enabled {
		logger.Warn("redis is disabled; using in-memory refresh session store")
		authService.WithRefreshSessionStore(auth.NewInMemoryRefreshSessionStore(cfg.Auth.Refresh.MaxSessionsPerUser))
		return func() {}, nil
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  cfg.Redis.ConnectTimeout,
		ReadTimeout:  cfg.Redis.ConnectTimeout,
		WriteTimeout: cfg.Redis.ConnectTimeout,
	})

	pingCtx, cancel := context.WithTimeout(ctx, cfg.Redis.ConnectTimeout)
	defer cancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	refreshStore, err := auth.NewRedisRefreshSessionStore(redisClient, auth.RefreshStoreConfig{
		KeyPrefix:          cfg.Auth.Refresh.KeyPrefix,
		MaxSessionsPerUser: cfg.Auth.Refresh.MaxSessionsPerUser,
	})
	if err != nil {
		_ = redisClient.Close()
		return nil, err
	}

	authService.WithRefreshSessionStore(refreshStore)
	logger.Info("redis refresh session store enabled", zap.String("addr", cfg.Redis.Addr))

	return func() {
		if err := redisClient.Close(); err != nil {
			logger.Warn("failed to close redis client", zap.Error(err))
		}
	}, nil
}
