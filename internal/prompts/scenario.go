package prompts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	PromptKindGlobalDetector = "global_detector"
	PromptKindScenarioStep   = "scenario_step"
)

var (
	ErrInvalidPromptKind      = errors.New("prompt kind is invalid")
	ErrInvalidGameSlug        = errors.New("gameSlug must not be empty")
	ErrInvalidScenarioName    = errors.New("scenario name must not be empty")
	ErrInvalidScenarioStep    = errors.New("scenario step code must not be empty")
	ErrInvalidScenarioOutcome = errors.New("scenario transition outcome must not be empty")
	ErrDetectorNotFound       = errors.New("global detector not found")
	ErrScenarioNotFound       = errors.New("scenario not found")
)

type ScenarioStepInput struct {
	Code           string
	Title          string
	PromptTemplate string
	Model          string
	Temperature    float64
	MaxTokens      int
	TimeoutMS      int
	RetryCount     int
	BackoffMS      int
	CooldownMS     int
	MinConfidence  float64
}

type ScenarioTransitionInput struct {
	FromStepCode string
	Outcome      string
	ToStepCode   string
	Terminal     bool
}

type CreateScenarioRequest struct {
	GameSlug    string
	Name        string
	Description string
	ActorID     string
	Steps       []ScenarioStepInput
	Transitions []ScenarioTransitionInput
}

type PromptTemplate struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Stage         string    `json:"stage"`
	GameSlug      string    `json:"gameSlug,omitempty"`
	Version       int       `json:"version"`
	Template      string    `json:"template"`
	Model         string    `json:"model"`
	Temperature   float64   `json:"temperature"`
	MaxTokens     int       `json:"maxTokens"`
	TimeoutMS     int       `json:"timeoutMs"`
	RetryCount    int       `json:"retryCount"`
	BackoffMS     int       `json:"backoffMs"`
	CooldownMS    int       `json:"cooldownMs"`
	MinConfidence float64   `json:"minConfidence"`
	IsActive      bool      `json:"isActive"`
	CreatedBy     string    `json:"createdBy"`
	ActivatedBy   string    `json:"activatedBy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	ActivatedAt   time.Time `json:"activatedAt,omitempty"`
}

type ScenarioStep struct {
	ID       string         `json:"id"`
	Code     string         `json:"code"`
	Title    string         `json:"title"`
	Position int            `json:"position"`
	Prompt   PromptTemplate `json:"prompt"`
}

type ScenarioTransition struct {
	ID           string `json:"id"`
	FromStepCode string `json:"fromStepCode"`
	Outcome      string `json:"outcome"`
	ToStepCode   string `json:"toStepCode,omitempty"`
	Terminal     bool   `json:"terminal"`
}

type ScenarioVersion struct {
	ID          string               `json:"id"`
	GameSlug    string               `json:"gameSlug"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Version     int                  `json:"version"`
	IsActive    bool                 `json:"isActive"`
	CreatedBy   string               `json:"createdBy"`
	ActivatedBy string               `json:"activatedBy,omitempty"`
	CreatedAt   time.Time            `json:"createdAt"`
	ActivatedAt time.Time            `json:"activatedAt,omitempty"`
	Steps       []ScenarioStep       `json:"steps"`
	Transitions []ScenarioTransition `json:"transitions"`
}

func ValidateCreateScenarioRequest(req CreateScenarioRequest) error {
	if strings.TrimSpace(req.GameSlug) == "" {
		return ErrInvalidGameSlug
	}
	if strings.TrimSpace(req.Name) == "" {
		return ErrInvalidScenarioName
	}
	if len(req.Steps) == 0 {
		return ErrInvalidScenarioStep
	}
	seen := make(map[string]struct{}, len(req.Steps))
	for _, step := range req.Steps {
		code := strings.TrimSpace(step.Code)
		if code == "" {
			return ErrInvalidScenarioStep
		}
		if _, ok := seen[code]; ok {
			return fmt.Errorf("duplicate scenario step code: %s", code)
		}
		seen[code] = struct{}{}
		if err := ValidateCreateRequest(CreateRequest{
			Stage:         code,
			Template:      step.PromptTemplate,
			Model:         step.Model,
			Temperature:   step.Temperature,
			MaxTokens:     step.MaxTokens,
			TimeoutMS:     step.TimeoutMS,
			RetryCount:    step.RetryCount,
			BackoffMS:     step.BackoffMS,
			CooldownMS:    step.CooldownMS,
			MinConfidence: step.MinConfidence,
		}); err != nil {
			return err
		}
	}
	for _, transition := range req.Transitions {
		if strings.TrimSpace(transition.FromStepCode) == "" {
			return ErrInvalidScenarioStep
		}
		if strings.TrimSpace(transition.Outcome) == "" {
			return ErrInvalidScenarioOutcome
		}
		if !transition.Terminal && strings.TrimSpace(transition.ToStepCode) == "" {
			return ErrInvalidScenarioStep
		}
	}
	return nil
}

