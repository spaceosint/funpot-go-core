package media

import "strings"

const (
	StageALabelCSDetected StageALabel = "cs_detected"
	StageALabelNotCS      StageALabel = "not_cs"
	StageALabelUncertain  StageALabel = "uncertain"
)

type StageALabel string

// NormalizeStageALabel maps classifier output into a strict stage A enum.
func NormalizeStageALabel(raw string) StageALabel {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case string(StageALabelCSDetected), "cs", "counter-strike", "counter strike", "yes", "true":
		return StageALabelCSDetected
	case string(StageALabelNotCS), "not-cs", "no", "false":
		return StageALabelNotCS
	default:
		return StageALabelUncertain
	}
}
