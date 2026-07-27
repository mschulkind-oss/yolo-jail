package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func packsFixture(t *testing.T, cfgBody string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

// D1: a pack problem must surface on the HOST at `yolo check`, where erroring is
// normal and the message is actionable — not only in the jail, where A12's fatal
// policy turns it into a refused boot the user has to diagnose from a log.
func TestSectionPacksFailsOnExecutableFile(t *testing.T) {
	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "skills", "s", "SKILL.md"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "evil.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)

	if r.failed == 0 {
		t.Errorf("expected a failure for an executable pack file:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "allow_exec") {
		t.Errorf("message should name the opt-in:\n%s", buf.String())
	}
}

// A clean pack passes and reports its file count.
func TestSectionPacksPassesCleanPack(t *testing.T) {
	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "skills", "s", "SKILL.md"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)
	if r.failed != 0 {
		t.Errorf("clean pack failed:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "file(s) stage") {
		t.Errorf("expected a file count:\n%s", buf.String())
	}
}

// Zero packs WARNS, and this test replaces the earlier one asserting silence.
//
// The old contract ("a user who does not use packs sees no new output") assumed packs
// were an opt-in extra alongside a Go-owned `agents` key. With that key gone, packs are
// the only delivery channel, so an empty list is the one state worth reporting: it is a
// jail with no agent in it. Not a FAIL — such a jail still boots, and a shell-only jail
// is a legitimate thing to want.
func TestSectionPacksWarnsWhenNoneConfigured(t *testing.T) {
	packsFixture(t, `{}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, buf.String())
	}
	if r.failed != 0 {
		t.Errorf("zero packs must not FAIL (a shell-only jail is legitimate):\n%s", buf.String())
	}
	// The guidance must name a command the user can actually run — this is the whole
	// point of the warning, and the half most likely to rot.
	for _, want := range []string{"no coding agent", "yolo pack --help"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("empty-packs warning missing %q:\n%s", want, buf.String())
		}
	}
}

// The empty-packs text is the SHARED config constant, not a local copy.
//
// It used to be duplicated here and in internal/cli/run (check cannot import the run
// pipeline), which let the two surfaces drift in wording with no test noticing — they had
// already drifted by a trailing period. Both now read config.NoPacksMessage/NoPacksGuidance,
// so `yolo check` and a launch cannot tell the user different things. This asserts the
// section renders those constants verbatim; the run side pins its own use of the same two.
func TestSectionPacksUsesTheSharedNoPacksText(t *testing.T) {
	packsFixture(t, `{}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)
	for _, want := range []string{config.NoPacksMessage, config.NoPacksGuidance} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("section does not render the shared constant %q verbatim:\n%s",
				want, buf.String())
		}
	}
}

// A never-installed GIT pack is reported, not fetched: `yolo check` must work offline
// and must never make a surprise network call.
func TestSectionPacksReportsUnfetchedGitPackWithoutFetching(t *testing.T) {
	packsFixture(t, `{"packs": ["git+https://example.invalid/o/r//p?ref=main"]}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)
	if r.failed == 0 {
		t.Errorf("expected a failure for a never-fetched pack:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "pack install") {
		t.Errorf("message should point at the fetch command:\n%s", buf.String())
	}
}

// The section ALWAYS prints its header and a trailing blank, on every branch.
//
// Both used to be conditional, because the section only existed for pack users; making
// the empty case warn meant every branch now prints, so the header and the separator
// became unconditional (a `defer`). That is easy to half-revert: the load-error branch
// returns early, and the golden that would otherwise pin section shape early-exits
// before Packs, so nothing else covers it. Without the trailing blank the next section
// header ("Entrypoint Dry-Run") runs straight into this one's last line.
func TestSectionPacksAlwaysFramesTheSection(t *testing.T) {
	for _, tc := range []struct{ name, cfg string }{
		{"empty (warn branch)", `{}`},
		{"unusable (load-error branch)", `{"packs": `},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packsFixture(t, tc.cfg)
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r)
			if !strings.Contains(buf.String(), "Packs") {
				t.Errorf("section header missing:\n%s", buf.String())
			}
			if !strings.HasSuffix(buf.String(), "\n\n") {
				t.Errorf("section must end with a blank line or the next header abuts "+
					"it:\n%q", buf.String())
			}
		})
	}
}

// A pack whose filters match nothing is a WARNING, not a failure: it may be
// legitimately empty mid-authoring, but it is nearly always a typo so it must not be
// silent.
func TestSectionPacksWarnsOnZeroStagedFiles(t *testing.T) {
	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "AGENTS.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": [{"source": "file://`+pack+`", "only": ["nope/*"]}]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)
	if r.failed != 0 {
		t.Errorf("an empty pack should warn, not fail:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "stages 0 files") {
		t.Errorf("expected a 0-files warning:\n%s", buf.String())
	}
}

// TestEmbeddedPacksPassCheckWithoutAnAddress is the regression for a failure only the
// integration suite caught: `yolo check` parsed every entry's Source as a fetchable
// address, so a `packs: ["claude"]` entry failed with `pack address "embedded:claude" has
// no scheme`. A user's valid config reported three failures.
//
// Reported PASSING rather than skipped: someone who wrote `packs: ["claude"]` should see it
// acknowledged, not wonder whether the key took effect.
func TestEmbeddedPacksPassCheckWithoutAnAddress(t *testing.T) {
	packsFixture(t, `{"packs": ["claude", "codex"]}`)

	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r)
	got := out.String()
	if strings.Contains(got, "no scheme") {
		t.Errorf("an embedded pack must not be parsed as an address:\n%s", got)
	}
	if r.failed != 0 {
		t.Errorf("want 0 failures for a valid embedded selection, got %d:\n%s",
			r.failed, got)
	}
	for _, name := range []string{"claude", "codex"} {
		if !strings.Contains(got, name) {
			t.Errorf("check should acknowledge the %s pack:\n%s", name, got)
		}
	}
}
