package cli

// applyhostidempotent_test.go pins ONE property of `yolo host apply --assert`: a second run against
// an unchanged config changes NOTHING in the home.
//
// Why it needs a file of its own rather than a line in the skills or briefing suite. Those suites
// each assert idempotency of the paths THEIR kind owns, and one of them (applyhostskillscompose)
// had to NARROW its snapshot to the skills destinations precisely because the whole home did not
// converge — the narrowing carried a comment saying so. A property that no kind's suite can assert
// is a property nothing owns, which is how `apply --host` shipped un-converged while every kind's
// own test was green. This file owns it.
//
// THE DEFECT IT CLOSES (V2). A surface's provenance record classified a `defaults` key as
// `defaults` on the first apply (the key is absent, so the default fills it) and as `host` on the
// second (the key is now in the file — because yolo's own default put it there). So
// host-provenance/<surface>.provenance differed between apply 1 and apply 2 and only converged from
// apply 3 on. Two applies in a row over an unchanged input produced two different homes, which is
// exactly the signal an apply report exists to give: "something changed" became indistinguishable
// from "the tool is unsettled".

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// shippedPacksFixture points a throwaway $HOME at a config selecting every pack yolo SHIPS.
//
// All six by bare name, deliberately: the defect lives in the `config` kind's provenance record,
// and which surfaces declare a `defaults` layer is a property of the shipped manifests rather than
// of anything a fixture could invent. A hand-rolled one-surface pack would pin the mechanism while
// leaving the real question — "does a plain `yolo host apply` converge?" — unasked.
func shippedPacksFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	selectPacks(t, home, `"claude","codex","copilot","opencode","pi","agy"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// TestApplyHostIsWholeHomeIdempotent — apply 2 is a no-op relative to apply 1, over the WHOLE home.
//
// Snapshotted with linkAwareHashes rather than a listing: the state this defect moved is a few
// bytes inside an existing file, so anything short of content hashing reports a converged home that
// is not one.
func TestApplyHostIsWholeHomeIdempotent(t *testing.T) {
	home := shippedPacksFixture(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	first := linkAwareHashes(t, home)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	second := linkAwareHashes(t, home)

	var diffs []string
	for p, h := range second {
		if _, had := first[p]; !had {
			diffs = append(diffs, p+" (added)")
			continue
		}
		if first[p] != h {
			diffs = append(diffs, p+" (content changed)")
		}
	}
	for p := range first {
		if _, ok := second[p]; !ok {
			diffs = append(diffs, p+" (removed)")
		}
	}
	sort.Strings(diffs)
	if len(diffs) != 0 {
		t.Errorf("a second `yolo host apply --assert` over an unchanged config changed the home: %v\n"+
			"apply must converge after ONE run, or a report cannot distinguish \"something "+
			"changed\" from \"the tool is unsettled\"\n%s", diffs, report)
	}
}

// TestApplyHostKeepsADefaultAttributedToDefaults is the defect at the level it actually lives,
// so a fix that merely stopped WRITING the record would not pass.
//
// The attribution is not cosmetic: `defaults` and `host` are different claims about who owns the
// key — fill-if-absent output versus the user's own value — and every reader that asks "did yolo
// set this?" reads exactly this file.
func TestApplyHostKeepsADefaultAttributedToDefaults(t *testing.T) {
	home := shippedPacksFixture(t)
	// pi/settings declares `theme` as a default and nothing above it claims the key, so it is the
	// shipped surface where a fill-if-absent attribution is observable end to end.
	record := filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"pi-settings.provenance")

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	if got := provenanceOf(t, record, "theme"); got != "defaults" {
		t.Fatalf("fixture bug: the FIRST apply must attribute `theme` to defaults, got %q", got)
	}
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if got := provenanceOf(t, record, "theme"); got != "defaults" {
		t.Errorf("the second apply relabelled yolo's own default as %q — the key is only in the "+
			"file because the default put it there, so `host` claims the user set a value they "+
			"never touched", got)
	}
}

// TestApplyHostGivesAnEditedDefaultBackToTheUser is the other half, and the reason the fix cannot
// be "always keep saying defaults": the moment the user's value differs from the default, the key
// IS theirs and the record must say so.
func TestApplyHostGivesAnEditedDefaultBackToTheUser(t *testing.T) {
	home := shippedPacksFixture(t)
	record := filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"pi-settings.provenance")
	settings := filepath.Join(home, ".pi", "agent", "settings.json")

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	edited := strings.Replace(string(data), `"system"`, `"solarized"`, 1)
	if edited == string(data) {
		t.Fatalf("fixture bug: the default value is not in %s:\n%s", settings, data)
	}
	if err := os.WriteFile(settings, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if got := provenanceOf(t, record, "theme"); got != "host" {
		t.Errorf("a default the user has since CHANGED must read `host`, got %q — otherwise the "+
			"idempotency fix hands the user's own value to yolo", got)
	}
}

// provenanceOf returns the layer the record attributes to one key, or "" when the key is absent.
func provenanceOf(t *testing.T, path, key string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("provenance record %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, layer, ok := strings.Cut(line, "\t")
		if ok && k == key {
			return layer
		}
	}
	return ""
}
