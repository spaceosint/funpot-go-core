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
	HTTPClient     *http.Client
}

type GeminiStageClassifier struct {
	apiKey         string
	baseURL        string
	maxInlineBytes int64
	httpClient     *http.Client
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
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &GeminiStageClassifier{
		apiKey:         strings.TrimSpace(cfg.APIKey),
		baseURL:        strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		maxInlineBytes: cfg.MaxInlineBytes,
		httpClient:     cfg.HTTPClient,
	}, nil
}

type geminiGenerateContentRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
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
	BlockReason string `json:"blockReason"`
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

	requestBody := geminiGenerateContentRequest{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{Text: buildGeminiInstruction(input)},
				{InlineData: &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}},
			},
		}},
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
		if payload.PromptFeedback.BlockReason != "" {
			return StageClassification{}, fmt.Errorf("gemini blocked prompt: %s", payload.PromptFeedback.BlockReason)
		}
		return StageClassification{}, ErrGeminiEmptyResponse
	}

	parsed, err := parseGeminiStageResponse(rawText)
	if err != nil {
		return StageClassification{}, err
	}
	if parsed.Confidence < 0 || parsed.Confidence > 1 {
		return StageClassification{}, ErrGeminiInvalidConfidence
	}

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

func buildGeminiInstruction(input StageRequest) string {
	base := `You analyze a livestream chunk for FunPot.
Stage: %s
Use this stage prompt as the source of truth:
%s`
	if strings.TrimSpace(input.PreviousState) == "" {
		return strings.TrimSpace(fmt.Sprintf(base+`
Return ONLY valid JSON with keys: label, confidence, summary.
- label: short snake_case decision for this stage.
- confidence: number between 0 and 1.
- summary: short rationale.`, input.Stage, strings.TrimSpace(input.Prompt.Template)))
	}
	return strings.TrimSpace(fmt.Sprintf(base+`
Previous persisted tracker state JSON:
%s
Return ONLY valid JSON. Update the tracker state using previous_state + new_chunk.
Required keys:
- updated_state: object
- delta: array or object describing evidence changes
- next_needed_evidence: array
Optional keys:
- hard_conflicts: array
- final_outcome: win | loss | draw | unknown
Do not return commentary outside JSON.`, input.Stage, strings.TrimSpace(input.Prompt.Template), strings.TrimSpace(input.PreviousState)))
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

func parseGeminiStageResponse(raw string) (geminiStageResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed geminiStageResponse
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		if strings.TrimSpace(parsed.Label) != "" {
			return parsed, nil
		}
	}

	var generic map[string]any
	if err := json.Unmarshal([]byte(cleaned), &generic); err != nil {
		return geminiStageResponse{}, fmt.Errorf("parse gemini stage response: %w", err)
	}
	parsed.Label = strings.TrimSpace(fmt.Sprint(generic["label"]))
	parsed.Summary = strings.TrimSpace(fmt.Sprint(generic["summary"]))
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
	if parsed.Label == "" {
		return geminiStageResponse{}, ErrGeminiEmptyResponse
	}
	return parsed, nil
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
