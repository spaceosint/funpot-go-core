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
