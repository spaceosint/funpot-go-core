package prompts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidGameSlug           = errors.New("gameSlug must not be empty")
	ErrInvalidStateSchemaName    = errors.New("state schema name must not be empty")
	ErrInvalidStateFieldKey      = errors.New("state field key must not be empty")
	ErrInvalidStateFieldType     = errors.New("state field type must not be empty")
	ErrInvalidStateSchemaJSON    = errors.New("stateSchemaJson must be a valid JSON object")
	ErrInvalidDeltaSchemaJSON    = errors.New("deltaSchemaJson must be a valid JSON object")
	ErrInvalidInitialStateJSON   = errors.New("initialStateJson must be a valid JSON object")
	ErrStateSchemaNotFound       = errors.New("state schema not found")
	ErrInvalidRuleSetName        = errors.New("rule set name must not be empty")
	ErrInvalidRuleFieldKey       = errors.New("rule item fieldKey must not be empty")
	ErrInvalidRuleOperation      = errors.New("rule item operation must not be empty")
	ErrInvalidRuleConfidenceMode = errors.New("rule item confidenceMode must not be empty")
	ErrInvalidRuleCondition      = errors.New("rule condition must not be empty")
	ErrInvalidRuleAction         = errors.New("rule action must not be empty")
	ErrRuleSetNotFound           = errors.New("rule set not found")
)

type StateFieldDefinition struct {
	Key                string   `json:"key"`
	Label              string   `json:"label"`
	Description        string   `json:"description,omitempty"`
	Type               string   `json:"type"`
	EnumValues         []string `json:"enumValues,omitempty"`
	ConfidenceRequired bool     `json:"confidenceRequired"`
	EvidenceBearing    bool     `json:"evidenceBearing"`
	Inferred           bool     `json:"inferred"`
	FinalOnly          bool     `json:"finalOnly"`
}

type StateSchemaCreateRequest struct {
	GameSlug         string
	Name             string
	Description      string
	Fields           []StateFieldDefinition
	StateSchemaJSON  string
	DeltaSchemaJSON  string
	InitialStateJSON string
	ActorID          string
}

type StateSchemaVersion struct {
	ID               string                 `json:"id"`
	GameSlug         string                 `json:"gameSlug"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description,omitempty"`
	Version          int                    `json:"version"`
	Fields           []StateFieldDefinition `json:"fields"`
	StateSchemaJSON  string                 `json:"stateSchemaJson,omitempty"`
	DeltaSchemaJSON  string                 `json:"deltaSchemaJson,omitempty"`
	InitialStateJSON string                 `json:"initialStateJson,omitempty"`
	IsActive         bool                   `json:"isActive"`
	CreatedBy        string                 `json:"createdBy"`
	ActivatedBy      string                 `json:"activatedBy,omitempty"`
	CreatedAt        time.Time              `json:"createdAt"`
	ActivatedAt      time.Time              `json:"activatedAt,omitempty"`
}

type RuleItem struct {
	ID             string   `json:"id"`
	FieldKey       string   `json:"fieldKey"`
	Operation      string   `json:"operation"`
	EvidenceKinds  []string `json:"evidenceKinds,omitempty"`
	ConfidenceMode string   `json:"confidenceMode"`
	FinalOnly      bool     `json:"finalOnly"`
}

type RuleCondition struct {
	ID          string `json:"id"`
	Priority    int    `json:"priority"`
	Condition   string `json:"condition"`
	Action      string `json:"action"`
	TargetField string `json:"targetField,omitempty"`
}

type RuleSetCreateRequest struct {
	GameSlug          string
	Name              string
	Description       string
	RuleItems         []RuleItem
	FinalizationRules []RuleCondition
	ActorID           string
}

type RuleSetVersion struct {
	ID                string          `json:"id"`
	GameSlug          string          `json:"gameSlug"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	Version           int             `json:"version"`
	RuleItems         []RuleItem      `json:"ruleItems"`
	FinalizationRules []RuleCondition `json:"finalizationRules"`
	IsActive          bool            `json:"isActive"`
	CreatedBy         string          `json:"createdBy"`
	ActivatedBy       string          `json:"activatedBy,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	ActivatedAt       time.Time       `json:"activatedAt,omitempty"`
}

