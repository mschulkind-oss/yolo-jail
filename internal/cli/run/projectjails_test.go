package run

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// THE LEGACY HOME'S MOVE WAITS FOR EVERY JAIL OF THE PROJECT (slice 6a-iii): once jail names are
// setup-qualified, a jail of another setup runs under a name of its own, so projectJails asks the
// legacy name, the selected setup's, and that of every setup with state under .yolo/setups. A jail
// running under another setup's name is running; a directory that names no setup names no jail.
func TestTheMoveAsksEverySetupsJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	for _, name := range []string{"lab", "Not_A_Setup"} {
		if err := os.MkdirAll(paths.SetupStateDir(ws, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := &Options{Workspace: ws, Setup: setup.MustBuiltIn("podman-self")}
	want := []string{yoloruntime.FromWorkspace(ws), yoloruntime.JailName(ws, "podman-self"), yoloruntime.JailName(ws, "lab")}
	if got := o.projectJailNames(); !slices.Equal(got, want) {
		t.Fatalf("projectJailNames = %v, want %v", got, want)
	}

	lab := yoloruntime.JailName(ws, "lab")
	o.LookPath = func(name string) (string, bool) { return "/bin/" + name, name == "podman" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if slices.Equal(argv, []string{"podman", "ps", "-a", "-q", "--filter", "name=^/" + lab + "$"}) {
			return ExecResult{Ran: true, Stdout: "0123abcd\n"}
		}
		return ExecResult{Ran: true}
	}
	if got := o.projectJails(); got != projectJailsRunning {
		t.Errorf("with the lab setup's jail running, projectJails = %v, want running", got)
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	if got := o.projectJails(); got != projectJailsStopped {
		t.Errorf("with no jail running, projectJails = %v, want stopped", got)
	}
}

// JailRunState is `yolo setup list`'s question (named-setups-plan.md slice 6b): a jail whose
// macos-user keeper holds its lock runs; with no keeper, no session and no container runtime on
// PATH, it is known stopped.
func TestJailRunStateAsksTheJailsKeepers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	ws := t.TempDir()
	cname := yoloruntime.JailName(ws, "seatbelt-self")
	if running, known := JailRunState(cname); running || !known {
		t.Errorf("with nothing of the jail, JailRunState = (%v, %v), want (false, true)", running, known)
	}
	held, err := holdLivenessLock(keeperKey(cname, keeperNotchMacosUser))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(held)
	if running, known := JailRunState(cname); !running || !known {
		t.Errorf("with its macos-user keeper alive, JailRunState = (%v, %v), want (true, true)", running, known)
	}
	if running, _ := JailRunState(yoloruntime.JailName(ws, "podman-self")); running {
		t.Error("another setup's keeper made this setup's jail run")
	}
}
