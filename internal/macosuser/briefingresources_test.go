package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// TestTheMacosUserBriefingNamesEveryCooperativeCPUVar pins the one-pointer briefing ruling:
// detailed native CPU guidance belongs in the generated workspace-config recipe, not here.
func TestTheMacosUserBriefingNamesEveryCooperativeCPUVar(t *testing.T) {
	body := jailcontent.BriefingContent(jailcontent.BriefingInput{
		Workspace: "/Users/Shared/yolo/proj",
		Mechanism: "macos-user",
		Home:      "/Users/" + SandboxUser,
	})
	if !strings.Contains(body, "Read the **jail-mechanics** skill before") {
		t.Fatal("macos-user briefing omits the jail-mechanics pointer")
	}
	for _, name := range CooperativeCPUVars {
		if strings.Contains(body, name) {
			t.Errorf("always-on briefing repeats native CPU detail %s instead of pointing to jail-mechanics", name)
		}
	}
}