func ValidateStateSchemaCreateRequest(req StateSchemaCreateRequest) error {
	if strings.TrimSpace(req.GameSlug) == "" {
		return ErrInvalidGameSlug
	}
	if strings.TrimSpace(req.Name) == "" {
		return ErrInvalidStateSchemaName
	}
	if len(req.Fields) == 0 && strings.TrimSpace(req.InitialStateJSON) == "" && strings.TrimSpace(req.StateSchemaJSON) == "" {
		return ErrInvalidStateFieldKey
	}
	seen := map[string]struct{}{}
	for _, field := range req.Fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			return ErrInvalidStateFieldKey
		}
		if strings.TrimSpace(field.Type) == "" {
			return ErrInvalidStateFieldType
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate state field key: %s", key)
		}
		seen[key] = struct{}{}
	}
	if raw := strings.TrimSpace(req.InitialStateJSON); raw != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return ErrInvalidInitialStateJSON
		}
	}
	if raw := strings.TrimSpace(req.StateSchemaJSON); raw != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return ErrInvalidStateSchemaJSON
		}
	}
	if raw := strings.TrimSpace(req.DeltaSchemaJSON); raw != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return ErrInvalidDeltaSchemaJSON
		}
	}
	return nil
}

func ValidateRuleSetCreateRequest(req RuleSetCreateRequest) error {
	if strings.TrimSpace(req.GameSlug) == "" {
		return ErrInvalidGameSlug
	}
	if strings.TrimSpace(req.Name) == "" {
		return ErrInvalidRuleSetName
	}
	if len(req.RuleItems) == 0 {
		return ErrInvalidRuleFieldKey
	}
	if len(req.FinalizationRules) == 0 {
		return ErrInvalidRuleCondition
	}
	for _, item := range req.RuleItems {
		if strings.TrimSpace(item.FieldKey) == "" {
			return ErrInvalidRuleFieldKey
		}
		if strings.TrimSpace(item.Operation) == "" {
			return ErrInvalidRuleOperation
		}
		if strings.TrimSpace(item.ConfidenceMode) == "" {
			return ErrInvalidRuleConfidenceMode
		}
	}
	for _, item := range req.FinalizationRules {
		if strings.TrimSpace(item.Condition) == "" {
			return ErrInvalidRuleCondition
		}
		if strings.TrimSpace(item.Action) == "" {
			return ErrInvalidRuleAction
		}
	}
	return nil
}

func (s *Service) initTrackerConfigMaps() {
	if s.stateSchemas == nil {
		s.stateSchemas = map[string][]StateSchemaVersion{}
	}
	if s.ruleSets == nil {
		s.ruleSets = map[string][]RuleSetVersion{}
	}
}

func (s *Service) ListStateSchemas(ctx context.Context) []StateSchemaVersion {
	if s.db != nil {
		items, err := s.listStateSchemasDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]StateSchemaVersion, 0)
	for _, versions := range s.stateSchemas {
		items = append(items, versions...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GameSlug == items[j].GameSlug {
			return items[i].Version > items[j].Version
		}
		return items[i].GameSlug < items[j].GameSlug
	})
	return items
}

