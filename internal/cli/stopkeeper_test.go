package cli

// stopkeeper_test.go pins what `yolo stop` does with a jail's keeper
// (docs/design/jail-lifetime-last-session-wins.md JL-D25, JL-D30), and that the CLI wires the
// keeper's daemon member.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/internaldaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestAStopWaitsForTheJailsKeeper is the call-site pin on JL-D25: after the runtime's stop, `yolo
// stop` hands the keeper's teardown to run.FinishStop and says "Stopped" only once that has finished;
// a teardown that did not finish is the stop's failure. A jail already ending, whose keeper is still
// at work, is waited for too.
func TestAStopWaitsForTheJailsKeeper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	var got []string
	finishRC := 0
	finishStop = func(stdout, _ io.Writer, ws, rt string, _ int64, capture func(string, string)) int {
		got = append(got, ws+" "+rt)
		if capture == nil {
			t.Error("the stop hands the reap no config capture")
		}
		fmt.Fprintln(stdout, "keeper teardown")
		return finishRC
	}
	var out, errb bytes.Buffer
	s := &stopRun{stats: []string{"true\n", ""}}
	if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil); rc != 0 {
		t.Fatalf("rc=%d, err=%s", rc, errb.String())
	}
	if len(got) != 1 || got[0] != "/ws podman" {
		t.Fatalf("the stop handed the teardown to FinishStop %v", got)
	}
	if i, j := strings.Index(out.String(), "keeper teardown"), strings.Index(out.String(), "Stopped "); i < 0 || j < i {
		t.Errorf("\"Stopped\" printed before the teardown finished:\n%s", out.String())
	}
	finishRC = 1
	out.Reset()
	s = &stopRun{stats: []string{"true\n", ""}}
	if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil); rc != 1 || strings.Contains(out.String(), "Stopped ") {
		t.Errorf("a teardown that did not finish: rc=%d\n%s", rc, out.String())
	}
	// Already ending: the container is gone and its keeper still holds the jail.
	finishRC, got = 0, nil
	cname := runtime.FromWorkspace("/ws")
	lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	s = &stopRun{stats: []string{"false\n"}}
	if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil); rc != 0 || len(got) != 1 ||
		!strings.Contains(out.String(), "already ending") {
		t.Errorf("a jail already ending was not waited for: rc=%d got=%v\n%s", rc, got, out.String())
	}
}

// TestTheCLIWiresTheJailKeeper: the `jail-keeper` member reaches run.KeeperMain, whose own misuse
// answer it gives, rather than internaldaemon's "not wired" refusal.
func TestTheCLIWiresTheJailKeeper(t *testing.T) {
	if internaldaemon.JailKeeper == nil {
		t.Fatal("the CLI does not wire the jail-keeper member")
	}
	saved := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	rc := internaldaemon.Run([]string{run.KeeperVerb, "--unknown"})
	_ = w.Close()
	os.Stderr = saved
	var errb bytes.Buffer
	_, _ = errb.ReadFrom(r)
	if rc != 2 || !strings.Contains(errb.String(), "jail-keeper: unexpected argument") {
		t.Errorf("rc=%d stderr=%q, want the keeper's own misuse answer", rc, errb.String())
	}
}
