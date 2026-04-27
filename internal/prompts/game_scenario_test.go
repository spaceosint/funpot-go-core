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
		Transitions: []GameScenarioTransition{{
			ID:         "tr-1",
			FromNodeID: "n1",
			ToNodeID:   "n2",
			Condition:  `game == "cs2"`,
			Priority:   10,
			TerminalConditions: []GameScenarioTerminalCondition{
				{
					ID:              "tm-1",
					Condition:       `winner == "ct"`,
					GameTitle:       map[string]string{"ru": "Победа CT", "en": "CT win"},
					DefaultLanguage: "ru",
					OutcomesCount:   2,
					OutcomeTemplates: []GameScenarioOutcomeTemplate{
						{ID: "ct", Title: map[string]string{"ru": "CT", "en": "CT"}},
						{ID: "t", Title: map[string]string{"ru": "T", "en": "T"}},
					},
					Priority: 100,
				},
			},
		}},
		ActorID: "admin-1",
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

func TestGameScenarioCreateRejectsMissingTransitionID(t *testing.T) {
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
		Name:          "invalid-transition-id",
		GameSlug:      "cs2",
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: pkg.ID}},
		Transitions:   []GameScenarioTransition{{FromNodeID: "n1", ToNodeID: "n1", Condition: `winner == "ct"`, Priority: 1}},
		ActorID:       "admin-1",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !errors.Is(err, ErrInvalidGameScenario) {
		t.Fatalf("expected ErrInvalidGameScenario, got %v", err)
	}
}

func TestGameScenarioCreateRejectsTerminalWithoutOutcomes(t *testing.T) {
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
		Name:          "invalid-terminal",
		GameSlug:      "cs2",
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: pkg.ID}},
		Transitions: []GameScenarioTransition{{
			ID:         "tr-1",
			FromNodeID: "n1",
			ToNodeID:   "n1",
			Condition:  `winner == "ct"`,
			Priority:   1,
			TerminalConditions: []GameScenarioTerminalCondition{
				{Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 0, Priority: 1},
			},
		}},
		ActorID: "admin-1",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !errors.Is(err, ErrInvalidGameScenario) {
		t.Fatalf("expected ErrInvalidGameScenario, got %v", err)
	}
}

func TestGameScenarioCreateRejectsTerminalWithoutID(t *testing.T) {
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
		Name:          "invalid-terminal-id",
		GameSlug:      "cs2",
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: pkg.ID}},
		Transitions: []GameScenarioTransition{{
			ID:         "tr-1",
			FromNodeID: "n1",
			ToNodeID:   "n1",
			Condition:  `winner == "ct"`,
			Priority:   1,
			TerminalConditions: []GameScenarioTerminalCondition{
				{Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 1, OutcomeTemplates: []GameScenarioOutcomeTemplate{{ID: "opt-1", Title: map[string]string{"ru": "Опция"}}}, Priority: 1},
			},
		}},
		ActorID: "admin-1",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !errors.Is(err, ErrInvalidGameScenario) {
		t.Fatalf("expected ErrInvalidGameScenario, got %v", err)
	}
}

