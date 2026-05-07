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
	"github.com/funpot/funpot-go-core/internal/media"
	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/streamers"
	"github.com/funpot/funpot-go-core/internal/users"
	"github.com/funpot/funpot-go-core/internal/wallet"
	"github.com/funpot/funpot-go-core/pkg/cache"
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
		db          *sql.DB
		redisClient *redis.Client
		userRepo    users.Repository
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

	if cfg.Redis.Enabled {
		redisCtx, cancel := context.WithTimeout(context.Background(), cfg.Redis.HealthcheckPing)
		redisClient, err = cache.OpenRedis(redisCtx, cfg.Redis)
		cancel()
		if err != nil {
			logger.Fatal("failed to connect to redis", zap.Error(err))
		}
		defer func() {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close redis", zap.Error(err))
			}
		}()
	}

	userService := users.NewService(userRepo)
	walletService := wallet.NewService()
	if db != nil {
		walletService = wallet.NewPostgresService(db)
	}
	adminService := admin.NewService(cfg.Admin.UserIDs)
	streamerValidator := streamers.TwitchValidator(streamers.NewTwitchAPIValidator(
		cfg.Twitch.ClientID,
		cfg.Twitch.ClientSecret,
		cfg.Twitch.TokenURL,
		cfg.Twitch.APIBaseURL,
		nil,
	))
	if cfg.Twitch.ClientID == "" || cfg.Twitch.ClientSecret == "" {
		logger.Warn("twitch credentials are not configured; submission live audience validation is disabled")
		streamerValidator = nil
	}
	streamersService := streamers.NewServiceWithValidator(streamerValidator)
	if db != nil {
		streamersService.SetStreamerRepository(streamers.NewPostgresStreamerRepository(db))
		streamersService.SetDecisionRepository(streamers.NewPostgresDecisionRepository(db))
	}
	streamersService.SetMinLiveViewers(cfg.Client.MinViewers)
	streamersService.SetLogger(logger.Named("streamers"))
	gamesService := games.NewService()
	promptsService := prompts.NewService()
	if db != nil {
		promptsService = prompts.NewPostgresService(db)
	}
	eventsService := events.NewService(nil)
	if db != nil {
		eventsService = events.NewPostgresService(db, nil)
	}
	if redisClient != nil {
		eventsService.WithRedisLiveState(redisClient, 6*time.Hour)
	}

	streamCapture := buildStreamCapture(cfg, streamersService)
	if configurableCapture, ok := streamCapture.(interface{ SetLogger(*zap.Logger) }); ok {
		configurableCapture.SetLogger(logger.Named("stream_capture"))
	}

	chunkPublisher := buildChunkPublisher(cfg, db, streamersService)
	streamWorker := media.NewWorker(
		streamCapture,
		buildStageClassifier(logger, cfg),
		promptsService,
		&media.InMemoryRunStore{},
		streamersService,
		media.NewInMemoryLocker(),
		media.WorkerConfig{
			LockTTL:        streamWorkerLockTTL(cfg),
			MinConfidence:  0.5,
			ChunkPublisher: chunkPublisher,
			LiveEvents:     eventsService,
		},
	)
	streamWorker.SetLogger(logger.Named("stream_worker"))
	streamScheduler := media.NewScheduler(streamWorker, streamProcessInterval(cfg))
	streamScheduler.SetLogger(logger.Named("stream_scheduler"))
	streamScheduler.SetLifecycleHooks(streamersService.MarkAnalysisActive, streamersService.MarkAnalysisInactive)
	streamersService.SetSubmissionHook(func(_ context.Context, streamerID string) error {
		return streamScheduler.Start(streamerID)
	})
	streamersService.SetTrackingStopHook(func(_ context.Context, streamerID string) error {
		if finalizer, ok := chunkPublisher.(streamChunkFinalizer); ok {
			if err := finalizer.Finalize(context.Background(), streamerID, time.Now().UTC()); err != nil {
				logger.Error("failed to finalize stream video on manual tracking stop", zap.String("streamerID", streamerID), zap.Error(err))
				return err
			}
		}
		streamScheduler.Stop(streamerID)
		return nil
	})

	authService, err := auth.NewService(logger, cfg.Auth, userService)
	if err != nil {
		logger.Fatal("failed to create auth service", zap.Error(err))
	}
	if cfg.Auth.Refresh.Enabled {
		refreshStore, err := auth.NewRedisRefreshSessionStore(redisClient, auth.RefreshStoreConfig{
			KeyPrefix:          cfg.Auth.Refresh.KeyPrefix,
			MaxSessionsPerUser: cfg.Auth.Refresh.MaxSessionsPerUser,
		})
		if err != nil {
			logger.Fatal("failed to configure refresh session store", zap.Error(err))
		}
		authService.WithRefreshSessionStore(refreshStore)
	}

	cleanupRefreshStore, err := setupRefreshSessionStore(ctx, logger, cfg, authService)
	if err != nil {
		logger.Fatal("failed to configure refresh sessions", zap.Error(err))
	}
	defer cleanupRefreshStore()

	readyFn := func() bool {
		if db != nil {
			pingCtx, cancel := context.WithTimeout(context.Background(), cfg.Database.HealthcheckPing)
			err := db.PingContext(pingCtx)
			cancel()
			if err != nil {
				return false
			}
		}

		if redisClient != nil {
			pingCtx, cancel := context.WithTimeout(context.Background(), cfg.Redis.HealthcheckPing)
			err := redisClient.Ping(pingCtx).Err()
			cancel()
			if err != nil {
				return false
			}
		}

		return true
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
		chunkPublisher,
		eventsService,
		app.ConfigResponseFromConfig(cfg),
		walletService,
	)

	application, err := app.New(cfg, logger, handler)
	if err != nil {
		logger.Fatal("failed to create app", zap.Error(err))
	}

	if err := application.Run(ctx); err != nil {
		logger.Fatal("application exited with error", zap.Error(err))
	}
}

