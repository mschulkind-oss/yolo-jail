package integration

// attachskewbriefing_test.go is the real-runtime pin on SK-D15
// (docs/design/attach-skew-and-contract-guardrails.md, OQ-SK4): an attach that goes ahead under
// YOLO_ALLOW_ATTACH_SKEW names the skew in the briefing it refreshes for the running jail. The
// unit tier pins the section and each skew's route into it against a faked runtime
// (internal/cli/run/attachskewbriefing_test.go); here the jail's frozen environment is a real
// container's, read by a real inspect.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAnAcknowledgedAttachNamesTheSkewInTheBriefing: the stand-in older jail cannot receive
// claude's zai profile. With the acknowledgment the attach goes ahead, says on stderr that the
// briefing carries the difference, and the claude briefing staged for that jail names the version
// it was launched with, the tag it lacks and the withheld names, never the key's value. The
// stand-in has no entrypoint to exec, so the command itself does not run; the briefing is written
// before the exec.
func TestAnAcknowledgedAttachNamesTheSkewInTheBriefing(t *testing.T) {
	requireJail(t)
	dir, cname := startStandInOlderJail(t)

	r := runCommand(t, dir, append(jailRunArgs(), "-p", "claude=zai", "--", "true"),
		withEnv("YOLO_ALLOW_ATTACH_SKEW=1"))
	got := r.combined()
	for _, want := range []string{"YOLO_ALLOW_ATTACH_SKEW is set", "agent-env-files",
		"The jail's briefing names this difference as well", "Attaching to existing jail"} {
		if !strings.Contains(got, want) {
			t.Errorf("the acknowledged attach must say %q:\n%s", want, got)
		}
	}

	staged, err := filepath.Glob(filepath.Join(paths.AgentsDir(), cname, "briefing-*"))
	if err != nil || len(staged) == 0 {
		t.Fatalf("no briefing staged for %s (%v):\n%s", cname, err, got)
	}
	var claude string
	for _, f := range staged {
		if strings.Contains(filepath.Base(f), "CLAUDE.md") {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			claude = string(b)
		}
	}
	for _, want := range []string{
		"## ⚠ This session runs in a jail that could not take what started it",
		"**The jail was launched with**: yolo 0.10.0",
		"**It lacks the `agent-env-files` contract**",
		"claude (profile zai): ",
	} {
		if !strings.Contains(claude, want) {
			t.Errorf("the claude briefing staged for the jail does not say %q:\n%s", want, claude)
		}
	}
	if strings.Contains(claude, "integration-probe-not-a-real-key") {
		t.Error("the briefing carries the key's value")
	}
}
