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
	DetectManager = func(Lookup) string { return "brew" }
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
	res := Check(reqs, nil)

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

// The declaring pack's OWN installer beats a package-manager hint, and the hint survives as
// the Fallback rather than being discarded.
//
// The reason for the order is staleness, measured rather than assumed: a tool that ships its
// own installer ships its own updater, while a distro package pins whatever that repo has —
// nixpkgs was current for claude-code/codex/pi-coding-agent and 16 releases behind for
// github-copilot-cli (1.0.61 vs 1.0.77), with nothing in the packaging to say which.
func TestSelfInstallBeatsAManagerHint(t *testing.T) {
	orig, origM := LookPath, DetectManager
	t.Cleanup(func() { LookPath, DetectManager = orig, origM })
	DetectManager = func(Lookup) string { return "brew" }
	LookPath = func(string) (string, error) { return "", errors.New("not found") }

	res := Check([]Requirement{
		// Both a self-installer and a cask hint: the installer leads, the cask is Fallback.
		{Bin: "claude", SelfInstall: "curl -fsSL https://claude.ai/install.sh | sh",
			Hints: map[string]string{"brew-cask": "claude-code"}},
		// A self-installer with NO hint: no fallback to report.
		{Bin: "solo", SelfInstall: "npm install -g solo-pkg"},
		// No self-installer (a `requires`): the hint is the remedy, as before.
		{Bin: "fzf", Hints: map[string]string{"brew": "fzf"}},
	}, nil)
	byBin := map[string]Result{}
	for _, r := range res {
		byBin[r.Bin] = r
	}

	claude := byBin["claude"]
	if claude.Remedy != "curl -fsSL https://claude.ai/install.sh | sh" {
		t.Errorf("the pack's own installer must be the primary remedy, got %q", claude.Remedy)
	}
	if claude.Flavor != selfInstallFlavor {
		t.Errorf("flavor = %q, want %q so Manifest knows to keep it out of the bundle",
			claude.Flavor, selfInstallFlavor)
	}
	if claude.Fallback != "brew install --cask claude-code" {
		t.Errorf("the manager hint must survive as the fallback (with its verb), got %q", claude.Fallback)
	}
	if solo := byBin["solo"]; solo.Remedy != "npm install -g solo-pkg" || solo.Fallback != "" {
		t.Errorf("a self-installer with no hint has no fallback: %+v", solo)
	}
	if fzf := byBin["fzf"]; fzf.Remedy != "brew install fzf" || fzf.Flavor != "brew" {
		t.Errorf("with no self-installer the hint is still the remedy: %+v", fzf)
	}

	// The BUNDLE carries neither the curl line nor claude's cask fallback: there is no way to
	// spell a curl-to-shell in a Brewfile, and the cask is the copy the remedy steers away from,
	// since the tool's own updater keeps the installer's copy current. Both self-installed deps
	// are left for the caller to print beside the bundle (Unbundled).
	name, body := Manifest(res)
	if name != "Brewfile" {
		t.Fatalf("manifest name = %q, want Brewfile", name)
	}
	if want := "brew \"fzf\"\n"; body != want {
		t.Errorf("Brewfile =\n%s\nwant\n%s (fzf alone: claude and solo have their own installers)", body, want)
	}
	var left []string
	for _, r := range Unbundled(res) {
		left = append(left, r.Bin)
	}
	if strings.Join(left, " ") != "claude solo" {
		t.Errorf("Unbundled = %v, want claude and solo, whose own installers are printed beside the bundle", left)
	}
}

// A brew-cask hint wins over a plain brew hint on a brew host, produces the --cask install
// command, and lands as a `cask "<token>"` Brewfile line — `brew bundle` on a `brew` line
// naming a cask token fails looking for a formula that does not exist.
func TestBrewCaskHint(t *testing.T) {
	orig, origM := LookPath, DetectManager
	t.Cleanup(func() { LookPath, DetectManager = orig, origM })
	DetectManager = func(Lookup) string { return "brew" }
	LookPath = func(string) (string, error) { return "", errors.New("not found") }

	res := Check([]Requirement{
		// A pure cask.
		{Bin: "claude", Hints: map[string]string{"brew-cask": "claude-code", "nix": "claude-code"}},
		// Both declared: the cask wins, because a same-named formula is the wrong-package
		// trap (brew's `copilot` formula is AWS's ECS CLI).
		{Bin: "copilot", Hints: map[string]string{"brew-cask": "copilot-cli", "brew": "copilot"}},
		// A real formula stays a formula.
		{Bin: "psql", Hints: map[string]string{"brew": "postgresql@16"}},
	}, nil)
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
	DetectManager = func(Lookup) string { return "apt" }
	apt := Check([]Requirement{{Bin: "claude", Hints: map[string]string{"brew-cask": "claude-code"}}}, nil)
	if apt[0].Remedy != "" || apt[0].Flavor != "" {
		t.Errorf("brew-cask must not be selected for apt, got %+v", apt[0])
	}
	if n, b := Manifest(apt); n != "" || b != "" {
		t.Errorf("no remedy → no manifest, got %q/%q", n, b)
	}
}

