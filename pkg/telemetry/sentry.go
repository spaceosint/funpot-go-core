package telemetry

import (
	"time"

	"github.com/getsentry/sentry-go"
	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/config"
)

// InitSentry configures the global Sentry client if a DSN is provided.
func InitSentry(cfg config.SentryConfig, logger *zap.Logger) error {
	if cfg.DSN == "" {
		logger.Info("sentry disabled: DSN not provided")
		return nil
	}

	opts := sentry.ClientOptions{
		Dsn:              cfg.DSN,
		Environment:      cfg.Environment,
		TracesSampleRate: cfg.SampleRate,
		Debug:            cfg.Debug,
	}

	if err := sentry.Init(opts); err != nil {
		return err
	}

	logger.Info("sentry initialized", zap.String("environment", cfg.Environment))
	return nil
}

// FlushSentry drains buffered Sentry events before shutdown.
func FlushSentry(timeout time.Duration) {
	sentry.Flush(timeout)
}
