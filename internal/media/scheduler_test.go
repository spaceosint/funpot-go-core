package media

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeProcessor struct {
	mu    sync.Mutex
	calls int
}

func (p *fakeProcessor) ProcessStreamer(_ context.Context, _ string) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return nil
}

func (p *fakeProcessor) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestSchedulerStartRunsImmediateCycle(t *testing.T) {
	processor := &fakeProcessor{}
	scheduler := NewSchedulerWithProcessor(processor, 25*time.Millisecond)

	if err := scheduler.Start("str-1"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer scheduler.Stop("str-1")

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if processor.callCount() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least one processing call")
}

func TestSchedulerStartIsIdempotentPerStreamer(t *testing.T) {
	processor := &fakeProcessor{}
	scheduler := NewSchedulerWithProcessor(processor, 50*time.Millisecond)
	if err := scheduler.Start("str-1"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer scheduler.Stop("str-1")

	if err := scheduler.Start("str-1"); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
}

func TestSchedulerLifecycleHooksTrackStartAndStop(t *testing.T) {
	processor := &fakeProcessor{}
	scheduler := NewSchedulerWithProcessor(processor, time.Hour)

	var (
		mu      sync.Mutex
		started []string
		stopped []string
	)
	scheduler.SetLifecycleHooks(func(streamerID string) {
		mu.Lock()
		started = append(started, streamerID)
		mu.Unlock()
	}, func(streamerID string) {
		mu.Lock()
		stopped = append(stopped, streamerID)
		mu.Unlock()
	})

	if err := scheduler.Start("str-42"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	scheduler.Stop("str-42")

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(started) == 1 && len(stopped) == 1
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(started) != 1 || started[0] != "str-42" {
		t.Fatalf("unexpected start hook calls: %#v", started)
	}
	if len(stopped) != 1 || stopped[0] != "str-42" {
		t.Fatalf("unexpected stop hook calls: %#v", stopped)
	}
}
