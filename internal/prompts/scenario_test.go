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

func TestScenarioServiceSupportsFullDetectorAndScenarioCRUD(t *testing.T) {
	svc := NewScenarioService()

	detector, err := svc.CreateGlobalDetector(context.Background(), CreateRequest{
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

	updatedDetector, err := svc.UpdateGlobalDetector(context.Background(), detector.ID, CreateRequest{
		Stage:         "global_detector_v2",
		Template:      "detect game precisely",
		Model:         "gemini-2.0-flash",
		Temperature:   0.2,
		MaxTokens:     300,
		TimeoutMS:     2500,
		RetryCount:    2,
		BackoffMS:     500,
		CooldownMS:    1500,
		MinConfidence: 0.8,
		ActorID:       "admin-2",
	})
	if err != nil {
		t.Fatalf("UpdateGlobalDetector() error = %v", err)
	}
	if updatedDetector.Stage != "global_detector_v2" {
		t.Fatalf("expected updated detector stage, got %q", updatedDetector.Stage)
	}

	secondDetector, err := svc.CreateGlobalDetector(context.Background(), CreateRequest{
		Stage:         "global_detector_v3",
		Template:      "detect cs2 or dota",
		Model:         "gemini-2.0-flash",
		Temperature:   0.2,
		MaxTokens:     512,
		TimeoutMS:     2500,
		RetryCount:    2,
		BackoffMS:     500,
		CooldownMS:    1500,
		MinConfidence: 0.8,
		ActorID:       "admin-2",
	})
	if err != nil {
		t.Fatalf("CreateGlobalDetector() second error = %v", err)
	}
	if _, err := svc.ActivateGlobalDetector(context.Background(), detector.ID, "admin-3"); err != nil {
		t.Fatalf("ActivateGlobalDetector() error = %v", err)
	}
	activeDetector, err := svc.GetActiveGlobalDetector(context.Background())
	if err != nil {
		t.Fatalf("GetActiveGlobalDetector() error = %v", err)
	}
	if activeDetector.ID != detector.ID {
		t.Fatalf("active detector id = %q, want %q", activeDetector.ID, detector.ID)
	}
	if err := svc.DeleteGlobalDetector(context.Background(), secondDetector.ID); err != nil {
		t.Fatalf("DeleteGlobalDetector() error = %v", err)
	}

	scenario, err := svc.CreateScenario(context.Background(), CreateScenarioRequest{
		GameSlug:    "counter_strike",
		Name:        "CS ranked flow",
		Description: "Wait for ranked match lifecycle",
		ActorID:     "admin-1",
		Steps: []ScenarioStepInput{
			{Code: "match_start", Title: "Match start", PromptTemplate: "Has a ranked match started?", Model: "gemini-2.0-flash", Temperature: 0.1, MaxTokens: 256, TimeoutMS: 2000, MinConfidence: 0.7},
		},
		Transitions: []ScenarioTransitionInput{
			{FromStepCode: "match_start", Outcome: "match_started", Terminal: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateScenario() error = %v", err)
	}
	updatedScenario, err := svc.UpdateScenario(context.Background(), scenario.ID, CreateScenarioRequest{
		GameSlug:    "counter_strike",
		Name:        "CS ranked flow updated",
		Description: "Updated description",
		ActorID:     "admin-2",
		Steps: []ScenarioStepInput{
			{Code: "match_start", Title: "Match start", PromptTemplate: "Has a ranked match started?", Model: "gemini-2.0-flash", Temperature: 0.2, MaxTokens: 128, TimeoutMS: 1500, MinConfidence: 0.6},
			{Code: "match_result", Title: "Match result", PromptTemplate: "Did the streamer win?", Model: "gemini-2.0-flash", Temperature: 0.2, MaxTokens: 128, TimeoutMS: 1500, MinConfidence: 0.6},
		},
		Transitions: []ScenarioTransitionInput{
			{FromStepCode: "match_start", Outcome: "match_started", ToStepCode: "match_result"},
			{FromStepCode: "match_result", Outcome: "win", Terminal: true},
		},
	})
	if err != nil {
		t.Fatalf("UpdateScenario() error = %v", err)
	}
	if updatedScenario.Name != "CS ranked flow updated" || len(updatedScenario.Steps) != 2 {
		t.Fatalf("unexpected updated scenario: %#v", updatedScenario)
	}

	secondScenario, err := svc.CreateScenario(context.Background(), CreateScenarioRequest{
		GameSlug:    "counter_strike",
		Name:        "CS alt flow",
		Description: "alt",
		ActorID:     "admin-3",
		Steps: []ScenarioStepInput{
			{Code: "alt_step", Title: "Alt", PromptTemplate: "alt?", Model: "gemini-2.0-flash", Temperature: 0.1, MaxTokens: 128, TimeoutMS: 1000, MinConfidence: 0.6},
		},
		Transitions: []ScenarioTransitionInput{
			{FromStepCode: "alt_step", Outcome: "done", Terminal: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateScenario() second error = %v", err)
	}
	if _, err := svc.ActivateScenario(context.Background(), secondScenario.ID, "admin-4"); err != nil {
		t.Fatalf("ActivateScenario() error = %v", err)
	}
	gotScenario, err := svc.GetScenario(context.Background(), secondScenario.ID)
	if err != nil {
		t.Fatalf("GetScenario() error = %v", err)
	}
	if !gotScenario.IsActive {
		t.Fatalf("expected active scenario after activation: %#v", gotScenario)
	}
	if err := svc.DeleteScenario(context.Background(), scenario.ID); err != nil {
		t.Fatalf("DeleteScenario() error = %v", err)
	}
}
