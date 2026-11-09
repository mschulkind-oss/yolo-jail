package cli

import (
	"strings"
	"testing"
)

// Usage texts that named a file or value a current install does not have (the 2026-11-07 message
// audit, docs/research/message-rewrites). Each case is a phrase that was false and the phrase that
// replaced it.
func TestUsageTextsNameWhatTheCodeDoes(t *testing.T) {
	var initBlurb string
	for _, c := range commandHelp {
		if c.name == "init" {
			initBlurb = c.blurb
		}
	}
	for _, tc := range []struct {
		name, text, wrong, want string
	}{
		{"yolo --help init", initBlurb, "yolo-jail.jsonc", "yolo-jail.toml"},
		{"yolo init-user-config --help", initUserConfigUsage, "own yolo-jail.jsonc", "own yolo-jail.toml"},
		{"yolo config-ref --help", configRefUsage, "workspace\nyolo-jail.jsonc and", "yolo-jail.toml"},
		{"yolo update --help", updateUsage, "config.jsonc", "yolo config set update_check false --user"},
		{"yolo programs --help", programsUsage, "config.jsonc", "yolo config set programs.autoprune true --user"},
		{"yolo apply --help", applyUsage, "(yolo-jail.local.jsonc", "(yolo-jail.local.toml"},
		{"yolo pack --help", packUsage, "AND the pack.json manifest", "pack.toml or pack.json manifest"},
		{"yolo surface --help", surfaceUsage, "'assert' there is no store", "'none' there is no store"},
	} {
		if strings.Contains(tc.text, tc.wrong) {
			t.Errorf("%s still says %q", tc.name, tc.wrong)
		}
		if !strings.Contains(tc.text, tc.want) {
			t.Errorf("%s does not say %q", tc.name, tc.want)
		}
	}
}
