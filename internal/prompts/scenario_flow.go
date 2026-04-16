package prompts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrScenarioPackageNotFound  = errors.New("scenario package not found")
	ErrScenarioStepNotFound     = errors.New("scenario step not found")
	ErrInvalidScenarioPackage   = errors.New("scenario package must contain at least one step")
	ErrInvalidScenarioStepID    = errors.New("scenario step id must not be empty")
	ErrInvalidScenarioInitial   = errors.New("scenario package must contain exactly one initial step")
	ErrInvalidScenarioCondition = errors.New("scenario step entry condition is invalid")
	ErrInvalidScenarioModelRef  = errors.New("scenario package llmModelConfigId must not be empty")
	ErrInvalidScenarioName      = errors.New("scenario package name must not be empty")
)

type ScenarioStep struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	GameSlug           string    `json:"gameSlug"`
	Folder             string    `json:"folder"`
	EntryCondition     string    `json:"entryCondition,omitempty"`
	PromptTemplate     string    `json:"promptTemplate"`
	ResponseSchemaJSON string    `json:"responseSchemaJson"`
	SegmentSeconds     int       `json:"segmentSeconds,omitempty"`
	MaxRequests        int       `json:"maxRequests,omitempty"`
	Initial            bool      `json:"initial"`
	Order              int       `json:"order"`
	CreatedAt          time.Time `json:"-"`
}

type ScenarioTransition struct {
	FromStepID string `json:"fromStepId"`
	ToStepID   string `json:"toStepId"`
	Condition  string `json:"condition"`
	Priority   int    `json:"priority"`
}

type ScenarioPackageTransition struct {
	ToPackageID        string `json:"toPackageId"`
	Condition          string `json:"condition,omitempty"`
	Priority           int    `json:"priority"`
	Action             string `json:"action,omitempty"`
	FinalStateOptionID string `json:"finalStateOptionId,omitempty"`
}

type ScenarioFinalStateOption struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Condition      string `json:"condition"`
	FinalStateJSON string `json:"finalStateJson,omitempty"`
	FinalLabel     string `json:"finalLabel,omitempty"`
}

type ScenarioPackage struct {
	ID                 string                      `json:"id"`
	Name               string                      `json:"name"`
	Version            int                         `json:"version"`
	GameSlug           string                      `json:"gameSlug"`
	LLMModelConfigID   string                      `json:"llmModelConfigId"`
	IsActive           bool                        `json:"isActive"`
	Steps              []ScenarioStep              `json:"steps"`
	Transitions        []ScenarioTransition        `json:"transitions"`
	PackageTransitions []ScenarioPackageTransition `json:"packageTransitions"`
	FinalStateOptions  []ScenarioFinalStateOption  `json:"finalStateOptions"`
	FinalCondition     string                      `json:"finalCondition,omitempty"`
	PotentialState     []ScenarioStateField        `json:"potentialState,omitempty"`
	CreatedBy          string                      `json:"createdBy"`
	ActivatedBy        string                      `json:"activatedBy,omitempty"`
	CreatedAt          time.Time                   `json:"createdAt"`
	ActivatedAt        time.Time                   `json:"activatedAt,omitempty"`
}

type ScenarioGraphNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	GameSlug string `json:"gameSlug"`
	Folder   string `json:"folder"`
	Initial  bool   `json:"initial"`
	Order    int    `json:"order"`
	Level    int    `json:"level"`
}

type ScenarioGraphEdge struct {
	ID         string `json:"id"`
	FromStepID string `json:"fromStepId"`
	ToStepID   string `json:"toStepId"`
	Condition  string `json:"condition"`
	Priority   int    `json:"priority"`
}

type ScenarioGraphGroup struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	GameSlug string   `json:"gameSlug"`
	Folder   string   `json:"folder"`
	NodeIDs  []string `json:"nodeIds"`
}

type ScenarioPackageGraph struct {
	PackageID   string               `json:"packageId"`
	PackageName string               `json:"packageName"`
	GameSlug    string               `json:"gameSlug"`
	Version     int                  `json:"version"`
	Nodes       []ScenarioGraphNode  `json:"nodes"`
	Edges       []ScenarioGraphEdge  `json:"edges"`
	Groups      []ScenarioGraphGroup `json:"groups"`
}

type ScenarioPackageCreateRequest struct {
	Name               string
	GameSlug           string
	LLMModelConfigID   string
	Steps              []ScenarioStep
	Transitions        []ScenarioTransition
	PackageTransitions []ScenarioPackageTransition
	FinalStateOptions  []ScenarioFinalStateOption
	FinalCondition     string
	ActorID            string
}

type ScenarioStateField struct {
	Path           string   `json:"path"`
	Type           string   `json:"type,omitempty"`
	PossibleValues []string `json:"possibleValues,omitempty"`
}

