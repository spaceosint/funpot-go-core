package media

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

var ErrSchedulerStreamerIDRequired = errors.New("streamerID is required")

type StreamProcessor interface {
	ProcessStreamer(ctx context.Context, streamerID string) error
}

type streamProcessorAdapter struct {
	worker *Worker
}

func (a streamProcessorAdapter) ProcessStreamer(ctx context.Context, streamerID string) error {
	_, err := a.worker.ProcessStreamer(ctx, streamerID)
	return err
}

type Scheduler struct {
	logger    *zap.Logger
	processor StreamProcessor
	interval  time.Duration

	mu   sync.Mutex
	jobs map[string]context.CancelFunc
}

func NewScheduler(worker *Worker, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return NewSchedulerWithProcessor(streamProcessorAdapter{worker: worker}, interval)
}

func NewSchedulerWithProcessor(processor StreamProcessor, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Scheduler{logger: zap.NewNop(), processor: processor, interval: interval, jobs: make(map[string]context.CancelFunc)}
}

func (s *Scheduler) SetLogger(logger *zap.Logger) {
	if s == nil {
		return
	}
	if logger == nil {
		s.logger = zap.NewNop()
		return
	}
	s.logger = logger
}

func (s *Scheduler) Start(streamerID string) error {
	logger := s.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	id := strings.TrimSpace(streamerID)
	if id == "" {
		logger.Warn("scheduler rejected empty streamer id")
		return ErrSchedulerStreamerIDRequired
	}

	s.mu.Lock()
	if _, exists := s.jobs[id]; exists {
		s.mu.Unlock()
		logger.Info("scheduler already running for streamer", zap.String("streamerID", id))
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.jobs[id] = cancel
	s.mu.Unlock()

	logger.Info("scheduler started for streamer", zap.String("streamerID", id), zap.Duration("interval", s.interval))
	go s.run(ctx, id)
	return nil
}

func (s *Scheduler) run(ctx context.Context, streamerID string) {
	logger := s.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	defer func() {
		s.remove(streamerID)
		logger.Info("scheduler stopped for streamer", zap.String("streamerID", streamerID))
	}()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.runCycle(ctx, streamerID)
	for {
		select {
		case <-ctx.Done():
			logger.Info("scheduler context cancelled", zap.String("streamerID", streamerID))
			return
		case <-ticker.C:
			s.runCycle(ctx, streamerID)
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context, streamerID string) {
	logger := s.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	if s.processor == nil {
		logger.Warn("scheduler skipped cycle because processor is not configured", zap.String("streamerID", streamerID))
		return
	}
	logger.Info("scheduler cycle triggered", zap.String("streamerID", streamerID))
	if err := s.processor.ProcessStreamer(ctx, streamerID); err != nil {
		logger.Error("scheduler cycle failed", zap.String("streamerID", streamerID), zap.Error(err))
		return
	}
	logger.Info("scheduler cycle completed", zap.String("streamerID", streamerID))
}

func (s *Scheduler) Stop(streamerID string) {
	id := strings.TrimSpace(streamerID)
	if id == "" {
		return
	}

	s.mu.Lock()
	cancel, ok := s.jobs[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *Scheduler) remove(streamerID string) {
	s.mu.Lock()
	delete(s.jobs, streamerID)
	s.mu.Unlock()
}