func buildStreamCapture(cfg config.Config, streamersService *streamers.Service) media.StreamCapture {
	if !cfg.Streamlink.Enabled {
		return media.NewStreamlinkCaptureAdapter(media.StreamlinkCaptureConfig{}, nil, nil)
	}

	return media.NewStreamlinkCaptureAdapter(media.StreamlinkCaptureConfig{
		BinaryPath:     cfg.Streamlink.BinaryPath,
		FFmpegBinary:   cfg.Streamlink.FFmpegBinary,
		Quality:        cfg.Streamlink.Quality,
		CaptureTimeout: cfg.Streamlink.CaptureTimeout,
		OutputDir:      cfg.Streamlink.OutputDir,
		URLTemplate:    cfg.Streamlink.URLTemplate,
	}, streamersService, nil)
}

func buildStageClassifier(logger *zap.Logger, cfg config.Config) media.StageClassifier {
	if cfg.Gemini.APIKey == "" {
		logger.Warn("gemini api key is not configured; using deterministic fallback classifier")
		return media.PromptedStageClassifier{}
	}

	classifier, err := media.NewGeminiStageClassifier(media.GeminiClassifierConfig{
		APIKey:         cfg.Gemini.APIKey,
		BaseURL:        cfg.Gemini.BaseURL,
		MaxInlineBytes: cfg.Gemini.MaxInlineBytes,
		ChatMaxTokens:  cfg.Gemini.ChatMaxTokens,
	})
	if err != nil {
		logger.Warn("failed to configure gemini classifier; using deterministic fallback classifier", zap.Error(err))
		return media.PromptedStageClassifier{}
	}

	return classifier
}

func streamProcessInterval(cfg config.Config) time.Duration {
	if cfg.Streamlink.CaptureTimeout > 0 {
		return cfg.Streamlink.CaptureTimeout
	}
	return 25 * time.Second
}

func streamWorkerLockTTL(cfg config.Config) time.Duration {
	interval := streamProcessInterval(cfg)
	// Keep lock slightly longer than capture interval to prevent overlapping cycles.
	return interval + 5*time.Second
}

func buildChunkPublisher(cfg config.Config, db *sql.DB, streamersService *streamers.Service) media.ChunkPublisher {
	if cfg.Streamlink.BunnyLibraryID == "" || cfg.Streamlink.BunnyAPIKey == "" {
		return nil
	}
	var videoStore media.UploadedVideoStore
	if db != nil {
		videoStore = media.NewPostgresUploadedVideoStore(db)
	}
	return media.NewBunnyChunkPublisher(media.BunnyChunkPublisherConfig{
		OutputDir:      cfg.Streamlink.OutputDir,
		FFmpegBinary:   cfg.Streamlink.FFmpegBinary,
		AggregateCount: cfg.Streamlink.AggregateCount,
		BaseURL:        cfg.Streamlink.BunnyBaseURL,
		LibraryID:      cfg.Streamlink.BunnyLibraryID,
		APIKey:         cfg.Streamlink.BunnyAPIKey,
		UsernameResolver: func(ctx context.Context, streamerID string) (string, error) {
			if streamersService == nil {
				return "", nil
			}
			return streamersService.ResolveStreamlinkChannel(ctx, streamerID)
		},
		VideoStore: videoStore,
	})
}

type streamChunkFinalizer interface {
	Finalize(ctx context.Context, streamerID string, capturedAt time.Time) error
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