func ValidateScenarioPackageCreateRequest(req ScenarioPackageCreateRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return ErrInvalidScenarioName
	}
	if strings.TrimSpace(req.LLMModelConfigID) == "" {
		return ErrInvalidScenarioModelRef
	}
	if len(req.Steps) == 0 {
		return ErrInvalidScenarioPackage
	}
	seenSteps := make(map[string]struct{}, len(req.Steps))
	initialCount := 0
	for _, step := range req.Steps {
		id := strings.TrimSpace(step.ID)
		if id == "" {
			return ErrInvalidScenarioStepID
		}
		if step.Initial {
			initialCount++
		}
		if err := validateScenarioCondition(step.EntryCondition); err != nil {
			return fmt.Errorf("%w: step %s: %v", ErrInvalidScenarioCondition, id, err)
		}
		seenSteps[id] = struct{}{}
	}
	if initialCount != 1 {
		return ErrInvalidScenarioInitial
	}
	for _, transition := range req.Transitions {
		from := strings.TrimSpace(transition.FromStepID)
		to := strings.TrimSpace(transition.ToStepID)
		if from == "" || to == "" {
			return ErrInvalidScenarioStepID
		}
		if _, ok := seenSteps[from]; !ok {
			return fmt.Errorf("%w: unknown transition fromStepId %s", ErrInvalidScenarioStepID, from)
		}
		if _, ok := seenSteps[to]; !ok {
			return fmt.Errorf("%w: unknown transition toStepId %s", ErrInvalidScenarioStepID, to)
		}
		if err := validateScenarioCondition(transition.Condition); err != nil {
			return fmt.Errorf("%w: transition %s -> %s: %v", ErrInvalidScenarioCondition, from, to, err)
		}
	}
	optionByID := make(map[string]ScenarioFinalStateOption, len(req.FinalStateOptions))
	for _, option := range req.FinalStateOptions {
		id := strings.TrimSpace(option.ID)
		if id == "" {
			return fmt.Errorf("%w: final state option id is required", ErrInvalidScenarioStepID)
		}
		if _, exists := optionByID[id]; exists {
			return fmt.Errorf("%w: duplicated final state option id %q", ErrInvalidScenarioPackage, id)
		}
		if strings.TrimSpace(option.Name) == "" {
			return fmt.Errorf("%w: final state option %s name is required", ErrInvalidScenarioPackage, id)
		}
		if err := validateScenarioCondition(option.Condition); err != nil {
			return fmt.Errorf("%w: final state option %s: %v", ErrInvalidScenarioCondition, id, err)
		}
		if strings.TrimSpace(option.FinalStateJSON) != "" {
			if !json.Valid([]byte(option.FinalStateJSON)) {
				return fmt.Errorf("%w: final state option %s finalStateJson must be valid json", ErrInvalidScenarioPackage, id)
			}
		}
		optionByID[id] = option
	}
	for _, transition := range req.PackageTransitions {
		action := normalizeScenarioPackageTransitionAction(transition.Action)
		if strings.TrimSpace(transition.ToPackageID) == "" && action != ScenarioPackageTransitionActionStopTracking {
			return fmt.Errorf("%w: package transition toPackageId is required", ErrInvalidScenarioStepID)
		}
		if strings.TrimSpace(transition.Condition) == "" {
			return fmt.Errorf("%w: package transition condition is required", ErrInvalidScenarioCondition)
		}
		if err := validateScenarioCondition(transition.Condition); err != nil {
			return fmt.Errorf("%w: package transition condition: %v", ErrInvalidScenarioCondition, err)
		}
		optionID := strings.TrimSpace(transition.FinalStateOptionID)
		if optionID != "" {
			if _, ok := optionByID[optionID]; !ok {
				return fmt.Errorf("%w: unknown package transition finalStateOptionId %s", ErrInvalidScenarioStepID, optionID)
			}
		}
		if action != "" && action != ScenarioPackageTransitionActionStopTracking {
			return fmt.Errorf("%w: package transition action %q is not supported", ErrInvalidScenarioPackage, transition.Action)
		}
	}
	if err := validateScenarioCondition(req.FinalCondition); err != nil {
		return fmt.Errorf("%w: final condition: %v", ErrInvalidScenarioCondition, err)
	}
	return nil
}

func normalizeScenarioSteps(steps []ScenarioStep, fallbackGameSlug string, now time.Time) []ScenarioStep {
	normalized := make([]ScenarioStep, len(steps))
	for i, step := range steps {
		normalized[i] = step
		if normalized[i].CreatedAt.IsZero() {
			normalized[i].CreatedAt = now
		}
		if normalized[i].Order <= 0 {
			normalized[i].Order = i + 1
		}
		if strings.TrimSpace(normalized[i].GameSlug) == "" {
			normalized[i].GameSlug = fallbackGameSlug
		}
		if normalized[i].SegmentSeconds <= 0 {
			if normalized[i].Initial {
				normalized[i].SegmentSeconds = 15
			} else {
				normalized[i].SegmentSeconds = 30
			}
		}
		if normalized[i].MaxRequests < 0 {
			normalized[i].MaxRequests = 0
		}
	}
	return normalized
}

