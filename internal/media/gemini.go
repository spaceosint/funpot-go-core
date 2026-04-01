package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

var (
	ErrGeminiAPIKeyRequired  = errors.New("gemini api key is required")
	ErrGeminiChunkRequired   = errors.New("gemini chunk reference is required")
	ErrGeminiChunkTooLarge   = errors.New("gemini chunk exceeds inline upload limit")
	ErrGeminiEmptyResponse   = errors.New("gemini returned empty response")
	ErrGeminiSchemaRequired  = errors.New("gemini response schema is required")
	ErrGeminiUnsupportedMIME = errors.New("gemini does not support the chunk mime type")
)

const geminiMaxOutputTokensLimit = 8192

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

type GeminiGenerateContentError struct {
	StatusCode      int
	Stage           string
	Model           string
	MIMEType        string
	HasChunk        bool
	MaxOutputTokens int
	Temperature     float64
	Body            string
}

func (e *GeminiGenerateContentError) Error() string {
	if e == nil {
		return "gemini generateContent failed"
	}
	return fmt.Sprintf("gemini generateContent failed: status=%d stage=%s model=%s mime=%s has_chunk=%t max_output_tokens=%d temperature=%.3f body=%s",
		e.StatusCode,
		strings.TrimSpace(e.Stage),
		strings.TrimSpace(e.Model),
		strings.TrimSpace(e.MIMEType),
		e.HasChunk,
		e.MaxOutputTokens,
		e.Temperature,
		strings.TrimSpace(e.Body),
	)
}

