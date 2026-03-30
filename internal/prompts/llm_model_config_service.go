package prompts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Service) ListLLMModelConfigs(ctx context.Context) ([]LLMModelConfig, error) {
	if s.modelConfigStore != nil {
		return s.modelConfigStore.List(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]LLMModelConfig, 0, len(s.modelConfigs))
	for _, item := range s.modelConfigs {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Service) CreateLLMModelConfig(ctx context.Context, req LLMModelConfigUpsertRequest) (LLMModelConfig, error) {
	prepared, err := sanitizeLLMModelConfigUpsertRequest(req)
	if err != nil {
		return LLMModelConfig{}, err
	}
	now := time.Now().UTC()
	item := LLMModelConfig{
		Name:          prepared.Name,
		Model:         prepared.Model,
		MetadataJSON:  prepared.MetadataJSON,
		Temperature:   prepared.Temperature,
		MaxTokens:     prepared.MaxTokens,
		TimeoutMS:     prepared.TimeoutMS,
		RetryCount:    prepared.RetryCount,
		BackoffMS:     prepared.BackoffMS,
		CooldownMS:    prepared.CooldownMS,
		MinConfidence: prepared.MinConfidence,
		CreatedBy:     prepared.ActorID,
		CreatedAt:     now,
	}
	if s.modelConfigStore != nil {
		return s.modelConfigStore.Create(ctx, item)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.configCounter++
	item.ID = fmt.Sprintf("llm-model-cfg-%d", s.configCounter)
	if len(s.modelConfigs) == 0 {
		item.IsActive = true
		item.ActivatedBy = prepared.ActorID
		item.ActivatedAt = now
	}
	s.modelConfigs[item.ID] = item
	return item, nil
}

func (s *Service) UpdateLLMModelConfig(ctx context.Context, id string, req LLMModelConfigUpsertRequest) (LLMModelConfig, error) {
	prepared, err := sanitizeLLMModelConfigUpsertRequest(req)
	if err != nil {
		return LLMModelConfig{}, err
	}
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	if s.modelConfigStore != nil {
		current, err := s.modelConfigStore.GetByID(ctx, lookup)
		if err != nil {
			return LLMModelConfig{}, err
		}
		current.Name = prepared.Name
		current.Model = prepared.Model
		current.MetadataJSON = prepared.MetadataJSON
		current.Temperature = prepared.Temperature
		current.MaxTokens = prepared.MaxTokens
		current.TimeoutMS = prepared.TimeoutMS
		current.RetryCount = prepared.RetryCount
		current.BackoffMS = prepared.BackoffMS
		current.CooldownMS = prepared.CooldownMS
		current.MinConfidence = prepared.MinConfidence
		return s.modelConfigStore.Update(ctx, current)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.modelConfigs[lookup]
	if !ok {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	current.Name = prepared.Name
	current.Model = prepared.Model
	current.MetadataJSON = prepared.MetadataJSON
	current.Temperature = prepared.Temperature
	current.MaxTokens = prepared.MaxTokens
	current.TimeoutMS = prepared.TimeoutMS
	current.RetryCount = prepared.RetryCount
	current.BackoffMS = prepared.BackoffMS
	current.CooldownMS = prepared.CooldownMS
	current.MinConfidence = prepared.MinConfidence
	s.modelConfigs[lookup] = current
	return current, nil
}

func (s *Service) DeleteLLMModelConfig(ctx context.Context, id string) error {
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return ErrLLMModelConfigNotFound
	}
	if s.modelConfigStore != nil {
		return s.modelConfigStore.Delete(ctx, lookup)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.modelConfigs[lookup]
	if !ok {
		return ErrLLMModelConfigNotFound
	}
	delete(s.modelConfigs, lookup)
	if item.IsActive {
		var replacementID string
		for cfgID := range s.modelConfigs {
			replacementID = cfgID
			break
		}
		if replacementID != "" {
			replacement := s.modelConfigs[replacementID]
			replacement.IsActive = true
			replacement.ActivatedAt = time.Now().UTC()
			s.modelConfigs[replacementID] = replacement
		}
	}
	return nil
}

func (s *Service) ActivateLLMModelConfig(ctx context.Context, id, actorID string) (LLMModelConfig, error) {
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	actor := strings.TrimSpace(actorID)
	if s.modelConfigStore != nil {
		return s.modelConfigStore.SetActive(ctx, lookup, actor, time.Now().UTC())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.modelConfigs[lookup]
	if !ok {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	now := time.Now().UTC()
	for cfgID, cfg := range s.modelConfigs {
		cfg.IsActive = cfgID == lookup
		if cfgID == lookup {
			cfg.ActivatedAt = now
			cfg.ActivatedBy = actor
		}
		s.modelConfigs[cfgID] = cfg
	}
	item = s.modelConfigs[lookup]
	return item, nil
}

func (s *Service) GetLLMModelConfig(ctx context.Context, id string) (LLMModelConfig, error) {
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	if s.modelConfigStore != nil {
		return s.modelConfigStore.GetByID(ctx, lookup)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.modelConfigs[lookup]
	if !ok {
		return LLMModelConfig{}, ErrLLMModelConfigNotFound
	}
	return item, nil
}
