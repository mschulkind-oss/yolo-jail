package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// A user with no packs must see NO new output: `yolo check` should not grow a section
// for a feature they do not use.
func TestSectionPacksSilentWhenNoneConfigured(t *testing.T) {
	packsFixture(t, `{}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)
	if buf.Len() != 0 {
		t.Errorf("expected no output with no packs configured:\n%s", buf.String())
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
