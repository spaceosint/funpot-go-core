package media

import "testing"

func TestNormalizeStageALabel(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want StageALabel
	}{
		{name: "exact cs", raw: "cs_detected", want: StageALabelCSDetected},
		{name: "alias cs", raw: "Counter-Strike", want: StageALabelCSDetected},
		{name: "boolean yes", raw: "true", want: StageALabelCSDetected},
		{name: "exact not cs", raw: "not_cs", want: StageALabelNotCS},
		{name: "boolean no", raw: "false", want: StageALabelNotCS},
		{name: "unknown", raw: "maybe", want: StageALabelUncertain},
		{name: "empty", raw: "   ", want: StageALabelUncertain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeStageALabel(tt.raw)
			if got != tt.want {
				t.Fatalf("NormalizeStageALabel(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