func normalizeScenarioTransitions(steps []ScenarioStep, transitions []ScenarioTransition) []ScenarioTransition {
	if len(transitions) > 0 {
		normalized := make([]ScenarioTransition, 0, len(transitions))
		for _, tr := range transitions {
			normalizedTransition := ScenarioTransition{
				FromStepID: strings.TrimSpace(tr.FromStepID),
				ToStepID:   strings.TrimSpace(tr.ToStepID),
				Condition:  strings.TrimSpace(tr.Condition),
				Priority:   tr.Priority,
			}
			if normalizedTransition.Priority <= 0 {
				normalizedTransition.Priority = 1
			}
			normalized = append(normalized, normalizedTransition)
		}
		return normalized
	}
	if len(steps) < 2 {
		return []ScenarioTransition{}
	}
	ordered := make([]ScenarioStep, len(steps))
	copy(ordered, steps)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Order == ordered[j].Order {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Order < ordered[j].Order
	})
	initialStep := ordered[0]
	for _, step := range ordered {
		if step.Initial {
			initialStep = step
			break
		}
	}
	autowired := make([]ScenarioTransition, 0, (len(ordered)-1)*2)
	for i := 0; i < len(ordered)-1; i++ {
		next := ordered[i+1]
		autowired = append(autowired, ScenarioTransition{
			FromStepID: strings.TrimSpace(ordered[i].ID),
			ToStepID:   strings.TrimSpace(next.ID),
			Condition:  strings.TrimSpace(next.EntryCondition),
			Priority:   1,
		})
	}
	if strings.TrimSpace(initialStep.EntryCondition) != "" {
		for _, step := range ordered {
			if strings.TrimSpace(step.ID) == strings.TrimSpace(initialStep.ID) {
				continue
			}
			autowired = append(autowired, ScenarioTransition{
				FromStepID: strings.TrimSpace(step.ID),
				ToStepID:   strings.TrimSpace(initialStep.ID),
				Condition:  strings.TrimSpace(initialStep.EntryCondition),
				Priority:   1,
			})
		}
	}
	return autowired
}

func cloneScenarioTransitions(transitions []ScenarioTransition) []ScenarioTransition {
	return append([]ScenarioTransition{}, transitions...)
}

func normalizeScenarioPackageTransitions(transitions []ScenarioPackageTransition) []ScenarioPackageTransition {
	normalized := make([]ScenarioPackageTransition, 0, len(transitions))
	for _, tr := range transitions {
		next := ScenarioPackageTransition{
			ToPackageID:        strings.TrimSpace(tr.ToPackageID),
			Condition:          strings.TrimSpace(tr.Condition),
			Priority:           tr.Priority,
			Action:             normalizeScenarioPackageTransitionAction(tr.Action),
			FinalStateOptionID: strings.TrimSpace(tr.FinalStateOptionID),
		}
		if next.Priority <= 0 {
			next.Priority = 1
		}
		normalized = append(normalized, next)
	}
	return normalized
}

func cloneScenarioPackageTransitions(transitions []ScenarioPackageTransition) []ScenarioPackageTransition {
	return append([]ScenarioPackageTransition{}, transitions...)
}

func normalizeFinalStateOptions(options []ScenarioFinalStateOption) []ScenarioFinalStateOption {
	normalized := make([]ScenarioFinalStateOption, 0, len(options))
	for _, item := range options {
		normalized = append(normalized, ScenarioFinalStateOption{
			ID:             strings.TrimSpace(item.ID),
			Name:           strings.TrimSpace(item.Name),
			Condition:      strings.TrimSpace(item.Condition),
			FinalStateJSON: strings.TrimSpace(item.FinalStateJSON),
			FinalLabel:     strings.TrimSpace(item.FinalLabel),
		})
	}
	return normalized
}

func cloneFinalStateOptions(options []ScenarioFinalStateOption) []ScenarioFinalStateOption {
	return append([]ScenarioFinalStateOption{}, options...)
}

