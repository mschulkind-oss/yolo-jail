package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// A LINKED `.yolo` IS NOT READ BY HOST `yolo check` (DS-D3): the jail can point it at another
// host directory, whose entry names this section would otherwise print. The section says why
// it read nothing, and no name from the other directory appears.
func TestTheHostDurableSectionDoesNotFollowALinkedYolo(t *testing.T) {
	ws, _ := durableCheckFixture(t)
	other, _ := durableCheckFixture(t)
	if err := os.MkdirAll(filepath.Join(other, ".yolo", "durable", "private-name"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(ws, ".yolo")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, ".yolo"), filepath.Join(ws, ".yolo")); err != nil {
		t.Fatal(err)
	}
	o := &Options{Getenv: func(string) string { return "" }, Now: func() time.Time { return durableNow }}
	out := runDurableSection(o, ws)
	if strings.Contains(out, "private-name") || strings.Contains(out, "worktrees/") {
		t.Errorf("host check read through a linked .yolo:\n%s", out)
	}
	if !strings.Contains(out, "not read: `.yolo` is a symbolic link") {
		t.Errorf("host check did not say why it read nothing:\n%s", out)
	}
}

// IN A JAIL, A CHECK OF ANOTHER WORKSPACE reports THAT workspace's durable dir, not the one
// $YOLO_DURABLE_DIR names (`cd /tmp/yolo-nested && yolo check`).
func TestAnInJailCheckOfAnotherWorkspaceReportsThatWorkspace(t *testing.T) {
	_, outerDir := durableCheckFixture(t)
	nested, nestedDir := durableCheckFixture(t)
	env := map[string]string{"YOLO_VERSION": "9.9.9", durable.EnvVar: outerDir}
	o := &Options{
		Getenv: func(k string) string { return env[k] },
		Now:    func() time.Time { return durableNow },
		Exec: func([]string, string, []string, time.Duration) ExecResult {
			return ExecResult{Ran: true, Stdout: "0\n"}
		},
	}
	out := runDurableSection(o, nested)
	if !strings.Contains(out, "  "+nestedDir+"  ") || strings.Contains(out, outerDir) {
		t.Errorf("the check of %s reported another dir:\n%s", nested, out)
	}
}

// ONE BUDGET BOUNDS THE WHOLE SECTION, git included: once it is spent no further git runs,
// and the columns say they were not computed.
func TestTheInJailDurableSectionStopsRunningGitAtItsBudget(t *testing.T) {
	ws, dir := durableCheckFixture(t)
	env := map[string]string{"YOLO_VERSION": "9.9.9", durable.EnvVar: dir}
	now := durableNow
	var ran int
	o := &Options{
		Getenv: func(k string) string { return env[k] },
		Now:    func() time.Time { return now },
		Exec: func([]string, string, []string, time.Duration) ExecResult {
			ran++
			now = now.Add(durableCheckBudget) // every git call spends the whole budget
			return ExecResult{Ran: true, Stdout: "0\n"}
		},
	}
	out := runDurableSection(o, ws)
	if ran != 1 {
		t.Errorf("git ran %d times past a spent budget", ran)
	}
	if !strings.Contains(out, "not computed") {
		t.Errorf("the columns past the budget do not say so:\n%s", out)
	}
}

// The footer says what deletes the directory, in the briefing's words (DS-D31); its removal
// advice is the one that keeps uncommitted work; and jail-chosen names reach the host
// terminal without their control characters.
func TestTheDurableSectionFooterAndNamesAreSafe(t *testing.T) {
	ws, dir := durableCheckFixture(t)
	if err := os.Mkdir(filepath.Join(dir, "evil\x1b[2Jname"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Options{Getenv: func(string) string { return "" }, Now: func() time.Time { return durableNow }}
	out := runDurableSection(o, ws)
	if !strings.Contains(out, "yolo never deletes anything here; a "+durable.CleanCommand+
		" in the workspace does. "+durable.RemoveAdvice) {
		t.Errorf("the footer does not say what deletes the dir and give the safe removal advice:\n%s", out)
	}
	if strings.Contains(out, "\x1b") || !strings.Contains(out, "evil[2Jname") {
		t.Errorf("a jail-chosen name reached the terminal unfiltered:\n%q", out)
	}
}
