package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The warning that the mechanics skill is stale names the skill as it is delivered under the
// user's shipped_skill_prefix (docs/reference/pack-system.md PN-D13), not the bare jail-mechanics.
func TestTheMechanicsRefreshWarningNamesTheDeliveredSkill(t *testing.T) {
	for _, tc := range []struct{ prefix, want string }{{"", "jail-mechanics"}, {"yolo", "yolo-jail-mechanics"}} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("YOLO_VERSION", "")
			body := ""
			if tc.prefix != "" {
				body = "shipped_skill_prefix = \"" + tc.prefix + "\"\n"
			}
			if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			(&Options{Stderr: &stderr}).mechanicsRefreshWarning("a reason", "a remedy")
			if !strings.Contains(stderr.String(), "Warning: "+tc.want+" still describes") {
				t.Errorf("the warning does not name %s: %q", tc.want, stderr.String())
			}
		})
	}
}
