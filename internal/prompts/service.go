package prompts

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service manages versioned LLM prompt templates for each stage.
type Service struct {
	mu               sync.RWMutex
	counter          int
	versions         map[string][]PromptVersion
	stateSchemas     map[string][]StateSchemaVersion
	ruleSets         map[string][]RuleSetVersion
	scenarioPackages map[string][]ScenarioPackage
	modelConfigs     []LLMModelConfig
	db               *sql.DB
	schemaMu         sync.Mutex
	schemaReady      bool
}

func NewService() *Service {
	return &Service{
		versions:         map[string][]PromptVersion{},
		stateSchemas:     map[string][]StateSchemaVersion{},
		ruleSets:         map[string][]RuleSetVersion{},
		scenarioPackages: map[string][]ScenarioPackage{},
		modelConfigs:     []LLMModelConfig{},
	}
}

func NewPostgresService(db *sql.DB) *Service {
	return &Service{
		versions:         map[string][]PromptVersion{},
		stateSchemas:     map[string][]StateSchemaVersion{},
		ruleSets:         map[string][]RuleSetVersion{},
		scenarioPackages: map[string][]ScenarioPackage{},
		modelConfigs:     []LLMModelConfig{},
		db:               db,
	}
}

func (s *Service) List(ctx context.Context) []PromptVersion {
	if s.db != nil {
		items, err := s.listPromptsDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]PromptVersion, 0)
	for _, byStage := range s.versions {
		items = append(items, byStage...)
	}
	sortPromptVersions(items)
	return items
}

func (s *Service) ListActive(ctx context.Context) []PromptVersion {
	if s.db != nil {
		items, err := s.listActivePromptsDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]PromptVersion, 0)
	for _, byStage := range s.versions {
		for _, item := range byStage {
			if item.IsActive {
				items = append(items, item)
				break
			}
		}
	}
	sortPromptVersions(items)
	return items
}

func sortPromptVersions(items []PromptVersion) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Position == items[j].Position {
			if items[i].Stage == items[j].Stage {
				return items[i].Version > items[j].Version
			}
			return items[i].Stage < items[j].Stage
		}
		return items[i].Position < items[j].Position
	})
}

func (s *Service) GetActiveByStage(ctx context.Context, stage string) (PromptVersion, error) {
	if s.db != nil {
		return s.getActivePromptByStageDB(ctx, stage)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	stage = strings.TrimSpace(stage)
	versions := s.versions[stage]
	for _, item := range versions {
		if item.IsActive {
			return item, nil
		}
	}
	return PromptVersion{}, ErrNotFound
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (PromptVersion, error) {
	if s.db != nil {
		return s.createPromptDB(ctx, req)
	}
	if err := ValidateCreateRequest(req); err != nil {
		return PromptVersion{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stage := strings.TrimSpace(req.Stage)
	nextVersion := len(s.versions[stage]) + 1
	s.counter++
	now := time.Now().UTC()
	position := req.Position
	if position <= 0 {
		position = s.nextPositionLocked()
	}
	item := PromptVersion{
		ID:            fmt.Sprintf("prompt-%d", s.counter),
		Stage:         stage,
		Position:      position,
		Version:       nextVersion,
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
	}
	if len(s.versions[stage]) == 0 {
		item.IsActive = true
		item.ActivatedAt = now
		item.ActivatedBy = strings.TrimSpace(req.ActorID)
	}
	s.versions[stage] = append(s.versions[stage], item)
	return item, nil
}

func (s *Service) Get(ctx context.Context, id string) (PromptVersion, error) {
	if s.db != nil {
		return s.getPromptDB(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	trimmedID := strings.TrimSpace(id)
	for _, byStage := range s.versions {
		for _, item := range byStage {
			if item.ID == trimmedID {
				return item, nil
			}
		}
	}
	return PromptVersion{}, ErrNotFound
}

func (s *Service) Update(ctx context.Context, id string, req CreateRequest) (PromptVersion, error) {
	if s.db != nil {
		return s.updatePromptDB(ctx, id, req)
	}
	if err := ValidateCreateRequest(req); err != nil {
		return PromptVersion{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	trimmedID := strings.TrimSpace(id)
	for stage, byStage := range s.versions {
		for i := range byStage {
			if byStage[i].ID != trimmedID {
				continue
			}
			item := byStage[i]
			item.Stage = strings.TrimSpace(req.Stage)
			if req.Position > 0 {
				item.Position = req.Position
			}
			item.Template = strings.TrimSpace(req.Template)
			item.Model = strings.TrimSpace(req.Model)
			item.Temperature = req.Temperature
			item.MaxTokens = req.MaxTokens
			item.TimeoutMS = req.TimeoutMS
			item.RetryCount = req.RetryCount
			item.BackoffMS = req.BackoffMS
			item.CooldownMS = req.CooldownMS
			item.MinConfidence = req.MinConfidence
			byStage[i] = item
			if item.Stage == stage {
				s.versions[stage] = byStage
				return item, nil
			}

			s.versions[stage] = append(byStage[:i], byStage[i+1:]...)
			newStage := s.versions[item.Stage]
			maxVersion := 0
			for _, existing := range newStage {
				if existing.Version > maxVersion {
					maxVersion = existing.Version
				}
			}
			item.Version = maxVersion + 1
			item.IsActive = false
			item.ActivatedBy = ""
			item.ActivatedAt = time.Time{}
			s.versions[item.Stage] = append(newStage, item)
			sortPromptVersions(s.versions[item.Stage])
			return item, nil
		}
	}
	return PromptVersion{}, ErrNotFound
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if s.db != nil {
		return s.deletePromptDB(ctx, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	trimmedID := strings.TrimSpace(id)
	for stage, byStage := range s.versions {
		removeIndex := -1
		wasActive := false
		for i := range byStage {
			if byStage[i].ID == trimmedID {
				removeIndex = i
				wasActive = byStage[i].IsActive
				break
			}
		}
		if removeIndex == -1 {
			continue
		}

		updated := append(byStage[:removeIndex], byStage[removeIndex+1:]...)
		if len(updated) == 0 {
			delete(s.versions, stage)
			return nil
		}
		if wasActive {
			bestIndex := 0
			for i := 1; i < len(updated); i++ {
				if updated[i].Version > updated[bestIndex].Version {
					bestIndex = i
				}
			}
			for i := range updated {
				updated[i].IsActive = false
			}
			updated[bestIndex].IsActive = true
		}
		s.versions[stage] = updated
		return nil
	}
	return ErrNotFound
}

func (s *Service) nextPositionLocked() int {
	maxPosition := 0
	for _, byStage := range s.versions {
		for _, item := range byStage {
			if item.Position > maxPosition {
				maxPosition = item.Position
			}
		}
	}
	return maxPosition + 1
}

func (s *Service) Activate(ctx context.Context, id, actorID string) (PromptVersion, error) {
	if s.db != nil {
		return s.activatePromptDB(ctx, id, actorID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for stage, byStage := range s.versions {
		activeIndex := -1
		for i := range byStage {
			if byStage[i].ID == id {
				activeIndex = i
				break
			}
		}
		if activeIndex == -1 {
			continue
		}
		now := time.Now().UTC()
		for i := range byStage {
			byStage[i].IsActive = i == activeIndex
			if i == activeIndex {
				byStage[i].ActivatedAt = now
				byStage[i].ActivatedBy = strings.TrimSpace(actorID)
			}
		}
		s.versions[stage] = byStage
		return byStage[activeIndex], nil
	}

	return PromptVersion{}, ErrNotFound
}
