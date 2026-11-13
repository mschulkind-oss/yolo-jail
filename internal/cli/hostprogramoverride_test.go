package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `yolo host` REFUSES A DEV OVERRIDE OF A PROGRAM NO PACK BUILDS FROM ITS TREE
// (docs/design/pack-buildable-programs.md §5, BG-D19): an override builds its checkout in place of a
// pack's tree, so one naming an npm agent can do nothing, and the refusal names the next step — a fork
// pack. Nothing is exec'd. An override naming no selected program is not this refusal. Red if the
// NonPackTreeOverrideLines check leaves hostMain.
func TestYoloHostRefusesAProgramOverrideOfAnNpmAgent(t *testing.T) {
	ws := t.TempDir()
	hostGateHome(t, `{"packs": ["claude"], "programs": {"overrides": {"claude": {"path": "`+ws+`"}}}}`, nil)
	got := captureHostExec(t)
	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "claude"}, &out, &errw, false, nil)
	if rc == 0 || got.execed {
		t.Fatalf("yolo host -- claude with an override of an npm agent: rc=%d execed=%v, want a refusal\n%s",
			rc, got.execed, errw.String())
	}
	for _, want := range []string{"yolo host: refusing to launch", "programs.overrides.claude", `via = "source"`, `fork_of = "claude"`} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, errw.String())
		}
	}

	hostGateHome(t, `{"packs": ["claude"], "programs": {"overrides": {"nosuchbin": {"path": "`+ws+`"}}}}`, nil)
	if _, _, errw := hostExecRun(t, "claude"); strings.Contains(errw, "refusing to launch") {
		t.Errorf("an override naming no selected program refused the launch:\n%s", errw)
	}
}
