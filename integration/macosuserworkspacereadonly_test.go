package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMacosUserWorkspaceReadonlyLocksTheConfig is the macos-user half of the lock the container
// backends perform beside workspace_readonly's declared entries: any entry write-protects the
// workspace config the launch read, so a session cannot switch its own protection off
// (macosuser.workspaceReadonlyRels). A real launch, so the profile under test is the one
// BuildRunPlan wrote, and the same probe under `{}` is the control: there the config must stay
// writable, or the refusal would be some other rule's.
//
// The two writes are the two ways to change a file: an append through its name, and a copy
// renamed over it (what an editor's atomic save does). A HARD LINK made under a name of the
// session's own is the residual the rule cannot name; it is RECORDED in the log, not asserted.
func TestMacosUserWorkspaceReadonlyLocksTheConfig(t *testing.T) {
	requireMacosUser(t)
	for _, tc := range []struct {
		name, config, want string
	}{
		{"with an entry", `{"workspace_readonly": ["vendored"]}`, "DENIED"},
		{"without one", `{}`, "ALLOWED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := macosUserWorkspace(t, tc.config)
			if err := os.MkdirAll(filepath.Join(ws, "vendored"), 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(ws, "yolo-jail.jsonc")
			q := "'" + strings.ReplaceAll(cfg, "'", `'\''`) + "'"
			probe := strings.Join([]string{
				`echo "=== CONFIGLOCK ==="`,
				`if ( printf '' >> ` + q + ` ) 2>/dev/null; then echo "write|ALLOWED"; else echo "write|DENIED"; fi`,
				`if ( cp ` + q + ` ` + q + `.yolo-it && mv ` + q + `.yolo-it ` + q + ` ) 2>/dev/null; then echo "replace|ALLOWED"; else rm -f ` + q + `.yolo-it; echo "replace|DENIED"; fi`,
				`if ( ln ` + q + ` ` + q + `.yolo-hl && printf '' >> ` + q + `.yolo-hl ) 2>/dev/null; then echo "hardlink|ALLOWED"; else echo "hardlink|DENIED"; fi; rm -f ` + q + `.yolo-hl`,
				`echo "=== END CONFIGLOCK ==="`,
			}, "\n")
			r := runMacosUser(t, ws, probe)
			if r.rc != 0 || !strings.Contains(r.stdout, "=== END CONFIGLOCK ===") {
				t.Fatalf("the launch did not run its probe (rc %d)\nstdout:\n%s\nstderr:\n%s",
					r.rc, r.stdout, r.stderr)
			}
			got := macosUserHomeProbeFields(t, r.stdout, "CONFIGLOCK")
			for _, key := range []string{"write", "replace"} {
				if got[key] != tc.want {
					t.Errorf("%s|%s, want %s: the workspace config's lock does not follow "+
						"workspace_readonly\n%s", key, got[key], tc.want, r.stdout)
				}
			}
			t.Logf("MEASUREMENT (the hard-link residual, %s): a hard link to the config, written "+
				"through: %s", tc.name, got["hardlink"])
		})
	}
}
