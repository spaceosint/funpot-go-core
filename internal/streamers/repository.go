package streamers

import "context"

// DecisionRepository persists detailed LLM stage decisions for streamer analysis runs.
type DecisionRepository interface {
	RecordLLMDecision(ctx context.Context, item LLMDecision) error
	ListLLMDecisions(ctx context.Context, streamerID string, limit int) ([]LLMDecision, error)
	ListAllLLMDecisions(ctx context.Context, streamerID string) ([]LLMDecision, error)
	DeleteAllLLMDecisions(ctx context.Context, streamerID string) (int, error)
}
