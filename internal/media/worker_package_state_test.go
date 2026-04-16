package media

import "testing"

func TestEnrichScenarioState(t *testing.T) {
	t.Parallel()

	got := enrichScenarioState(`{"game":"cs2"}`, `{"state":{"round":3}}`, "pkg-2", "step-a", map[string]any{
		"status": "accepted",
	})
	if got == "" {
		t.Fatalf("enrichScenarioState() returned empty state")
	}
	pkgID := scenarioStatePackageID(got)
	if pkgID != "pkg-2" {
		t.Fatalf("scenarioStatePackageID() = %q, want pkg-2", pkgID)
	}
}