func (e *GeminiGenerateContentError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.StatusCode {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
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

func (c *GeminiStageClassifier) Classify(ctx context.Context, input StageRequest) (StageClassification, error) {
	return c.classify(ctx, input, false)
}

func (c *GeminiStageClassifier) classify(ctx context.Context, input StageRequest, forceBootstrap bool) (StageClassification, error) {
	chunkRef := strings.TrimSpace(input.Chunk.Reference)
	hasChunk := chunkRef != ""
	var (
		data     []byte
		mimeType string
		err      error
	)
	if hasChunk {
		data, mimeType, err = loadGeminiChunk(chunkRef, c.maxInlineBytes)
		if err != nil {
			return StageClassification{}, err
		}
		if err := validateGeminiMIMEType(mimeType); err != nil {
			return StageClassification{}, err
		}
	} else if !allowsEmptyChunk(input.Stage) {
		return StageClassification{}, ErrGeminiChunkRequired
	}

	sessionKey := geminiSessionKey(input)
	promptFingerprint := geminiPromptFingerprint(input)
	contents := c.prepareSessionContents(sessionKey, promptFingerprint, input, mimeType, data, forceBootstrap, hasChunk)

	requestBody := geminiGenerateContentRequest{
		Contents:         contents,
		GenerationConfig: sanitizeGeminiGenerationConfig(input.Prompt.Temperature, input.Prompt.MaxTokens),
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
		return StageClassification{}, &GeminiGenerateContentError{
			StatusCode:      resp.StatusCode,
			Stage:           input.Stage,
			Model:           model,
			MIMEType:        mimeType,
			HasChunk:        hasChunk,
			MaxOutputTokens: requestBody.GenerationConfig.MaxOutputTokens,
			Temperature:     requestBody.GenerationConfig.Temperature,
			Body:            string(responseBody),
		}
	}

	var payload geminiGenerateContentResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return StageClassification{}, fmt.Errorf("decode gemini response: %w", err)
	}
	rawText := extractGeminiResponseText(payload)
	if rawText == "" {
		err := describeGeminiEmptyResponse(payload, responseBody)
		return StageClassification{}, fmt.Errorf("%v; stage=%s streamer_id=%s session_key=%s prompt_id=%s force_bootstrap=%t", err, strings.TrimSpace(input.Stage), strings.TrimSpace(input.StreamerID), sessionKey, strings.TrimSpace(input.Prompt.ID), forceBootstrap)
	}

	parsedPayload, err := parseGeminiStageResponse(rawText, input.ResponseSchema)
	if err != nil {
		if errors.Is(err, ErrGeminiEmptyResponse) {
			return StageClassification{}, fmt.Errorf("%v; stage=%s streamer_id=%s session_key=%s prompt_id=%s raw_text=%s", err, strings.TrimSpace(input.Stage), strings.TrimSpace(input.StreamerID), sessionKey, strings.TrimSpace(input.Prompt.ID), strconv.Quote(trimForLog(rawText, 512)))
		}
		return StageClassification{}, err
	}
	c.storeSessionResponse(sessionKey, promptFingerprint, contents, payload, rawText)

	return StageClassification{
		Label:            "state_updated",
		Confidence:       1,
		RawResponse:      strings.TrimSpace(rawText),
		RequestRef:       endpoint,
		ResponseRef:      strconv.Itoa(resp.StatusCode),
		RequestPayload:   sanitizeGeminiRequestPayload(requestBody),
		ResponsePayload:  string(responseBody),
		TokensIn:         payload.UsageMetadata.PromptTokenCount,
		TokensOut:        payload.UsageMetadata.CandidatesTokenCount,
		Latency:          time.Since(started),
		UpdatedStateJSON: parsedPayload,
	}, nil
}

func sanitizeGeminiGenerationConfig(temperature float64, maxTokens int) geminiGenerationConfig {
	cfg := geminiGenerationConfig{
		ResponseMIMEType: "application/json",
	}
	if !math.IsNaN(temperature) && !math.IsInf(temperature, 0) && temperature >= 0 && temperature <= 2 {
		cfg.Temperature = temperature
	}
	if maxTokens > 0 && maxTokens <= geminiMaxOutputTokensLimit {
		cfg.MaxOutputTokens = maxTokens
	}
	return cfg
}

func sanitizeGeminiRequestPayload(request geminiGenerateContentRequest) string {
	safe := geminiGenerateContentRequest{
		GenerationConfig: request.GenerationConfig,
		Contents:         make([]geminiContent, 0, len(request.Contents)),
	}
	for _, content := range request.Contents {
		safeContent := geminiContent{
			Role:  content.Role,
			Parts: make([]geminiPart, 0, len(content.Parts)),
		}
		for _, part := range content.Parts {
			safePart := geminiPart{Text: part.Text}
			if part.InlineData != nil {
				safePart.InlineData = &geminiInlineData{
					MimeType: part.InlineData.MimeType,
					Data:     "video data",
				}
			}
			safeContent.Parts = append(safeContent.Parts, safePart)
		}
		safe.Contents = append(safe.Contents, safeContent)
	}
	payload, err := json.Marshal(safe)
	if err != nil {
		return ""
	}
	return string(payload)
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
		strings.TrimSpace(input.ResponseSchema),
	}, "|")
}

