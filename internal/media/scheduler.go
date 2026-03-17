package media

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
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
	return &Scheduler{processor: processor, interval: interval, jobs: make(map[string]context.CancelFunc)}
}

func (s *Scheduler) Start(streamerID string) error {
	id := strings.TrimSpace(streamerID)
	if id == "" {
		return ErrSchedulerStreamerIDRequired
	}

	s.mu.Lock()
	if _, exists := s.jobs[id]; exists {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.jobs[id] = cancel
	s.mu.Unlock()

	go s.run(ctx, id)
	return nil
}

func (s *Scheduler) run(ctx context.Context, streamerID string) {
	defer s.remove(streamerID)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.runCycle(ctx, streamerID)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runCycle(ctx, streamerID)
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context, streamerID string) {
	if s.processor == nil {
		return
	}
	_ = s.processor.ProcessStreamer(ctx, streamerID)
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
