package cli

// hostbriefingbase_test.go pins docs/plans/notch-convergence.md item 26 (row D7) at the host
// apply: every briefing destination a selected pack declares opens with the host notch's base —
// the confinement header telling a host agent it is on the human's REAL machine — then the
// USER-SCOPE agents_md_extra, then the packs' prose, through the one per-destination composer
// the jail uses. Before it the host wrote the packs' prose alone, so a host agent was never told
// where it was (env-manager Phase 8's done-when) and agents_md_extra never reached one.
//
// Through applyHost, the front door, so dropping the base from applyHostBriefings' request fails
// these rather than a unit test of the composer.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestHostApplyTellsTheAgentItIsOnTheRealMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"agents_md_extra":"USER EXTRA: prefer small commits."}`)
	stubDeclaredBins(t)
	// A workspace whose own config names agents_md_extra too: it is agent-editable, so it reaches
	// a jail's briefing and never a file in the real home.
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, config.WorkspaceConfigName), `{"agents_md_extra":"WORKSPACE EXTRA"}`)
	t.Chdir(ws)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("a destination the claude pack declares must be composed even with no prose "+
			"pack selected: %v", err)
	}
	body := string(got)
	for _, want := range []string{
		"# YOLO Environment — host",
		"this is the human's REAL",
		"Changes are NOT disposable.",
		"Agent autonomy is **OFF**",
		"USER EXTRA: prefer small commits.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the host briefing must say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "WORKSPACE EXTRA") {
		t.Errorf("a workspace's agents_md_extra reached a file in the real home:\n%s", body)
	}
	// None of the jail's launch sections: they would be false sentences on the real machine.
	for _, jailOnly := range []string{"/workspace", "sandboxed container", "## Limitations"} {
		if strings.Contains(body, jailOnly) {
			t.Errorf("the host briefing carries the jail's %q:\n%s", jailOnly, body)
		}
	}
	// The extra follows the header, as a jail's follows its body.
	if strings.Index(body, "USER EXTRA") < strings.Index(body, "Agent autonomy is **OFF**") {
		t.Errorf("agents_md_extra must follow the base:\n%s", body)
	}
}
