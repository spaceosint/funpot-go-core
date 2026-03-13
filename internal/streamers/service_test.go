package streamers

import "testing"

func TestSubmitAndList(t *testing.T) {
	svc := NewService()

	_, err := svc.Submit(t.Context(), "", "user-1")
	if err == nil {
		t.Fatalf("expected validation error")
	}

	sub, err := svc.Submit(t.Context(), "best_streamer", "user-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if sub.Status != "pending" {
		t.Fatalf("expected pending status, got %s", sub.Status)
	}

	items := svc.List(t.Context(), "best", 1)
	if len(items) != 1 {
		t.Fatalf("expected one result, got %d", len(items))
	}
	if items[0].Username != "best_streamer" {
		t.Fatalf("unexpected username: %s", items[0].Username)
	}
}
