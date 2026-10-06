package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// TestTheMacosUserBriefingNamesEveryCooperativeCPUVar: the briefing tells the agent which
// variables `resources.cpus` sets on this backend, and jailcontent cannot import this package,
// so it spells them out. This pins that spelling to the list buildPlan actually sets: a
// variable added to CooperativeCPUVars and not to the briefing, or the reverse, fails here.
func TestTheMacosUserBriefingNamesEveryCooperativeCPUVar(t *testing.T) {
	body := jailcontent.BriefingContent(jailcontent.BriefingInput{
		Workspace: "/Users/Shared/yolo/proj",
		Mechanism: "macos-user",
		Home:      "/Users/" + SandboxUser,
	})
	words := strings.Join(strings.Fields(body), " ")
	want := "`cpus` only sets the " + strings.Join(CooperativeCPUVars[:len(CooperativeCPUVars)-1], ", ") +
		" and " + CooperativeCPUVars[len(CooperativeCPUVars)-1] + " defaults"
	if !strings.Contains(words, want) {
		t.Errorf("the macos-user briefing does not name the cooperative cpus variables as %q:\n%s", want, words)
	}
}
