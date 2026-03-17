package prompts

import (
	"context"
	"testing"
)

func TestCreateAndActivate(t *testing.T) {
	svc := NewService()
	created, err := svc.Create(context.Background(), CreateRequest{
		Stage:         StageA,
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
			req:  CreateRequest{Stage: "unknown", Template: "a", Model: "m", Temperature: 0, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.1},
			err:  ErrInvalidStage,
		},
		{
			name: "invalid confidence",
			req:  CreateRequest{Stage: StageA, Template: "a", Model: "m", Temperature: 0, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 1.5},
			err:  ErrInvalidMinConfidence,
		},
		{
			name: "ok",
			req:  CreateRequest{Stage: StageB, Template: "a", Model: "m", Temperature: 0.2, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.4},
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

func TestGetActiveByStage(t *testing.T) {
	svc := NewService()
	created, err := svc.Create(context.Background(), CreateRequest{
		Stage:         StageA,
		Template:      "detect cs",
		Model:         "gemini-2.0-flash",
		Temperature:   0.2,
		MaxTokens:     128,
		TimeoutMS:     2000,
		RetryCount:    1,
		BackoffMS:     100,
		CooldownMS:    1000,
		MinConfidence: 0.6,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.GetActiveByStage(context.Background(), StageA); err != ErrNotFound {
		t.Fatalf("GetActiveByStage() err = %v, want %v", err, ErrNotFound)
	}
	if _, err := svc.Activate(context.Background(), created.ID, "admin-1"); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	active, err := svc.GetActiveByStage(context.Background(), StageA)
	if err != nil {
		t.Fatalf("GetActiveByStage() error = %v", err)
	}
	if active.ID != created.ID {
		t.Fatalf("active ID = %s, want %s", active.ID, created.ID)
	}
}
