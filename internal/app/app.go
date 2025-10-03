package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/config"
)

// App encapsulates the HTTP server lifecycle and shared dependencies.
type App struct {
	cfg        config.Config
	logger     *zap.Logger
	httpServer *http.Server
}

// New constructs an App from configuration and dependencies.
func New(cfg config.Config, logger *zap.Logger, handler http.Handler) (*App, error) {
	if handler == nil {
		return nil, errors.New("http handler is required")
	}

	srv := &http.Server{
		Addr:         cfg.Server.Address,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &App{
		cfg:        cfg,
		logger:     logger,
		httpServer: srv,
	}, nil
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (a *App) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("starting http server", zap.String("address", a.cfg.Server.Address))
		errCh <- a.httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server error: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := a.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	return nil
}
