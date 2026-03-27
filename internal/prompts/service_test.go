package prompts

import (
	"context"
	"testing"
)

func TestCreateAndActivate(t *testing.T) {
	svc := NewService()
	created, err := svc.Create(context.Background(), CreateRequest{
		Stage:         "detector",
		Position:      1,
		Template:      "detect cs",
		Model:         "gemini-2.0-flash",
		Temperature:   0.2,
		MaxTokens:     512,
		TimeoutMS:     2000,
		RetryCount:    2,
		BackoffMS:     500,
		CooldownMS:    30000,
		MinConfidence: 0.7,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("expected version 1, got %d", created.Version)
	}
	if created.Position != 1 {
		t.Fatalf("expected position 1, got %d", created.Position)
	}
	if !created.IsActive {
		t.Fatal("expected first prompt version to become active automatically")
	}

	active, err := svc.Activate(context.Background(), created.ID, "admin-1")
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if !active.IsActive {
		t.Fatal("expected active prompt")
	}
}

func TestValidateCreateRequest(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
		err  error
	}{
		{
			name: "invalid stage",
			req:  CreateRequest{Stage: "   ", Template: "a", Model: "m", Temperature: 0, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.1},
			err:  ErrInvalidStage,
		},
		{
			name: "invalid confidence",
			req:  CreateRequest{Stage: "detector", Template: "a", Model: "m", Temperature: 0, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 1.5},
			err:  ErrInvalidMinConfidence,
		},
		{
			name: "ok with custom stage",
			req:  CreateRequest{Stage: "ranked_mode", Template: "a", Model: "m", Temperature: 0.2, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateRequest(tc.req)
			if tc.err == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.err != nil && err != tc.err {
				t.Fatalf("expected %v, got %v", tc.err, err)
			}
		})
	}
}

func TestListActiveOrdersByPosition(t *testing.T) {
	svc := NewService()
	third, _ := svc.Create(context.Background(), CreateRequest{Stage: "result", Position: 3, Template: "c", Model: "m", Temperature: 0.1, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4})
	first, _ := svc.Create(context.Background(), CreateRequest{Stage: "detector", Position: 1, Template: "a", Model: "m", Temperature: 0.1, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4})
	second, _ := svc.Create(context.Background(), CreateRequest{Stage: "queue", Position: 2, Template: "b", Model: "m", Temperature: 0.1, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4})
	for _, id := range []string{third.ID, first.ID, second.ID} {
		if _, err := svc.Activate(context.Background(), id, "admin-1"); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
	}

	items := svc.ListActive(context.Background())
	if len(items) != 3 {
		t.Fatalf("len(ListActive()) = %d, want 3", len(items))
	}
	if items[0].Stage != "detector" || items[1].Stage != "queue" || items[2].Stage != "result" {
		t.Fatalf("unexpected order: %#v", items)
	}
}

func TestCreateAutoActivatesFirstPromptPerStageOnly(t *testing.T) {
	svc := NewService()
	first, err := svc.Create(context.Background(), CreateRequest{Stage: "detector", Position: 1, Template: "first", Model: "m", Temperature: 0.1, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4, ActorID: "admin-1"})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := svc.Create(context.Background(), CreateRequest{Stage: "detector", Position: 1, Template: "second", Model: "m", Temperature: 0.1, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4, ActorID: "admin-2"})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	if !first.IsActive {
		t.Fatal("expected first prompt to be active")
	}
	if second.IsActive {
		t.Fatal("expected later prompt versions to require explicit activation")
	}

	active, err := svc.GetActiveByStage(context.Background(), "detector")
	if err != nil {
		t.Fatalf("GetActiveByStage() error = %v", err)
	}
	if active.ID != first.ID {
		t.Fatalf("active id = %q, want %q", active.ID, first.ID)
	}
}

func TestPromptCRUD(t *testing.T) {
	svc := NewService()
	created, err := svc.Create(context.Background(), CreateRequest{
		Stage:         "detector",
		Position:      1,
		Template:      "first",
		Model:         "m",
		Temperature:   0.1,
		MaxTokens:     1,
		TimeoutMS:     1,
		MinConfidence: 0.4,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("Get().ID = %q, want %q", got.ID, created.ID)
	}

	updated, err := svc.Update(context.Background(), created.ID, CreateRequest{
		Stage:         "detector",
		Position:      2,
		Template:      "updated",
		Model:         "m2",
		Temperature:   0.2,
		MaxTokens:     2,
		TimeoutMS:     2,
		RetryCount:    1,
		BackoffMS:     10,
		CooldownMS:    5,
		MinConfidence: 0.5,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Template != "updated" || updated.Position != 2 {
		t.Fatalf("Update() = %#v", updated)
	}

	if err := svc.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := svc.Get(context.Background(), created.ID); err != ErrNotFound {
		t.Fatalf("Get() after delete error = %v, want %v", err, ErrNotFound)
	}
}