func (c *GeminiStageClassifier) prepareSessionContents(sessionKey, promptFingerprint string, input StageRequest, mimeType string, chunk []byte, forceBootstrap bool, hasChunk bool) []geminiContent {
	userTurn := geminiContent{Role: "user"}
	if hasChunk {
		userTurn.Parts = append(userTurn.Parts, geminiPart{InlineData: &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(chunk)}})
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
Use this admin-managed tracker prompt as the source of truth (including the expected JSON template):
%s
Expected response schema:
%s`
	return strings.TrimSpace(fmt.Sprintf(base+`
Return ONLY valid JSON that matches the admin-managed JSON template from the prompt above.
Do not add keys that are not present in the schema/template.
Never emit narrative commentary outside JSON.`, input.Stage, strings.TrimSpace(input.StreamerID), formatChunkCapturedAt(input.Chunk.CapturedAt), formatChunkReference(input.Chunk.Reference), strings.TrimSpace(input.Prompt.Template), strings.TrimSpace(input.ResponseSchema)))
}

func buildGeminiContinuationInstruction(input StageRequest) string {
	return strings.TrimSpace(fmt.Sprintf(`Continue the existing match chat session.
Stage: %s
Streamer ID: %s
Chunk captured at: %s
Chunk reference: %s
Expected response schema:
%s
Do not repeat full state snapshots from earlier turns.
Use only the expected response schema for this request and keep payload compact.
Return ONLY concrete changes discovered in this chunk when the schema supports delta-style updates.
If there are no concrete changes, return a schema-valid "no changes" response.
Return JSON that matches the admin-managed JSON template exactly.
Return ONLY valid JSON using the same schema as before.`, input.Stage, strings.TrimSpace(input.StreamerID), formatChunkCapturedAt(input.Chunk.CapturedAt), formatChunkReference(input.Chunk.Reference), strings.TrimSpace(input.ResponseSchema)))
}

func allowsEmptyChunk(stage string) bool {
	switch strings.TrimSpace(strings.ToLower(stage)) {
	case trackerStageClose, trackerStageFinalize:
		return true
	default:
		return false
	}
}

func formatChunkReference(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "n/a"
	}
	return strings.TrimSpace(ref)
}

func formatChunkCapturedAt(ts time.Time) string {
	if ts.IsZero() {
		return "n/a"
	}
	return ts.UTC().Format(time.RFC3339Nano)
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

func parseGeminiStageResponse(raw, responseSchema string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return "", ErrGeminiEmptyResponse
	}
	if strings.TrimSpace(responseSchema) == "" {
		return "", ErrGeminiSchemaRequired
	}

	if err := validateGeminiResponseSchema(cleaned, responseSchema); err != nil {
		return "", err
	}

	var payload any
	decoder := json.NewDecoder(strings.NewReader(cleaned))
	if err := decoder.Decode(&payload); err != nil {
		return "", fmt.Errorf("parse gemini stage response: %w", err)
	}
	if decoder.More() {
		return "", fmt.Errorf("parse gemini stage response: unexpected trailing tokens")
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("normalize gemini stage response: %w", err)
	}
	cleaned = strings.TrimSpace(string(normalized))
	if cleaned == "" || cleaned == "null" {
		return "", ErrGeminiEmptyResponse
	}
	return cleaned, nil
}

func validateGeminiResponseSchema(responseRaw, responseSchema string) error {
	schemaRaw := strings.TrimSpace(responseSchema)
	if schemaRaw == "" {
		return nil
	}

	var schemaDoc any
	if err := json.Unmarshal([]byte(schemaRaw), &schemaDoc); err != nil {
		return fmt.Errorf("invalid responseSchemaJson: %w", err)
	}
	normalizedSchema := normalizeResponseSchema(schemaDoc)
	schemaBody, err := json.Marshal(normalizedSchema)
	if err != nil {
		return fmt.Errorf("marshal response schema: %w", err)
	}

	var payload any
	if err := json.Unmarshal([]byte(responseRaw), &payload); err != nil {
		return fmt.Errorf("parse gemini stage response: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	const schemaURL = "inmemory://response-schema.json"
	if err := compiler.AddResource(schemaURL, strings.NewReader(string(schemaBody))); err != nil {
		return fmt.Errorf("load response schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("compile response schema: %w", err)
	}
	if err := compiled.Validate(payload); err != nil {
		return fmt.Errorf("gemini response does not match responseSchemaJson: %w", err)
	}
	return nil
}

func normalizeResponseSchema(schemaDoc any) any {
	object, ok := schemaDoc.(map[string]any)
	if !ok {
		return schemaDoc
	}
	if _, hasType := object["type"]; hasType {
		return schemaDoc
	}
	return templateValueToSchema(schemaDoc)
}

func templateValueToSchema(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		properties := make(map[string]any, len(typed))
		required := make([]string, 0, len(typed))
		for key, item := range typed {
			properties[key] = templateValueToSchema(item)
			required = append(required, key)
		}
		sort.Strings(required)
		return map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		}
	case []any:
		schema := map[string]any{"type": "array"}
		if len(typed) > 0 {
			schema["items"] = templateValueToSchema(typed[0])
		}
		return schema
	case string:
		return map[string]any{"type": "string"}
	case bool:
		return map[string]any{"type": "boolean"}
	case nil:
		return map[string]any{"type": "null"}
	case float64:
		if float64(int64(typed)) == typed {
			return map[string]any{"type": "integer"}
		}
		return map[string]any{"type": "number"}
	default:
		return map[string]any{}
	}
}

func trimForLog(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
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
