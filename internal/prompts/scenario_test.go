package prompts

import (
	"context"
	"testing"
)

func TestScenarioServiceCreatesGlobalDetectorAndScenario(t *testing.T) {
	svc := NewScenarioService()
	global, err := svc.CreateGlobalDetector(context.Background(), CreateRequest{
		Stage:         "global_detector",
		Template:      "detect current game",
		Model:         "gemini-2.0-flash",
		Temperature:   0.1,
		MaxTokens:     256,
		TimeoutMS:     2000,
		RetryCount:    1,
		BackoffMS:     250,
		CooldownMS:    1000,
		MinConfidence: 0.7,
		ActorID:       "admin-1",
	})
	if err != nil {
		t.Fatalf("CreateGlobalDetector() error = %v", err)
	}
	if !global.IsActive {
		t.Fatal("expected active global detector")
	}

	scenario, err := svc.CreateScenario(context.Background(), CreateScenarioRequest{
		GameSlug:    "counter_strike",
		Name:        "CS ranked flow",
		Description: "Wait for ranked match lifecycle",
		ActorID:     "admin-1",
		Steps: []ScenarioStepInput{
			{Code: "match_start", Title: "Match start", PromptTemplate: "Has a ranked match started?", Model: "gemini-2.0-flash", Temperature: 0.1, MaxTokens: 256, TimeoutMS: 2000, MinConfidence: 0.7},
			{Code: "match_result", Title: "Match result", PromptTemplate: "Did the streamer win?", Model: "gemini-2.0-flash", Temperature: 0.1, MaxTokens: 256, TimeoutMS: 2000, MinConfidence: 0.7},
		},
		Transitions: []ScenarioTransitionInput{
			{FromStepCode: "match_start", Outcome: "match_started", ToStepCode: "match_result"},
			{FromStepCode: "match_result", Outcome: "win", Terminal: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateScenario() error = %v", err)
	}
	if !scenario.IsActive {
		t.Fatal("expected first scenario version to become active")
	}
	if len(scenario.Steps) != 2 || len(scenario.Transitions) != 2 {
		t.Fatalf("unexpected scenario payload: %#v", scenario)
	}

	activeScenario, err := svc.GetActiveScenarioByGame(context.Background(), "counter_strike")
	if err != nil {
		t.Fatalf("GetActiveScenarioByGame() error = %v", err)
	}
	if activeScenario.ID != scenario.ID {
		t.Fatalf("active scenario id = %q, want %q", activeScenario.ID, scenario.ID)
	}

	entry, ok := activeScenario.EntryStep()
	if !ok || entry.Code != "match_start" {
		t.Fatalf("EntryStep() = %#v, %v", entry, ok)
	}
	transition, ok := activeScenario.ResolveTransition("match_start", "match_started")
	if !ok || transition.ToStepCode != "match_result" {
		t.Fatalf("ResolveTransition() = %#v, %v", transition, ok)
	}
}

func TestValidateCreateScenarioRequestRejectsBrokenTransition(t *testing.T) {
	err := ValidateCreateScenarioRequest(CreateScenarioRequest{
		GameSlug: "counter_strike",
		Name:     "broken",
		Steps: []ScenarioStepInput{
			{Code: "step-1", PromptTemplate: "prompt", Model: "gemini", Temperature: 0.1, MaxTokens: 1, TimeoutMS: 1, MinConfidence: 0.1},
		},
		Transitions: []ScenarioTransitionInput{{FromStepCode: "step-1", Outcome: "go"}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
