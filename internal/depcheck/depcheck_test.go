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

// A brew-cask hint wins over a plain brew hint on a brew host, produces the --cask install
// command, and lands as a `cask "<token>"` Brewfile line — `brew bundle` on a `brew` line
// naming a cask token fails looking for a formula that does not exist.
func TestBrewCaskHint(t *testing.T) {
	orig, origM := LookPath, DetectManager
	t.Cleanup(func() { LookPath, DetectManager = orig, origM })
	DetectManager = func() string { return "brew" }
	LookPath = func(string) (string, error) { return "", errors.New("not found") }

	res := Check([]Requirement{
		// A pure cask.
		{Bin: "claude", Hints: map[string]string{"brew-cask": "claude-code", "nix": "claude-code"}},
		// Both declared: the cask wins, because a same-named formula is the wrong-package
		// trap (brew's `copilot` formula is AWS's ECS CLI).
		{Bin: "copilot", Hints: map[string]string{"brew-cask": "copilot-cli", "brew": "copilot"}},
		// A real formula stays a formula.
		{Bin: "psql", Hints: map[string]string{"brew": "postgresql@16"}},
	})
	byBin := map[string]Result{}
	for _, r := range res {
		byBin[r.Bin] = r
	}
	if got := byBin["claude"].Remedy; got != "brew install --cask claude-code" {
		t.Errorf("cask remedy = %q, want the --cask form", got)
	}
	if got := byBin["copilot"].Remedy; got != "brew install --cask copilot-cli" {
		t.Errorf("brew-cask must win over brew, got %q", got)
	}
	if got := byBin["psql"].Remedy; got != "brew install postgresql@16" {
		t.Errorf("formula remedy = %q, want the plain form", got)
	}
	// Manager stays plain "brew" — the flavor is a property of the package, not the host.
	if byBin["claude"].Manager != "brew" || byBin["claude"].Flavor != "brew-cask" {
		t.Errorf("claude manager/flavor = %q/%q, want brew/brew-cask",
			byBin["claude"].Manager, byBin["claude"].Flavor)
	}
	if byBin["psql"].Flavor != "brew" {
		t.Errorf("psql flavor = %q, want brew", byBin["psql"].Flavor)
	}

	// One Brewfile carries both verbs: formulae first, then casks.
	name, body := Manifest(res)
	if name != "Brewfile" {
		t.Fatalf("manifest name = %q, want Brewfile", name)
	}
	want := "brew \"postgresql@16\"\ncask \"claude-code\"\ncask \"copilot-cli\"\n"
	if body != want {
		t.Errorf("Brewfile =\n%s\nwant\n%s", body, want)
	}

	// A non-brew host cannot select a brew-cask hint at all: nothing covers it, so the
	// result is missing-with-no-remedy rather than a bogus `apt install claude-code`.
	DetectManager = func() string { return "apt" }
	apt := Check([]Requirement{{Bin: "claude", Hints: map[string]string{"brew-cask": "claude-code"}}})
	if apt[0].Remedy != "" || apt[0].Flavor != "" {
		t.Errorf("brew-cask must not be selected for apt, got %+v", apt[0])
	}
	if n, b := Manifest(apt); n != "" || b != "" {
		t.Errorf("no remedy → no manifest, got %q/%q", n, b)
	}
}
