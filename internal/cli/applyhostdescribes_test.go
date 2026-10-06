package cli

// applyhostdescribes_test.go pins `describes` at `yolo host apply` for a kind with an AT-LAUNCH
// shape (docs/design/boundary-broker.md BB-D69): whether the host delivers a pack's loophole or
// env is decided per CONTRIBUTION by the notch line's own outcome (hostNotchOutcomeOf), so the
// briefing gate must ask the same thing. A per-kind gate counted every loophole as delivered,
// because some loopholes have a doorway at `yolo host --`, and so wrote prose about a loophole
// with none into a real home while the same run's notch line said that loophole does not apply.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// describesPack is a file:// pack named lh: a loophole of its own (no doorway: a pack yolo does
// not ship opens none at `yolo host --`), a plain env var (delivered by `yolo host --`), one
// briefing file describing each, and one file addressed to claude describing the loophole.
func describesPack(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lh")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"lh","contributes":[`+
		`{"kind":"loophole","from":"loopholes/lhole"},`+
		`{"kind":"env","vars":{"LH_PLAIN":"1"}},`+
		`{"kind":"briefing","from":"briefing/hole.md","describes":["loophole"]},`+
		`{"kind":"briefing","from":"briefing/env.md","describes":["env"]},`+
		`{"kind":"briefing","from":"files/addressed.md","agents":["claude"],"describes":["loophole"]}]}`)
	writeFile(t, filepath.Join(dir, "loopholes", "lhole", "manifest.jsonc"), `{
		"name": "lhole", "description": "d", "version": 1, "default_enabled": true,
		"transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "lhole", "--socket", "{socket}"],
		                "publishes": "socket", "scope": "host"}}`)
	writeFile(t, filepath.Join(dir, "briefing", "hole.md"), "LOOPHOLE PROSE\n")
	writeFile(t, filepath.Join(dir, "briefing", "env.md"), "ENV PROSE\n")
	writeFile(t, filepath.Join(dir, "files", "addressed.md"), "ADDRESSED LOOPHOLE PROSE\n")
	return dir
}

func TestApplyHostWithholdsProseAboutALoopholeTheHostOpensNoDoorwayFor(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude",{"source":"file://`+describesPack(t)+`","name":"lh"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	defaultReport(t)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("no briefing written: %v\n%s", err, report)
	}
	for _, withheld := range []string{"LOOPHOLE PROSE", "ADDRESSED LOOPHOLE PROSE"} {
		if strings.Contains(string(got), withheld) {
			t.Errorf("prose about a loophole with no doorway at the host reached a real home "+
				"(%q):\n%s\n%s", withheld, got, report)
		}
	}
	// The env var is delivered by `yolo host --`, so prose about it holds at the host.
	if !strings.Contains(string(got), "ENV PROSE") {
		t.Errorf("prose about an env var `yolo host --` delivers must reach the host:\n%s", got)
	}
	// The same run's notch line says the loophole does not apply, and names both withheld files.
	if !hasLine(report, "does not apply at the host", "loophole",
		"lh: briefing/hole.md describes loophole; lh: files/addressed.md describes loophole") {
		t.Errorf("the notch line must name each withheld file beside the kind:\n%s", report)
	}
	if strings.Contains(report, "briefing/env.md") {
		t.Errorf("a delivered file is not a notch fact:\n%s", report)
	}
}

// Under --verbose the destinations lines must not claim a delivery the gate withheld: the
// zero-ceremony merge line and the addressed line name only what reaches a destination.
func TestApplyHostVerboseClaimsNoDeliveryTheGateWithheld(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude",{"source":"file://`+describesPack(t)+`","name":"lh"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERBOSE", "1")

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, report)
	}
	// briefing/env.md still merges into claude's file, so the merge line stays.
	if !hasLine(report, "lh declares no destination — merging into the ones your packs name",
		".claude/CLAUDE.md") {
		t.Errorf("the merge line for a delivered file is gone:\n%s", report)
	}
	if hasLine(report, "lh addresses claude", "files/addressed.md reaches") {
		t.Errorf("the addressed line claims a delivery the gate withheld:\n%s", report)
	}
}

// THE ADOPTION GATE COMPOSES BY THE SAME CENSUS AS THE RENDER. A home whose ~/.claude/CLAUDE.md
// already holds exactly what the render writes is not an adoption, record or not (a state-dir
// prune loses the record). A gate composing with the per-kind answer would see prose about lh's
// loophole that the render leaves out, and ask to take over a file it then rewrites identically.
func TestApplyHostAdoptionGateComposesByTheCensus(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude",{"source":"file://`+describesPack(t)+`","name":"lh"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	defaultReport(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	written, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Lose the ownership record, so only the comparison against the composition can tell the
	// file is yolo's own bytes.
	if err := os.Remove(hostBriefingManifestPath(home)); err != nil {
		t.Fatal(err)
	}
	rc, report := applyWith(t, true, nil) // nil stdin: a prompt would fail closed
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "not adopted") || strings.Contains(report, "would have your own prose") {
		t.Errorf("the gate asked to adopt a file identical to the render's output:\n%s", report)
	}
	again, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(written) {
		t.Errorf("the second apply changed the briefing:\n--- first\n%s\n--- second\n%s", written, again)
	}
}
