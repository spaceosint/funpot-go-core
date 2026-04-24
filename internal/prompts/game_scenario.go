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
	ID                 string                          `json:"id"`
	FromNodeID         string                          `json:"fromNodeId"`
	ToNodeID           string                          `json:"toNodeId"`
	Condition          string                          `json:"condition"`
	Priority           int                             `json:"priority"`
	TerminalConditions []GameScenarioTerminalCondition `json:"terminalConditions,omitempty"`
}

type GameScenarioTerminalCondition struct {
	ID              string `json:"id"`
	TransitionID    string `json:"transitionId,omitempty"`
	Condition       string `json:"condition"`
	ResultLabel     string `json:"resultLabel,omitempty"`
	ResultStateJSON string `json:"resultStateJson,omitempty"`
	Priority        int    `json:"priority"`
}

type GameScenario struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	GameSlug      string                   `json:"gameSlug"`
	Version       int                      `json:"version"`
	IsActive      bool                     `json:"isActive"`
	InitialNodeID string                   `json:"initialNodeId"`
	Nodes         []GameScenarioNode       `json:"nodes"`
	Transitions   []GameScenarioTransition `json:"transitions"`
	CreatedBy     string                   `json:"createdBy"`
	ActivatedBy   string                   `json:"activatedBy,omitempty"`
	CreatedAt     time.Time                `json:"createdAt"`
	ActivatedAt   time.Time                `json:"activatedAt,omitempty"`
}

type GameScenarioCreateRequest struct {
	Name          string
	GameSlug      string
	InitialNodeID string
	Nodes         []GameScenarioNode
	Transitions   []GameScenarioTransition
	ActorID       string
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
	for _, tr := range req.Transitions {
		for _, tc := range tr.TerminalConditions {
			if strings.TrimSpace(tc.Condition) == "" {
				return fmt.Errorf("%w: transition %s terminal condition is required", ErrInvalidGameScenario, strings.TrimSpace(tr.ID))
			}
			if err := validateScenarioCondition(tc.Condition); err != nil {
				return fmt.Errorf("%w: transition %s terminal condition: %v", ErrInvalidGameScenario, strings.TrimSpace(tr.ID), err)
			}
			if strings.TrimSpace(tc.ResultStateJSON) != "" && !isValidJSON(tc.ResultStateJSON) {
				return fmt.Errorf("%w: transition %s terminal resultStateJson must be valid json", ErrInvalidGameScenario, strings.TrimSpace(tr.ID))
			}
		}
	}
	return nil
}

