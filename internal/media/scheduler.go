package media

import (
	"context"
	"errors"
	"fmt"
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
	locker    Locker
	nowFn     func() time.Time

	mu      sync.Mutex
	jobs    map[string]context.CancelFunc
	onStart func(string)
	onStop  func(string)
}

func NewScheduler(worker *Worker, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return NewSchedulerWithProcessor(streamProcessorAdapter{worker: worker}, interval)
}

func NewSchedulerWithProcessor(processor StreamProcessor, interval time.Duration) *Scheduler {
	return NewSchedulerWithProcessorAndLocker(processor, interval, NewInMemoryLocker())
}

func NewSchedulerWithProcessorAndLocker(processor StreamProcessor, interval time.Duration, locker Locker) *Scheduler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if locker == nil {
		locker = NewInMemoryLocker()
	}
	return &Scheduler{
		logger:    zap.NewNop(),
		processor: processor,
		interval:  interval,
		locker:    locker,
		nowFn: func() time.Time {
			return time.Now().UTC()
		},
		jobs: make(map[string]context.CancelFunc),
	}
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

func (s *Scheduler) SetLifecycleHooks(onStart, onStop func(string)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStart = onStart
	s.onStop = onStop
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
	onStart := s.onStart
	s.mu.Unlock()

	logger.Info("scheduler started for streamer", zap.String("streamerID", id), zap.Duration("interval", s.interval))
	if onStart != nil {
		onStart(id)
	}
	go s.run(ctx, id)
	return nil
}

func (s *Scheduler) run(ctx context.Context, streamerID string) {
	logger := s.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	defer func() {
		onStop := s.remove(streamerID)
		if onStop != nil {
			onStop(streamerID)
		}
		logger.Info("scheduler stopped for streamer", zap.String("streamerID", streamerID))
	}()

	s.runCycle(ctx, streamerID)
	for {
		wait := s.waitUntilNextCycle()
		select {
		case <-ctx.Done():
			logger.Info("scheduler context cancelled", zap.String("streamerID", streamerID))
			return
		case <-time.After(wait):
			s.runCycle(ctx, streamerID)
		}
	}
}

func (s *Scheduler) waitUntilNextCycle() time.Duration {
	if s == nil || s.interval <= 0 {
		return 10 * time.Second
	}
	wait := s.waitUntilNextWindow()
	if wait <= 0 {
		return 0
	}
	return wait
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
	windowStart := s.currentWindowStart()
	windowKey := fmt.Sprintf("stream-schedule:%s:%d", streamerID, windowStart.Unix())
	windowTTL := time.Until(windowStart.Add(s.interval))
	if windowTTL <= 0 {
		windowTTL = s.interval
	}
	if s.locker != nil && !s.locker.TryLock(windowKey, windowTTL) {
		logger.Info("scheduler cycle skipped due to existing window idempotency key", zap.String("streamerID", streamerID), zap.String("windowKey", windowKey), zap.Time("windowStart", windowStart), zap.Duration("windowTTL", windowTTL))
		return
	}
	logger.Info("scheduler cycle triggered", zap.String("streamerID", streamerID), zap.Time("windowStart", windowStart), zap.String("windowKey", windowKey))
	if err := s.processor.ProcessStreamer(ctx, streamerID); err != nil {
		if errors.Is(err, ErrTrackingStop) {
			logger.Info("scheduler stopping tracking after processor requested shutdown", zap.String("streamerID", streamerID))
			s.Stop(streamerID)
			return
		}
		logger.Error("scheduler cycle failed", zap.String("streamerID", streamerID), zap.Error(err))
		return
	}
	logger.Info("scheduler cycle completed", zap.String("streamerID", streamerID))
}

func (s *Scheduler) currentWindowStart() time.Time {
	now := time.Now().UTC()
	if s != nil && s.nowFn != nil {
		now = s.nowFn().UTC()
	}
	if s == nil || s.interval <= 0 {
		return now
	}
	return now.Truncate(s.interval)
}

func (s *Scheduler) waitUntilNextWindow() time.Duration {
	if s == nil || s.interval <= 0 {
		return 10 * time.Second
	}
	now := s.currentWindowStart()
	current := time.Now().UTC()
	if s.nowFn != nil {
		current = s.nowFn().UTC()
	}
	next := now.Add(s.interval)
	wait := time.Until(next)
	if s.nowFn != nil {
		wait = next.Sub(current)
	}
	if wait <= 0 {
		return 0
	}
	return wait
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

func (s *Scheduler) remove(streamerID string) func(string) {
	s.mu.Lock()
	onStop := s.onStop
	delete(s.jobs, streamerID)
	s.mu.Unlock()
	return onStop
}