func (s *Service) CreateStateSchema(ctx context.Context, req StateSchemaCreateRequest) (StateSchemaVersion, error) {
	if s.db != nil {
		return s.createStateSchemaDB(ctx, req)
	}
	if err := ValidateStateSchemaCreateRequest(req); err != nil {
		return StateSchemaVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	gameSlug := strings.TrimSpace(req.GameSlug)
	now := time.Now().UTC()
	s.counter++
	item := StateSchemaVersion{ID: fmt.Sprintf("state-schema-%d", s.counter), GameSlug: gameSlug, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Version: len(s.stateSchemas[gameSlug]) + 1, Fields: append([]StateFieldDefinition(nil), req.Fields...), StateSchemaJSON: strings.TrimSpace(req.StateSchemaJSON), DeltaSchemaJSON: strings.TrimSpace(req.DeltaSchemaJSON), InitialStateJSON: strings.TrimSpace(req.InitialStateJSON), CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now}
	if len(s.stateSchemas[gameSlug]) == 0 {
		item.IsActive = true
		item.ActivatedBy = strings.TrimSpace(req.ActorID)
		item.ActivatedAt = now
	}
	s.stateSchemas[gameSlug] = append(s.stateSchemas[gameSlug], item)
	return item, nil
}

func (s *Service) GetStateSchema(ctx context.Context, id string) (StateSchemaVersion, error) {
	if s.db != nil {
		return s.getStateSchemaDB(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, versions := range s.stateSchemas {
		for _, item := range versions {
			if item.ID == strings.TrimSpace(id) {
				return item, nil
			}
		}
	}
	return StateSchemaVersion{}, ErrStateSchemaNotFound
}

func (s *Service) UpdateStateSchema(ctx context.Context, id string, req StateSchemaCreateRequest) (StateSchemaVersion, error) {
	if s.db != nil {
		return s.updateStateSchemaDB(ctx, id, req)
	}
	if err := ValidateStateSchemaCreateRequest(req); err != nil {
		return StateSchemaVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.stateSchemas {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			updated := item
			updated.GameSlug = strings.TrimSpace(req.GameSlug)
			updated.Name = strings.TrimSpace(req.Name)
			updated.Description = strings.TrimSpace(req.Description)
			updated.Fields = append([]StateFieldDefinition(nil), req.Fields...)
			updated.StateSchemaJSON = strings.TrimSpace(req.StateSchemaJSON)
			updated.DeltaSchemaJSON = strings.TrimSpace(req.DeltaSchemaJSON)
			updated.InitialStateJSON = strings.TrimSpace(req.InitialStateJSON)
			if updated.GameSlug != gameSlug {
				s.stateSchemas[gameSlug] = append(versions[:i], versions[i+1:]...)
				s.stateSchemas[updated.GameSlug] = append(s.stateSchemas[updated.GameSlug], updated)
			} else {
				versions[i] = updated
				s.stateSchemas[gameSlug] = versions
			}
			return updated, nil
		}
	}
	return StateSchemaVersion{}, ErrStateSchemaNotFound
}

func (s *Service) DeleteStateSchema(ctx context.Context, id string) error {
	if s.db != nil {
		return s.deleteStateSchemaDB(ctx, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.stateSchemas {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			s.stateSchemas[gameSlug] = append(versions[:i], versions[i+1:]...)
			if item.IsActive && len(s.stateSchemas[gameSlug]) > 0 {
				s.stateSchemas[gameSlug][0].IsActive = true
			}
			return nil
		}
	}
	return ErrStateSchemaNotFound
}

func (s *Service) ActivateStateSchema(ctx context.Context, id, actorID string) (StateSchemaVersion, error) {
	if s.db != nil {
		return s.activateStateSchemaDB(ctx, id, actorID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.stateSchemas {
		active := -1
		for i := range versions {
			if versions[i].ID == lookup {
				active = i
				break
			}
		}
		if active == -1 {
			continue
		}
		now := time.Now().UTC()
		for i := range versions {
			versions[i].IsActive = i == active
			if i == active {
				versions[i].ActivatedAt = now
				versions[i].ActivatedBy = strings.TrimSpace(actorID)
			}
		}
		s.stateSchemas[gameSlug] = versions
		return versions[active], nil
	}
	return StateSchemaVersion{}, ErrStateSchemaNotFound
}

func (s *Service) GetActiveStateSchema(ctx context.Context, gameSlug string) (StateSchemaVersion, error) {
	if s.db != nil {
		return s.getActiveStateSchemaDB(ctx, gameSlug)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.stateSchemas[strings.TrimSpace(gameSlug)] {
		if item.IsActive {
			return item, nil
		}
	}
	return StateSchemaVersion{}, ErrStateSchemaNotFound
}

func (s *Service) ListRuleSets(ctx context.Context) []RuleSetVersion {
	if s.db != nil {
		items, err := s.listRuleSetsDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]RuleSetVersion, 0)
	for _, versions := range s.ruleSets {
		items = append(items, versions...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GameSlug == items[j].GameSlug {
			return items[i].Version > items[j].Version
		}
		return items[i].GameSlug < items[j].GameSlug
	})
	return items
}

func (s *Service) CreateRuleSet(ctx context.Context, req RuleSetCreateRequest) (RuleSetVersion, error) {
	if s.db != nil {
		return s.createRuleSetDB(ctx, req)
	}
	if err := ValidateRuleSetCreateRequest(req); err != nil {
		return RuleSetVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	gameSlug := strings.TrimSpace(req.GameSlug)
	now := time.Now().UTC()
	s.counter++
	item := RuleSetVersion{ID: fmt.Sprintf("rule-set-%d", s.counter), GameSlug: gameSlug, Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Version: len(s.ruleSets[gameSlug]) + 1, RuleItems: append([]RuleItem(nil), req.RuleItems...), FinalizationRules: append([]RuleCondition(nil), req.FinalizationRules...), CreatedBy: strings.TrimSpace(req.ActorID), CreatedAt: now}
	if len(s.ruleSets[gameSlug]) == 0 {
		item.IsActive = true
		item.ActivatedAt = now
		item.ActivatedBy = strings.TrimSpace(req.ActorID)
	}
	s.ruleSets[gameSlug] = append(s.ruleSets[gameSlug], item)
	return item, nil
}

func (s *Service) GetRuleSet(ctx context.Context, id string) (RuleSetVersion, error) {
	if s.db != nil {
		return s.getRuleSetDB(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, versions := range s.ruleSets {
		for _, item := range versions {
			if item.ID == strings.TrimSpace(id) {
				return item, nil
			}
		}
	}
	return RuleSetVersion{}, ErrRuleSetNotFound
}

func (s *Service) UpdateRuleSet(ctx context.Context, id string, req RuleSetCreateRequest) (RuleSetVersion, error) {
	if s.db != nil {
		return s.updateRuleSetDB(ctx, id, req)
	}
	if err := ValidateRuleSetCreateRequest(req); err != nil {
		return RuleSetVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.ruleSets {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			updated := item
			updated.GameSlug = strings.TrimSpace(req.GameSlug)
			updated.Name = strings.TrimSpace(req.Name)
			updated.Description = strings.TrimSpace(req.Description)
			updated.RuleItems = append([]RuleItem(nil), req.RuleItems...)
			updated.FinalizationRules = append([]RuleCondition(nil), req.FinalizationRules...)
			if updated.GameSlug != gameSlug {
				s.ruleSets[gameSlug] = append(versions[:i], versions[i+1:]...)
				s.ruleSets[updated.GameSlug] = append(s.ruleSets[updated.GameSlug], updated)
			} else {
				versions[i] = updated
				s.ruleSets[gameSlug] = versions
			}
			return updated, nil
		}
	}
	return RuleSetVersion{}, ErrRuleSetNotFound
}

func (s *Service) DeleteRuleSet(ctx context.Context, id string) error {
	if s.db != nil {
		return s.deleteRuleSetDB(ctx, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.ruleSets {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			s.ruleSets[gameSlug] = append(versions[:i], versions[i+1:]...)
			if item.IsActive && len(s.ruleSets[gameSlug]) > 0 {
				s.ruleSets[gameSlug][0].IsActive = true
			}
			return nil
		}
	}
	return ErrRuleSetNotFound
}

func (s *Service) ActivateRuleSet(ctx context.Context, id, actorID string) (RuleSetVersion, error) {
	if s.db != nil {
		return s.activateRuleSetDB(ctx, id, actorID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTrackerConfigMaps()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.ruleSets {
		active := -1
		for i := range versions {
			if versions[i].ID == lookup {
				active = i
				break
			}
		}
		if active == -1 {
			continue
		}
		now := time.Now().UTC()
		for i := range versions {
			versions[i].IsActive = i == active
			if i == active {
				versions[i].ActivatedAt = now
				versions[i].ActivatedBy = strings.TrimSpace(actorID)
			}
		}
		s.ruleSets[gameSlug] = versions
		return versions[active], nil
	}
	return RuleSetVersion{}, ErrRuleSetNotFound
}

func (s *Service) GetActiveRuleSet(ctx context.Context, gameSlug string) (RuleSetVersion, error) {
	if s.db != nil {
		return s.getActiveRuleSetDB(ctx, gameSlug)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.ruleSets[strings.TrimSpace(gameSlug)] {
		if item.IsActive {
			return item, nil
		}
	}
	return RuleSetVersion{}, ErrRuleSetNotFound
}

var _ sync.Locker
