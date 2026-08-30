package cli

// applyhostdeps_test.go pins Phase 8's contract: `yolo host apply` reports the REAL state of a
// pack's host deps. The three cases below are the three outcomes, and the third is the one
// worth having a test for — a missing bin with no install_hints must still be named. The
// tempting implementation (iterate depcheck.Missing, print the remedies) silently caps the
// report at the deps yolo happens to know how to fix.
//
// Determinism: PATH is replaced by a temp dir holding exactly the fake binaries a case
// wants, so both the bin probe AND depcheck's package-manager detection (which probes PATH
// for apt/dnf/pacman/brew) resolve from the test's own fixture rather than the machine's.
// The depcheck seams (LookPath/DetectManager) are deliberately NOT stubbed here: stubbing
// them would test this file's formatting while leaving the reuse of check-deps' probe —
// the actual Phase 8 requirement — unexercised.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
)

// TestApplyHostReportsPresentDep: a declared bin that is on PATH reports present, with the
// resolved path — not the old static "confirm-gated" line.
func TestApplyHostReportsPresentDep(t *testing.T) {
	binDir := fakeBinDir(t, "presentbin")
	report := runApplyHostForDeps(t, `{"kind":"program","bin":"presentbin","via":"npm",`+
		`"package":"p","install_hints":{"apt":"present-pkg","nix":"present-pkg"}}`)

	if !strings.Contains(report, "presentbin") {
		t.Fatalf("the declared bin is not named in the output:\n%s", report)
	}
	if !strings.Contains(report, "present at "+filepath.Join(binDir, "presentbin")) {
		t.Errorf("a bin on PATH should report present with its resolved path:\n%s", report)
	}
	if strings.Contains(report, "MISSING") {
		t.Errorf("a present bin must not be reported missing:\n%s", report)
	}
	// The install deferral note is only interesting when something is missing.
	if strings.Contains(report, "Phase 4.3") {
		t.Errorf("nothing is missing, so there is no install to defer:\n%s", report)
	}
}

// TestApplyHostReportsMissingDepWithRemedy: a missing `requires` bin reports the
// install_hints remedy for the DETECTED host manager (here apt, the only manager on the
// fixture PATH), and says that apply does not run it.
//
// A `requires` rather than a `program`, deliberately: since the pack's own installer became
// the PREFERRED remedy (item #6), a program with a `via` never reaches its hints, so a
// program here would be testing the self-install path under the wrong name. `requires`
// installs nothing by definition, which makes it the kind whose hints are load-bearing.
func TestApplyHostReportsMissingDepWithRemedy(t *testing.T) {
	fakeBinDir(t, "apt") // the detected manager; the declared bin is deliberately absent
	report := runApplyHostForDeps(t, `{"kind":"requires","bin":"missingbin",`+
		`"install_hints":{"apt":"missing-apt-pkg","brew":"missing-brew-pkg"}}`)

	// The remedy must be the shared checker's own, not a second rendering of it — compare
	// against depcheck for the same requirement, so a divergent formatter fails here.
	want := depcheck.Check([]depcheck.Requirement{{Bin: "missingbin",
		Hints: map[string]string{"apt": "missing-apt-pkg", "brew": "missing-brew-pkg"}}})[0].Remedy
	if want == "" {
		t.Fatal("fixture PATH did not yield a detected manager with a hint — test setup bug")
	}
	if !strings.Contains(report, "missingbin") || !strings.Contains(report, "MISSING") {
		t.Fatalf("a missing bin must be named as missing:\n%s", report)
	}
	if !strings.Contains(report, want) {
		t.Errorf("missing dep should carry the detected manager's remedy %q:\n%s", want, report)
	}
	if strings.Contains(report, "missing-brew-pkg") {
		t.Errorf("the remedy must be for the DETECTED manager only, not every hint:\n%s", report)
	}
	if !strings.Contains(report, "Phase 4.3") {
		t.Errorf("the output must say apply does not run the install (Phase 4.3):\n%s", report)
	}
}

