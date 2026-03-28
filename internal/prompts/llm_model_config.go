package prompts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrLLMModelConfigNotFound    = errors.New("llm model config not found")
	ErrInvalidLLMModelConfigName = errors.New("llm model config name must not be empty")
)

type LLMModelConfigCreateRequest struct {
	Name          string
	Model         string
	Temperature   float64
	MaxTokens     int
	TimeoutMS     int
	RetryCount    int
	BackoffMS     int
	CooldownMS    int
	MinConfidence float64
	ActorID       string
}

type LLMModelConfig struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
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

func validateLLMModelConfigRequest(req LLMModelConfigCreateRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return ErrInvalidLLMModelConfigName
	}
	if err := ValidateCreateRequest(CreateRequest{
		Stage:         "llm_model_config",
		Template:      "preset",
		Model:         req.Model,
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		TimeoutMS:     req.TimeoutMS,
		RetryCount:    req.RetryCount,
		BackoffMS:     req.BackoffMS,
		CooldownMS:    req.CooldownMS,
		MinConfidence: req.MinConfidence,
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) ListLLMModelConfigs(ctx context.Context) []LLMModelConfig {
	if s.db != nil {
		items, err := s.listLLMModelConfigsDB(ctx)
		if err == nil {
			return items
		}
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := append([]LLMModelConfig(nil), s.modelConfigs...)
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

func (s *Service) CreateLLMModelConfig(ctx context.Context, req LLMModelConfigCreateRequest) (LLMModelConfig, error) {
	if err := validateLLMModelConfigRequest(req); err != nil {
		return LLMModelConfig{}, err
	}
	if s.db != nil {
		return s.createLLMModelConfigDB(ctx, req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.counter++
	item := LLMModelConfig{
		ID:            fmt.Sprintf("llm-model-config-%d", s.counter),
		Name:          strings.TrimSpace(req.Name),
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
		IsActive:      len(s.modelConfigs) == 0,
	}
	if item.IsActive {
		item.ActivatedBy = item.CreatedBy
		item.ActivatedAt = now
	}
	s.modelConfigs = append(s.modelConfigs, item)
	return item, nil
}

func (s *Service) GetLLMModelConfig(ctx context.Context, id string) (LLMModelConfig, error) {
	if s.db != nil {
		return s.getLLMModelConfigDB(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	lookup := strings.TrimSpace(id)
	for _, item := range s.modelConfigs {
		if item.ID == lookup {
			return item, nil
		}
	}
	return LLMModelConfig{}, ErrLLMModelConfigNotFound
}

func (s *Service) UpdateLLMModelConfig(ctx context.Context, id string, req LLMModelConfigCreateRequest) (LLMModelConfig, error) {
	if err := validateLLMModelConfigRequest(req); err != nil {
		return LLMModelConfig{}, err
	}
	if s.db != nil {
		return s.updateLLMModelConfigDB(ctx, id, req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := strings.TrimSpace(id)
	for i := range s.modelConfigs {
		if s.modelConfigs[i].ID != lookup {
			continue
		}
		s.modelConfigs[i].Name = strings.TrimSpace(req.Name)
		s.modelConfigs[i].Model = strings.TrimSpace(req.Model)
		s.modelConfigs[i].Temperature = req.Temperature
		s.modelConfigs[i].MaxTokens = req.MaxTokens
		s.modelConfigs[i].TimeoutMS = req.TimeoutMS
		s.modelConfigs[i].RetryCount = req.RetryCount
		s.modelConfigs[i].BackoffMS = req.BackoffMS
		s.modelConfigs[i].CooldownMS = req.CooldownMS
		s.modelConfigs[i].MinConfidence = req.MinConfidence
		return s.modelConfigs[i], nil
	}
	return LLMModelConfig{}, ErrLLMModelConfigNotFound
}

func (s *Service) DeleteLLMModelConfig(ctx context.Context, id string) error {
	if s.db != nil {
		return s.deleteLLMModelConfigDB(ctx, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := strings.TrimSpace(id)
	for i, item := range s.modelConfigs {
		if item.ID != lookup {
			continue
		}
		s.modelConfigs = append(s.modelConfigs[:i], s.modelConfigs[i+1:]...)
		if item.IsActive && len(s.modelConfigs) > 0 {
			s.modelConfigs[0].IsActive = true
		}
		return nil
	}
	return ErrLLMModelConfigNotFound
}

func (s *Service) ActivateLLMModelConfig(ctx context.Context, id, actorID string) (LLMModelConfig, error) {
	if s.db != nil {
		return s.activateLLMModelConfigDB(ctx, id, actorID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := strings.TrimSpace(id)
	now := time.Now().UTC()
	for i := range s.modelConfigs {
		s.modelConfigs[i].IsActive = s.modelConfigs[i].ID == lookup
		if s.modelConfigs[i].IsActive {
			s.modelConfigs[i].ActivatedBy = strings.TrimSpace(actorID)
			s.modelConfigs[i].ActivatedAt = now
			return s.modelConfigs[i], nil
		}
	}
	return LLMModelConfig{}, ErrLLMModelConfigNotFound
}
