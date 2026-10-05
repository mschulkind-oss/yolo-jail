package run

// platformswitchhostmgmt_test.go pins the jail notch's half of what a PP-D1 line says about a
// switch yolo's host apply wrote (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29): its remedy depends on whether a host apply renders in this home. Under
// host_management "none" — the unset key since the `assert` retirement (OQ-CO14) — `yolo host
// apply` refuses, so a line sending the user to it repeated at every launch; under "own" it
// renders, and removes the key once claude's host selection leaves the platform.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// THE ATTACH ARM, through deliverChannelOnAttach, so deleting the field's assignment in
// notePlatformSwitchConflicts fails here in one direction or the other: the user config is read
// at the launch (config.HostManagementMode), never by packload.
func TestAnAttachNamesAYoloWrittenSwitchsRemovalByWhetherAHostApplyRendersHere(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "bedrock")}
	for _, tc := range []struct {
		name, userConfig string
		want, wantNot    []string
	}{
		{"unset", "",
			[]string{"remove CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json by hand",
				`host_management is not "own"`, "`yolo host apply --revert`"},
			[]string{"or run `yolo host apply` with"}},
		{"none", `{"host_management": "none"}`,
			[]string{"remove CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json by hand",
				`host_management is not "own"`, "`yolo host apply --revert`"},
			[]string{"or run `yolo host apply` with"}},
		{"own", `{"host_management": "own"}`,
			[]string{"or run `yolo host apply` with claude on a provider of another platform, which removes it."},
			[]string{"by hand", "--revert"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(), nil)
			home := os.Getenv("HOME")
			if tc.userConfig != "" {
				writeUserConfig(t, home, tc.userConfig)
			}
			writeHostClaudeSettings(t, home, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
			rec := render.Host(home, nil, render.OwnershipUnstated).LeafRecordPath("claude", "settings")
			if err := os.MkdirAll(filepath.Dir(rec), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rec, []byte(`{"/env/CLAUDE_CODE_USE_BEDROCK": "1"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
				stagedPacks{root: "/ctx/packs", packs: packs}, channel)
			got := stderr.String()
			if !strings.Contains(got, "which `yolo host apply` wrote there for claude's host selection") {
				t.Fatalf("no line naming the switch yolo wrote:\n%s", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("the line does not say %q:\n%s", w, got)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("the line says %q:\n%s", w, got)
				}
			}
		})
	}
}
