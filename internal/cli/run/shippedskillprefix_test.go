package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The launch's briefing names yolo's built-in skills under the user's shipped_skill_prefix
// (docs/design/pack-naming.md PN-D13), and bare without one. Through refreshJailBriefings, the
// call site that reads the key, so deleting that read fails this.
func TestTheLaunchBriefingFollowsTheShippedSkillPrefix(t *testing.T) {
	for _, tc := range []struct{ prefixLine, want, unwanted string }{
		{"", "**configuring-the-jail**", "**yolo-configuring-the-jail**"},
		{`,"shipped_skill_prefix":"yolo"`, "**yolo-configuring-the-jail**", "**configuring-the-jail**"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			home := packHome(t)
			packDir := filepath.Join(t.TempDir(), "bp")
			if err := os.MkdirAll(packDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writePack(t, packDir, `{"name":"bp","contributes":[{"kind":"briefing","into":".claude/CLAUDE.md"}]}`)
			dir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			body := `{"packs":[{"source":"file://` + packDir + `","name":"bp"}]` + tc.prefixLine + "}\n"
			if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			jailcontent.SetPackSkillDirs(nil)
			jailcontent.SetPackSkillTargets(nil)
			t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

			o := goldenOptions(t.TempDir(), home)
			o.Stdout, o.Stderr = discardBuf(), discardBuf()
			const cname = "yolo-test-prefix"
			root, packs, briefings, err := o.stagePacks(cname)
			if err != nil {
				t.Fatalf("stagePacks: %v", err)
			}
			staging, err := o.refreshJailBriefings(cname, jsonx.NewOrderedMap(), "podman",
				stagedPacks{root: root, packs: packs, briefings: briefings}, ioprio.Normal)
			if err != nil {
				t.Fatalf("refreshJailBriefings: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.want) || strings.Contains(string(data), tc.unwanted) {
				t.Errorf("the briefing does not name %s alone:\n%s", tc.want, data)
			}
		})
	}
}