func TestGameScenarioActivateKeepsSingleActiveAcrossSlugs(t *testing.T) {
	t.Parallel()

	svc := NewService()
	cfg := mustCreateModelConfig(t, svc)
	rootPkg, err := svc.CreateScenarioPackage(context.Background(), ScenarioPackageCreateRequest{
		Name:             "root",
		GameSlug:         "global",
		LLMModelConfigID: cfg.ID,
		ActorID:          "admin-1",
		Steps:            []ScenarioStep{{ID: "root", Name: "root", PromptTemplate: "x", ResponseSchemaJSON: `{}`, Initial: true, Order: 1}},
	})
	if err != nil {
		t.Fatalf("create root package: %v", err)
	}

	first, err := svc.CreateGameScenario(context.Background(), GameScenarioCreateRequest{
		Name:          "first",
		GameSlug:      "global",
		InitialNodeID: "n1",
		Nodes:         []GameScenarioNode{{ID: "n1", ScenarioPackageID: rootPkg.ID}},
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateGameScenario first: %v", err)
	}
	if !first.IsActive {
		t.Fatalf("expected first scenario to be active")
	}

	second, err := svc.CreateGameScenario(context.Background(), GameScenarioCreateRequest{
		Name:          "second",
		GameSlug:      "cs2",
		InitialNodeID: "n2",
		Nodes:         []GameScenarioNode{{ID: "n2", ScenarioPackageID: rootPkg.ID}},
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateGameScenario second: %v", err)
	}
	if second.IsActive {
		t.Fatalf("expected second scenario to stay inactive before explicit activation")
	}

	if _, err := svc.ActivateGameScenario(context.Background(), second.ID, "admin-2"); err != nil {
		t.Fatalf("ActivateGameScenario second: %v", err)
	}
	got, err := svc.GetActiveGameScenario(context.Background(), "global")
	if err != nil {
		t.Fatalf("GetActiveGameScenario fallback: %v", err)
	}
	if got.ID != second.ID {
		t.Fatalf("expected globally active scenario %s, got %s", second.ID, got.ID)
	}
}

func TestGameScenarioResolveTerminalConditionFallsBackToGlobalScope(t *testing.T) {
	t.Parallel()

	scenario := GameScenario{
		InitialNodeID: "n1",
		Transitions: []GameScenarioTransition{
			{
				ID:         "edge-1",
				FromNodeID: "n1",
				ToNodeID:   "n2",
				Condition:  `winner == "ct"`,
				Priority:   100,
				TerminalConditions: []GameScenarioTerminalCondition{
					{ID: "edge-win", Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 1, OutcomeTemplates: []GameScenarioOutcomeTemplate{{ID: "opt", Title: map[string]string{"ru": "Опция"}}}, Priority: 100},
				},
			},
		},
	}

	terminal, ok, err := scenario.ResolveTerminalCondition("", `{"winner":"ct"}`)
	if err != nil {
		t.Fatalf("ResolveTerminalCondition(no-transition): %v", err)
	}
	if !ok {
		t.Fatalf("expected global terminal match without transition")
	}
	if terminal.ID != "edge-win" {
		t.Fatalf("expected global terminal match edge-win, got %s", terminal.ID)
	}
	if terminal.TransitionID != "edge-1" {
		t.Fatalf("expected transition reference edge-1, got %s", terminal.TransitionID)
	}
}

func TestGameScenarioResolveTerminalConditionPrefersTransitionScope(t *testing.T) {
	t.Parallel()

	scenario := GameScenario{
		InitialNodeID: "n1",
		Transitions: []GameScenarioTransition{
			{
				ID:         "edge-1",
				FromNodeID: "n1",
				ToNodeID:   "n2",
				Condition:  `winner == "ct"`,
				Priority:   100,
				TerminalConditions: []GameScenarioTerminalCondition{
					{ID: "edge-win-high", Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 1, OutcomeTemplates: []GameScenarioOutcomeTemplate{{ID: "opt", Title: map[string]string{"ru": "Опция"}}}, Priority: 200},
					{ID: "edge-win-low", Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 1, OutcomeTemplates: []GameScenarioOutcomeTemplate{{ID: "opt", Title: map[string]string{"ru": "Опция"}}}, Priority: 10},
				},
			},
		},
	}

	terminal, ok, err := scenario.ResolveTerminalCondition("edge-1", `{"winner":"ct"}`)
	if err != nil {
		t.Fatalf("ResolveTerminalCondition(edge): %v", err)
	}
	if !ok {
		t.Fatalf("expected edge terminal condition to match")
	}
	if terminal.ID != "edge-win-high" {
		t.Fatalf("expected edge-level terminal condition, got %s", terminal.ID)
	}
}

func TestGameScenarioResolveTerminalConditionPrefersMatchedTransitionBeforeGlobal(t *testing.T) {
	t.Parallel()

	scenario := GameScenario{
		InitialNodeID: "n1",
		Transitions: []GameScenarioTransition{
			{
				ID:         "edge-1",
				FromNodeID: "n1",
				ToNodeID:   "n2",
				Condition:  `winner == "ct"`,
				Priority:   100,
				TerminalConditions: []GameScenarioTerminalCondition{
					{ID: "edge-win-local", Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 1, OutcomeTemplates: []GameScenarioOutcomeTemplate{{ID: "opt", Title: map[string]string{"ru": "Опция"}}}, Priority: 10},
				},
			},
			{
				ID:         "edge-2",
				FromNodeID: "n2",
				ToNodeID:   "n3",
				Condition:  `winner == "ct"`,
				Priority:   90,
				TerminalConditions: []GameScenarioTerminalCondition{
					{ID: "edge-win-global", Condition: `winner == "ct"`, DefaultLanguage: "ru", GameTitle: map[string]string{"ru": "Игра"}, OutcomesCount: 1, OutcomeTemplates: []GameScenarioOutcomeTemplate{{ID: "opt", Title: map[string]string{"ru": "Опция"}}}, Priority: 999},
				},
			},
		},
	}

	terminal, ok, err := scenario.ResolveTerminalCondition("edge-1", `{"winner":"ct"}`)
	if err != nil {
		t.Fatalf("ResolveTerminalCondition(edge): %v", err)
	}
	if !ok {
		t.Fatalf("expected terminal condition to match")
	}
	if terminal.ID != "edge-win-local" {
		t.Fatalf("expected transition-scoped terminal to be preferred, got %s", terminal.ID)
	}
	if terminal.TransitionID != "edge-1" {
		t.Fatalf("expected transition reference edge-1, got %s", terminal.TransitionID)
	}
}
