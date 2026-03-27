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

func TestScenarioPackageBuildVisualGraph(t *testing.T) {
	t.Parallel()

	pkg := ScenarioPackage{
		ID:       "pkg-1",
		Name:     "default graph",
		GameSlug: "global",
		Version:  3,
		Steps: []ScenarioStep{
			{ID: "root_detect", Name: "Root", GameSlug: "global", Initial: true, Order: 1},
			{ID: "cs2_mode", Name: "Mode", GameSlug: "cs2", Folder: "cs2", Order: 2},
			{ID: "cs2_faceit", Name: "Faceit", GameSlug: "cs2", Folder: "cs2/faceit", Order: 3},
		},
		Transitions: []ScenarioTransition{
			{FromStepID: "root_detect", ToStepID: "cs2_mode", Condition: "game == cs2", Priority: 1},
			{FromStepID: "cs2_mode", ToStepID: "cs2_faceit", Condition: "mode == faceit", Priority: 1},
		},
	}

	graph := pkg.BuildVisualGraph()
	if graph.PackageID != "pkg-1" || graph.PackageName != "default graph" || graph.Version != 3 {
		t.Fatalf("unexpected graph metadata: %#v", graph)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(graph.Nodes))
	}
	if graph.Nodes[0].ID != "root_detect" || graph.Nodes[0].Level != 1 {
		t.Fatalf("unexpected root node: %#v", graph.Nodes[0])
	}
	if graph.Nodes[2].ID != "cs2_faceit" || graph.Nodes[2].Level != 3 {
		t.Fatalf("unexpected nested node: %#v", graph.Nodes[2])
	}
	if len(graph.Edges) != 2 || graph.Edges[0].FromStepID != "cs2_mode" || graph.Edges[1].FromStepID != "root_detect" {
		t.Fatalf("unexpected edges ordering/content: %#v", graph.Edges)
	}
	if len(graph.Groups) != 3 {
		t.Fatalf("expected 3 groups, got %d (%#v)", len(graph.Groups), graph.Groups)
	}
}

func TestScenarioPackageUpdateAcrossGameDeactivatesAndNormalizesSteps(t *testing.T) {
	t.Parallel()

	svc := NewService()
	created, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:     "global flow",
		GameSlug: "global",
		ActorID:  "admin-1",
		Steps: []ScenarioStep{
			{ID: "root_detect", Name: "Root", PromptTemplate: "detect", ResponseSchemaJSON: `{}`, Initial: true},
		},
	})
	if err != nil {
		t.Fatalf("create scenario package: %v", err)
	}
	if !created.IsActive {
		t.Fatalf("expected created package to be active: %#v", created)
	}

	updated, err := svc.UpdateScenarioPackage(context.Background(), created.ID, ScenarioPackageCreateRequest{
		Name:     "cs2 flow",
		GameSlug: "cs2",
		ActorID:  "admin-1",
		Steps: []ScenarioStep{
			{ID: "cs2_mode", Name: "Mode", PromptTemplate: "mode", ResponseSchemaJSON: `{}`},
		},
	})
	if err != nil {
		t.Fatalf("update scenario package: %v", err)
	}
	if updated.IsActive {
		t.Fatalf("expected moved package to be inactive: %#v", updated)
	}
	if updated.Steps[0].Order != 1 {
		t.Fatalf("expected normalized step order=1, got %#v", updated.Steps[0])
	}
	if updated.Steps[0].GameSlug != "cs2" {
		t.Fatalf("expected normalized step gameSlug=cs2, got %#v", updated.Steps[0])
	}
	if updated.Steps[0].CreatedAt.IsZero() {
		t.Fatalf("expected normalized step createdAt to be set, got %#v", updated.Steps[0])
	}
}
