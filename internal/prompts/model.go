package prompts

import "time"

// PromptVersion is retained as a runtime transport struct consumed by media worker.
// It is populated from scenario steps at runtime and is no longer CRUD-managed.
type PromptVersion struct {
	ID            string    `json:"id"`
	Stage         string    `json:"stage"`
	Position      int       `json:"position"`
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