// ScenarioService stores global detector prompts and game scenarios. The current
// implementation is in-memory, but its API is shaped so it can be backed by DB tables.
type ScenarioService struct {
	mu              sync.RWMutex
	counter         int
	globalDetectors []PromptTemplate
	scenariosByGame map[string][]ScenarioVersion
}

func NewScenarioService() *ScenarioService {
	return &ScenarioService{scenariosByGame: map[string][]ScenarioVersion{}}
}

func (s *ScenarioService) CreateGlobalDetector(ctx context.Context, req CreateRequest) (PromptTemplate, error) {
	return s.createPrompt(ctx, PromptKindGlobalDetector, "", req)
}

func (s *ScenarioService) ListGlobalDetectors(_ context.Context) []PromptTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]PromptTemplate, len(s.globalDetectors))
	copy(items, s.globalDetectors)
	sort.Slice(items, func(i, j int) bool {
		return items[i].Version > items[j].Version
	})
	return items
}

func (s *ScenarioService) createPrompt(_ context.Context, kind string, gameSlug string, req CreateRequest) (PromptTemplate, error) {
	if kind != PromptKindGlobalDetector && kind != PromptKindScenarioStep {
		return PromptTemplate{}, ErrInvalidPromptKind
	}
	if err := ValidateCreateRequest(req); err != nil {
		return PromptTemplate{}, err
	}
	if kind == PromptKindScenarioStep && strings.TrimSpace(gameSlug) == "" {
		return PromptTemplate{}, ErrInvalidGameSlug
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	now := time.Now().UTC()
	item := PromptTemplate{
		ID:            fmt.Sprintf("prompt-template-%d", s.counter),
		Kind:          kind,
		Stage:         strings.TrimSpace(req.Stage),
		GameSlug:      strings.TrimSpace(gameSlug),
		Version:       len(s.globalDetectors) + 1,
		Template:      strings.TrimSpace(req.Template),
		Model:         strings.TrimSpace(req.Model),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		TimeoutMS:     req.TimeoutMS,
		RetryCount:    req.RetryCount,
		BackoffMS:     req.BackoffMS,
		CooldownMS:    req.CooldownMS,
		MinConfidence: req.MinConfidence,
		CreatedBy:     strings.TrimSpace(req.ActorID),
		CreatedAt:     now,
		IsActive:      true,
		ActivatedBy:   strings.TrimSpace(req.ActorID),
		ActivatedAt:   now,
	}
	for i := range s.globalDetectors {
		s.globalDetectors[i].IsActive = false
	}
	s.globalDetectors = append(s.globalDetectors, item)
	return item, nil
}

func (s *ScenarioService) GetActiveGlobalDetector(_ context.Context) (PromptTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.globalDetectors) - 1; i >= 0; i-- {
		if s.globalDetectors[i].IsActive {
			return s.globalDetectors[i], nil
		}
	}
	return PromptTemplate{}, ErrNotFound
}

func (s *ScenarioService) GetGlobalDetector(_ context.Context, id string) (PromptTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, detector := range s.globalDetectors {
		if detector.ID == strings.TrimSpace(id) {
			return detector, nil
		}
	}
	return PromptTemplate{}, ErrDetectorNotFound
}