// TestPresentReprobesThroughTheSeam pins the RE-PROBE half of an install: Present answers
// "is it there NOW", through the same LookPath seam Check uses.
//
// The seam is the assertion, not a detail of the test. `yolo host apply`'s dependency gate
// re-probes after running a remedy and refuses when the binary still is not there
// (docs/reference/report-tiers.md's dependency rule, point 5), so a Present that reached
// exec.LookPath directly would be a second opinion about one PATH — with the first one stubbed
// and the second one reading the developer's real machine.
func TestPresentReprobesThroughTheSeam(t *testing.T) {
	real := LookPath
	t.Cleanup(func() { LookPath = real })

	asked := 0
	LookPath = func(bin string) (string, error) {
		asked++
		if bin == "there" {
			return "/stub/there", nil
		}
		return "", errors.New("not found")
	}
	if p, ok := Present("there", nil); !ok || p != "/stub/there" {
		t.Errorf("Present(there) = %q/%v, want the seam's path", p, ok)
	}
	if p, ok := Present("gone", nil); ok || p != "" {
		t.Errorf("Present(gone) = %q/%v, want not found", p, ok)
	}
	if asked != 2 {
		t.Errorf("the seam was consulted %d times, want 2 — Present probed something else", asked)
	}
}

// only is a Lookup that finds exactly the named binaries, at /launch/path/<name>.
func only(have ...string) Lookup {
	return func(bin string) (string, error) {
		for _, h := range have {
			if h == bin {
				return "/launch/path/" + bin, nil
			}
		}
		return "", errors.New("not found")
	}
}

// TestEveryProbeReadsTheCallersLookup: the presence probe, the package-manager guess and the
// re-probe all go through the lookup the caller hands them (host-agent-environment.md's one
// resolver: the dependency probe and the package-manager guess), never LookPath or a bare exec.LookPath beside it. A
// manager only the lookup can see names the remedy; with a lookup that sees none, no manager is
// named, whatever the machine's own PATH holds.
func TestEveryProbeReadsTheCallersLookup(t *testing.T) {
	real := LookPath
	t.Cleanup(func() { LookPath = real })
	LookPath = func(bin string) (string, error) {
		t.Errorf("LookPath(%q) consulted although the caller passed a lookup", bin)
		return "", errors.New("not found")
	}
	req := []Requirement{{Bin: "tool", Hints: map[string]string{"pacman": "tool-pkg", "nix": "tool-nix"}},
		{Bin: "have", Hints: map[string]string{"pacman": "have-pkg"}}}
	res := Check(req, only("pacman", "have"))
	if res[0].Bin != "have" || !res[0].Present || res[0].Path != "/launch/path/have" {
		t.Errorf("have = %+v, want present at the lookup's path", res[0])
	}
	if res[1].Manager != "pacman" || res[1].Remedy != "sudo pacman -S --noconfirm tool-pkg" {
		t.Errorf("tool = %+v, want pacman's remedy: pacman is on the lookup's PATH", res[1])
	}
	if res := Check(req, only()); res[1].Manager != "" || res[1].Remedy != "" {
		t.Errorf("with a lookup that sees no manager, manager/remedy = %q/%q, want none",
			res[1].Manager, res[1].Remedy)
	}
	if p, ok := Present("have", only("have")); !ok || p != "/launch/path/have" {
		t.Errorf("Present = %q/%v, want the lookup's answer", p, ok)
	}
}

// TestNixIsOfferedOnlyWhereTheLookupFindsIt: nix is probed like every other manager, never reached
// by elimination (docs/design/provisioner-sets.md, OQ-PS9's answer). A lookup that finds no
// manager names none, so a host without nix is never told to run `nix profile install` while
// `yolo check` reports nix missing; the binary is still missing, with no remedy and no bundle. A
// lookup that finds nix, and no manager ahead of it, names nix's remedy as before.
func TestNixIsOfferedOnlyWhereTheLookupFindsIt(t *testing.T) {
	req := []Requirement{
		{Bin: "tool", Hints: map[string]string{"nix": "tool-nix"}},
		// The pack's own installer needs no package manager: it still leads, with no
		// package-manager fallback to name.
		{Bin: "vendored", SelfInstall: "npm install -g vendored", Hints: map[string]string{"nix": "vendored"}},
	}

	none := Check(req, only())
	if r := none[0]; r.Manager != "" || r.Remedy != "" || r.Flavor != "" || !r.Hinted {
		t.Errorf("no manager on the lookup: tool = %+v, want no manager, no remedy, and hinted", r)
	}
	if r := Check([]Requirement{{Bin: "bare"}}, only())[0]; r.Hinted {
		t.Errorf("a requirement with no hint reported Hinted: %+v", r)
	}
	if r := none[1]; r.Remedy != "npm install -g vendored" || r.Fallback != "" {
		t.Errorf("no manager on the lookup: vendored = %+v, want its own installer and no fallback", r)
	}
	if got := len(Missing(none)); got != 2 {
		t.Errorf("Missing() = %d, want 2: a binary with no remedy is still missing", got)
	}
	if n, b := Manifest(none); n != "" || b != "" {
		t.Errorf("no manager → no bundle, got %q/%q", n, b)
	}

	withNix := Check(req, only("nix"))
	if r := withNix[0]; r.Manager != "nix" || r.Remedy != "nix profile install nixpkgs#tool-nix" {
		t.Errorf("nix on the lookup: tool = %+v, want nix's remedy", r)
	}
	if r := withNix[1]; r.Fallback != "nix profile install nixpkgs#vendored" {
		t.Errorf("nix on the lookup: vendored fallback = %q, want nix's", r.Fallback)
	}
	// Last, not first: a manager ahead of nix in the probe order still wins.
	if r := Check(req, only("nix", "apt"))[0]; r.Manager != "apt" {
		t.Errorf("apt and nix on the lookup: manager = %q, want apt", r.Manager)
	}
}
