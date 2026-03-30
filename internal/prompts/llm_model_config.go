package prompts

import "errors"

var ErrLLMModelConfigNotFound = errors.New("llm model config not found")

type LLMModelConfig struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}
