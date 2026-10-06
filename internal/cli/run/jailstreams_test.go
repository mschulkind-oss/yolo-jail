package run

// jailstreams_test.go pins where a launch relays the JAIL'S OWN lines before its ready: the
// runtime client's and pid 1's, which the keeper frames apart from its own (keeperframe.go).

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestTheJailsOwnLinesGoToItsJailWriters: what the runtime client prints when it refuses the
// container reaches Options.JailStdout and JailStderr, and not the launch's own writers. A build
// jail's act reads them there to say why a jail stopped before its build line ran (cli's jailTail,
// docs/design/patched-extensions.md PPX-D39). Red if Run's relay goes back to the process's own
// streams, which the act cannot read.
func TestTheJailsOwnLinesGoToItsJailWriters(t *testing.T) {
	packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	var jailOut, jailErr bytes.Buffer
	const said, refused = "the fixture runtime client says this on its stdout", "Error: the fixture runtime refused the container"
	argv, printed := fakePodmanLaunchIn(t, t.TempDir(), "echo '"+said+"'; echo '"+refused+"' >&2",
		func(o *Options) {
			o.JailStdout, o.JailStderr = &jailOut, &jailErr
			o.OnJailReady = func() { t.Error("OnJailReady was called for a jail the runtime refused") }
		})
	if argv == nil {
		t.Fatalf("the launch never reached the runtime:\n%s", printed)
	}
	if got := jailOut.String(); !strings.Contains(got, said) {
		t.Errorf("JailStdout holds %q, want the runtime client's stdout %q\nlaunch: %s", got, said, printed)
	}
	if got := jailErr.String(); !strings.Contains(got, refused) {
		t.Errorf("JailStderr holds %q, want the runtime client's stderr %q\nlaunch: %s", got, refused, printed)
	}
	if strings.Contains(printed, refused) || strings.Contains(printed, said) {
		t.Errorf("the jail's own lines reached the launch's writers:\n%s", printed)
	}
}

// TestTheLaunchSaysWhenTheJailsBootIsDone: Options.OnJailReady is called once, when the keeper
// relays the boot done, after the boot's lines reached JailStderr; a build jail's act reads it to
// tell a boot that went on to be done from one that stopped by it (cli's jailTail). Red if Run
// stops calling it from the relay's ready.
func TestTheLaunchSaysWhenTheJailsBootIsDone(t *testing.T) {
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	// A KEEPER THAT ONLY RELAYS: its start, one line of pid 1's boot, the boot done; then it holds
	// until the launch lets go of its lifeline.
	defaultKeeperSpawner = func(_ *Options, planPath string, progress, lifeline, _ *os.File, _ []*os.File) (func() int, error) {
		removeKeeperPlan(planPath)
		prog, err := dupCloseOnExec(progress)
		if err != nil {
			return nil, err
		}
		life, err := dupCloseOnExec(lifeline)
		if err != nil {
			_ = prog.Close()
			return nil, err
		}
		for _, f := range []struct {
			tag     byte
			payload string
		}{{frameStarted, "4242"}, {frameJailStderr, "a boot line\n"}, {frameReady, ""}} {
			if err := writeFrame(prog, f.tag, []byte(f.payload)); err != nil {
				_ = prog.Close()
				_ = life.Close()
				return nil, err
			}
		}
		_ = prog.Close()
		return func() int {
			_, _ = io.Copy(io.Discard, life)
			_ = life.Close()
			return 0
		}, nil
	}
	var stdout, stderr, jailErr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.RestoreTerminal = func() {}
	o.JailStderr = &jailErr
	var readies int
	var atReady string
	o.OnJailReady = func() { readies++; atReady = jailErr.String() }
	done := make(chan struct{})
	go func() { defer close(done); Run(*o) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the launch did not end\nstderr:\n%s", stderr.String())
	}
	if readies != 1 || !strings.Contains(atReady, "a boot line") {
		t.Errorf("OnJailReady was called %d times, with JailStderr holding %q at the first; want once, after "+
			"the boot's line\nstderr:\n%s", readies, atReady, stderr.String())
	}
}
