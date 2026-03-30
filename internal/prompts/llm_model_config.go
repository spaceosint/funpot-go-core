package prompts

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrLLMModelConfigNotFound  = errors.New("llm model config not found")
	ErrInvalidModelConfigName  = errors.New("llm model config name is required")
	ErrInvalidModelConfigModel = errors.New("llm model config model is required")
)

type LLMModelConfig struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Model         string    `json:"model"`
	MetadataJSON  string    `json:"metadataJson,omitempty"`
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

type LLMModelConfigUpsertRequest struct {
	Name          string
	Model         string
	MetadataJSON  string
	Temperature   float64
	MaxTokens     int
	TimeoutMS     int
	RetryCount    int
	BackoffMS     int
	CooldownMS    int
	MinConfidence float64
	ActorID       string
}

type modelConfigStore interface {
	List(context.Context) ([]LLMModelConfig, error)
	Create(context.Context, LLMModelConfig) (LLMModelConfig, error)
	Update(context.Context, LLMModelConfig) (LLMModelConfig, error)
	Delete(context.Context, string) error
	SetActive(context.Context, string, string, time.Time) (LLMModelConfig, error)
	GetByID(context.Context, string) (LLMModelConfig, error)
}

func sanitizeLLMModelConfigUpsertRequest(req LLMModelConfigUpsertRequest) (LLMModelConfigUpsertRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Model = strings.TrimSpace(req.Model)
	req.MetadataJSON = strings.TrimSpace(req.MetadataJSON)
	req.ActorID = strings.TrimSpace(req.ActorID)

	if req.Name == "" {
		return LLMModelConfigUpsertRequest{}, ErrInvalidModelConfigName
	}
	if req.Model == "" {
		return LLMModelConfigUpsertRequest{}, ErrInvalidModelConfigModel
	}
	return req, nil
}
