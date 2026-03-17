package media

import (
	"context"
	"fmt"
	"time"
)

// StreamlinkCaptureAdapter is a placeholder adapter that returns capture references.
// Real Streamlink process integration can replace this implementation behind StreamCapture.
type StreamlinkCaptureAdapter struct{}

func (a StreamlinkCaptureAdapter) Capture(_ context.Context, streamerID string) (ChunkRef, error) {
	return ChunkRef{Reference: fmt.Sprintf("streamlink://%s/%d", streamerID, time.Now().UTC().UnixNano())}, nil
}

// PromptedStageAClassifier is a deterministic baseline classifier that accepts
// stage prompt context and returns a normalized guess until Gemini integration is wired.
type PromptedStageAClassifier struct{}

func (c PromptedStageAClassifier) Classify(_ context.Context, input StageARequest) (StageAClassification, error) {
	label := string(StageALabelCSDetected)
	if input.Prompt.Template == "" {
		label = string(StageALabelUncertain)
	}
	return StageAClassification{Label: label, Confidence: 0.75}, nil
}
