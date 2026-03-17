package prompts

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidStage         = errors.New("stage must be one of: stage_a, stage_b, stage_c, stage_d")
	ErrInvalidTemplate      = errors.New("template must not be empty")
	ErrInvalidModel         = errors.New("model must not be empty")
	ErrInvalidTemperature   = errors.New("temperature must be between 0 and 2")
	ErrInvalidMaxTokens     = errors.New("maxTokens must be greater than 0")
	ErrInvalidTimeoutMS     = errors.New("timeoutMs must be greater than 0")
	ErrInvalidRetryCount    = errors.New("retryCount must be between 0 and 10")
	ErrInvalidBackoffMS     = errors.New("backoffMs must be greater than or equal to 0")
	ErrInvalidCooldownMS    = errors.New("cooldownMs must be greater than or equal to 0")
	ErrInvalidMinConfidence = errors.New("minConfidence must be between 0 and 1")
	ErrNotFound             = errors.New("prompt version not found")
)

const (
	StageA = "stage_a"
	StageB = "stage_b"
	StageC = "stage_c"
	StageD = "stage_d"
)

var supportedStages = map[string]struct{}{
	StageA: {},
	StageB: {},
	StageC: {},
	StageD: {},
}

type CreateRequest struct {
	Stage         string
	Template      string
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

type PromptVersion struct {
	ID            string    `json:"id"`
	Stage         string    `json:"stage"`
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

func ValidateCreateRequest(req CreateRequest) error {
	req.Stage = strings.TrimSpace(req.Stage)
	if _, ok := supportedStages[req.Stage]; !ok {
		return ErrInvalidStage
	}
	if strings.TrimSpace(req.Template) == "" {
		return ErrInvalidTemplate
	}
	if strings.TrimSpace(req.Model) == "" {
		return ErrInvalidModel
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		return ErrInvalidTemperature
	}
	if req.MaxTokens <= 0 {
		return ErrInvalidMaxTokens
	}
	if req.TimeoutMS <= 0 {
		return ErrInvalidTimeoutMS
	}
	if req.RetryCount < 0 || req.RetryCount > 10 {
		return ErrInvalidRetryCount
	}
	if req.BackoffMS < 0 {
		return ErrInvalidBackoffMS
	}
	if req.CooldownMS < 0 {
		return ErrInvalidCooldownMS
	}
	if req.MinConfidence < 0 || req.MinConfidence > 1 {
		return ErrInvalidMinConfidence
	}
	return nil
}
