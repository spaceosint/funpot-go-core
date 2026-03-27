package prompts

import (
	"context"
	"testing"
)

func TestScenarioPackageResolveStep(t *testing.T) {
	t.Parallel()

	svc := NewService()
	pkg, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:     "cs2 flow",
		GameSlug: "global",
		ActorID:  "admin-1",
		Steps: []ScenarioStep{
			{ID: "game_detect", Name: "Game detect", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true, Order: 1},
			{ID: "cs2_mode", Name: "CS2 mode", Folder: "cs2", PromptTemplate: "mode", ResponseSchemaJSON: `{}`, Order: 2},
			{ID: "cs2_faceit", Name: "Faceit", Folder: "cs2/faceit", PromptTemplate: "faceit", ResponseSchemaJSON: `{}`, Order: 3},
		},
		Transitions: []ScenarioTransition{
			{FromStepID: "game_detect", ToStepID: "cs2_mode", Condition: "game == cs2", Priority: 1},
			{FromStepID: "cs2_mode", ToStepID: "cs2_faceit", Condition: "mode == faceit", Priority: 1},
		},
	})
	if err != nil {
		t.Fatalf("create scenario package: %v", err)
	}

	step, entered, err := pkg.ResolveStep("", `{"game":"cs2"}`)
	if err != nil {
		t.Fatalf("resolve initial: %v", err)
	}
	if !entered || step.ID != "game_detect" {
		t.Fatalf("expected initial game_detect entered=true, got entered=%v step=%s", entered, step.ID)
	}

	step, entered, err = pkg.ResolveStep("game_detect", `{"game":"cs2"}`)
	if err != nil {
		t.Fatalf("resolve game transition: %v", err)
	}
	if !entered || step.ID != "cs2_mode" {
		t.Fatalf("expected transition to cs2_mode, got entered=%v step=%s", entered, step.ID)
	}

	step, entered, err = pkg.ResolveStep("cs2_mode", `{"game":"cs2","mode":"none"}`)
	if err != nil {
		t.Fatalf("resolve hold transition: %v", err)
	}
	if entered || step.ID != "cs2_mode" {
		t.Fatalf("expected stay at cs2_mode, got entered=%v step=%s", entered, step.ID)
	}

	step, entered, err = pkg.ResolveStep("cs2_mode", `{"game":"cs2","mode":"faceit"}`)
	if err != nil {
		t.Fatalf("resolve faceit transition: %v", err)
	}
	if !entered || step.ID != "cs2_faceit" {
		t.Fatalf("expected transition to cs2_faceit, got entered=%v step=%s", entered, step.ID)
	}
}

func TestEvaluateCondition(t *testing.T) {
	t.Parallel()
	payload := map[string]any{"game": "cs2", "mode": "faceit", "nested": map[string]any{"value": "x"}}

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{name: "equals", expr: "game == cs2", want: true},
		{name: "not equals", expr: "mode != premier", want: true},
		{name: "exists", expr: "exists(nested.value)", want: true},
		{name: "not_exists", expr: "not_exists(nested.missing)", want: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := evaluateCondition(tc.expr, payload)
			if err != nil {
				t.Fatalf("evaluateCondition(%q) error: %v", tc.expr, err)
			}
			if got != tc.want {
				t.Fatalf("evaluateCondition(%q)=%v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}