// TestApplyHostPrefersThePacksOwnInstaller is item #6: for a `program`, the remedy leads
// with the tool's OWN installer, derived from the via/url/package the pack already declares
// — no new schema. The package-manager hint is kept as a secondary line rather than
// dropped, so a user who prefers their own manager still sees the token.
//
// Why the order and not the reverse: a tool that ships an installer ships an updater, while
// a distro package pins whatever that repo has. Measured 2026-08-02, nixpkgs was current
// for claude-code/codex/pi-coding-agent and github-copilot-cli was 16 releases behind
// (1.0.61 vs 1.0.77), with nothing in the output to say which.
func TestApplyHostPrefersThePacksOwnInstaller(t *testing.T) {
	fakeBinDir(t, "apt") // a detected manager, so the hint IS selectable
	report := runApplyHostForDeps(t,
		`{"kind":"program","bin":"npmtool","via":"npm","package":"@org/npmtool",`+
			`"install_hints":{"apt":"npmtool-apt"}}`,
		`{"kind":"program","bin":"curltool","via":"installer","url":"https://example/i.sh"}`)

	// The npm program: its own `npm install -g` leads; the apt hint trails as an alternative.
	if !strings.Contains(report, "MISSING → npm install -g @org/npmtool") {
		t.Errorf("an npm program's remedy should be its OWN npm install:\n%s", report)
	}
	if !strings.Contains(report, "or via apt: sudo apt install -y npmtool-apt") {
		t.Errorf("the package-manager hint should remain as a secondary line:\n%s", report)
	}
	if strings.Index(report, "npm install -g @org/npmtool") >
		strings.Index(report, "sudo apt install -y npmtool-apt") {
		t.Errorf("the pack's own installer must come FIRST, the manager hint second:\n%s", report)
	}

	// The installer program: the curl-to-shell command, printed as a suggestion the USER
	// runs. yolo must not run it — that is env-manager Phase 4.3's confirm-gated territory,
	// and the report says as much.
	if !strings.Contains(report, "MISSING → curl -fsSL https://example/i.sh | sh") {
		t.Errorf("an installer program's remedy should be its own curl-to-shell line:\n%s", report)
	}
	if !strings.Contains(report, "installs nothing") {
		t.Errorf("the report must still say yolo runs none of these:\n%s", report)
	}
}

// TestApplyHostReportsMissingDepWithoutRemedy: a dep with NO remedy at all still needs its
// bin, so it is reported as missing-with-no-known-remedy rather than omitted. This is the
// no-silent-caps half of the no-silent-skip rule.
//
// A `requires` with no hints is now the only way to reach this state, and that is the
// measure of what item #6 changed: a `program` used to land here whenever it had no hint for
// the host's manager (every shipped pack), and now derives its remedy from its own
// via/package instead. `requires` installs nothing, so hints are its only remedy source.
func TestApplyHostReportsMissingDepWithoutRemedy(t *testing.T) {
	fakeBinDir(t) // nothing on PATH: no manager, no bin
	report := runApplyHostForDeps(t, `{"kind":"requires","bin":"nohintbin"}`)

	if !strings.Contains(report, "nohintbin") {
		t.Fatalf("a hint-less dep must still be reported by name:\n%s", report)
	}
	if !strings.Contains(report, "MISSING") {
		t.Errorf("a hint-less missing bin is still MISSING:\n%s", report)
	}
	if !strings.Contains(report, "no install_hints") {
		t.Errorf("the output should say WHY there is no remedy:\n%s", report)
	}
	// And say the thing that is specific to this kind: yolo will never install it, so the
	// user is the only actor. A program's line says the opposite (here is the command).
	if !strings.Contains(report, "never installed by yolo") {
		t.Errorf("a `requires` with no remedy should say yolo installs it never:\n%s", report)
	}
}

