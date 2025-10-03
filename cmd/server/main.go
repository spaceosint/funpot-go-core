package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/funpot/funpot-go-core/internal/app"
	"github.com/funpot/funpot-go-core/internal/config"
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

	handler := app.NewHandler(logger, func() bool { return true }, telemetryProvider.MetricsHandler())

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
