package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A bare profile is selected for EVERY program a pack installs (config.FoldProfiles), so one that
// reaches nothing for a program that is no agent at all (chrome-devtools-mcp) says so beside the
// agents. The launch explains the shared reason once and gives one next step for the group
// (2026-11-24: nine programs printed the same four-hundred-character sentence nine times).
// Driven through noteUseProfiles, so deleting the grouping call fails here.
func TestABareProfileExplainsASharedReasonOnceForEveryProgram(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "copilot"),
		officialPack(t, "agy"), officialPack(t, "chrome-devtools"), officialPack(t, "bedrock"), officialPack(t, "aws-auth")}
	got := profileLineChannelWith(t, packs, func(o *Options) { o.ProfileName = "bedrock" }, emptyEnv(), nil)
	var unreached []string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "reaches nothing for") {
			unreached = append(unreached, line)
		}
	}
	if len(unreached) != 1 {
		t.Fatalf("want one reaches-nothing line, got %d:\n%s", len(unreached), got)
	}
	line := unreached[0]
	if n := strings.Count(line, "names no endpoint, only platform"); n != 1 {
		t.Errorf("the shared reason is said %d times, want once:\n%s", n, line)
	}
	for _, want := range []string{"agy, chrome-devtools-mcp and copilot", "-p agy= -p chrome-devtools-mcp= -p copilot=", "-p <program>=<name>"} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q:\n%s", want, line)
		}
	}
}