// TestApplyHostReportsHintsForAnotherManager: hints that cover other managers but not this
// host's is a distinct state, and the pack author is the one who can fix it — so the line
// names the managers that are covered rather than reading as a yolo limitation.
//
// On a `requires` (not a program) for the reason above: a program with a `via` now always
// has a remedy of its own, so it cannot reach the no-remedy branch this covers.
func TestApplyHostReportsHintsForAnotherManager(t *testing.T) {
	fakeBinDir(t, "apt")
	report := runApplyHostForDeps(t, `{"kind":"requires","bin":"wrongmgr",`+
		`"install_hints":{"brew":"w-brew","dnf":"w-dnf"}}`)

	if !strings.Contains(report, "wrongmgr") || !strings.Contains(report, "MISSING") {
		t.Fatalf("the bin must be named as missing:\n%s", report)
	}
	// Deterministic, sorted, and naming the covered managers — not the absent one.
	if !strings.Contains(report, "install_hints cover brew/dnf but not apt") {
		t.Errorf("the line should name the covered managers and this host's:\n%s", report)
	}
}

// TestApplyHostNamesTheRequiresKindNotProgram: `requires` and `program` share the dep probe
// but are different claims, and the report has to say which one asked. Otherwise a user
// reading "program fzf MISSING" would look for the install yolo was going to do, and there
// isn't one.
func TestApplyHostNamesTheRequiresKindNotProgram(t *testing.T) {
	fakeBinDir(t, "apt")
	report := runApplyHostForDeps(t,
		`{"kind":"requires","bin":"reqbin","install_hints":{"apt":"reqbin-pkg"}}`)

	if !strings.Contains(report, "requires") {
		t.Errorf("the line must name the `requires` kind:\n%s", report)
	}
	if strings.Contains(report, "program") {
		t.Errorf("a pack declaring no `program` must not have one reported:\n%s", report)
	}
	if !strings.Contains(report, "sudo apt install -y reqbin-pkg") {
		t.Errorf("a requires' hints feed the remedy exactly as program's do:\n%s", report)
	}
}

// TestApplyHostDepNoteTrailsTheBlockOnce: the "apply installs nothing" note is a property of
// the command, so two missing deps in one pack get ONE note, and it trails the dep lines
// rather than wedging itself between them.
func TestApplyHostDepNoteTrailsTheBlockOnce(t *testing.T) {
	fakeBinDir(t, "apt")
	report := runApplyHostForDeps(t,
		`{"kind":"program","bin":"missA","via":"npm","package":"a","install_hints":{"apt":"a-pkg"}}`,
		`{"kind":"program","bin":"missB","via":"npm","package":"b","install_hints":{"apt":"b-pkg"}}`)

	if !strings.Contains(report, "missA") || !strings.Contains(report, "missB") {
		t.Fatalf("both declared bins must be reported:\n%s", report)
	}
	if n := strings.Count(report, "Phase 4.3"); n != 1 {
		t.Errorf("install-deferral note appeared %d times, want exactly 1:\n%s", n, report)
	}
	if strings.Index(report, "Phase 4.3") < strings.Index(report, "missB") {
		t.Errorf("the note should follow the last dep line, not split the block:\n%s", report)
	}
}

// fakeBinDir creates a temp dir holding an executable stub per name and makes it the ONLY
// entry on PATH, so every probe in this file resolves from the fixture. Returns the dir.
func fakeBinDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// runApplyHostForDeps writes a pack declaring the given contributions, points a throwaway
// $HOME's user config at it, runs `yolo host apply` in its default observe posture, and
// returns the combined output. Both the home and the config live under t.TempDir() — the
// real $HOME is never read or written.
func runApplyHostForDeps(t *testing.T, contributions ...string) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "depspack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	packJSON := `{"name":"depspack","contributes":[` + strings.Join(contributions, ",") + `]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(packJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"packs":[{"source":"file://` + packDir + `","name":"depspack"}]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("host apply rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	return out.String() + errw.String()
}
