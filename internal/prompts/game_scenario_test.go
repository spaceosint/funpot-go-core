package prompts

import (
	"context"
	"errors"
	"testing"
)

func TestGameScenarioCRUD(t *testing.T) {
	t.Parallel()

	svc := NewService()
	cfg := mustCreateModelConfig(t, svc)
	rootPkg, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:             "root",
		GameSlug:         "global",
		LLMModelConfigID: cfg.ID,
		ActorID:          "admin-1",
		Steps: []ScenarioStep{
			{ID: "root", Name: "root", PromptTemplate: "x", ResponseSchemaJSON: `{}`, Initial: true, Order: 1},
		},
	})
	if err != nil {
		t.Fatalf("create root package: %v", err)
	}
	nextPkg, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:             "next",
		GameSlug:         "cs2",
		LLMModelConfigID: cfg.ID,
		ActorID:          "admin-1",
		Steps: []ScenarioStep{
			{ID: "next", Name: "next", PromptTemplate: "x", ResponseSchemaJSON: `{}`, Initial: true, Order: 1, EntryCondition: `game == "cs2"`},
		},
	})
	if err != nil {
		t.Fatalf("create next package: %v", err)
	}

	created, err := svc.CreateGameScenario(context.Background(), GameScenarioCreateRequest{
		Name:          "cs2 tournament",
		GameSlug:      "cs2",
		InitialNodeID: "n1",
		Nodes: []GameScenarioNode{
			{ID: "n1", ScenarioPackageID: rootPkg.ID},
			{ID: "n2", ScenarioPackageID: nextPkg.ID},
		},
		Transitions:        []GameScenarioTransition{{FromNodeID: "n1", ToNodeID: "n2", Condition: `game == "cs2"`, Priority: 10}},
		TerminalConditions: []GameScenarioTerminalCondition{{Condition: `winner == "ct"`, ResultLabel: "ct_win", ResultStateJSON: `{"result":"win"}`, Priority: 100}},
		ActorID:            "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateGameScenario() error = %v", err)
	}
	if created.ID == "" || created.InitialNodeID != "n1" {
		t.Fatalf("unexpected created game scenario: %#v", created)
	}
	loaded, err := svc.GetGameScenario(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetGameScenario() error = %v", err)
	}
	if loaded.Name != "cs2 tournament" {
		t.Fatalf("unexpected loaded name %q", loaded.Name)
	}

	activated, err := svc.ActivateGameScenario(context.Background(), created.ID, "admin-1")
	if err != nil {
		t.Fatalf("ActivateGameScenario() error = %v", err)
	}
	if !activated.IsActive {
		t.Fatalf("expected active game scenario")
	}
}

func TestGameScenarioCreateRejectsMissingTransitionCondition(t *testing.T) {
	t.Parallel()

	svc := NewService()
	cfg := mustCreateModelConfig(t, svc)
	pkg, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:             "root",
		GameSlug:         "global",
		LLMModelConfigID: cfg.ID,
		ActorID:          "admin-1",
		Steps:            []ScenarioStep{{ID: "root", Name: "root", PromptTemplate: "x", ResponseSchemaJSON: `{}`, Initial: true, Order: 1}},
	})
	if err != nil {
		t.Fatalf("create scenario package: %v", err)
	}

	_, err = svc.CreateGameScenario(context.Background(), GameScenarioCreateRequest{
		Name:          "invalid",
		GameSlug:      "cs2",
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: pkg.ID}},
		Transitions:   []GameScenarioTransition{{FromNodeID: "n1", ToNodeID: "n1", Priority: 1}},
		ActorID:       "admin-1",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !errors.Is(err, ErrInvalidGameScenario) {
		t.Fatalf("expected ErrInvalidGameScenario, got %v", err)
	}
}
