package streamers

type Streamer struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Online      bool   `json:"online"`
	Viewers     int    `json:"viewers"`
	AddedBy     string `json:"addedBy"`
	Status      string `json:"status"`
}

type Submission struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

type LLMDecision struct {
	ID              string  `json:"id"`
	RunID           string  `json:"runId"`
	StreamerID      string  `json:"streamerId"`
	Stage           string  `json:"stage"`
	Label           string  `json:"label"`
	Confidence      float64 `json:"confidence"`
	PromptVersionID string  `json:"promptVersionId,omitempty"`
	PromptText      string  `json:"promptText,omitempty"`
	Model           string  `json:"model,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	MaxTokens       int     `json:"maxTokens,omitempty"`
	TimeoutMS       int     `json:"timeoutMs,omitempty"`
	ChunkRef        string  `json:"chunkRef,omitempty"`
	RawResponse     string  `json:"rawResponse,omitempty"`
	TokensIn        int     `json:"tokensIn,omitempty"`
	TokensOut       int     `json:"tokensOut,omitempty"`
	LatencyMS       int64   `json:"latencyMs,omitempty"`
	CreatedAt       string  `json:"createdAt"`
}

type LLMStatus struct {
	StreamerID        string        `json:"streamerId"`
	State             string        `json:"state"`
	CurrentRunID      string        `json:"currentRunId,omitempty"`
	CurrentStage      string        `json:"currentStage,omitempty"`
	CurrentLabel      string        `json:"currentLabel,omitempty"`
	CurrentConfidence float64       `json:"currentConfidence,omitempty"`
	DetectedGameKey   string        `json:"detectedGameKey,omitempty"`
	UpdatedAt         string        `json:"updatedAt,omitempty"`
	LatestByStage     []LLMDecision `json:"latestByStage"`
}

type RecordDecisionRequest struct {
	RunID           string
	StreamerID      string
	Stage           string
	Label           string
	Confidence      float64
	PromptVersionID string
	PromptText      string
	Model           string
	Temperature     float64
	MaxTokens       int
	TimeoutMS       int
	ChunkRef        string
	RawResponse     string
	TokensIn        int
	TokensOut       int
	LatencyMS       int64
}
