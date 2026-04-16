package prompts

import "testing"

func TestScenarioPackageResolveNextPackage(t *testing.T) {
	t.Parallel()

	pkg := ScenarioPackage{
		ID: "pkg-root",
		PackageTransitions: []ScenarioPackageTransition{
			{ToPackageID: "pkg-cs2", Priority: 10},
		},
	}

	resolution, err := pkg.ResolveNextPackage(`{"game":"cs2"}`)
	if err != nil {
		t.Fatalf("ResolveNextPackage() error = %v", err)
	}
	if resolution.StopTracking {
		t.Fatalf("ResolveNextPackage() stop=%v, want false", resolution.StopTracking)
	}
	if !resolution.Changed || resolution.PackageID != "pkg-cs2" {
		t.Fatalf("ResolveNextPackage() = (%q,%v), want (pkg-cs2,true)", resolution.PackageID, resolution.Changed)
	}

	resolution, err = pkg.ResolveNextPackage(`{"game":"valorant"}`)
	if err != nil {
		t.Fatalf("ResolveNextPackage() fallback error = %v", err)
	}
	if resolution.StopTracking {
		t.Fatalf("ResolveNextPackage() fallback stop=%v, want false", resolution.StopTracking)
	}
	if !resolution.Changed || resolution.PackageID != "pkg-cs2" {
		t.Fatalf("ResolveNextPackage() fallback = (%q,%v), want (pkg-cs2,true)", resolution.PackageID, resolution.Changed)
	}
}

func TestScenarioPackageResolveNextPackageStopTracking(t *testing.T) {
	t.Parallel()

	pkg := ScenarioPackage{
		ID: "pkg-root",
		FinalStateOptions: []ScenarioFinalStateOption{
			{ID: "ct_win", Name: "CT win", Condition: `outcome == "ct_win"`, FinalStateJSON: `{"result":"win"}`, FinalLabel: "ct_win"},
		},
		PackageTransitions: []ScenarioPackageTransition{
			{Priority: 1, Action: ScenarioPackageTransitionActionStopTracking, FinalStateOptionID: "ct_win"},
		},
		FinalCondition: `outcome == "ct_win"`,
	}

	resolution, err := pkg.ResolveNextPackage(`{"outcome":"ct_win"}`)
	if err != nil {
		t.Fatalf("ResolveNextPackage() error = %v", err)
	}
	if !resolution.StopTracking {
		t.Fatalf("ResolveNextPackage() stop=%v, want true", resolution.StopTracking)
	}
	if resolution.Changed {
		t.Fatalf("ResolveNextPackage() changed=%v, want false", resolution.Changed)
	}
	if resolution.PackageID != "pkg-root" {
		t.Fatalf("ResolveNextPackage() next=%q, want pkg-root", resolution.PackageID)
	}
	if resolution.FinalStateJSON != `{"result":"win"}` {
		t.Fatalf("ResolveNextPackage() final state=%q, want %q", resolution.FinalStateJSON, `{"result":"win"}`)
	}
	if resolution.FinalLabel != "ct_win" {
		t.Fatalf("ResolveNextPackage() final label=%q, want ct_win", resolution.FinalLabel)
	}

	resolution, err = pkg.ResolveNextPackage(`{"outcome":"t_win"}`)
	if err != nil {
		t.Fatalf("ResolveNextPackage() final condition mismatch error = %v", err)
	}
	if resolution.StopTracking {
		t.Fatalf("ResolveNextPackage() final condition mismatch stop=%v, want false", resolution.StopTracking)
	}
}