func (s *ScenarioService) UpdateGlobalDetector(_ context.Context, id string, req CreateRequest) (PromptTemplate, error) {
	if err := ValidateCreateRequest(req); err != nil {
		return PromptTemplate{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := strings.TrimSpace(id)
	for i := range s.globalDetectors {
		if s.globalDetectors[i].ID != targetID {
			continue
		}
		item := s.globalDetectors[i]
		item.Stage = strings.TrimSpace(req.Stage)
		item.Template = strings.TrimSpace(req.Template)
		item.Model = strings.TrimSpace(req.Model)
		item.Temperature = req.Temperature
		item.MaxTokens = req.MaxTokens
		item.TimeoutMS = req.TimeoutMS
		item.RetryCount = req.RetryCount
		item.BackoffMS = req.BackoffMS
		item.CooldownMS = req.CooldownMS
		item.MinConfidence = req.MinConfidence
		s.globalDetectors[i] = item
		return item, nil
	}
	return PromptTemplate{}, ErrDetectorNotFound
}

func (s *ScenarioService) ActivateGlobalDetector(_ context.Context, id, actorID string) (PromptTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := strings.TrimSpace(id)
	activeIdx := -1
	for i := range s.globalDetectors {
		if s.globalDetectors[i].ID == targetID {
			activeIdx = i
			break
		}
	}
	if activeIdx == -1 {
		return PromptTemplate{}, ErrDetectorNotFound
	}

	now := time.Now().UTC()
	for i := range s.globalDetectors {
		s.globalDetectors[i].IsActive = i == activeIdx
		if i == activeIdx {
			s.globalDetectors[i].ActivatedAt = now
			s.globalDetectors[i].ActivatedBy = strings.TrimSpace(actorID)
		}
	}
	return s.globalDetectors[activeIdx], nil
}

func (s *ScenarioService) DeleteGlobalDetector(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := strings.TrimSpace(id)
	for i := range s.globalDetectors {
		if s.globalDetectors[i].ID != targetID {
			continue
		}
		wasActive := s.globalDetectors[i].IsActive
		s.globalDetectors = append(s.globalDetectors[:i], s.globalDetectors[i+1:]...)
		if wasActive && len(s.globalDetectors) > 0 {
			last := len(s.globalDetectors) - 1
			s.globalDetectors[last].IsActive = true
			s.globalDetectors[last].ActivatedAt = time.Now().UTC()
		}
		return nil
	}
	return ErrDetectorNotFound
}

func (s *ScenarioService) CreateScenario(_ context.Context, req CreateScenarioRequest) (ScenarioVersion, error) {
	if err := ValidateCreateScenarioRequest(req); err != nil {
		return ScenarioVersion{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	gameSlug := strings.ToLower(strings.TrimSpace(req.GameSlug))
	version := len(s.scenariosByGame[gameSlug]) + 1
	now := time.Now().UTC()
	s.counter++
	scenario := ScenarioVersion{
		ID:          fmt.Sprintf("scenario-%d", s.counter),
		GameSlug:    gameSlug,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Version:     version,
		IsActive:    len(s.scenariosByGame[gameSlug]) == 0,
		CreatedBy:   strings.TrimSpace(req.ActorID),
		CreatedAt:   now,
	}
	if scenario.IsActive {
		scenario.ActivatedBy = strings.TrimSpace(req.ActorID)
		scenario.ActivatedAt = now
	}
	for idx, step := range req.Steps {
		s.counter++
		prompt := PromptTemplate{
			ID:            fmt.Sprintf("prompt-template-%d", s.counter),
			Kind:          PromptKindScenarioStep,
			Stage:         strings.TrimSpace(step.Code),
			GameSlug:      gameSlug,
			Version:       scenario.Version,
			Template:      strings.TrimSpace(step.PromptTemplate),
			Model:         strings.TrimSpace(step.Model),
			Temperature:   step.Temperature,
			MaxTokens:     step.MaxTokens,
			TimeoutMS:     step.TimeoutMS,
			RetryCount:    step.RetryCount,
			BackoffMS:     step.BackoffMS,
			CooldownMS:    step.CooldownMS,
			MinConfidence: step.MinConfidence,
			CreatedBy:     strings.TrimSpace(req.ActorID),
			CreatedAt:     now,
			IsActive:      scenario.IsActive,
			ActivatedBy:   scenario.ActivatedBy,
			ActivatedAt:   scenario.ActivatedAt,
		}
		scenario.Steps = append(scenario.Steps, ScenarioStep{
			ID:       fmt.Sprintf("scenario-step-%d", s.counter),
			Code:     strings.TrimSpace(step.Code),
			Title:    strings.TrimSpace(step.Title),
			Position: idx + 1,
			Prompt:   prompt,
		})
	}
	for _, transition := range req.Transitions {
		s.counter++
		scenario.Transitions = append(scenario.Transitions, ScenarioTransition{
			ID:           fmt.Sprintf("scenario-transition-%d", s.counter),
			FromStepCode: strings.TrimSpace(transition.FromStepCode),
			Outcome:      strings.TrimSpace(transition.Outcome),
			ToStepCode:   strings.TrimSpace(transition.ToStepCode),
			Terminal:     transition.Terminal,
		})
	}
	s.scenariosByGame[gameSlug] = append(s.scenariosByGame[gameSlug], scenario)
	return scenario, nil
}

func (s *ScenarioService) GetScenario(_ context.Context, id string) (ScenarioVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	targetID := strings.TrimSpace(id)
	for _, scenarios := range s.scenariosByGame {
		for _, scenario := range scenarios {
			if scenario.ID == targetID {
				return scenario, nil
			}
		}
	}
	return ScenarioVersion{}, ErrScenarioNotFound
}

func (s *ScenarioService) UpdateScenario(_ context.Context, id string, req CreateScenarioRequest) (ScenarioVersion, error) {
	if err := ValidateCreateScenarioRequest(req); err != nil {
		return ScenarioVersion{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := strings.TrimSpace(id)
	targetGame := strings.ToLower(strings.TrimSpace(req.GameSlug))
	for game, scenarios := range s.scenariosByGame {
		for idx := range scenarios {
			if scenarios[idx].ID != targetID {
				continue
			}
			current := scenarios[idx]
			now := time.Now().UTC()
			updated := ScenarioVersion{
				ID:          current.ID,
				GameSlug:    targetGame,
				Name:        strings.TrimSpace(req.Name),
				Description: strings.TrimSpace(req.Description),
				Version:     current.Version,
				IsActive:    current.IsActive,
				CreatedBy:   current.CreatedBy,
				ActivatedBy: current.ActivatedBy,
				CreatedAt:   current.CreatedAt,
				ActivatedAt: current.ActivatedAt,
			}
			for stepIdx, step := range req.Steps {
				s.counter++
				prompt := PromptTemplate{
					ID:            fmt.Sprintf("prompt-template-%d", s.counter),
					Kind:          PromptKindScenarioStep,
					Stage:         strings.TrimSpace(step.Code),
					GameSlug:      targetGame,
					Version:       updated.Version,
					Template:      strings.TrimSpace(step.PromptTemplate),
					Model:         strings.TrimSpace(step.Model),
					Temperature:   step.Temperature,
					MaxTokens:     step.MaxTokens,
					TimeoutMS:     step.TimeoutMS,
					RetryCount:    step.RetryCount,
					BackoffMS:     step.BackoffMS,
					CooldownMS:    step.CooldownMS,
					MinConfidence: step.MinConfidence,
					CreatedBy:     strings.TrimSpace(req.ActorID),
					CreatedAt:     now,
					IsActive:      updated.IsActive,
					ActivatedBy:   updated.ActivatedBy,
					ActivatedAt:   updated.ActivatedAt,
				}
				updated.Steps = append(updated.Steps, ScenarioStep{
					ID:       fmt.Sprintf("scenario-step-%d", s.counter),
					Code:     strings.TrimSpace(step.Code),
					Title:    strings.TrimSpace(step.Title),
					Position: stepIdx + 1,
					Prompt:   prompt,
				})
			}
			for _, transition := range req.Transitions {
				s.counter++
				updated.Transitions = append(updated.Transitions, ScenarioTransition{
					ID:           fmt.Sprintf("scenario-transition-%d", s.counter),
					FromStepCode: strings.TrimSpace(transition.FromStepCode),
					Outcome:      strings.TrimSpace(transition.Outcome),
					ToStepCode:   strings.TrimSpace(transition.ToStepCode),
					Terminal:     transition.Terminal,
				})
			}

			if game == targetGame {
				scenarios[idx] = updated
				s.scenariosByGame[game] = scenarios
			} else {
				s.scenariosByGame[game] = append(scenarios[:idx], scenarios[idx+1:]...)
				s.scenariosByGame[targetGame] = append(s.scenariosByGame[targetGame], updated)
			}
			return updated, nil
		}
	}
	return ScenarioVersion{}, ErrScenarioNotFound
}

func (s *ScenarioService) ActivateScenario(_ context.Context, id, actorID string) (ScenarioVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for game, scenarios := range s.scenariosByGame {
		activeIdx := -1
		for i := range scenarios {
			if scenarios[i].ID == strings.TrimSpace(id) {
				activeIdx = i
				break
			}
		}
		if activeIdx == -1 {
			continue
		}
		now := time.Now().UTC()
		for i := range scenarios {
			scenarios[i].IsActive = i == activeIdx
			if i == activeIdx {
				scenarios[i].ActivatedAt = now
				scenarios[i].ActivatedBy = strings.TrimSpace(actorID)
			}
			for j := range scenarios[i].Steps {
				scenarios[i].Steps[j].Prompt.IsActive = i == activeIdx
				if i == activeIdx {
					scenarios[i].Steps[j].Prompt.ActivatedAt = now
					scenarios[i].Steps[j].Prompt.ActivatedBy = strings.TrimSpace(actorID)
				}
			}
		}
		s.scenariosByGame[game] = scenarios
		return scenarios[activeIdx], nil
	}
	return ScenarioVersion{}, ErrScenarioNotFound
}

func (s *ScenarioService) DeleteScenario(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := strings.TrimSpace(id)
	for game, scenarios := range s.scenariosByGame {
		for idx := range scenarios {
			if scenarios[idx].ID != targetID {
				continue
			}
			wasActive := scenarios[idx].IsActive
			scenarios = append(scenarios[:idx], scenarios[idx+1:]...)
			if len(scenarios) == 0 {
				delete(s.scenariosByGame, game)
				return nil
			}
			if wasActive {
				scenarios[0].IsActive = true
				scenarios[0].ActivatedAt = time.Now().UTC()
			}
			s.scenariosByGame[game] = scenarios
			return nil
		}
	}
	return ErrScenarioNotFound
}

func (s *ScenarioService) GetActiveScenarioByGame(_ context.Context, gameSlug string) (ScenarioVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, scenario := range s.scenariosByGame[strings.ToLower(strings.TrimSpace(gameSlug))] {
		if scenario.IsActive {
			return scenario, nil
		}
	}
	return ScenarioVersion{}, ErrScenarioNotFound
}

func (s *ScenarioService) ListScenarios(_ context.Context) []ScenarioVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ScenarioVersion, 0)
	for _, scenarios := range s.scenariosByGame {
		items = append(items, scenarios...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GameSlug == items[j].GameSlug {
			return items[i].Version > items[j].Version
		}
		return items[i].GameSlug < items[j].GameSlug
	})
	return items
}

func (v ScenarioVersion) EntryStep() (ScenarioStep, bool) {
	if len(v.Steps) == 0 {
		return ScenarioStep{}, false
	}
	return v.Steps[0], true
}

func (v ScenarioVersion) ResolveTransition(fromStepCode, outcome string) (ScenarioTransition, bool) {
	fromStepCode = strings.TrimSpace(fromStepCode)
	outcome = strings.TrimSpace(outcome)
	for _, transition := range v.Transitions {
		if transition.FromStepCode == fromStepCode && strings.EqualFold(transition.Outcome, outcome) {
			return transition, true
		}
	}
	return ScenarioTransition{}, false
}

func (v ScenarioVersion) FindStep(code string) (ScenarioStep, bool) {
	code = strings.TrimSpace(code)
	for _, step := range v.Steps {
		if step.Code == code {
			return step, true
		}
	}
	return ScenarioStep{}, false
}
