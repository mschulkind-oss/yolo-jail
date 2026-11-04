package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAtGuestNamesTheSeatbeltSetupTheModeMeant: `--at guest` launched the Seatbelt sandbox as
// the user config's `macos_user_mode` said, so its refusal names seatbelt-self where that mode
// is "self" and seatbelt-yolo-account otherwise.
func TestAtGuestNamesTheSeatbeltSetupTheModeMeant(t *testing.T) {
	for _, tc := range []struct{ userCfg, want, not string }{
		{"\n", "yolo --setup seatbelt-yolo-account -- <cmd>", "seatbelt-self"},
		{"macos_user_mode = \"self\"\n", "yolo --setup seatbelt-self -- <cmd>", "seatbelt-yolo-account"},
	} {
		home, _ := withHomeAndCwd(t)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), tc.userCfg)
		got := atRemoved("guest").Error()
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) {
			t.Errorf("user config %q: `--at guest` refusal = %q, want it to name %q alone", tc.userCfg, got, tc.want)
		}
	}
}
