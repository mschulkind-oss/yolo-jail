package cli

// applyhostdeps_test.go pins Phase 8's contract: `apply --host` reports the REAL state of a
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

// TestApplyHostReportsMissingDepWithRemedy: a missing bin reports the install_hints remedy
// for the DETECTED host manager (here apt, the only manager on the fixture PATH), and says
// that apply does not run it.
func TestApplyHostReportsMissingDepWithRemedy(t *testing.T) {
	fakeBinDir(t, "apt") // the detected manager; the declared bin is deliberately absent
	report := runApplyHostForDeps(t, `{"kind":"program","bin":"missingbin","via":"npm",`+
		`"package":"p","install_hints":{"apt":"missing-apt-pkg","brew":"missing-brew-pkg"}}`)

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

// TestApplyHostReportsMissingDepWithoutRemedy: a program with NO install_hints still needs
// its bin, so it is reported as missing-with-no-known-remedy rather than omitted. This is
// the no-silent-caps half of the no-silent-skip rule, and it is the state EVERY shipped pack
// is in (they declare `via` and no hints), so it is the common output rather than an edge.
func TestApplyHostReportsMissingDepWithoutRemedy(t *testing.T) {
	fakeBinDir(t) // nothing on PATH: no manager, no bin
	report := runApplyHostForDeps(t, `{"kind":"program","bin":"nohintbin","via":"npm","package":"p"}`)

	if !strings.Contains(report, "nohintbin") {
		t.Fatalf("a hint-less program must still be reported by name:\n%s", report)
	}
	if !strings.Contains(report, "MISSING") {
		t.Errorf("a hint-less missing bin is still MISSING:\n%s", report)
	}
	if !strings.Contains(report, "no install_hints") {
		t.Errorf("the output should say WHY there is no remedy:\n%s", report)
	}
	// A pack with a `via` DOES know how to install into a jail; saying so distinguishes
	// "yolo has nothing" from "yolo has something it will not run against your real host".
	if !strings.Contains(report, "via npm") {
		t.Errorf("a hint-less program with a `via` should name the jail install path:\n%s", report)
	}
}

// TestApplyHostReportsHintsForAnotherManager: hints that cover other managers but not this
// host's is a THIRD state, and the pack author is the one who can fix it — so the line names
// the managers that are covered rather than reading as a yolo limitation.
func TestApplyHostReportsHintsForAnotherManager(t *testing.T) {
	fakeBinDir(t, "apt")
	report := runApplyHostForDeps(t, `{"kind":"program","bin":"wrongmgr","via":"installer",`+
		`"url":"https://example/i.sh","install_hints":{"brew":"w-brew","dnf":"w-dnf"}}`)

	if !strings.Contains(report, "wrongmgr") || !strings.Contains(report, "MISSING") {
		t.Fatalf("the bin must be named as missing:\n%s", report)
	}
	// Deterministic, sorted, and naming the covered managers — not the absent one.
	if !strings.Contains(report, "install_hints cover brew/dnf but not apt") {
		t.Errorf("the line should name the covered managers and this host's:\n%s", report)
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
// $HOME's user config at it, runs `apply --host` in its default observe posture, and
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
	if rc := applyHost(&out, &errw, false, false); rc != 0 {
		t.Fatalf("apply --host rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	return out.String() + errw.String()
}
