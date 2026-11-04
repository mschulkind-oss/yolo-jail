package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAnOffSwitchConvertsToItsPacksOff: a user `loopholes.<name>.enabled: false` on LC-D3's
// loopholes converts to `off = true` under the pack's `[allow.<pack>]` table, and a profile
// written beside it survives conversion rather than being refused as dormant
// (docs/design/legacy-config-compat.md LC-D3). The equality proof reads the off switch where the
// launch does (loopholes.LaunchBlock), so the plan passing is the proof that the converted
// config keeps each loophole off.
func TestAnOffSwitchConvertsToItsPacksOff(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["aws-auth", "cgroup-delegate", "claude"], "loopholes": {`+
		`"aws-auth": {"enabled": false, "settings": {"profile": "example-profile"}}, `+
		`"cgroup-delegate": {"enabled": false}, "claude-oauth-broker": {"enabled": false}}}`)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) != 0 {
		t.Fatalf("the proof did not pass: equal=%v not converted=%+v differences=%+v", p.Equal, p.NotConverted, p.Differences)
	}
	user := string(target(t, p, filepath.Join(dir, "config.jsonc")).Bytes)
	for _, want := range []string{
		"[allow.aws-auth]\noff = true\nprofile = \"example-profile\"",
		"[allow.cgroup-delegate]\noff = true",
		"[allow.claude-auth]\noff = true",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("the user config lacks %q:\n%s", want, user)
		}
	}
	if strings.Contains(user, "loopholes") {
		t.Errorf("the user config still says loopholes:\n%s", user)
	}
}
