package prompts

import (
	"context"
	"errors"
	"testing"
)

func TestServiceStateSchemaLifecycle(t *testing.T) {
	svc := NewService()
	created, err := svc.CreateStateSchema(context.Background(), StateSchemaCreateRequest{
		GameSlug: "cs2",
		Name:     "CS2 baseline",
		Fields:   []StateFieldDefinition{{Key: "score.ct", Type: "number"}},
		ActorID:  "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateStateSchema() error = %v", err)
	}
	if !created.IsActive {
		t.Fatal("expected first schema to be active")
	}
	updated, err := svc.UpdateStateSchema(context.Background(), created.ID, StateSchemaCreateRequest{
		GameSlug: "cs2",
		Name:     "CS2 baseline v2",
		Fields:   []StateFieldDefinition{{Key: "score.t", Type: "number"}},
		ActorID:  "admin-2",
	})
	if err != nil {
		t.Fatalf("UpdateStateSchema() error = %v", err)
	}
	if updated.Name != "CS2 baseline v2" {
		t.Fatalf("updated name = %q", updated.Name)
	}
	active, err := svc.GetActiveStateSchema(context.Background(), "cs2")
	if err != nil {
		t.Fatalf("GetActiveStateSchema() error = %v", err)
	}
	if active.ID != created.ID {
		t.Fatalf("active id = %q, want %q", active.ID, created.ID)
	}
}

func TestValidateStateSchemaCreateRequestAllowsInitialStateJSONWithoutFields(t *testing.T) {
	err := ValidateStateSchemaCreateRequest(StateSchemaCreateRequest{
		GameSlug:         "cs2",
		Name:             "CS2 full state",
		InitialStateJSON: `{"session_status":{"value":"unknown"}}`,
	})
	if err != nil {
		t.Fatalf("ValidateStateSchemaCreateRequest() error = %v", err)
	}
}

func TestValidateStateSchemaCreateRequestRejectsInvalidInitialStateJSON(t *testing.T) {
	err := ValidateStateSchemaCreateRequest(StateSchemaCreateRequest{
		GameSlug:         "cs2",
		Name:             "CS2 full state",
		InitialStateJSON: `[]`,
	})
	if !errors.Is(err, ErrInvalidInitialStateJSON) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidInitialStateJSON)
	}
}

func TestServiceRuleSetLifecycle(t *testing.T) {
	svc := NewService()
	created, err := svc.CreateRuleSet(context.Background(), RuleSetCreateRequest{
		GameSlug:          "cs2",
		Name:              "CS2 rules",
		RuleItems:         []RuleItem{{FieldKey: "score.ct", Operation: "set", ConfidenceMode: "strict"}},
		FinalizationRules: []RuleCondition{{Priority: 1, Condition: "final_banner_seen", Action: "finalize_win"}},
		ActorID:           "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateRuleSet() error = %v", err)
	}
	if !created.IsActive {
		t.Fatal("expected first rule set to be active")
	}
	fetched, err := svc.GetRuleSet(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetRuleSet() error = %v", err)
	}
	if fetched.Name != created.Name {
		t.Fatalf("fetched name = %q, want %q", fetched.Name, created.Name)
	}
}
