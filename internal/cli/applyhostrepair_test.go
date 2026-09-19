package cli

// applyhostrepair_test.go is the COMMAND-LEVEL half of the one-shot repair
// (internal/agentcfg/rejectedvalues.go): the report says so.
//
// The repair itself is pinned where it happens (internal/agentcfg/rejectedvalues_test.go for
// the composed surface, internal/entrypoint/rejectedvalues_test.go for both writers). This
// exists because the thing only this level can measure is the LINE THE USER READS — and the
// requirement that made the mechanism acceptable at all was that a one-shot edit of a file in
// someone's real home is never silent.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyHostReportsTheRepairedValue runs `yolo host apply` (observe, so it writes nothing)
// over a home whose pi settings file still holds the `"theme": "system"` yolo shipped, and
// requires the report to name it.
//
// OBSERVE rather than --assert, deliberately: the dry run is the posture where a user decides
// whether to let the apply happen, so it is the one that has to disclose the edit BEFORE it is
// made — and it needs no confirmation input, which keeps this test about the line rather than
// about the gate.
//
// FAILS IF THE PRINTER IN apply.go IS DELETED, and also if HostRenderResult.Repaired stops
// being populated — the only two ways this fact can go missing from the report.
func TestApplyHostReportsTheRepairedValue(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"),
		`{"theme":"system","defaultModel":"sonnet"}`)
	selectPacks(t, home, `"pi"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("host apply rc=%d\n%s", rc, report)
	}
	for _, want := range []string{"repaired", "would remove", `theme = "system"`, "light/dark"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report never says %q — a one-shot edit of a key in the user's own "+
				"config file may not be silent:\n%s", want, report)
		}
	}
}

// TestApplyHostSaysNothingAboutAnUntouchedValue is the other half, and it matters as much: the
// report must not mention a repair on a home where none is due. A line claiming yolo edited a
// config file it did not touch is the one failure mode of a disclosure that is worse than
// silence.
func TestApplyHostSaysNothingAboutAnUntouchedValue(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"theme":"dracula"}`)
	selectPacks(t, home, `"pi"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("host apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "repaired") {
		t.Errorf("the report claims a repair on a home with a chosen theme:\n%s", report)
	}
}
