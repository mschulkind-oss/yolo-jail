package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPShippingAdviceThroughMainHasNoSlackIntegration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local/state"))
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("YOLO_VERSION", "")
	for _, args := range [][]string{{"yolo", "config-ref"}, {"yolo", "mcp-auth", "--help"}} {
		var rc int
		out, errs := captureStdio(t, func() { rc = Main(args) })
		if rc != 0 || out == "" {
			t.Fatalf("advice call %v failed: rc=%d stderr=%q", args, rc, errs)
		}
		for _, marker := range []string{"slack-mcp", "mcp.slack.com", "slack-skills-plugin", "1601185624273.8899143856786", "mcp-auth status slack", "mcp-auth header slack"} {
			if strings.Contains(out+errs, marker) {
				t.Errorf("advice call %v advertises unauthorized integration %q", args, marker)
			}
		}
	}
}
