package run

// userprecedence_test.go pins OQ-CN8 (docs/reference/providers.md, ruled
// 2026-09-28) through the production launch composition and claude's REAL launcher: a value the
// user sets on the command line (`ANTHROPIC_MODEL=x claude`) or exports in a jail shell beats
// the one claude's profile composes, as it did before the per-agent files; with no value of the
// user's, the profile's wins. The launcher sources claude's own file AFTER the user's shell, so
// deleting the precedence the file's lines carry (writing them plain-form again) fails here.

import (
	"path/filepath"
	"testing"
)

func TestAPerCommandValueBeatsTheProfilesComposedValue(t *testing.T) {
	jail, _, _ := launchGateJail(t, []string{"claude", "zai"},
		func(o *Options) { o.UseProfiles = map[string]string{"claude": "zai"} })
	composed := jail.agentEnv("claude")
	profileModel := composed["ANTHROPIC_MODEL"]
	if profileModel == "" {
		t.Fatalf("claude on zai composes no ANTHROPIC_MODEL; the test has no subject: %v", composed)
	}
	launcher := filepath.Join(jail.home, ".yolo", "bin", "launch", "claude")
	for name, script := range map[string]string{
		"a per-command value": `. "$HOME/.config/yolo-user-env.sh"; ANTHROPIC_MODEL=user-pick exec "$0"`,
		"a shell export":      `. "$HOME/.config/yolo-user-env.sh"; export ANTHROPIC_MODEL=user-pick; exec "$0"`,
	} {
		out := filepath.Join(jail.home, "override.env")
		jail.run(script, launcher, out)
		if got := readEnvDump(t, out)["ANTHROPIC_MODEL"]; got != "user-pick" {
			t.Errorf("%s: claude's ANTHROPIC_MODEL = %q, want the user's user-pick over the "+
				"profile's %q", name, got, profileModel)
		}
	}
}