func buildPotentialStateFields(steps []ScenarioStep) []ScenarioStateField {
	if len(steps) == 0 {
		return nil
	}
	fieldsByPath := make(map[string]ScenarioStateField)
	for _, step := range steps {
		schemaRaw := strings.TrimSpace(step.ResponseSchemaJSON)
		if schemaRaw == "" {
			continue
		}
		var schemaDoc any
		if err := json.Unmarshal([]byte(schemaRaw), &schemaDoc); err != nil {
			continue
		}
		walkScenarioStateSchema(schemaDoc, "", fieldsByPath)
	}
	fields := make([]ScenarioStateField, 0, len(fieldsByPath))
	for _, item := range fieldsByPath {
		if len(item.PossibleValues) > 1 {
			sort.Strings(item.PossibleValues)
		}
		fields = append(fields, item)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
	return fields
}

func walkScenarioStateSchema(node any, path string, fields map[string]ScenarioStateField) {
	object, ok := node.(map[string]any)
	if !ok {
		return
	}
	if propertiesRaw, ok := object["properties"]; ok {
		if properties, ok := propertiesRaw.(map[string]any); ok {
			for key, child := range properties {
				childPath := key
				if path != "" {
					childPath = path + "." + key
				}
				walkScenarioStateSchema(child, childPath, fields)
			}
		}
	}

	possibleValues := schemaNodePossibleValues(object)
	fieldType := strings.TrimSpace(toString(object["type"]))
	if path != "" && (fieldType != "" || len(possibleValues) > 0 || len(object) == 0) {
		current, exists := fields[path]
		if !exists {
			current = ScenarioStateField{Path: path}
		}
		if current.Type == "" && fieldType != "" {
			current.Type = fieldType
		}
		current.PossibleValues = mergeUniqueStrings(current.PossibleValues, possibleValues)
		fields[path] = current
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, child := range items {
			walkScenarioStateSchema(child, path, fields)
		}
	}
}

func schemaNodePossibleValues(node map[string]any) []string {
	out := make([]string, 0)
	if enumRaw, ok := node["enum"]; ok {
		if enumItems, ok := enumRaw.([]any); ok {
			for _, item := range enumItems {
				out = append(out, strings.TrimSpace(toString(item)))
			}
		}
	}
	if constRaw, ok := node["const"]; ok {
		out = append(out, strings.TrimSpace(toString(constRaw)))
	}
	if typeRaw, ok := node["type"]; ok {
		switch strings.TrimSpace(toString(typeRaw)) {
		case "boolean":
			out = append(out, "true", "false")
		}
	}
	return uniqueNonEmpty(out)
}

func mergeUniqueStrings(base []string, extra []string) []string {
	combined := append([]string{}, base...)
	combined = append(combined, extra...)
	return uniqueNonEmpty(combined)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, item := range values {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func hydrateScenarioPackageDerivedFields(item ScenarioPackage) ScenarioPackage {
	item.PotentialState = buildPotentialStateFields(item.Steps)
	item.FinalCondition = strings.TrimSpace(item.FinalCondition)
	return item
}

func (s *Service) ListScenarioPackages(ctx context.Context) []ScenarioPackage {
	if s.scenarioStore != nil {
		items, err := s.scenarioStore.List(ctx)
		if err == nil {
			for i := range items {
				items[i] = hydrateScenarioPackageDerivedFields(items[i])
			}
			return items
		}
	}
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ScenarioPackage, 0)
	for _, versions := range s.scenarioPackages {
		items = append(items, versions...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GameSlug == items[j].GameSlug {
			return items[i].Version > items[j].Version
		}
		return items[i].GameSlug < items[j].GameSlug
	})
	for i := range items {
		items[i] = hydrateScenarioPackageDerivedFields(items[i])
	}
	return items
}

func (s *Service) CreateScenarioPackage(ctx context.Context, req ScenarioPackageCreateRequest) (ScenarioPackage, error) {
	if err := ValidateScenarioPackageCreateRequest(req); err != nil {
		return ScenarioPackage{}, err
	}
	gameSlug := strings.TrimSpace(req.GameSlug)
	if gameSlug == "" {
		gameSlug = "global"
	}
	now := time.Now().UTC()
	normalizedSteps := normalizeScenarioSteps(req.Steps, gameSlug, now)
	normalizedTransitions := normalizeScenarioTransitions(normalizedSteps, req.Transitions)
	normalizedPackageTransitions := normalizeScenarioPackageTransitions(req.PackageTransitions)
	normalizedFinalStateOptions := normalizeFinalStateOptions(req.FinalStateOptions)
	normalizedFinalCondition := strings.TrimSpace(req.FinalCondition)
	req.Steps = normalizedSteps
	if strings.TrimSpace(req.LLMModelConfigID) != "" {
		if _, err := s.GetLLMModelConfig(ctx, req.LLMModelConfigID); err != nil {
			return ScenarioPackage{}, err
		}
	}
	if s.scenarioStore != nil {
		item := ScenarioPackage{
			Name:               strings.TrimSpace(req.Name),
			GameSlug:           gameSlug,
			LLMModelConfigID:   strings.TrimSpace(req.LLMModelConfigID),
			Steps:              append([]ScenarioStep(nil), req.Steps...),
			Transitions:        cloneScenarioTransitions(normalizedTransitions),
			PackageTransitions: cloneScenarioPackageTransitions(normalizedPackageTransitions),
			FinalStateOptions:  cloneFinalStateOptions(normalizedFinalStateOptions),
			FinalCondition:     normalizedFinalCondition,
			CreatedBy:          strings.TrimSpace(req.ActorID),
			CreatedAt:          now,
		}
		created, err := s.scenarioStore.Create(ctx, item)
		if err != nil {
			return ScenarioPackage{}, err
		}
		return hydrateScenarioPackageDerivedFields(created), nil
	}
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scenarioPackages == nil {
		s.scenarioPackages = map[string][]ScenarioPackage{}
	}
	s.counter++
	version := len(s.scenarioPackages[gameSlug]) + 1
	item := ScenarioPackage{
		ID:                 fmt.Sprintf("scenario-pkg-%d", s.counter),
		Name:               strings.TrimSpace(req.Name),
		Version:            version,
		GameSlug:           gameSlug,
		LLMModelConfigID:   strings.TrimSpace(req.LLMModelConfigID),
		Steps:              append([]ScenarioStep(nil), req.Steps...),
		Transitions:        cloneScenarioTransitions(normalizedTransitions),
		PackageTransitions: cloneScenarioPackageTransitions(normalizedPackageTransitions),
		FinalStateOptions:  cloneFinalStateOptions(normalizedFinalStateOptions),
		FinalCondition:     normalizedFinalCondition,
		CreatedBy:          strings.TrimSpace(req.ActorID),
		CreatedAt:          now,
	}
	if len(s.scenarioPackages[gameSlug]) == 0 {
		item.IsActive = true
		item.ActivatedBy = strings.TrimSpace(req.ActorID)
		item.ActivatedAt = now
	}
	s.scenarioPackages[gameSlug] = append(s.scenarioPackages[gameSlug], item)
	return hydrateScenarioPackageDerivedFields(item), nil
}

func (s *Service) GetScenarioPackage(ctx context.Context, id string) (ScenarioPackage, error) {
	if s.scenarioStore != nil {
		lookup := strings.TrimSpace(id)
		if lookup == "" {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		item, err := s.scenarioStore.GetByID(ctx, lookup)
		if err != nil {
			return ScenarioPackage{}, err
		}
		return hydrateScenarioPackageDerivedFields(item), nil
	}
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	lookup := strings.TrimSpace(id)
	for _, versions := range s.scenarioPackages {
		for _, item := range versions {
			if item.ID == lookup {
				return hydrateScenarioPackageDerivedFields(item), nil
			}
		}
	}
	return ScenarioPackage{}, ErrScenarioPackageNotFound
}

func (s *Service) UpdateScenarioPackage(ctx context.Context, id string, req ScenarioPackageCreateRequest) (ScenarioPackage, error) {
	if err := ValidateScenarioPackageCreateRequest(req); err != nil {
		return ScenarioPackage{}, err
	}
	targetGameSlug := strings.TrimSpace(req.GameSlug)
	if targetGameSlug == "" {
		targetGameSlug = "global"
	}
	now := time.Now().UTC()
	normalizedSteps := normalizeScenarioSteps(req.Steps, targetGameSlug, now)
	normalizedTransitions := normalizeScenarioTransitions(normalizedSteps, req.Transitions)
	normalizedPackageTransitions := normalizeScenarioPackageTransitions(req.PackageTransitions)
	normalizedFinalStateOptions := normalizeFinalStateOptions(req.FinalStateOptions)
	normalizedFinalCondition := strings.TrimSpace(req.FinalCondition)
	req.Steps = normalizedSteps
	if strings.TrimSpace(req.LLMModelConfigID) != "" {
		if _, err := s.GetLLMModelConfig(ctx, req.LLMModelConfigID); err != nil {
			return ScenarioPackage{}, err
		}
	}
	if s.scenarioStore != nil {
		lookup := strings.TrimSpace(id)
		if lookup == "" {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		current, err := s.scenarioStore.GetByID(ctx, lookup)
		if err != nil {
			return ScenarioPackage{}, err
		}
		previousGameSlug := current.GameSlug
		current.Name = strings.TrimSpace(req.Name)
		current.GameSlug = targetGameSlug
		current.LLMModelConfigID = strings.TrimSpace(req.LLMModelConfigID)
		current.Steps = append([]ScenarioStep(nil), req.Steps...)
		current.Transitions = cloneScenarioTransitions(normalizedTransitions)
		current.PackageTransitions = cloneScenarioPackageTransitions(normalizedPackageTransitions)
		current.FinalStateOptions = cloneFinalStateOptions(normalizedFinalStateOptions)
		current.FinalCondition = normalizedFinalCondition
		if current.GameSlug != previousGameSlug {
			current.IsActive = false
			current.ActivatedBy = ""
			current.ActivatedAt = time.Time{}
		}
		updated, err := s.scenarioStore.Update(ctx, current)
		if err != nil {
			return ScenarioPackage{}, err
		}
		return hydrateScenarioPackageDerivedFields(updated), nil
	}
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.scenarioPackages {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			updated := item
			updated.Name = strings.TrimSpace(req.Name)
			updated.GameSlug = targetGameSlug
			updated.LLMModelConfigID = strings.TrimSpace(req.LLMModelConfigID)
			updated.Steps = append([]ScenarioStep(nil), req.Steps...)
			updated.Transitions = cloneScenarioTransitions(normalizedTransitions)
			updated.PackageTransitions = cloneScenarioPackageTransitions(normalizedPackageTransitions)
			updated.FinalStateOptions = cloneFinalStateOptions(normalizedFinalStateOptions)
			updated.FinalCondition = normalizedFinalCondition
			if updated.GameSlug != gameSlug {
				updated.IsActive = false
				updated.ActivatedBy = ""
				updated.ActivatedAt = time.Time{}
			}
			if updated.GameSlug != gameSlug {
				s.scenarioPackages[gameSlug] = append(versions[:i], versions[i+1:]...)
				s.scenarioPackages[updated.GameSlug] = append(s.scenarioPackages[updated.GameSlug], updated)
			} else {
				versions[i] = updated
				s.scenarioPackages[gameSlug] = versions
			}
			return hydrateScenarioPackageDerivedFields(updated), nil
		}
	}
	return ScenarioPackage{}, ErrScenarioPackageNotFound
}

func (s *Service) DeleteScenarioPackage(ctx context.Context, id string) error {
	if s.scenarioStore != nil {
		lookup := strings.TrimSpace(id)
		if lookup == "" {
			return ErrScenarioPackageNotFound
		}
		return s.scenarioStore.Delete(ctx, lookup)
	}
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.scenarioPackages {
		for i, item := range versions {
			if item.ID != lookup {
				continue
			}
			s.scenarioPackages[gameSlug] = append(versions[:i], versions[i+1:]...)
			if item.IsActive && len(s.scenarioPackages[gameSlug]) > 0 {
				s.scenarioPackages[gameSlug][0].IsActive = true
			}
			return nil
		}
	}
	return ErrScenarioPackageNotFound
}

func (s *Service) ActivateScenarioPackage(ctx context.Context, id, actorID string) (ScenarioPackage, error) {
	if s.scenarioStore != nil {
		lookup := strings.TrimSpace(id)
		if lookup == "" {
			return ScenarioPackage{}, ErrScenarioPackageNotFound
		}
		item, err := s.scenarioStore.SetActive(ctx, lookup, strings.TrimSpace(actorID), time.Now().UTC())
		if err != nil {
			return ScenarioPackage{}, err
		}
		return hydrateScenarioPackageDerivedFields(item), nil
	}
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := strings.TrimSpace(id)
	for gameSlug, versions := range s.scenarioPackages {
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
			if i == active {
				versions[i].ActivatedAt = now
				versions[i].ActivatedBy = strings.TrimSpace(actorID)
			}
		}
		s.scenarioPackages[gameSlug] = versions
		return hydrateScenarioPackageDerivedFields(versions[active]), nil
	}
	return ScenarioPackage{}, ErrScenarioPackageNotFound
}

func (s *Service) GetActiveScenarioPackage(ctx context.Context, gameSlug string) (ScenarioPackage, error) {
	if s.scenarioStore != nil {
		key := strings.TrimSpace(gameSlug)
		if key == "" {
			key = "global"
		}
		item, err := s.scenarioStore.GetActiveByGameSlug(ctx, key)
		if err != nil {
			return ScenarioPackage{}, err
		}
		return hydrateScenarioPackageDerivedFields(item), nil
	}
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.scenarioPackages == nil {
		return ScenarioPackage{}, ErrScenarioPackageNotFound
	}
	key := strings.TrimSpace(gameSlug)
	if key == "" {
		key = "global"
	}
	for _, item := range s.scenarioPackages[key] {
		if item.IsActive {
			return hydrateScenarioPackageDerivedFields(item), nil
		}
	}
	return ScenarioPackage{}, ErrScenarioPackageNotFound
}

func (p ScenarioPackage) ResolveStep(currentStepID, stateJSON string) (ScenarioStep, bool, error) {
	byID := make(map[string]ScenarioStep, len(p.Steps))
	for _, step := range p.Steps {
		byID[step.ID] = step
	}

	state := parseJSONMap(stateJSON)
	current := strings.TrimSpace(currentStepID)
	if current == "" {
		entry, err := p.InitialStep()
		if err != nil {
			return ScenarioStep{}, false, err
		}
		return entry, true, nil
	}

	active, ok := byID[current]
	if !ok {
		return ScenarioStep{}, false, ErrScenarioStepNotFound
	}

	transitions := make([]ScenarioTransition, 0)
	for _, item := range p.Transitions {
		if strings.EqualFold(strings.TrimSpace(item.FromStepID), current) {
			transitions = append(transitions, item)
		}
	}
	sort.Slice(transitions, func(i, j int) bool { return transitions[i].Priority > transitions[j].Priority })
	for _, tr := range transitions {
		ok, err := evaluateCondition(tr.Condition, state)
		if err != nil || !ok {
			continue
		}
		next, ok := byID[strings.TrimSpace(tr.ToStepID)]
		if !ok {
			continue
		}
		return next, next.ID != current, nil
	}
	return active, false, nil
}

const ScenarioPackageTransitionActionStopTracking = "stop_tracking"

func normalizeScenarioPackageTransitionAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

type ScenarioPackageResolution struct {
	PackageID      string
	Changed        bool
	StopTracking   bool
	FinalStateJSON string
	FinalLabel     string
}

func (p ScenarioPackage) ResolveNextPackage(stateJSON string) (ScenarioPackageResolution, error) {
	currentPackageID := strings.TrimSpace(p.ID)
	if len(p.PackageTransitions) == 0 {
		return ScenarioPackageResolution{PackageID: currentPackageID}, nil
	}
	state := parseJSONMap(stateJSON)
	transitions := cloneScenarioPackageTransitions(p.PackageTransitions)
	optionsByID := make(map[string]ScenarioFinalStateOption, len(p.FinalStateOptions))
	for _, item := range p.FinalStateOptions {
		optionsByID[strings.TrimSpace(item.ID)] = item
	}
	sort.Slice(transitions, func(i, j int) bool { return transitions[i].Priority > transitions[j].Priority })
	for _, tr := range transitions {
		condition := strings.TrimSpace(tr.Condition)
		optionID := strings.TrimSpace(tr.FinalStateOptionID)
		matched, err := evaluateCondition(condition, state)
		if err != nil || !matched {
			continue
		}
		option := ScenarioFinalStateOption{}
		if optionID != "" {
			option = optionsByID[optionID]
		}
		if normalizeScenarioPackageTransitionAction(tr.Action) == ScenarioPackageTransitionActionStopTracking {
			return ScenarioPackageResolution{
				PackageID:      currentPackageID,
				StopTracking:   true,
				FinalStateJSON: strings.TrimSpace(option.FinalStateJSON),
				FinalLabel:     strings.TrimSpace(option.FinalLabel),
			}, nil
		}
		target := strings.TrimSpace(tr.ToPackageID)
		if target == "" || target == currentPackageID {
			return ScenarioPackageResolution{PackageID: currentPackageID}, nil
		}
		return ScenarioPackageResolution{PackageID: target, Changed: true}, nil
	}
	return ScenarioPackageResolution{PackageID: currentPackageID}, nil
}

func (p ScenarioPackage) CanEnter(stateJSON string) (bool, error) {
	initial, err := p.InitialStep()
	if err != nil {
		return false, err
	}
	state := parseJSONMap(stateJSON)
	return evaluateCondition(initial.EntryCondition, state)
}

func (p ScenarioPackage) InitialStep() (ScenarioStep, error) {
	initial := make([]ScenarioStep, 0, len(p.Steps))
	for _, step := range p.Steps {
		if step.Initial {
			initial = append(initial, step)
		}
	}
	if len(initial) != 1 {
		return ScenarioStep{}, ErrInvalidScenarioInitial
	}
	return initial[0], nil
}

func (p ScenarioPackage) BuildVisualGraph() ScenarioPackageGraph {
	nodes := make([]ScenarioGraphNode, 0, len(p.Steps))
	for _, step := range p.Steps {
		nodes = append(nodes, ScenarioGraphNode{
			ID:       step.ID,
			Name:     step.Name,
			GameSlug: step.GameSlug,
			Folder:   step.Folder,
			Initial:  step.Initial,
			Order:    step.Order,
			Level:    scenarioStepLevel(step),
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Level == nodes[j].Level {
			if nodes[i].Order == nodes[j].Order {
				return nodes[i].ID < nodes[j].ID
			}
			return nodes[i].Order < nodes[j].Order
		}
		return nodes[i].Level < nodes[j].Level
	})

	edges := make([]ScenarioGraphEdge, 0, len(p.Transitions))
	for i, tr := range p.Transitions {
		edges = append(edges, ScenarioGraphEdge{
			ID:         "edge-" + strconv.Itoa(i+1),
			FromStepID: tr.FromStepID,
			ToStepID:   tr.ToStepID,
			Condition:  tr.Condition,
			Priority:   tr.Priority,
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromStepID == edges[j].FromStepID {
			if edges[i].Priority == edges[j].Priority {
				return edges[i].ToStepID < edges[j].ToStepID
			}
			return edges[i].Priority < edges[j].Priority
		}
		return edges[i].FromStepID < edges[j].FromStepID
	})

	groupsByKey := make(map[string]*ScenarioGraphGroup)
	for _, node := range nodes {
		groupFolder := strings.TrimSpace(node.Folder)
		if groupFolder == "" {
			groupFolder = "root"
		}
		groupGame := strings.TrimSpace(node.GameSlug)
		if groupGame == "" {
			groupGame = strings.TrimSpace(p.GameSlug)
		}
		if groupGame == "" {
			groupGame = "global"
		}
		key := groupGame + "/" + groupFolder
		group, ok := groupsByKey[key]
		if !ok {
			group = &ScenarioGraphGroup{
				ID:       key,
				Label:    key,
				GameSlug: groupGame,
				Folder:   groupFolder,
				NodeIDs:  []string{},
			}
			groupsByKey[key] = group
		}
		group.NodeIDs = append(group.NodeIDs, node.ID)
	}
	groups := make([]ScenarioGraphGroup, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		sort.Strings(group.NodeIDs)
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })

	return ScenarioPackageGraph{
		PackageID:   p.ID,
		PackageName: p.Name,
		GameSlug:    p.GameSlug,
		Version:     p.Version,
		Nodes:       nodes,
		Edges:       edges,
		Groups:      groups,
	}
}

func scenarioStepLevel(step ScenarioStep) int {
	folder := strings.Trim(strings.TrimSpace(step.Folder), "/")
	if step.Initial {
		return 1
	}
	if folder == "" {
		return 1
	}
	parts := strings.Split(folder, "/")
	level := 1 + len(parts)
	if level < 1 {
		return 1
	}
	return level
}

func evaluateCondition(condition string, payload map[string]any) (bool, error) {
	expr := strings.TrimSpace(condition)
	if len(expr) >= 3 && strings.EqualFold(expr[:3], "if ") {
		expr = strings.TrimSpace(expr[3:])
	}
	if expr == "" {
		return true, nil
	}
	return evaluateBooleanExpression(expr, payload)
}

func evaluateBooleanExpression(expr string, payload map[string]any) (bool, error) {
	trimmed := trimConditionParentheses(expr)
	if trimmed == "" {
		return false, fmt.Errorf("unsupported condition: %s", expr)
	}
	if segments, ok := splitConditionByTopLevelOperator(trimmed, []string{"||", "|"}); ok {
		for _, segment := range segments {
			matched, err := evaluateBooleanExpression(segment, payload)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
	if segments, ok := splitConditionByTopLevelOperator(trimmed, []string{"&&", "&"}); ok {
		for _, segment := range segments {
			matched, err := evaluateBooleanExpression(segment, payload)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}
	return evaluateAtomicCondition(trimmed, payload)
}

func evaluateAtomicCondition(expr string, payload map[string]any) (bool, error) {
	if strings.HasPrefix(expr, "exists(") && strings.HasSuffix(expr, ")") {
		path := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "exists("), ")"))
		_, ok := lookupJSONPath(payload, path)
		return ok, nil
	}
	if strings.HasPrefix(expr, "not_exists(") && strings.HasSuffix(expr, ")") {
		path := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "not_exists("), ")"))
		_, ok := lookupJSONPath(payload, path)
		return !ok, nil
	}
	for _, op := range []string{">=", "<=", "!=", "==", "=", ">", "<"} {
		idx := strings.Index(expr, op)
		if idx <= 0 {
			continue
		}
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+len(op):])
		if left == "" || right == "" || strings.ContainsAny(right[:1], "<>!=") {
			return false, fmt.Errorf("unsupported condition: %s", expr)
		}
		raw, ok := lookupJSONPath(payload, left)
		if !ok {
			return false, nil
		}
		switch op {
		case "=", "==", "!=":
			leftValue := fmt.Sprint(raw)
			rightValue := strings.Trim(right, "'\"")
			matched := strings.EqualFold(leftValue, rightValue)
			if op == "!=" {
				return !matched, nil
			}
			return matched, nil
		case ">", "<", ">=", "<=":
			leftNumber, ok := conditionNumber(raw)
			if !ok {
				return false, nil
			}
			rightNumber, ok := conditionNumber(strings.Trim(right, "'\""))
			if !ok {
				return false, fmt.Errorf("unsupported condition: %s", expr)
			}
			switch op {
			case ">":
				return leftNumber > rightNumber, nil
			case "<":
				return leftNumber < rightNumber, nil
			case ">=":
				return leftNumber >= rightNumber, nil
			default:
				return leftNumber <= rightNumber, nil
			}
		}
	}
	if isScenarioConditionShorthandLiteral(expr) {
		if modeRaw, ok := payload["mode"]; ok {
			return strings.EqualFold(fmt.Sprint(modeRaw), expr), nil
		}
		return false, nil
	}
	return false, fmt.Errorf("unsupported condition: %s", expr)
}

