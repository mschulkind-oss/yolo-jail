package depcheck

import (
	"errors"
	"strings"
	"testing"
)

// Check probes each requirement, picks the detected manager's hint for a missing bin,
// and never claims a missing-with-no-hint as satisfied.
func TestCheck(t *testing.T) {
	// Deterministic seams: only "have" is present; manager is brew.
	orig, origM := LookPath, DetectManager
	t.Cleanup(func() { LookPath, DetectManager = orig, origM })
	DetectManager = func() string { return "brew" }
	LookPath = func(bin string) (string, error) {
		if bin == "have" {
			return "/opt/homebrew/bin/have", nil
		}
		return "", errors.New("not found")
	}

	reqs := []Requirement{
		{Bin: "have", Hints: map[string]string{"brew": "have-pkg"}},
		{Bin: "want", Hints: map[string]string{"brew": "want-pkg", "apt": "want-apt"}},
		{Bin: "nohint"}, // missing, no remedy
	}
	res := Check(reqs)

	byBin := map[string]Result{}
	for _, r := range res {
		byBin[r.Bin] = r
	}
	if !byBin["have"].Present || byBin["have"].Path == "" {
		t.Errorf("have should be present with a path: %+v", byBin["have"])
	}
	if byBin["want"].Present {
		t.Error("want should be missing")
	}
	if byBin["want"].Remedy != "brew install want-pkg" {
		t.Errorf("want remedy should use the brew hint, got %q", byBin["want"].Remedy)
	}
	if byBin["nohint"].Present || byBin["nohint"].Remedy != "" {
		t.Errorf("nohint should be missing with NO remedy (never claimed satisfied): %+v", byBin["nohint"])
	}

	// Missing = the two absent ones (with and without a remedy).
	if got := len(Missing(res)); got != 2 {
		t.Errorf("Missing() = %d, want 2", got)
	}
}

// Manifest emits the manager's own bundle for the missing-with-remedy set.
func TestManifestBrewfile(t *testing.T) {
	res := []Result{
		{Bin: "have", Present: true, Manager: "brew"},
		{Bin: "redis", Manager: "brew", Remedy: "brew install redis"},
		{Bin: "psql", Manager: "brew", Remedy: "brew install postgresql@16"},
	}
	name, body := Manifest(res)
	if name != "Brewfile" {
		t.Fatalf("brew manager should yield a Brewfile, got %q", name)
	}
	// Only the missing ones, one brew "<pkg>" line each, sorted.
	if !strings.Contains(body, `brew "postgresql@16"`) || !strings.Contains(body, `brew "redis"`) {
		t.Errorf("Brewfile missing entries:\n%s", body)
	}
	if strings.Contains(body, "have") {
		t.Errorf("Brewfile should not list a present binary:\n%s", body)
	}

	// apt manager yields a plain package list, not a Brewfile.
	apt := []Result{{Bin: "x", Manager: "apt", Remedy: "sudo apt install -y x-pkg"}}
	n2, b2 := Manifest(apt)
	if n2 != "apt-packages.txt" || !strings.Contains(b2, "x-pkg") {
		t.Errorf("apt manifest wrong: %q / %q", n2, b2)
	}

	// Nothing missing → no manifest.
	if n, b := Manifest([]Result{{Bin: "have", Present: true}}); n != "" || b != "" {
		t.Errorf("no missing deps should yield no manifest, got %q/%q", n, b)
	}
}
