package prompts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrGameScenarioNotFound = errors.New("game scenario not found")
	ErrInvalidGameScenario  = errors.New("game scenario is invalid")
)

type GameScenarioNode struct {
	ID                string `json:"id"`
	Alias             string `json:"alias"`
	ScenarioPackageID string `json:"scenarioPackageId"`
}

type GameScenarioTransition struct {
	ID         string `json:"id"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Condition  string `json:"condition"`
	Priority   int    `json:"priority"`
}

type GameScenarioTerminalCondition struct {
	ID              string `json:"id"`
	Condition       string `json:"condition"`
	ResultLabel     string `json:"resultLabel,omitempty"`
	ResultStateJSON string `json:"resultStateJson,omitempty"`
	Priority        int    `json:"priority"`
}

type GameScenario struct {
	ID                 string                          `json:"id"`
	Name               string                          `json:"name"`
	GameSlug           string                          `json:"gameSlug"`
	Version            int                             `json:"version"`
	IsActive           bool                            `json:"isActive"`
	InitialNodeID      string                          `json:"initialNodeId"`
	Nodes              []GameScenarioNode              `json:"nodes"`
	Transitions        []GameScenarioTransition        `json:"transitions"`
	TerminalConditions []GameScenarioTerminalCondition `json:"terminalConditions"`
	CreatedBy          string                          `json:"createdBy"`
	ActivatedBy        string                          `json:"activatedBy,omitempty"`
	CreatedAt          time.Time                       `json:"createdAt"`
	ActivatedAt        time.Time                       `json:"activatedAt,omitempty"`
}

type GameScenarioCreateRequest struct {
	Name               string
	GameSlug           string
	InitialNodeID      string
	Nodes              []GameScenarioNode
	Transitions        []GameScenarioTransition
	TerminalConditions []GameScenarioTerminalCondition
	ActorID            string
}

func (s *Service) validateGameScenarioRequest(ctx context.Context, req GameScenarioCreateRequest) error {
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.GameSlug) == "" {
		return ErrInvalidGameScenario
	}
	if len(req.Nodes) == 0 {
		return fmt.Errorf("%w: nodes are required", ErrInvalidGameScenario)
	}
	nodeIDs := make(map[string]GameScenarioNode, len(req.Nodes))
	for _, node := range req.Nodes {
		nodeID := strings.TrimSpace(node.ID)
		if nodeID == "" || strings.TrimSpace(node.ScenarioPackageID) == "" {
			return fmt.Errorf("%w: node id and scenarioPackageId are required", ErrInvalidGameScenario)
		}
		if _, exists := nodeIDs[nodeID]; exists {
			return fmt.Errorf("%w: duplicated node id %s", ErrInvalidGameScenario, nodeID)
		}
		pkg, err := s.GetScenarioPackage(ctx, node.ScenarioPackageID)
		if err != nil {
			return fmt.Errorf("%w: node %s references unknown scenario package %s", ErrInvalidGameScenario, nodeID, node.ScenarioPackageID)
		}
		if _, err := pkg.InitialStep(); err != nil {
			return fmt.Errorf("%w: package %s has no valid initial step", ErrInvalidGameScenario, node.ScenarioPackageID)
		}
		nodeIDs[nodeID] = node
	}
	if _, ok := nodeIDs[strings.TrimSpace(req.InitialNodeID)]; !ok {
		return fmt.Errorf("%w: initialNodeId must reference existing node", ErrInvalidGameScenario)
	}
	for _, tr := range req.Transitions {
		if _, ok := nodeIDs[strings.TrimSpace(tr.FromNodeID)]; !ok {
			return fmt.Errorf("%w: transition fromNodeId %s not found", ErrInvalidGameScenario, tr.FromNodeID)
		}
		toNode, ok := nodeIDs[strings.TrimSpace(tr.ToNodeID)]
		if !ok {
			return fmt.Errorf("%w: transition toNodeId %s not found", ErrInvalidGameScenario, tr.ToNodeID)
		}
		if strings.TrimSpace(tr.Condition) == "" {
			return fmt.Errorf("%w: transition condition is required", ErrInvalidGameScenario)
		}
		if err := validateScenarioCondition(tr.Condition); err != nil {
			return fmt.Errorf("%w: transition condition: %v", ErrInvalidGameScenario, err)
		}
		pkg, err := s.GetScenarioPackage(ctx, toNode.ScenarioPackageID)
		if err != nil {
			return fmt.Errorf("%w: transition target package %s not found", ErrInvalidGameScenario, toNode.ScenarioPackageID)
		}
		if _, err := pkg.InitialStep(); err != nil {
			return fmt.Errorf("%w: transition target package %s has no initial step", ErrInvalidGameScenario, toNode.ScenarioPackageID)
		}
	}
	for _, tc := range req.TerminalConditions {
		if strings.TrimSpace(tc.Condition) == "" {
			return fmt.Errorf("%w: terminal condition is required", ErrInvalidGameScenario)
		}
		if err := validateScenarioCondition(tc.Condition); err != nil {
			return fmt.Errorf("%w: terminal condition: %v", ErrInvalidGameScenario, err)
		}
		if strings.TrimSpace(tc.ResultStateJSON) != "" && !isValidJSON(tc.ResultStateJSON) {
			return fmt.Errorf("%w: terminal resultStateJson must be valid json", ErrInvalidGameScenario)
		}
	}
	return nil
}

func (s *Service) ListGameScenarios(ctx context.Context) []GameScenario {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]GameScenario, 0)
	for _, versions := range s.gameScenarios {
		items = append(items, versions...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GameSlug == items[j].GameSlug {
			return items[i].Version > items[j].Version
		}
		return items[i].GameSlug < items[j].GameSlug
	})
	return items
}

func (s *Service) CreateGameScenario(ctx context.Context, req GameScenarioCreateRequest) (GameScenario, error) {
	if err := s.validateGameScenarioRequest(ctx, req); err != nil {
		return GameScenario{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	versions := s.gameScenarios[req.GameSlug]
	item := GameScenario{
		ID:                 fmt.Sprintf("game-scenario-%d", s.counter),
		Name:               strings.TrimSpace(req.Name),
		GameSlug:           strings.TrimSpace(req.GameSlug),
		Version:            len(versions) + 1,
		InitialNodeID:      strings.TrimSpace(req.InitialNodeID),
		Nodes:              append([]GameScenarioNode(nil), req.Nodes...),
		Transitions:        append([]GameScenarioTransition(nil), req.Transitions...),
		TerminalConditions: append([]GameScenarioTerminalCondition(nil), req.TerminalConditions...),
		CreatedBy:          strings.TrimSpace(req.ActorID),
		CreatedAt:          now,
	}
	if len(versions) == 0 {
		item.IsActive = true
		item.ActivatedBy = strings.TrimSpace(req.ActorID)
		item.ActivatedAt = now
	}
	s.gameScenarios[req.GameSlug] = append(versions, item)
	return item, nil
}

func (s *Service) GetGameScenario(ctx context.Context, id string) (GameScenario, error) {
	_ = ctx
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, versions := range s.gameScenarios {
		for _, item := range versions {
			if item.ID == lookup {
				return item, nil
			}
		}
	}
	return GameScenario{}, ErrGameScenarioNotFound
}

func (s *Service) UpdateGameScenario(ctx context.Context, id string, req GameScenarioCreateRequest) (GameScenario, error) {
	if err := s.validateGameScenarioRequest(ctx, req); err != nil {
		return GameScenario{}, err
	}
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for slug, versions := range s.gameScenarios {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			updated := item
			updated.Name = strings.TrimSpace(req.Name)
			updated.GameSlug = strings.TrimSpace(req.GameSlug)
			updated.InitialNodeID = strings.TrimSpace(req.InitialNodeID)
			updated.Nodes = append([]GameScenarioNode(nil), req.Nodes...)
			updated.Transitions = append([]GameScenarioTransition(nil), req.Transitions...)
			updated.TerminalConditions = append([]GameScenarioTerminalCondition(nil), req.TerminalConditions...)
			if updated.GameSlug != slug {
				updated.IsActive = false
				updated.ActivatedBy = ""
				updated.ActivatedAt = time.Time{}
				s.gameScenarios[slug] = append(versions[:i], versions[i+1:]...)
				s.gameScenarios[updated.GameSlug] = append(s.gameScenarios[updated.GameSlug], updated)
			} else {
				versions[i] = updated
				s.gameScenarios[slug] = versions
			}
			return updated, nil
		}
	}
	return GameScenario{}, ErrGameScenarioNotFound
}

func (s *Service) DeleteGameScenario(ctx context.Context, id string) error {
	_ = ctx
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return ErrGameScenarioNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for slug, versions := range s.gameScenarios {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			s.gameScenarios[slug] = append(versions[:i], versions[i+1:]...)
			return nil
		}
	}
	return ErrGameScenarioNotFound
}

func (s *Service) ActivateGameScenario(ctx context.Context, id, actorID string) (GameScenario, error) {
	_ = ctx
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for slug, versions := range s.gameScenarios {
		active := -1
		for i := range versions {
			if versions[i].ID == lookup {
				active = i
				break
			}
		}
		if active == -1 {
			continue
		}
		now := time.Now().UTC()
		for i := range versions {
			versions[i].IsActive = i == active
			if versions[i].IsActive {
				versions[i].ActivatedBy = strings.TrimSpace(actorID)
				versions[i].ActivatedAt = now
			} else {
				versions[i].ActivatedBy = ""
				versions[i].ActivatedAt = time.Time{}
			}
		}
		s.gameScenarios[slug] = versions
		return versions[active], nil
	}
	return GameScenario{}, ErrGameScenarioNotFound
}

func isValidJSON(raw string) bool {
	return json.Valid([]byte(strings.TrimSpace(raw)))
}