func (s *Service) ListGameScenarios(ctx context.Context) []GameScenario {
	if s.gameScenarioStore != nil {
		items, err := s.gameScenarioStore.List(ctx)
		if err == nil {
			return items
		}
	}
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
	if s.gameScenarioStore != nil {
		item := GameScenario{
			Name:          strings.TrimSpace(req.Name),
			GameSlug:      strings.TrimSpace(req.GameSlug),
			InitialNodeID: strings.TrimSpace(req.InitialNodeID),
			Nodes:         append([]GameScenarioNode(nil), req.Nodes...),
			Transitions:   append([]GameScenarioTransition(nil), req.Transitions...),
			CreatedBy:     strings.TrimSpace(req.ActorID),
			CreatedAt:     time.Now().UTC(),
		}
		created, err := s.gameScenarioStore.Create(ctx, item)
		if err != nil {
			return GameScenario{}, err
		}
		return created, nil
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	versions := s.gameScenarios[req.GameSlug]
	item := GameScenario{
		ID:            fmt.Sprintf("game-scenario-%d", s.counter),
		Name:          strings.TrimSpace(req.Name),
		GameSlug:      strings.TrimSpace(req.GameSlug),
		Version:       len(versions) + 1,
		InitialNodeID: strings.TrimSpace(req.InitialNodeID),
		Nodes:         append([]GameScenarioNode(nil), req.Nodes...),
		Transitions:   append([]GameScenarioTransition(nil), req.Transitions...),
		CreatedBy:     strings.TrimSpace(req.ActorID),
		CreatedAt:     now,
	}
	if !s.hasActiveGameScenarioLocked() {
		item.IsActive = true
		item.ActivatedBy = strings.TrimSpace(req.ActorID)
		item.ActivatedAt = now
	}
	s.gameScenarios[req.GameSlug] = append(versions, item)
	return item, nil
}

func (s *Service) GetGameScenario(ctx context.Context, id string) (GameScenario, error) {
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	if s.gameScenarioStore != nil {
		return s.gameScenarioStore.GetByID(ctx, lookup)
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
	if s.gameScenarioStore != nil {
		current, err := s.gameScenarioStore.GetByID(ctx, lookup)
		if err != nil {
			return GameScenario{}, err
		}
		current.Name = strings.TrimSpace(req.Name)
		current.GameSlug = strings.TrimSpace(req.GameSlug)
		current.InitialNodeID = strings.TrimSpace(req.InitialNodeID)
		current.Nodes = append([]GameScenarioNode(nil), req.Nodes...)
		current.Transitions = append([]GameScenarioTransition(nil), req.Transitions...)
		return s.gameScenarioStore.Update(ctx, current)
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
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return ErrGameScenarioNotFound
	}
	if s.gameScenarioStore != nil {
		return s.gameScenarioStore.Delete(ctx, lookup)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for slug, versions := range s.gameScenarios {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			removedActive := item.IsActive
			s.gameScenarios[slug] = append(versions[:i], versions[i+1:]...)
			if removedActive {
				s.ensureAtLeastOneActiveLocked(item.ActivatedBy)
			}
			return nil
		}
	}
	return ErrGameScenarioNotFound
}

func (s *Service) ActivateGameScenario(ctx context.Context, id, actorID string) (GameScenario, error) {
	lookup := strings.TrimSpace(id)
	if lookup == "" {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	if s.gameScenarioStore != nil {
		return s.gameScenarioStore.SetActive(ctx, lookup, strings.TrimSpace(actorID), time.Now().UTC())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		foundSlug string
		foundIdx  = -1
	)
	for slug, versions := range s.gameScenarios {
		for i := range versions {
			if versions[i].ID == lookup {
				foundSlug = slug
				foundIdx = i
				break
			}
		}
		if foundIdx >= 0 {
			break
		}
	}
	if foundIdx < 0 {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	now := time.Now().UTC()
	for slug, versions := range s.gameScenarios {
		for i := range versions {
			versions[i].IsActive = slug == foundSlug && i == foundIdx
			if versions[i].IsActive {
				versions[i].ActivatedBy = strings.TrimSpace(actorID)
				versions[i].ActivatedAt = now
			} else {
				versions[i].ActivatedBy = ""
				versions[i].ActivatedAt = time.Time{}
			}
		}
		s.gameScenarios[slug] = versions
	}
	return s.gameScenarios[foundSlug][foundIdx], nil
}

func (s *Service) GetActiveGameScenario(ctx context.Context, gameSlug string) (GameScenario, error) {
	lookup := strings.TrimSpace(gameSlug)
	if lookup == "" {
		return GameScenario{}, ErrGameScenarioNotFound
	}
	if s.gameScenarioStore != nil {
		item, err := s.gameScenarioStore.GetActiveByGameSlug(ctx, lookup)
		if err == nil {
			return item, nil
		}
		if !errors.Is(err, ErrGameScenarioNotFound) {
			return GameScenario{}, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.gameScenarios[lookup]
	for _, item := range versions {
		if item.IsActive {
			return item, nil
		}
	}
	for _, versions := range s.gameScenarios {
		for _, item := range versions {
			if item.IsActive {
				return item, nil
			}
		}
	}
	return GameScenario{}, ErrGameScenarioNotFound
}

func (g GameScenario) InitialNode() (GameScenarioNode, error) {
	initial := strings.TrimSpace(g.InitialNodeID)
	if initial == "" {
		return GameScenarioNode{}, ErrInvalidGameScenario
	}
	for _, node := range g.Nodes {
		if strings.TrimSpace(node.ID) == initial {
			return node, nil
		}
	}
	return GameScenarioNode{}, ErrInvalidGameScenario
}

func (g GameScenario) ResolveTerminalCondition(transitionID, stateJSON string) (GameScenarioTerminalCondition, bool, error) {
	state := parseJSONMap(stateJSON)
	return g.ResolveTerminalConditionWithState(transitionID, state)
}

func (g GameScenario) ResolveTerminalConditionWithState(transitionID string, state map[string]any) (GameScenarioTerminalCondition, bool, error) {
	lookupTransitionID := strings.TrimSpace(transitionID)
	orderedForTransition := make([]GameScenarioTerminalCondition, 0)
	if lookupTransitionID != "" {
		for _, tr := range g.Transitions {
			if strings.TrimSpace(tr.ID) == lookupTransitionID {
				for _, terminal := range tr.TerminalConditions {
					terminal.TransitionID = lookupTransitionID
					orderedForTransition = append(orderedForTransition, terminal)
				}
				break
			}
		}
	}
	if terminal, ok, err := evaluateOrderedTerminals(orderedForTransition, state); err != nil {
		return GameScenarioTerminalCondition{}, false, err
	} else if ok {
		return terminal, true, nil
	}

	orderedGlobal := make([]GameScenarioTerminalCondition, 0)
	for _, tr := range g.Transitions {
		transitionRef := strings.TrimSpace(tr.ID)
		if transitionRef == lookupTransitionID {
			continue
		}
		for _, terminal := range tr.TerminalConditions {
			terminal.TransitionID = transitionRef
			orderedGlobal = append(orderedGlobal, terminal)
		}
	}
	if terminal, ok, err := evaluateOrderedTerminals(orderedGlobal, state); err != nil {
		return GameScenarioTerminalCondition{}, false, err
	} else if ok {
		return terminal, true, nil
	}
	return GameScenarioTerminalCondition{}, false, nil
}

func evaluateOrderedTerminals(items []GameScenarioTerminalCondition, state map[string]any) (GameScenarioTerminalCondition, bool, error) {
	if len(items) == 0 {
		return GameScenarioTerminalCondition{}, false, nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority == items[j].Priority {
			left := strings.TrimSpace(items[i].TransitionID) + ":" + strings.TrimSpace(items[i].ID)
			right := strings.TrimSpace(items[j].TransitionID) + ":" + strings.TrimSpace(items[j].ID)
			return left < right
		}
		return items[i].Priority > items[j].Priority
	})
	for _, terminal := range items {
		ok, err := evaluateCondition(terminal.Condition, state)
		if err != nil {
			return GameScenarioTerminalCondition{}, false, err
		}
		if ok {
			return terminal, true, nil
		}
	}
	return GameScenarioTerminalCondition{}, false, nil
}

func (s *Service) hasActiveGameScenarioLocked() bool {
	for _, versions := range s.gameScenarios {
		for _, item := range versions {
			if item.IsActive {
				return true
			}
		}
	}
	return false
}

func (s *Service) ensureAtLeastOneActiveLocked(actorID string) {
	if s.hasActiveGameScenarioLocked() {
		return
	}
	for slug, versions := range s.gameScenarios {
		if len(versions) == 0 {
			continue
		}
		versions[0].IsActive = true
		versions[0].ActivatedBy = strings.TrimSpace(actorID)
		versions[0].ActivatedAt = time.Now().UTC()
		s.gameScenarios[slug] = versions
		return
	}
}

func (g GameScenario) ResolveNode(currentNodeID, stateJSON string) (GameScenarioNode, string, bool, error) {
	return g.ResolveNodeWithState(currentNodeID, parseJSONMap(stateJSON))
}

func (g GameScenario) ResolveNodeWithState(currentNodeID string, state map[string]any) (GameScenarioNode, string, bool, error) {
	activeNodeID := strings.TrimSpace(currentNodeID)
	if activeNodeID == "" {
		initial, err := g.InitialNode()
		return initial, "", true, err
	}
	nodeByID := make(map[string]GameScenarioNode, len(g.Nodes))
	for _, node := range g.Nodes {
		nodeByID[strings.TrimSpace(node.ID)] = node
	}
	currentNode, ok := nodeByID[activeNodeID]
	if !ok {
		initial, err := g.InitialNode()
		return initial, "", true, err
	}
	outgoing := make([]GameScenarioTransition, 0)
	for _, tr := range g.Transitions {
		if strings.TrimSpace(tr.FromNodeID) != activeNodeID {
			continue
		}
		outgoing = append(outgoing, tr)
	}
	sort.SliceStable(outgoing, func(i, j int) bool {
		if outgoing[i].Priority == outgoing[j].Priority {
			left := strings.TrimSpace(outgoing[i].ID) + strings.TrimSpace(outgoing[i].ToNodeID)
			right := strings.TrimSpace(outgoing[j].ID) + strings.TrimSpace(outgoing[j].ToNodeID)
			return left < right
		}
		return outgoing[i].Priority > outgoing[j].Priority
	})
	for _, tr := range outgoing {
		matched, err := evaluateCondition(tr.Condition, state)
		if err != nil {
			return GameScenarioNode{}, "", false, err
		}
		if !matched {
			continue
		}
		targetID := strings.TrimSpace(tr.ToNodeID)
		next, exists := nodeByID[targetID]
		if !exists {
			return GameScenarioNode{}, "", false, ErrInvalidGameScenario
		}
		return next, strings.TrimSpace(tr.ID), targetID != strings.TrimSpace(currentNode.ID), nil
	}
	return currentNode, "", false, nil
}

func isValidJSON(raw string) bool {
	return json.Valid([]byte(strings.TrimSpace(raw)))
}
