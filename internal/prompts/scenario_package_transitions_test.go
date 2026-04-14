package prompts

import "testing"

func TestScenarioPackageResolveNextPackage(t *testing.T) {
	t.Parallel()

	pkg := ScenarioPackage{
		ID: "pkg-root",
		PackageTransitions: []ScenarioPackageTransition{
			{ToPackageID: "pkg-cs2", Condition: `game == "cs2"`, Priority: 10},
			{ToPackageID: "pkg-dota2", Condition: `game == "dota2"`, Priority: 5},
		},
	}

	next, changed, err := pkg.ResolveNextPackage(`{"game":"cs2"}`)
	if err != nil {
		t.Fatalf("ResolveNextPackage() error = %v", err)
	}
	if !changed || next != "pkg-cs2" {
		t.Fatalf("ResolveNextPackage() = (%q,%v), want (pkg-cs2,true)", next, changed)
	}

	next, changed, err = pkg.ResolveNextPackage(`{"game":"valorant"}`)
	if err != nil {
		t.Fatalf("ResolveNextPackage() no-match error = %v", err)
	}
	if changed || next != "pkg-root" {
		t.Fatalf("ResolveNextPackage() no-match = (%q,%v), want (pkg-root,false)", next, changed)
	}
}
