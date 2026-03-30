package prompts

import "sync"

// Service stores only scenario-graph v2 configuration in memory.
// Legacy prompt/version, tracker schema, rule-set, and model-config surfaces were removed.
type Service struct {
	mu               sync.RWMutex
	counter          int
	scenarioPackages map[string][]ScenarioPackage
}

func NewService() *Service {
	return &Service{scenarioPackages: map[string][]ScenarioPackage{}}
}

// NewPostgresService keeps the constructor shape for callers but intentionally
// runs scenario storage in-memory only.
func NewPostgresService(_ any) *Service {
	return NewService()
}
