package prompts

import (
	"context"
	"database/sql"
	"encoding/json"
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

const scenarioConfigDDL = `
CREATE TABLE IF NOT EXISTS prompt_global_detectors (
    id TEXT PRIMARY KEY,
    stage TEXT NOT NULL,
    version INTEGER NOT NULL,
    template TEXT NOT NULL,
    model TEXT NOT NULL,
    temperature DOUBLE PRECISION NOT NULL,
    max_tokens INTEGER NOT NULL,
    timeout_ms INTEGER NOT NULL,
    retry_count INTEGER NOT NULL,
    backoff_ms INTEGER NOT NULL,
    cooldown_ms INTEGER NOT NULL,
    min_confidence DOUBLE PRECISION NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    CHECK (char_length(id) > 0),
    CHECK (char_length(stage) > 0),
    CHECK (char_length(template) > 0),
    CHECK (char_length(model) > 0),
    CHECK (version > 0),
    CHECK (temperature >= 0 AND temperature <= 2),
    CHECK (max_tokens > 0),
    CHECK (timeout_ms > 0),
    CHECK (retry_count >= 0 AND retry_count <= 10),
    CHECK (backoff_ms >= 0),
    CHECK (cooldown_ms >= 0),
    CHECK (min_confidence >= 0 AND min_confidence <= 1)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_global_detectors_active
    ON prompt_global_detectors ((is_active)) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_prompt_global_detectors_version
    ON prompt_global_detectors (version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS prompt_scenarios (
    id TEXT PRIMARY KEY,
    game_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL,
    activated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    steps_json JSONB NOT NULL,
    transitions_json JSONB NOT NULL,
    CHECK (char_length(id) > 0),
    CHECK (char_length(game_slug) > 0),
    CHECK (char_length(name) > 0),
    CHECK (version > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_scenarios_active_game
    ON prompt_scenarios (game_slug) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_prompt_scenarios_game_version
    ON prompt_scenarios (game_slug, version DESC, created_at DESC);
`

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
		if err := ValidateCreateRequest(CreateRequest{Stage: code, Template: step.PromptTemplate, Model: step.Model, Temperature: step.Temperature, MaxTokens: step.MaxTokens, TimeoutMS: step.TimeoutMS, RetryCount: step.RetryCount, BackoffMS: step.BackoffMS, CooldownMS: step.CooldownMS, MinConfidence: step.MinConfidence}); err != nil {
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

type ScenarioService struct {
	mu              sync.RWMutex
	counter         int
	globalDetectors []PromptTemplate
	scenariosByGame map[string][]ScenarioVersion

	db              *sql.DB
	schemaMu        sync.Mutex
	schemaEnsured   bool
	schemaEnsureErr error
}

func NewScenarioService() *ScenarioService {
	return &ScenarioService{scenariosByGame: map[string][]ScenarioVersion{}}
}

func NewPostgresScenarioService(db *sql.DB) *ScenarioService {
	return &ScenarioService{db: db, scenariosByGame: map[string][]ScenarioVersion{}}
}

func (s *ScenarioService) ensureSchema(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.schemaEnsured {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, scenarioConfigDDL); err != nil {
		s.schemaEnsureErr = fmt.Errorf("ensure prompt scenario schema: %w", err)
		return s.schemaEnsureErr
	}
	s.schemaEnsured = true
	s.schemaEnsureErr = nil
	return nil
}

func (s *ScenarioService) CreateGlobalDetector(ctx context.Context, req CreateRequest) (PromptTemplate, error) {
	if s.db != nil {
		return s.createGlobalDetectorDB(ctx, req)
	}
	return s.createPromptMemory(ctx, PromptKindGlobalDetector, "", req)
}

func (s *ScenarioService) ListGlobalDetectors(ctx context.Context) []PromptTemplate {
	if s.db != nil {
		items, err := s.listGlobalDetectorsDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]PromptTemplate, len(s.globalDetectors))
	copy(items, s.globalDetectors)
	sort.Slice(items, func(i, j int) bool { return items[i].Version > items[j].Version })
	return items
}

func (s *ScenarioService) createPromptMemory(_ context.Context, kind string, gameSlug string, req CreateRequest) (PromptTemplate, error) {
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
	item := PromptTemplate{ID: fmt.Sprintf("prompt-template-%d", s.counter), Kind: kind, Stage: strings.TrimSpace(req.Stage), GameSlug: strings.TrimSpace(gameSlug), Version: len(s.globalDetectors) + 1, Template: strings.TrimSpace(req.Template), Model: strings.TrimSpace(req.Model), Temperature: req.Temperature, MaxTokens: req.MaxTokens, TimeoutMS: req.TimeoutMS, RetryCount: req.RetryCount, BackoffMS: req.BackoffMS, CooldownMS: req.CooldownMS, MinConfidence: req.MinConfidence, CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now, IsActive: true, ActivatedBy: strings.TrimSpace(req.ActorID), ActivatedAt: now}
	for i := range s.globalDetectors {
		s.globalDetectors[i].IsActive = false
	}
	s.globalDetectors = append(s.globalDetectors, item)
	return item, nil
}

func (s *ScenarioService) GetActiveGlobalDetector(ctx context.Context) (PromptTemplate, error) {
	if s.db != nil {
		return s.getActiveGlobalDetectorDB(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.globalDetectors) - 1; i >= 0; i-- {
		if s.globalDetectors[i].IsActive {
			return s.globalDetectors[i], nil
		}
	}
	return PromptTemplate{}, ErrNotFound
}

func (s *ScenarioService) GetGlobalDetector(ctx context.Context, id string) (PromptTemplate, error) {
	if s.db != nil {
		return s.getGlobalDetectorDB(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, detector := range s.globalDetectors {
		if detector.ID == strings.TrimSpace(id) {
			return detector, nil
		}
	}
	return PromptTemplate{}, ErrDetectorNotFound
}

func (s *ScenarioService) UpdateGlobalDetector(ctx context.Context, id string, req CreateRequest) (PromptTemplate, error) {
	if s.db != nil {
		return s.updateGlobalDetectorDB(ctx, id, req)
	}
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

func (s *ScenarioService) ActivateGlobalDetector(ctx context.Context, id, actorID string) (PromptTemplate, error) {
	if s.db != nil {
		return s.activateGlobalDetectorDB(ctx, id, actorID)
	}
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

func (s *ScenarioService) DeleteGlobalDetector(ctx context.Context, id string) error {
	if s.db != nil {
		return s.deleteGlobalDetectorDB(ctx, id)
	}
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

func (s *ScenarioService) CreateScenario(ctx context.Context, req CreateScenarioRequest) (ScenarioVersion, error) {
	if s.db != nil {
		return s.createScenarioDB(ctx, req)
	}
	return s.createScenarioMemory(ctx, req)
}

func (s *ScenarioService) createScenarioMemory(_ context.Context, req CreateScenarioRequest) (ScenarioVersion, error) {
	if err := ValidateCreateScenarioRequest(req); err != nil {
		return ScenarioVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	gameSlug := strings.ToLower(strings.TrimSpace(req.GameSlug))
	version := len(s.scenariosByGame[gameSlug]) + 1
	now := time.Now().UTC()
	s.counter++
	scenario := ScenarioVersion{ID: fmt.Sprintf("scenario-%d", s.counter), GameSlug: gameSlug, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Version: version, IsActive: len(s.scenariosByGame[gameSlug]) == 0, CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now}
	if scenario.IsActive {
		scenario.ActivatedBy = strings.TrimSpace(req.ActorID)
		scenario.ActivatedAt = now
	}
	populateScenarioDetails(&s.counter, &scenario, req, now)
	s.scenariosByGame[gameSlug] = append(s.scenariosByGame[gameSlug], scenario)
	return scenario, nil
}

func populateScenarioDetails(counter *int, scenario *ScenarioVersion, req CreateScenarioRequest, now time.Time) {
	for idx, step := range req.Steps {
		*counter++
		prompt := PromptTemplate{ID: fmt.Sprintf("prompt-template-%d", *counter), Kind: PromptKindScenarioStep, Stage: strings.TrimSpace(step.Code), GameSlug: scenario.GameSlug, Version: scenario.Version, Template: strings.TrimSpace(step.PromptTemplate), Model: strings.TrimSpace(step.Model), Temperature: step.Temperature, MaxTokens: step.MaxTokens, TimeoutMS: step.TimeoutMS, RetryCount: step.RetryCount, BackoffMS: step.BackoffMS, CooldownMS: step.CooldownMS, MinConfidence: step.MinConfidence, CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now, IsActive: scenario.IsActive, ActivatedBy: scenario.ActivatedBy, ActivatedAt: scenario.ActivatedAt}
		scenario.Steps = append(scenario.Steps, ScenarioStep{ID: fmt.Sprintf("scenario-step-%d", *counter), Code: strings.TrimSpace(step.Code), Title: strings.TrimSpace(step.Title), Position: idx + 1, Prompt: prompt})
	}
	for _, transition := range req.Transitions {
		*counter++
		scenario.Transitions = append(scenario.Transitions, ScenarioTransition{ID: fmt.Sprintf("scenario-transition-%d", *counter), FromStepCode: strings.TrimSpace(transition.FromStepCode), Outcome: strings.TrimSpace(transition.Outcome), ToStepCode: strings.TrimSpace(transition.ToStepCode), Terminal: transition.Terminal})
	}
}

func (s *ScenarioService) GetScenario(ctx context.Context, id string) (ScenarioVersion, error) {
	if s.db != nil {
		return s.getScenarioDB(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, scenarios := range s.scenariosByGame {
		for _, scenario := range scenarios {
			if scenario.ID == strings.TrimSpace(id) {
				return scenario, nil
			}
		}
	}
	return ScenarioVersion{}, ErrScenarioNotFound
}

func (s *ScenarioService) UpdateScenario(ctx context.Context, id string, req CreateScenarioRequest) (ScenarioVersion, error) {
	if s.db != nil {
		return s.updateScenarioDB(ctx, id, req)
	}
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
			updated := ScenarioVersion{ID: current.ID, GameSlug: targetGame, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Version: current.Version, IsActive: current.IsActive, CreatedBy: current.CreatedBy, ActivatedBy: current.ActivatedBy, CreatedAt: current.CreatedAt, ActivatedAt: current.ActivatedAt}
			populateScenarioDetails(&s.counter, &updated, req, now)
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

func (s *ScenarioService) ActivateScenario(ctx context.Context, id, actorID string) (ScenarioVersion, error) {
	if s.db != nil {
		return s.activateScenarioDB(ctx, id, actorID)
	}
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

func (s *ScenarioService) DeleteScenario(ctx context.Context, id string) error {
	if s.db != nil {
		return s.deleteScenarioDB(ctx, id)
	}
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

func (s *ScenarioService) GetActiveScenarioByGame(ctx context.Context, gameSlug string) (ScenarioVersion, error) {
	if s.db != nil {
		return s.getActiveScenarioByGameDB(ctx, gameSlug)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, scenario := range s.scenariosByGame[strings.ToLower(strings.TrimSpace(gameSlug))] {
		if scenario.IsActive {
			return scenario, nil
		}
	}
	return ScenarioVersion{}, ErrScenarioNotFound
}

func (s *ScenarioService) ListScenarios(ctx context.Context) []ScenarioVersion {
	if s.db != nil {
		items, err := s.listScenariosDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ScenarioVersion, 0)
	for _, scenarios := range s.scenariosByGame {
		items = append(items, scenarios...)
	}
	sortScenarios(items)
	return items
}

func sortScenarios(items []ScenarioVersion) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].GameSlug == items[j].GameSlug {
			return items[i].Version > items[j].Version
		}
		return items[i].GameSlug < items[j].GameSlug
	})
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

func (s *ScenarioService) createGlobalDetectorDB(ctx context.Context, req CreateRequest) (PromptTemplate, error) {
	if err := ValidateCreateRequest(req); err != nil {
		return PromptTemplate{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return PromptTemplate{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PromptTemplate{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM prompt_global_detectors`).Scan(&version); err != nil {
		return PromptTemplate{}, fmt.Errorf("select detector version: %w", err)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("prompt-template-%d", now.UnixNano())
	if _, err := tx.ExecContext(ctx, `UPDATE prompt_global_detectors SET is_active = FALSE WHERE is_active = TRUE`); err != nil {
		return PromptTemplate{}, fmt.Errorf("deactivate detectors: %w", err)
	}
	item := PromptTemplate{ID: id, Kind: PromptKindGlobalDetector, Stage: strings.TrimSpace(req.Stage), Version: version, Template: strings.TrimSpace(req.Template), Model: strings.TrimSpace(req.Model), Temperature: req.Temperature, MaxTokens: req.MaxTokens, TimeoutMS: req.TimeoutMS, RetryCount: req.RetryCount, BackoffMS: req.BackoffMS, CooldownMS: req.CooldownMS, MinConfidence: req.MinConfidence, IsActive: true, CreatedBy: strings.TrimSpace(req.ActorID), ActivatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now, ActivatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_global_detectors (id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,TRUE,$13,$14,$15,$16)`, item.ID, item.Stage, item.Version, item.Template, item.Model, item.Temperature, item.MaxTokens, item.TimeoutMS, item.RetryCount, item.BackoffMS, item.CooldownMS, item.MinConfidence, item.CreatedBy, item.ActivatedBy, item.CreatedAt, item.ActivatedAt); err != nil {
		return PromptTemplate{}, fmt.Errorf("insert detector: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PromptTemplate{}, err
	}
	return item, nil
}

func (s *ScenarioService) listGlobalDetectorsDB(ctx context.Context) ([]PromptTemplate, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM prompt_global_detectors ORDER BY version DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list detectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var items []PromptTemplate
	for rows.Next() {
		item, err := scanDetector(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ScenarioService) getActiveGlobalDetectorDB(ctx context.Context) (PromptTemplate, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return PromptTemplate{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM prompt_global_detectors WHERE is_active = TRUE ORDER BY version DESC LIMIT 1`)
	item, err := scanDetector(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptTemplate{}, ErrNotFound
	}
	return item, err
}

func (s *ScenarioService) getGlobalDetectorDB(ctx context.Context, id string) (PromptTemplate, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return PromptTemplate{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at FROM prompt_global_detectors WHERE id = $1`, strings.TrimSpace(id))
	item, err := scanDetector(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptTemplate{}, ErrDetectorNotFound
	}
	return item, err
}

func (s *ScenarioService) updateGlobalDetectorDB(ctx context.Context, id string, req CreateRequest) (PromptTemplate, error) {
	if err := ValidateCreateRequest(req); err != nil {
		return PromptTemplate{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return PromptTemplate{}, err
	}
	const query = `UPDATE prompt_global_detectors SET stage=$2, template=$3, model=$4, temperature=$5, max_tokens=$6, timeout_ms=$7, retry_count=$8, backoff_ms=$9, cooldown_ms=$10, min_confidence=$11 WHERE id=$1 RETURNING id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at`
	row := s.db.QueryRowContext(ctx, query, strings.TrimSpace(id), strings.TrimSpace(req.Stage), strings.TrimSpace(req.Template), strings.TrimSpace(req.Model), req.Temperature, req.MaxTokens, req.TimeoutMS, req.RetryCount, req.BackoffMS, req.CooldownMS, req.MinConfidence)
	item, err := scanDetector(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptTemplate{}, ErrDetectorNotFound
	}
	return item, err
}

func (s *ScenarioService) activateGlobalDetectorDB(ctx context.Context, id, actorID string) (PromptTemplate, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return PromptTemplate{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PromptTemplate{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.ExecContext(ctx, `UPDATE prompt_global_detectors SET is_active=FALSE WHERE is_active=TRUE`)
	if err != nil {
		return PromptTemplate{}, fmt.Errorf("deactivate detectors: %w", err)
	}
	_ = result
	row := tx.QueryRowContext(ctx, `UPDATE prompt_global_detectors SET is_active=TRUE, activated_by=$2, activated_at=$3 WHERE id=$1 RETURNING id, stage, version, template, model, temperature, max_tokens, timeout_ms, retry_count, backoff_ms, cooldown_ms, min_confidence, is_active, created_by, activated_by, created_at, activated_at`, strings.TrimSpace(id), strings.TrimSpace(actorID), time.Now().UTC())
	item, err := scanDetector(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptTemplate{}, ErrDetectorNotFound
	}
	if err != nil {
		return PromptTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return PromptTemplate{}, err
	}
	return item, nil
}

func (s *ScenarioService) deleteGlobalDetectorDB(ctx context.Context, id string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var wasActive bool
	if err := tx.QueryRowContext(ctx, `SELECT is_active FROM prompt_global_detectors WHERE id=$1`, strings.TrimSpace(id)).Scan(&wasActive); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDetectorNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_global_detectors WHERE id=$1`, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("delete detector: %w", err)
	}
	if wasActive {
		_, err = tx.ExecContext(ctx, `UPDATE prompt_global_detectors SET is_active=TRUE, activated_at=$1 WHERE id = (SELECT id FROM prompt_global_detectors ORDER BY version DESC, created_at DESC LIMIT 1)`, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("reactivate detector: %w", err)
		}
	}
	return tx.Commit()
}

func scanDetector(row interface{ Scan(dest ...any) error }) (PromptTemplate, error) {
	var item PromptTemplate
	var activatedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.Stage, &item.Version, &item.Template, &item.Model, &item.Temperature, &item.MaxTokens, &item.TimeoutMS, &item.RetryCount, &item.BackoffMS, &item.CooldownMS, &item.MinConfidence, &item.IsActive, &item.CreatedBy, &item.ActivatedBy, &item.CreatedAt, &activatedAt); err != nil {
		return PromptTemplate{}, err
	}
	item.Kind = PromptKindGlobalDetector
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time.UTC()
	}
	item.CreatedAt = item.CreatedAt.UTC()
	return item, nil
}

func (s *ScenarioService) createScenarioDB(ctx context.Context, req CreateScenarioRequest) (ScenarioVersion, error) {
	if err := ValidateCreateScenarioRequest(req); err != nil {
		return ScenarioVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScenarioVersion{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	gameSlug := strings.ToLower(strings.TrimSpace(req.GameSlug))
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM prompt_scenarios WHERE game_slug = $1`, gameSlug).Scan(&version); err != nil {
		return ScenarioVersion{}, fmt.Errorf("select scenario version: %w", err)
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_scenarios WHERE game_slug = $1`, gameSlug).Scan(&existing); err != nil {
		return ScenarioVersion{}, fmt.Errorf("count scenarios: %w", err)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("scenario-%d", now.UnixNano())
	scenario := buildScenarioRecord(id, version, existing == 0, now, req)
	if scenario.IsActive {
		if _, err := tx.ExecContext(ctx, `UPDATE prompt_scenarios SET is_active = FALSE WHERE game_slug = $1`, gameSlug); err != nil {
			return ScenarioVersion{}, err
		}
	}
	stepsJSON, transitionsJSON, err := marshalScenarioPayload(scenario)
	if err != nil {
		return ScenarioVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_scenarios (id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, scenario.ID, scenario.GameSlug, scenario.Name, scenario.Description, scenario.Version, scenario.IsActive, scenario.CreatedBy, scenario.ActivatedBy, scenario.CreatedAt, nullableTime(scenario.ActivatedAt), stepsJSON, transitionsJSON); err != nil {
		return ScenarioVersion{}, fmt.Errorf("insert scenario: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ScenarioVersion{}, err
	}
	return scenario, nil
}

func buildScenarioRecord(id string, version int, active bool, now time.Time, req CreateScenarioRequest) ScenarioVersion {
	scenario := ScenarioVersion{ID: id, GameSlug: strings.ToLower(strings.TrimSpace(req.GameSlug)), Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Version: version, IsActive: active, CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now}
	if active {
		scenario.ActivatedBy = strings.TrimSpace(req.ActorID)
		scenario.ActivatedAt = now
	}
	counter := 0
	populateScenarioDetails(&counter, &scenario, req, now)
	return scenario
}

func marshalScenarioPayload(item ScenarioVersion) ([]byte, []byte, error) {
	stepsJSON, err := json.Marshal(item.Steps)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal scenario steps: %w", err)
	}
	transitionsJSON, err := json.Marshal(item.Transitions)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal scenario transitions: %w", err)
	}
	return stepsJSON, transitionsJSON, nil
}

func (s *ScenarioService) getScenarioDB(ctx context.Context, id string) (ScenarioVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioVersion{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json FROM prompt_scenarios WHERE id = $1`, strings.TrimSpace(id))
	item, err := scanScenario(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioVersion{}, ErrScenarioNotFound
	}
	return item, err
}

func (s *ScenarioService) updateScenarioDB(ctx context.Context, id string, req CreateScenarioRequest) (ScenarioVersion, error) {
	if err := ValidateCreateScenarioRequest(req); err != nil {
		return ScenarioVersion{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioVersion{}, err
	}
	current, err := s.getScenarioDB(ctx, id)
	if err != nil {
		return ScenarioVersion{}, err
	}
	updated := buildScenarioRecord(current.ID, current.Version, current.IsActive, current.CreatedAt, CreateScenarioRequest{GameSlug: req.GameSlug, Name: req.Name, Description: req.Description, ActorID: current.CreatedBy, Steps: req.Steps, Transitions: req.Transitions})
	updated.ActivatedAt = current.ActivatedAt
	updated.ActivatedBy = current.ActivatedBy
	updated.CreatedAt = current.CreatedAt
	updated.CreatedBy = current.CreatedBy
	stepsJSON, transitionsJSON, err := marshalScenarioPayload(updated)
	if err != nil {
		return ScenarioVersion{}, err
	}
	const query = `UPDATE prompt_scenarios SET game_slug=$2, name=$3, description=$4, steps_json=$5, transitions_json=$6 WHERE id=$1 RETURNING id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json`
	row := s.db.QueryRowContext(ctx, query, strings.TrimSpace(id), updated.GameSlug, updated.Name, updated.Description, stepsJSON, transitionsJSON)
	item, err := scanScenario(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioVersion{}, ErrScenarioNotFound
	}
	return item, err
}

func (s *ScenarioService) activateScenarioDB(ctx context.Context, id, actorID string) (ScenarioVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioVersion{}, err
	}
	current, err := s.getScenarioDB(ctx, id)
	if err != nil {
		return ScenarioVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScenarioVersion{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `UPDATE prompt_scenarios SET is_active = FALSE WHERE game_slug = $1`, current.GameSlug); err != nil {
		return ScenarioVersion{}, err
	}
	row := tx.QueryRowContext(ctx, `UPDATE prompt_scenarios SET is_active = TRUE, activated_by = $2, activated_at = $3 WHERE id = $1 RETURNING id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json`, current.ID, strings.TrimSpace(actorID), time.Now().UTC())
	item, err := scanScenario(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioVersion{}, ErrScenarioNotFound
	}
	if err != nil {
		return ScenarioVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScenarioVersion{}, err
	}
	return item, nil
}

func (s *ScenarioService) deleteScenarioDB(ctx context.Context, id string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	current, err := s.getScenarioDB(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_scenarios WHERE id = $1`, current.ID); err != nil {
		return err
	}
	if current.IsActive {
		_, err = tx.ExecContext(ctx, `UPDATE prompt_scenarios SET is_active = TRUE, activated_at = $2 WHERE id = (SELECT id FROM prompt_scenarios WHERE game_slug = $1 ORDER BY version DESC, created_at DESC LIMIT 1)`, current.GameSlug, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ScenarioService) getActiveScenarioByGameDB(ctx context.Context, gameSlug string) (ScenarioVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return ScenarioVersion{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json FROM prompt_scenarios WHERE game_slug = $1 AND is_active = TRUE ORDER BY version DESC LIMIT 1`, strings.ToLower(strings.TrimSpace(gameSlug)))
	item, err := scanScenario(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioVersion{}, ErrScenarioNotFound
	}
	return item, err
}

func (s *ScenarioService) listScenariosDB(ctx context.Context) ([]ScenarioVersion, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, game_slug, name, description, version, is_active, created_by, activated_by, created_at, activated_at, steps_json, transitions_json FROM prompt_scenarios ORDER BY game_slug ASC, version DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var items []ScenarioVersion
	for rows.Next() {
		item, err := scanScenario(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanScenario(row interface{ Scan(dest ...any) error }) (ScenarioVersion, error) {
	var item ScenarioVersion
	var activatedAt sql.NullTime
	var stepsJSON, transitionsJSON []byte
	if err := row.Scan(&item.ID, &item.GameSlug, &item.Name, &item.Description, &item.Version, &item.IsActive, &item.CreatedBy, &item.ActivatedBy, &item.CreatedAt, &activatedAt, &stepsJSON, &transitionsJSON); err != nil {
		return ScenarioVersion{}, err
	}
	if err := json.Unmarshal(stepsJSON, &item.Steps); err != nil {
		return ScenarioVersion{}, fmt.Errorf("unmarshal scenario steps: %w", err)
	}
	if err := json.Unmarshal(transitionsJSON, &item.Transitions); err != nil {
		return ScenarioVersion{}, fmt.Errorf("unmarshal scenario transitions: %w", err)
	}
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time.UTC()
	}
	item.CreatedAt = item.CreatedAt.UTC()
	for i := range item.Steps {
		item.Steps[i].Prompt.Kind = PromptKindScenarioStep
		item.Steps[i].Prompt.GameSlug = item.GameSlug
		item.Steps[i].Prompt.Version = item.Version
		item.Steps[i].Prompt.IsActive = item.IsActive
		item.Steps[i].Prompt.ActivatedBy = item.ActivatedBy
		item.Steps[i].Prompt.ActivatedAt = item.ActivatedAt
	}
	return item, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
