package prompts

import (
	"database/sql"
	"sync"
)

// Service stores scenario-graph v2 configuration in memory and model configs in configured store.
type Service struct {
	mu                sync.RWMutex
	counter           int
	configCounter     int
	scenarioPackages  map[string][]ScenarioPackage
	gameScenarios     map[string][]GameScenario
	modelConfigs      map[string]LLMModelConfig
	modelConfigStore  modelConfigStore
	scenarioStore     scenarioPackageStore
	gameScenarioStore gameScenarioStore
}

func NewService() *Service {
	return &Service{
		scenarioPackages: map[string][]ScenarioPackage{},
		gameScenarios:    map[string][]GameScenario{},
		modelConfigs:     map[string]LLMModelConfig{},
	}
}

func NewPostgresService(db *sql.DB) *Service {
	svc := NewService()
	if db != nil {
		svc.modelConfigStore = NewPostgresModelConfigStore(db)
		svc.scenarioStore = NewPostgresScenarioPackageStore(db)
		svc.gameScenarioStore = NewPostgresGameScenarioStore(db)
	}
	return svc
}
