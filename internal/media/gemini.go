package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrGeminiAPIKeyRequired    = errors.New("gemini api key is required")
	ErrGeminiChunkRequired     = errors.New("gemini chunk reference is required")
	ErrGeminiChunkTooLarge     = errors.New("gemini chunk exceeds inline upload limit")
	ErrGeminiEmptyResponse     = errors.New("gemini returned empty response")
	ErrGeminiInvalidConfidence = errors.New("gemini confidence must be between 0 and 1")
	ErrGeminiUnsupportedMIME   = errors.New("gemini does not support the chunk mime type")
)

type GeminiClassifierConfig struct {
	APIKey         string
	BaseURL        string
	MaxInlineBytes int64
	ChatMaxTokens  int
	HTTPClient     *http.Client
}

type GeminiStageClassifier struct {
	apiKey         string
	baseURL        string
	maxInlineBytes int64
	maxChatTokens  int
	httpClient     *http.Client
	sessionsMu     sync.Mutex
	sessions       map[string]geminiChatSession
}

type geminiChatSession struct {
	TokenCount        int
	PromptFingerprint string
}

func NewGeminiStageClassifier(cfg GeminiClassifierConfig) (*GeminiStageClassifier, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, ErrGeminiAPIKeyRequired
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://generativelanguage.googleapis.com"
	}
	if cfg.MaxInlineBytes <= 0 {
		cfg.MaxInlineBytes = 19 * 1024 * 1024
	}
	if cfg.ChatMaxTokens <= 0 {
		cfg.ChatMaxTokens = 900000
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &GeminiStageClassifier{
		apiKey:         strings.TrimSpace(cfg.APIKey),
		baseURL:        strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		maxInlineBytes: cfg.MaxInlineBytes,
		maxChatTokens:  cfg.ChatMaxTokens,
		httpClient:     cfg.HTTPClient,
		sessions:       make(map[string]geminiChatSession),
	}, nil
}

type geminiGenerateContentRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiGenerationConfig struct {
	Temperature      float64 `json:"temperature,omitempty"`
	MaxOutputTokens  int     `json:"maxOutputTokens,omitempty"`
	ResponseMIMEType string  `json:"responseMimeType,omitempty"`
	MediaResolution  string  `json:"mediaResolution,omitempty"`
}

type geminiGenerateContentResponse struct {
	Candidates     []geminiCandidate    `json:"candidates"`
	UsageMetadata  geminiUsageMetadata  `json:"usageMetadata"`
	PromptFeedback geminiPromptFeedback `json:"promptFeedback"`
}

type geminiPromptFeedback struct {
	BlockReason   string               `json:"blockReason"`
	SafetyRatings []geminiSafetyRating `json:"safetyRatings"`
}

type geminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked"`
}

type geminiCandidate struct {
	Content      geminiContentResponse `json:"content"`
	FinishReason string                `json:"finishReason"`
}

type geminiContentResponse struct {
	Parts []geminiPartResponse `json:"parts"`
}

