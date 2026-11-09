package run

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestRunReleasesTheProjectLockOnceItsJailIsUp (review m5): a launch that finds the legacy home in
// place decides about it under the PROJECT lock (holdHomeLock, SUP-D36) and holds it until its jail
// is up, then lets it go (Run's releaseHomeLock at READY), so a launch of another setup of the
// project need not wait for this session to end. It drives Run down the podman fresh-launch path
// to a real ready keeper (TestMain's in-process one) and, at runContainer's clock read for its
// first session's start, after READY, asks for the project lock without waiting. Deleting Run's
// release at READY fails this: the lock is then held until the launch returns.
func TestRunReleasesTheProjectLockOnceItsJailIsUp(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": []}`+"\n")
	ws := t.TempDir()
	seedLegacyHome(t, ws)
	cname := yoloruntime.JailName(ws, "podman-self")
	project := yoloruntime.FromWorkspace(ws)
	jail := newFakeJail(t, cname)
	started := filepath.Join(jail.dir, "started")
	bin := heldDir(t)
	// The runtime: `run` is the main process, its boot and a hold until the fake stop; `exec` is the
	// first session, which ends at once. Every other runtime call is o.Exec's.
	podman := "#!/bin/sh\ncase \"$1\" in\nrun)\n  " + recordPID(bin) + "\n  : > '" + started + "'\n" +
		"  echo " + shellQuoteForTest(entrypoint.BootReadyLine) + " >&2\n" +
		"  " + holdUntil(filepath.Join(jail.dir, "stop")) + "\n  exit 143;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(podman), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")

	o := dispatchOptions(t, ws, "podman", new(bytes.Buffer), new(bytes.Buffer), nil)
	var stdout, stderr lockedBuffer
	o.Stdout, o.Stderr = &stdout, &stderr
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	// No container of the name until the main process has started, so the launch is a fresh one.
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "ps" {
			if _, err := os.Stat(started); err != nil {
				return ExecResult{Ran: true}
			}
		}
		return jail.exec(argv, dir, env, timeout)
	}
	o.PIDAlive = func(int) bool { return false }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.CapturesDir = func() string { return "" }
	o.AcceptConfigChanges = true
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })

	// AFTER READY: runContainer's one clock read before its first session runs.
	var fired atomic.Bool
	var free atomic.Bool
	o.Now = func() time.Time {
		if pc, _, _, ok := goruntime.Caller(1); ok && strings.HasSuffix(goruntime.FuncForPC(pc).Name(), ".runContainer") &&
			fired.CompareAndSwap(false, true) {
			if lock, ok := tryWorkspaceLock(project); ok {
				free.Store(true)
				lock.Close()
			}
		}
		return time.Unix(0, 0)
	}

	rc := -1
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		rc = Run(*o)
	}()
	select {
	case <-returned:
	case <-time.After(sealedLaunchBound):
		t.Fatalf("the launch had not returned after %s\nstdout:\n%s\nstderr:\n%s",
			sealedLaunchBound, stdout.String(), stderr.String())
	}
	printed := stdout.String() + stderr.String()
	if !strings.Contains(printed, "Moved this project's home") {
		t.Fatalf("the launch did not move the legacy home, so it never took the project lock and this test "+
			"says nothing (rc %d):\n%s", rc, printed)
	}
	if !fired.Load() {
		t.Fatalf("the launch never reached its first session's start (rc %d):\n%s", rc, printed)
	}
	if !free.Load() {
		t.Errorf("the project lock (%s) was still held after the jail was up, so another setup's launch "+
			"would wait for this session to end (rc %d):\n%s", launchLockPath(project), rc, printed)
	}
}
