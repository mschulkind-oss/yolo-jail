package depcheck

import (
	"strings"
	"testing"
)

// prefer_test.go covers the user's provisioner order (Requirement.Prefer,
// docs/design/provisioner-sets.md PS-D11): a re-ranking of the recipes a pack declares, walked
// first, with yolo's default order after it.

// claudeReq is a program with its own installer and a cask hint, as packs/claude declares it.
func claudeReq(prefer ...string) Requirement {
	return Requirement{Bin: "claude", SelfInstall: "install-claude", SelfInstallNoTerminal: true,
		Hints: map[string]string{"brew-cask": "claude-code", "nix": "claude-code"}, Prefer: prefer}
}

// TestAUserOrderRanksAManagerAboveThePacksInstaller: "claude from brew". With brew ranked first
// and on the PATH, brew's cask is the remedy, the pack's installer is the alternative, the
// remedy keeps its terminal (a manager's sudo asks there), and the result says the user's order
// chose it. With no order the pack's installer leads, as it always has.
func TestAUserOrderRanksAManagerAboveThePacksInstaller(t *testing.T) {
	r := Check([]Requirement{claudeReq("brew")}, only("brew"))[0]
	if r.Remedy != "brew install --cask claude-code" || r.Via != "brew" || r.Manager != "brew" {
		t.Errorf("brew ranked first: remedy %q via %q manager %q, want brew's cask", r.Remedy, r.Via, r.Manager)
	}
	if r.Fallback != "install-claude" || r.FallbackVia != Pack || r.AltLabel() != "the pack's own installer" {
		t.Errorf("brew ranked first: fallback %q (%q, %q), want the pack's installer", r.Fallback,
			r.FallbackVia, r.AltLabel())
	}
	if r.NoTerminal {
		t.Error("a brew remedy must keep the terminal: NoTerminal is the pack's installer's alone")
	}
	if !r.Ranked {
		t.Error("Ranked = false: the user's order chose brew over the default's pack installer")
	}

	def := Check([]Requirement{claudeReq()}, only("brew"))[0]
	if def.Remedy != "install-claude" || def.Via != Pack || def.Ranked || !def.NoTerminal {
		t.Errorf("no order: %+v, want the pack's own installer, not ranked, with no terminal", def)
	}
	if def.Fallback != "brew install --cask claude-code" || def.AltLabel() != "via brew" {
		t.Errorf("no order: fallback %q (%q), want brew's cask via brew", def.Fallback, def.AltLabel())
	}
}

// TestAnOrderSkipsWhatThisHostLacks: ordered, first match. A manager the PATH lacks, or one the
// pack hints nothing for, is skipped rather than fatal, and the default order still follows the
// user's list, so naming brew leaves an npm-only program its own installer.
func TestAnOrderSkipsWhatThisHostLacks(t *testing.T) {
	// brew is not on the PATH: nix, ranked second, wins.
	if r := Check([]Requirement{claudeReq("brew", "nix")}, only("nix"))[0]; r.Via != "nix" ||
		r.Remedy != "nix profile install nixpkgs#claude-code" {
		t.Errorf("brew absent, nix second: %+v, want nix's remedy", r)
	}
	// Nothing ranked is here: the default order, and Ranked says the order changed nothing.
	if r := Check([]Requirement{claudeReq("pacman")}, only("brew"))[0]; r.Via != Pack || r.Ranked {
		t.Errorf("pacman absent: %+v, want the pack's installer, not ranked", r)
	}
	// A program with no brew hint keeps its own installer under a brew-first order.
	pi := Requirement{Bin: "pi", SelfInstall: "npm install -g pi", Prefer: []string{"brew"}}
	if r := Check([]Requirement{pi}, only("brew"))[0]; r.Remedy != "npm install -g pi" || r.Ranked {
		t.Errorf("npm-only program under brew-first: %+v, want its own installer", r)
	}
	// A manager ranked above the DETECTED one wins although detection would pick the other: the
	// order replaces detectManager's answer for the binaries it covers.
	fd := Requirement{Bin: "fd", Hints: map[string]string{"apt": "fd-find", "nix": "fd"}, Prefer: []string{"nix"}}
	r := Check([]Requirement{fd}, only("apt", "nix"))[0]
	if r.Remedy != "nix profile install nixpkgs#fd" || r.Manager != "nix" || !r.Ranked {
		t.Errorf("nix ranked over the detected apt: %+v, want nix's remedy, ranked", r)
	}
	if r.Fallback != "" {
		t.Errorf("a manager remedy with no pack installer has no fallback, got %q", r.Fallback)
	}
}

// TestABundleHoldsOneManager: an order can resolve two binaries to two managers, and the bundle
// holds the first manager's tokens alone; the other's command is printed beside it (Unbundled),
// so the file, its install command and the list of what it leaves out still agree.
func TestABundleHoldsOneManager(t *testing.T) {
	res := Check([]Requirement{
		{Bin: "aaa", Hints: map[string]string{"brew": "aaa"}, Prefer: []string{"brew"}},
		{Bin: "bbb", Hints: map[string]string{"apt": "bbb"}},
	}, only("apt", "brew"))
	name, body := Manifest(res)
	if name != "Brewfile" || body != "brew \"aaa\"\n" {
		t.Errorf("Manifest = %q %q, want a Brewfile of aaa alone", name, body)
	}
	if cmd := BundleInstall(res, "/b"); !strings.HasPrefix(cmd, "brew bundle") {
		t.Errorf("BundleInstall = %q, want brew's", cmd)
	}
	left := Unbundled(res)
	if len(left) != 1 || left[0].Bin != "bbb" || left[0].Remedy != "sudo apt install -y bbb" {
		t.Errorf("Unbundled = %+v, want bbb's apt command", left)
	}
}

// TestWinnerIsChecksAnswer: Winner, which the host floor asks of a program it may hold, names
// the provisioner Check's remedy comes from, present binary or not.
func TestWinnerIsChecksAnswer(t *testing.T) {
	for _, tc := range []struct {
		prefer []string
		path   []string
		want   string
	}{
		{nil, []string{"brew"}, Pack},
		{[]string{"brew"}, []string{"brew"}, "brew"},
		{[]string{"brew"}, nil, Pack},
		{[]string{"nix", "brew"}, []string{"brew", "nix"}, "nix"},
	} {
		got := Winner(claudeReq(tc.prefer...), only(append(tc.path, "claude")...))
		if got != tc.want {
			t.Errorf("Winner(prefer %v, PATH %v) = %q, want %q", tc.prefer, tc.path, got, tc.want)
		}
	}
	if got := Winner(Requirement{Bin: "x"}, only()); got != "" {
		t.Errorf("Winner with no recipe = %q, want \"\"", got)
	}
}

// TestProvisionersNameThePackAndEveryManager: the vocabulary an order is written in.
func TestProvisionersNameThePackAndEveryManager(t *testing.T) {
	if got := strings.Join(Provisioners(), " "); got != "pack apt dnf pacman brew nix" {
		t.Errorf("Provisioners() = %q", got)
	}
	if !IsProvisioner("brew") || IsProvisioner("brew-cask") || IsProvisioner("npm") {
		t.Error("IsProvisioner: brew is one; brew-cask is a hint flavor and npm is the pack's recipe, not provisioners")
	}
}
