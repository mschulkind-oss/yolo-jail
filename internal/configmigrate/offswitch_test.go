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

// TestAnOffSwitchOnAnUnselectedPackConvertsToNothing: `enabled: false` for an off-switch loophole
// whose pack nothing selects (not listed, and no listed pack `needs` it) keeps off a loophole that
// does not run anyway, so it converts to nothing and the transcript says why. Writing
// `[allow.aws-auth] off = true` would name a pack that is not selected, which the converted
// config's own checks refuse. A needed pack's switch (claude-auth under claude) still converts.
func TestAnOffSwitchOnAnUnselectedPackConvertsToNothing(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["claude"], "loopholes": {`+
		`"aws-auth": {"enabled": false}, "claude-oauth-broker": {"enabled": false}}}`)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) != 0 || p.Refused != nil {
		t.Fatalf("the plan did not pass: equal=%v not converted=%+v refused=%v differences=%+v", p.Equal, p.NotConverted, p.Refused, p.Differences)
	}
	user := string(target(t, p, filepath.Join(dir, "config.jsonc")).Bytes)
	if strings.Contains(user, "aws-auth") || !strings.Contains(user, "[allow.claude-auth]\noff = true") {
		t.Errorf("want no aws-auth line and claude-auth's off:\n%s", user)
	}
	if text := p.Transcript(); !strings.Contains(text, `"loopholes.aws-auth.enabled": false dropped: no selected pack runs aws-auth`) {
		t.Errorf("the transcript does not say why the switch was dropped:\n%s", text)
	}
}

// TestAProjectEnabledTrueOnAnOffSwitchConvertsToNothing: a workspace config's `enabled: true` on
// an off-switch loophole turns nothing on (the loophole runs when its pack is selected, and a
// workspace cannot lift the user's off, LC-D3), so converting the project drops it, says so, and
// the proof holds with the user's legacy `enabled: false` beside it: the loophole stays off.
func TestAProjectEnabledTrueOnAnOffSwitchConvertsToNothing(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": false, "settings": {"profile": "p"}}}}`)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"aws-auth": {"enabled": true}}}`)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) != 0 {
		t.Fatalf("the proof did not pass: equal=%v not converted=%+v differences=%+v", p.Equal, p.NotConverted, p.Differences)
	}
	if project := string(target(t, p, filepath.Join(ws, "yolo-jail.jsonc")).Bytes); strings.Contains(project, "aws-auth") {
		t.Errorf("the project still names aws-auth:\n%s", project)
	}
	if text := p.Transcript(); !strings.Contains(text, `"loopholes.aws-auth.enabled": true dropped: aws-auth runs whenever its pack is selected`) {
		t.Errorf("the transcript does not say why:\n%s", text)
	}
}
