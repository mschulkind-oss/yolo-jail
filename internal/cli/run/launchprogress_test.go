package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// immediate shows every step at Start, so a fake step that returns at once still
// leaves the start and result lines a real slow one would.
var immediate = &progress.Config{Immediate: true}

// A live progress line on an interactive launch redraws on the terminal and never
// in launch.log: the tee's WriteTransient is the terminal half alone, and the step's
// result line still reaches both.
func TestTheLaunchLogHoldsNoProgressRedraws(t *testing.T) {
	var term, log bytes.Buffer
	tee := teeLog{w: &term, log: &log}
	line := progress.Config{Live: true, Immediate: true}.Start(tee, "Copying the image into podman")
	line.Set("layer 3 of 92 (40 MB of 3.2 GB)")
	line.Done("")
	if !strings.Contains(term.String(), "\r\x1b[KCopying the image into podman… layer 3 of 92") {
		t.Errorf("the terminal never saw the live frame: %q", term.String())
	}
	if strings.Contains(log.String(), "\r") {
		t.Errorf("launch.log received a redraw: %q", log.String())
	}
	if !strings.HasPrefix(log.String(), "Copying the image into podman: done — layer 3 of 92") {
		t.Errorf("launch.log is missing the step's result line: %q", log.String())
	}
}

// The rendering follows the launch STREAM: live on a terminal, lines anywhere else
// (a pipe, CI, a redirect) and on a dumb terminal, which cannot erase a line.
func TestProgressIsLiveOnlyOnARealTerminal(t *testing.T) {
	for _, tc := range []struct {
		tty  bool
		term string
		want bool
	}{
		{true, "xterm-256color", true},
		{false, "xterm-256color", false},
		{true, "dumb", false},
	} {
		o := &Options{
			IsTTYStderr: func() bool { return tc.tty },
			Getenv: func(k string) string {
				if k == "TERM" {
					return tc.term
				}
				return ""
			},
		}
		if got := o.progressConfig().Live; got != tc.want {
			t.Errorf("tty=%v TERM=%q: Live=%v, want %v", tc.tty, tc.term, got, tc.want)
		}
	}
}

// The image load is handed the launch's rendering — the call site that makes the
// image copy's progress live on a terminal. Delete the Progress line in
// autoLoadImage and this fails.
func TestTheImageLoadIsHandedTheLaunchRendering(t *testing.T) {
	var seen image.AutoLoadOptions
	o := &Options{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getenv:      func(string) string { return "xterm" },
		IsTTYStdout: func() bool { return false },
		IsTTYStderr: func() bool { return true },
		Getpid:      os.Getpid,
		PathExists:  func(string) bool { return false },
		autoLoad: func(opts image.AutoLoadOptions) image.LoadResult {
			seen = opts
			return image.LoadResult{OK: true}
		},
	}
	o.autoLoadImage(cfgWithPackages(t, `{}`), "podman", "/repo", storePackagesPlan{})
	if !seen.Progress.Live {
		t.Error("the image load got a line-oriented rendering on a terminal launch stream")
	}
	if seen.Progress.Width == nil {
		t.Error("the image load got no terminal width, so a live line could wrap")
	}
}

// The source build of yolo's own binaries narrates itself, and nix's summaries —
// which the build writes to o.Stderr — pass THROUGH the line rather than across it.
func TestThePrefixBuildIsNarrated(t *testing.T) {
	root := stageBundle(t, false)
	store := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := &Options{Stdout: &stdout, Stderr: &stderr, Progress: immediate}
	o.BuildJailPrefix = func(string) (string, []string) {
		fmt.Fprintln(o.Stderr, "Building yolo-jail-go")
		return store, nil
	}
	fillDefaults(o)
	if _, ok := o.resolveJailPrefix(root, "podman"); !ok {
		t.Fatal("resolveJailPrefix refused a checkout whose build succeeded")
	}
	s := stderr.String()
	start := strings.Index(s, "Building yolo's own binaries with nix…\n")
	summary := strings.Index(s, "Building yolo-jail-go\n")
	done := strings.Index(s, "Building yolo's own binaries with nix: done (")
	if start < 0 || summary < start || done < summary {
		t.Errorf("want start, nix summary, result in that order on stderr:\n%s", s)
	}
	if stdout.Len() != 0 {
		t.Errorf("progress reached stdout, the jailed command's stream: %q", stdout.String())
	}
}

// The opt-in store-delivered builds narrate themselves too.
func TestTheStorePackageBuildsAreNarrated(t *testing.T) {
	var stderr bytes.Buffer
	o := &Options{
		Stdout:   &bytes.Buffer{},
		Stderr:   &stderr,
		Progress: immediate,
		BuildImageExtras: func(string) (string, error) {
			return "/nix/store/extras", nil
		},
	}
	fillDefaults(o)
	if _, ok := o.addImageExtras(storePackagesPlan{Active: true}, "/repo"); !ok {
		t.Fatal("addImageExtras refused a build that succeeded")
	}
	if !strings.Contains(stderr.String(), "Building the image's bulk extras with nix: done (") {
		t.Errorf("the extras build was not narrated:\n%s", stderr.String())
	}
}

// The runtime probe is narrated (silent in practice: it answers in well under the
// grace period), and a probe that fails closes its line as failed.
func TestTheRuntimeProbeIsNarrated(t *testing.T) {
	var stderr bytes.Buffer
	o := &Options{
		Stdout:   &bytes.Buffer{},
		Stderr:   &stderr,
		Progress: immediate,
		Exec: func([]string, string, []string, time.Duration) ExecResult {
			return ExecResult{Ran: true, RC: 125, Stderr: "cannot connect"}
		},
	}
	fillDefaults(o)
	if ok, _ := o.runtimeIsConnectable("podman"); ok {
		t.Fatal("a probe that exited 125 reported the runtime connectable")
	}
	if !strings.Contains(stderr.String(), "Checking that podman is running: failed (") {
		t.Errorf("the runtime probe was not narrated:\n%s", stderr.String())
	}
}

// A launch that has to wait for another launch's housekeeping pass says so and
// shows the wait, instead of sitting silent between the nix build and the load
// decision; the pass it waits on has been measured at 62 s and 116 s.
func TestTheHousekeepingLockWaitIsNarrated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lockPath := HousekeepingLockPath()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	stderr := &syncBuffer{}
	o := &Options{Stdout: &bytes.Buffer{}, Stderr: stderr, Progress: immediate,
		Getenv: func(string) string { return "" }}
	lock := o.lockHousekeepingFn()
	got := make(chan func())
	go func() { got <- lock() }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr.String(), "Waiting for the housekeeping lock…") {
		if time.Now().After(deadline) {
			t.Fatalf("the waiter never announced itself:\n%s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
	(<-got)()
	s := stderr.String()
	if !strings.Contains(s, "Waiting for another launch's housekeeping pass to finish") {
		t.Errorf("no waiting notice:\n%s", s)
	}
	if !strings.Contains(s, "Waiting for the housekeeping lock: done (") {
		t.Errorf("the wait did not close:\n%s", s)
	}
}

// Starting the host services is narrated, after the disclosures — which are
// printed before the line starts, so no progress line can sit in front of one.
func TestTheHostServiceStartIsNarratedAfterItsDisclosures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	cname := "yolo-progress-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.Progress = immediate
	o.PathExists = func(string) bool { return false }

	o.startLoopholesDisclosed(cname, "podman", newConfig(), nil)
	if !strings.Contains(errBuf.String(), "Starting host services: done (") {
		t.Errorf("the host-service start was not narrated:\n%s", errBuf.String())
	}
}
