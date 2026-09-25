package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// `yolo pack init --from-plugin` promises no approval that does not exist.
//
// Its code-running-components notice used to end "If this pack is ever consumed from a git
// address, `yolo pack install` will ask you to approve them." OQ-TP9
// (docs/design/trust-paths.md) deleted that prompt, and since the launch-time pack refresh
// (maintainer ruling, 2026-09-25) a git pack may never pass through `yolo pack install` at
// all. So the notice now says nothing asks later, and names where to review instead.
func TestInitFromPluginPromisesNoLaterApproval(t *testing.T) {
	src := writeSourcePlugin(t, "acme-hooks", `{"name":"acme-hooks","skills":["./"],
		"hooks":{"PreToolUse":[]}}`)
	packDir := filepath.Join(t.TempDir(), "wrapper")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", "--from-plugin", src, packDir},
		&out, &errw, false); rc != 0 {
		t.Fatalf("rc = %d: %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "RUNS CODE") {
		t.Fatalf("the fixture must declare a code-running component, or this measures "+
			"nothing:\n%s", got)
	}
	if strings.Contains(got, "ask you to approve") {
		t.Errorf("init promises an approval prompt OQ-TP9 deleted:\n%s", got)
	}
	for _, want := range []string{"Nothing asks you to approve them later", "yolo pack footprint"} {
		if !strings.Contains(got, want) {
			t.Errorf("init's notice is missing %q:\n%s", want, got)
		}
	}
}
