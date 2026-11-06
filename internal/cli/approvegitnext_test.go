package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
)

// Every `yolo …` command a git push refusal or a pending gh write names as its next step,
// whether in backticks or alone on its own indented line in the menu, is one this CLI runs:
// its subcommand is registered, and `yolo approve` takes the arguments the step gives it,
// reaching the store (an unknown request is exit 4, never a usage error).
func TestGitRefusalNextStepsNameRealCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	cmd := regexp.MustCompile("`(yolo [^`]*)`")
	seen := map[string]bool{}
	steps := append(gitbroker.RefusalNextSteps(), ghbroker.PendingMessageSamples()...)
	for _, step := range steps {
		for _, m := range cmd.FindAllStringSubmatch(step, -1) {
			seen[m[1]] = true
		}
		for _, line := range strings.Split(step, "\n") {
			if c, ok := strings.CutPrefix(line, "    "); ok && strings.HasPrefix(c, "yolo ") {
				seen[strings.TrimSpace(c)] = true
			}
		}
	}
	const id = "r-00000000000000000000000000000000"
	for _, must := range []string{"yolo wait " + id, "yolo approve " + id, "yolo approve " + id + " --for 15m", "yolo request git push main", "yolo config apply"} {
		if !seen[must] {
			t.Errorf("no next step names %q", must)
		}
	}
	// --add and --remember are removed (GB-D28, OQ-GW2): no refusal offers them, in a menu
	// line or inside a sentence.
	for _, step := range steps {
		for _, removed := range []string{"--add", "--remember"} {
			if strings.Contains(step, removed) {
				t.Errorf("a next step still offers the removed %s:\n%s", removed, step)
			}
		}
	}
	for line := range seen {
		args := strings.Fields(strings.ReplaceAll(line, "<id>", "r-ffffffffffffffffffffffffffffffff"))
		run, ok := registry[args[1]]
		if !ok {
			t.Errorf("%q names a subcommand this CLI does not have", line)
			continue
		}
		if args[1] != "approve" {
			continue
		}
		want := 0
		if len(args) > 2 {
			want = approveExitUnknownID
		}
		rc, out, errs := captureStd(t, func() int { return run(args[1:]) })
		if rc != want {
			t.Errorf("%q exited %d, want %d:\n%s%s", line, rc, want, out, errs)
		}
	}
}
