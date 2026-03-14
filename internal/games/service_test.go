package games

import (
	"context"
	"errors"
	"testing"
)

func TestServiceCreateValidation(t *testing.T) {
	tests := []struct {
		name string
		req  UpsertRequest
		err  error
	}{
		{name: "missing slug", req: UpsertRequest{Title: "CS2", Status: StatusDraft}, err: ErrInvalidSlug},
		{name: "missing title", req: UpsertRequest{Slug: "cs2", Status: StatusDraft}, err: ErrInvalidTitle},
		{name: "invalid status", req: UpsertRequest{Slug: "cs2", Title: "CS2", Status: "unknown"}, err: ErrInvalidStatus},
	}

	svc := NewService()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), tt.req)
			if !errors.Is(err, tt.err) {
				t.Fatalf("expected %v, got %v", tt.err, err)
			}
		})
	}
}

func TestServiceCRUD(t *testing.T) {
	svc := NewService()
	ctx := context.Background()

	created, err := svc.Create(ctx, UpsertRequest{
		Slug:        " cs2 ",
		Title:       "Counter-Strike 2",
		Description: "  shooter  ",
		Rules:       []string{" rule 1 ", "", " rule 2 "},
		Status:      "ACTIVE",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Slug != "cs2" || created.Status != StatusActive {
		t.Fatalf("unexpected normalized game: %+v", created)
	}
	if len(created.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(created.Rules))
	}

	if _, err := svc.Create(ctx, UpsertRequest{Slug: "cs2", Title: "Duplicate", Status: StatusDraft}); !errors.Is(err, ErrDuplicateSlug) {
		t.Fatalf("expected duplicate slug error, got %v", err)
	}

	updated, err := svc.Update(ctx, created.ID, UpsertRequest{Slug: "cs2-premier", Title: "CS2 Premier", Status: StatusArchived})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Slug != "cs2-premier" || updated.Status != StatusArchived {
		t.Fatalf("unexpected updated game: %+v", updated)
	}

	games := svc.List(ctx)
	if len(games) != 1 {
		t.Fatalf("expected one game, got %d", len(games))
	}

	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := svc.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found on second delete, got %v", err)
	}
}