func conditionNumber(raw any) (float64, bool) {
	switch typed := raw.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case string:
		v, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return v, true
	default:
		return 0, false
	}
}

func trimConditionParentheses(expr string) string {
	trimmed := strings.TrimSpace(expr)
	for strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		depth := 0
		validOuter := true
		for idx, ch := range trimmed {
			switch ch {
			case '(':
				depth++
			case ')':
				depth--
				if depth < 0 {
					return strings.TrimSpace(expr)
				}
				if depth == 0 && idx < len(trimmed)-1 {
					validOuter = false
				}
			}
		}
		if !validOuter || depth != 0 {
			break
		}
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	return trimmed
}

func splitConditionByTopLevelOperator(expr string, operators []string) ([]string, bool) {
	parts := make([]string, 0, 2)
	start := 0
	depth := 0
	matched := false
	for idx := 0; idx < len(expr); idx++ {
		switch expr[idx] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, false
			}
		}
		if depth != 0 {
			continue
		}
		for _, op := range operators {
			if strings.HasPrefix(expr[idx:], op) {
				segment := strings.TrimSpace(expr[start:idx])
				if segment == "" {
					return nil, false
				}
				parts = append(parts, segment)
				start = idx + len(op)
				idx += len(op) - 1
				matched = true
				break
			}
		}
	}
	if !matched || depth != 0 {
		return nil, false
	}
	last := strings.TrimSpace(expr[start:])
	if last == "" {
		return nil, false
	}
	parts = append(parts, last)
	return parts, true
}

func parseJSONMap(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func lookupJSONPath(payload map[string]any, path string) (any, bool) {
	cleanPath := strings.TrimSpace(path)
	cleanPath = strings.TrimPrefix(cleanPath, "$.")
	cleanPath = strings.TrimPrefix(cleanPath, "$")
	cleanPath = strings.TrimPrefix(cleanPath, ".")
	parts := strings.Split(cleanPath, ".")
	var current any = payload
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			v, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = v
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			current = typed[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func validateScenarioCondition(condition string) error {
	expr := strings.TrimSpace(condition)
	if isScenarioConditionShorthandLiteral(expr) {
		return nil
	}
	_, err := evaluateCondition(expr, map[string]any{})
	return err
}

var scenarioConditionLiteralPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func isScenarioConditionShorthandLiteral(expr string) bool {
	clean := strings.TrimSpace(expr)
	if clean == "" {
		return false
	}
	return scenarioConditionLiteralPattern.MatchString(clean)
}