type geminiPartResponse struct {
	Text string `json:"text"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiStageResponse struct {
	Label              string          `json:"label"`
	Confidence         float64         `json:"confidence"`
	Summary            string          `json:"summary,omitempty"`
	UpdatedState       json.RawMessage `json:"updated_state,omitempty"`
	Delta              json.RawMessage `json:"delta,omitempty"`
	NextNeededEvidence json.RawMessage `json:"next_needed_evidence,omitempty"`
	HardConflicts      json.RawMessage `json:"hard_conflicts,omitempty"`
	FinalOutcome       string          `json:"final_outcome,omitempty"`
}

func (c *GeminiStageClassifier) Classify(ctx context.Context, input StageRequest) (StageClassification, error) {
	result, err := c.classify(ctx, input, false)
	if err == nil {
		return result, nil
	}
	if !isGeminiSessionRecoveryError(err) {
		return StageClassification{}, err
	}
	c.resetSession(geminiSessionKey(input))
	return c.classify(ctx, input, true)
}

func (c *GeminiStageClassifier) classify(ctx context.Context, input StageRequest, forceBootstrap bool) (StageClassification, error) {
	chunkRef := strings.TrimSpace(input.Chunk.Reference)
	if chunkRef == "" {
		return StageClassification{}, ErrGeminiChunkRequired
	}
	data, mimeType, err := loadGeminiChunk(chunkRef, c.maxInlineBytes)
	if err != nil {
		return StageClassification{}, err
	}

	if err := validateGeminiMIMEType(mimeType); err != nil {
		return StageClassification{}, err
	}

	sessionKey := geminiSessionKey(input)
	promptFingerprint := geminiPromptFingerprint(input)
	contents := c.prepareSessionContents(sessionKey, promptFingerprint, input, mimeType, data, forceBootstrap)

	requestBody := geminiGenerateContentRequest{
		Contents: contents,
		GenerationConfig: geminiGenerationConfig{
			Temperature:      input.Prompt.Temperature,
			MaxOutputTokens:  input.Prompt.MaxTokens,
			ResponseMIMEType: "application/json",
			MediaResolution:  "MEDIA_RESOLUTION_LOW",
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return StageClassification{}, err
	}

	model := normalizeGeminiModel(input.Prompt.Model)

	requestCtx := ctx
	cancel := func() {}
	if input.Prompt.TimeoutMS > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(input.Prompt.TimeoutMS)*time.Millisecond)
	}
	defer cancel()

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", c.baseURL, model, c.apiKey)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return StageClassification{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	started := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StageClassification{}, err
	}
	defer resp.Body.Close() //nolint:errcheck

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return StageClassification{}, err
	}
	if resp.StatusCode >= 400 {
		return StageClassification{}, fmt.Errorf("gemini generateContent failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var payload geminiGenerateContentResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return StageClassification{}, fmt.Errorf("decode gemini response: %w", err)
	}
	rawText := extractGeminiResponseText(payload)
	if rawText == "" {
		return StageClassification{}, describeGeminiEmptyResponse(payload, responseBody)
	}

	parsed, err := parseGeminiStageResponse(rawText)
	if err != nil {
		return StageClassification{}, err
	}
	parsed = normalizeGeminiTrackerResponse(input, parsed)
	if parsed.Confidence < 0 || parsed.Confidence > 1 {
		return StageClassification{}, ErrGeminiInvalidConfidence
	}
	if err := validateGeminiTrackerResponse(input.Stage, parsed); err != nil {
		return StageClassification{}, err
	}
	c.storeSessionResponse(sessionKey, promptFingerprint, contents, payload, rawText)

	label := strings.TrimSpace(parsed.Label)
	if label == "" && len(parsed.UpdatedState) > 0 {
		label = "state_updated"
	}
	if label == "" && strings.TrimSpace(parsed.FinalOutcome) != "" {
		label = strings.TrimSpace(parsed.FinalOutcome)
	}
	confidence := parsed.Confidence
	if confidence == 0 && (len(parsed.UpdatedState) > 0 || strings.TrimSpace(parsed.FinalOutcome) != "") {
		confidence = 1
	}
	return StageClassification{
		Label:             label,
		Confidence:        confidence,
		RawResponse:       strings.TrimSpace(rawText),
		RequestRef:        endpoint,
		ResponseRef:       strconv.Itoa(resp.StatusCode),
		TokensIn:          payload.UsageMetadata.PromptTokenCount,
		TokensOut:         payload.UsageMetadata.CandidatesTokenCount,
		Latency:           time.Since(started),
		NormalizedOutcome: firstNonEmpty(strings.TrimSpace(parsed.FinalOutcome), label),
		UpdatedStateJSON:  marshalRawMessage(parsed.UpdatedState),
		EvidenceDeltaJSON: marshalRawMessage(parsed.Delta),
		NextEvidenceJSON:  marshalRawMessage(parsed.NextNeededEvidence),
		ConflictsJSON:     marshalRawMessage(parsed.HardConflicts),
		FinalOutcome:      strings.TrimSpace(parsed.FinalOutcome),
	}, nil
}

func isGeminiSessionRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrGeminiEmptyResponse) {
		return true
	}
	return strings.Contains(err.Error(), ErrGeminiEmptyResponse.Error())
}

func geminiSessionKey(input StageRequest) string {
	key := strings.TrimSpace(input.StreamerID)
	if key == "" {
		key = "global"
	}
	return strings.ToLower(key)
}

func geminiPromptFingerprint(input StageRequest) string {
	return strings.Join([]string{
		strings.TrimSpace(input.Prompt.ID),
		strings.TrimSpace(input.Prompt.Template),
		strings.TrimSpace(input.StateSchema),
		strings.TrimSpace(input.RuleSet),
	}, "|")
}

func (c *GeminiStageClassifier) prepareSessionContents(sessionKey, promptFingerprint string, input StageRequest, mimeType string, chunk []byte, forceBootstrap bool) []geminiContent {
	userTurn := geminiContent{
		Role: "user",
		Parts: []geminiPart{
			{InlineData: &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(chunk)}},
		},
	}
	c.sessionsMu.Lock()
	session, hasSession := c.sessions[sessionKey]
	shouldRotate := forceBootstrap || !hasSession || session.TokenCount >= c.maxChatTokens || session.PromptFingerprint != promptFingerprint
	if shouldRotate {
		userTurn.Parts = append([]geminiPart{{Text: buildGeminiInstruction(input)}}, userTurn.Parts...)
		c.sessions[sessionKey] = geminiChatSession{
			TokenCount:        0,
			PromptFingerprint: promptFingerprint,
		}
		c.sessionsMu.Unlock()
		return []geminiContent{userTurn}
	}
	userTurn.Parts = append([]geminiPart{{Text: buildGeminiContinuationInstruction(input)}}, userTurn.Parts...)
	c.sessionsMu.Unlock()
	return []geminiContent{userTurn}
}

func (c *GeminiStageClassifier) resetSession(sessionKey string) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	delete(c.sessions, strings.ToLower(strings.TrimSpace(sessionKey)))
}

func (c *GeminiStageClassifier) storeSessionResponse(sessionKey, promptFingerprint string, requestContents []geminiContent, payload geminiGenerateContentResponse, rawText string) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	session, ok := c.sessions[sessionKey]
	if !ok || session.PromptFingerprint != promptFingerprint {
		return
	}
	updated := geminiChatSession{
		PromptFingerprint: promptFingerprint,
	}
	_ = requestContents
	_ = rawText
	updated.TokenCount = session.TokenCount + payload.UsageMetadata.PromptTokenCount + payload.UsageMetadata.CandidatesTokenCount
	c.sessions[sessionKey] = updated
}

func buildGeminiInstruction(input StageRequest) string {
	base := `You analyze a livestream chunk for FunPot.
Stage: %s
Streamer ID: %s
Chunk captured at: %s
Chunk reference: %s
Use this admin-managed tracker prompt as the source of truth:
%s
Active state schema:
%s
Active rule set:
%s`
	previousState := strings.TrimSpace(input.PreviousState)
	if previousState == "" {
		previousState = defaultTrackerState()
	}
	if !isTrackerStage(input.Stage) {
		return strings.TrimSpace(fmt.Sprintf(base+`
Return ONLY valid JSON with keys: label, confidence, summary.
- label: short snake_case decision for this stage.
- confidence: number between 0 and 1.
- summary: short rationale.`, input.Stage, strings.TrimSpace(input.StreamerID), input.Chunk.CapturedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(input.Chunk.Reference), strings.TrimSpace(input.Prompt.Template), strings.TrimSpace(input.StateSchema), strings.TrimSpace(input.RuleSet)))
	}
	return strings.TrimSpace(fmt.Sprintf(base+`
Previous persisted tracker state JSON:
%s
Update the tracker using previous_state + new_chunk for this 10-second window.
Return ONLY valid JSON with this exact shape:
{
  "label": "state_updated | finalized | unknown",
  "confidence": 0.0,
  "updated_state": {},
  "delta": [],
  "next_needed_evidence": [],
  "hard_conflicts": [],
  "final_outcome": "win | loss | draw | unknown"
}
Rules:
- Treat one chat/session as one match.
- Always update and return compact state JSON in updated_state.
- Never emit narrative commentary outside JSON.
- Keep final_outcome as unknown until direct evidence exists.
- Store contradictions in hard_conflicts instead of overwriting prior facts.`, input.Stage, strings.TrimSpace(input.StreamerID), input.Chunk.CapturedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(input.Chunk.Reference), strings.TrimSpace(input.Prompt.Template), strings.TrimSpace(input.StateSchema), strings.TrimSpace(input.RuleSet), previousState))
}

func buildGeminiContinuationInstruction(input StageRequest) string {
	previousState := strings.TrimSpace(input.PreviousState)
	if previousState == "" {
		previousState = defaultTrackerState()
	}
	return strings.TrimSpace(fmt.Sprintf(`Continue the existing match chat session.
Stage: %s
Streamer ID: %s
Chunk captured at: %s
Chunk reference: %s
Previous persisted tracker state JSON:
%s
Return ONLY valid JSON using the same schema as before.`, input.Stage, strings.TrimSpace(input.StreamerID), input.Chunk.CapturedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(input.Chunk.Reference), previousState))
}

func loadGeminiChunk(path string, maxBytes int64) ([]byte, string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if maxBytes > 0 && stat.Size() > maxBytes {
		return nil, "", fmt.Errorf("%w: size=%d limit=%d", ErrGeminiChunkTooLarge, stat.Size(), maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, detectChunkMimeType(path), nil
}

func detectChunkMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".ts":
		return "video/mp2t"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	}
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}

func extractGeminiResponseText(payload geminiGenerateContentResponse) string {
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				return strings.TrimSpace(part.Text)
			}
		}
	}
	return ""
}

func describeGeminiEmptyResponse(payload geminiGenerateContentResponse, responseBody []byte) error {
	parts := []string{ErrGeminiEmptyResponse.Error()}
	if len(payload.Candidates) == 0 {
		parts = append(parts, "candidates=0")
	}
	if reasons := geminiFinishReasons(payload.Candidates); len(reasons) > 0 {
		parts = append(parts, "finish_reasons="+strings.Join(reasons, ","))
	}
	if reason := strings.TrimSpace(payload.PromptFeedback.BlockReason); reason != "" {
		parts = append(parts, "block_reason="+reason)
	}
	if ratings := geminiBlockedSafetyCategories(payload.PromptFeedback.SafetyRatings); len(ratings) > 0 {
		parts = append(parts, "blocked_safety_categories="+strings.Join(ratings, ","))
	}
	if body := strings.TrimSpace(string(responseBody)); body != "" {
		if len(body) > 512 {
			body = body[:512] + "..."
		}
		parts = append(parts, "body="+strconv.Quote(body))
	}
	return errors.New(strings.Join(parts, "; "))
}

func geminiFinishReasons(candidates []geminiCandidate) []string {
	if len(candidates) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		reason := strings.TrimSpace(candidate.FinishReason)
		if reason == "" {
			continue
		}
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		reasons = append(reasons, reason)
	}
	return reasons
}

func geminiBlockedSafetyCategories(ratings []geminiSafetyRating) []string {
	if len(ratings) == 0 {
		return nil
	}
	categories := make([]string, 0, len(ratings))
	for _, rating := range ratings {
		if !rating.Blocked {
			continue
		}
		category := strings.TrimSpace(rating.Category)
		if category == "" {
			category = "unknown"
		}
		probability := strings.TrimSpace(rating.Probability)
		if probability != "" {
			category += ":" + probability
		}
		categories = append(categories, category)
	}
	return categories
}

func parseGeminiStageResponse(raw string) (geminiStageResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed geminiStageResponse
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		if hasGeminiResponsePayload(parsed) {
			return parsed, nil
		}
	}

	var generic map[string]any
	if err := json.Unmarshal([]byte(cleaned), &generic); err != nil {
		return geminiStageResponse{}, fmt.Errorf("parse gemini stage response: %w", err)
	}
	parsed.Label = strings.TrimSpace(stringValue(generic["label"]))
	parsed.Summary = strings.TrimSpace(stringValue(generic["summary"]))
	switch value := generic["confidence"].(type) {
	case float64:
		parsed.Confidence = value
	case string:
		confidence, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return geminiStageResponse{}, fmt.Errorf("parse gemini confidence: %w", err)
		}
		parsed.Confidence = confidence
	}
	parsed.UpdatedState = rawMessageFromGenericValue(generic["updated_state"])
	parsed.Delta = rawMessageFromGenericValue(generic["delta"])
	parsed.NextNeededEvidence = rawMessageFromGenericValue(generic["next_needed_evidence"])
	parsed.HardConflicts = rawMessageFromGenericValue(generic["hard_conflicts"])
	parsed.FinalOutcome = strings.TrimSpace(stringValue(generic["final_outcome"]))
	if !hasGeminiResponsePayload(parsed) {
		return geminiStageResponse{}, ErrGeminiEmptyResponse
	}
	return parsed, nil
}

func hasGeminiResponsePayload(parsed geminiStageResponse) bool {
	return strings.TrimSpace(parsed.Label) != "" || len(parsed.UpdatedState) > 0 || strings.TrimSpace(parsed.FinalOutcome) != ""
}

func rawMessageFromGenericValue(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return json.RawMessage(body)
}

func normalizeGeminiModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || strings.EqualFold(trimmed, "gemini") {
		return "gemini-2.0-flash"
	}
	return trimmed
}

func validateGeminiMIMEType(mimeType string) error {
	switch strings.TrimSpace(strings.ToLower(mimeType)) {
	case "video/mp4", "video/mpeg", "video/mov", "video/avi", "video/x-flv", "video/mpg", "video/webm", "video/wmv", "video/3gpp", "audio/mpeg", "audio/wav":
		return nil
	default:
		return fmt.Errorf("%w: %s (convert Streamlink .ts chunks to a supported format such as video/mp4 before calling Gemini)", ErrGeminiUnsupportedMIME, mimeType)
	}
}

func marshalRawMessage(value json.RawMessage) string {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	return trimmed
}

func validateGeminiTrackerResponse(stage string, parsed geminiStageResponse) error {
	if !isTrackerStage(stage) {
		return nil
	}
	if len(parsed.UpdatedState) == 0 || strings.TrimSpace(string(parsed.UpdatedState)) == "null" {
		return fmt.Errorf("gemini tracker response for %s must include updated_state", strings.TrimSpace(stage))
	}
	if len(parsed.Delta) == 0 || strings.TrimSpace(string(parsed.Delta)) == "null" {
		return fmt.Errorf("gemini tracker response for %s must include delta", strings.TrimSpace(stage))
	}
	if len(parsed.NextNeededEvidence) == 0 || strings.TrimSpace(string(parsed.NextNeededEvidence)) == "null" {
		return fmt.Errorf("gemini tracker response for %s must include next_needed_evidence", strings.TrimSpace(stage))
	}
	if strings.TrimSpace(parsed.FinalOutcome) != "" {
		switch strings.TrimSpace(parsed.FinalOutcome) {
		case "win", "loss", "draw", "unknown":
		default:
			return fmt.Errorf("gemini tracker response for %s has invalid final_outcome %q", strings.TrimSpace(stage), parsed.FinalOutcome)
		}
	}
	return nil
}

func normalizeGeminiTrackerResponse(input StageRequest, parsed geminiStageResponse) geminiStageResponse {
	if !isTrackerStage(input.Stage) || !isTrackerStartStage(input.Stage) {
		return parsed
	}
	if len(parsed.UpdatedState) == 0 || strings.TrimSpace(string(parsed.UpdatedState)) == "null" {
		fallbackState := strings.TrimSpace(input.PreviousState)
		if fallbackState == "" {
			fallbackState = defaultTrackerState()
		}
		parsed.UpdatedState = json.RawMessage(fallbackState)
	}
	if len(parsed.Delta) == 0 || strings.TrimSpace(string(parsed.Delta)) == "null" {
		parsed.Delta = json.RawMessage("[]")
	}
	if len(parsed.NextNeededEvidence) == 0 || strings.TrimSpace(string(parsed.NextNeededEvidence)) == "null" {
		parsed.NextNeededEvidence = json.RawMessage("[]")
	}
	if strings.TrimSpace(parsed.FinalOutcome) == "" {
		parsed.FinalOutcome = "unknown"
	}
	return parsed
}

func isTrackerStartStage(stage string) bool {
	switch strings.TrimSpace(strings.ToLower(stage)) {
	case trackerStageDiscovery, "start", "discovery":
		return true
	default:
		return false
	}
}
