package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// concurrentlaunch_test.go covers what two `yolo` processes do to each other in ONE
// workspace — the workspace flock, the notice it prints while blocked, and the attach
// that ends the wait.
//
// It is an integration test because the whole mechanism is inter-PROCESS: the flock is
// held across a fork of the launcher, released only once a real container is running,
// and the payoff is a `podman exec` into a container this process did not create. A unit
// test can (and does, in internal/cli/run/flock_test.go) prove that acquireWorkspaceLock
// emits the notice on contention; only a real pair of launches proves that the run
// pipeline still calls it with the seam wired, and that the loser reaches the container
// instead of colliding with it.

// syncBuffer is an io.Writer safe to read while a child process writes to it — the test
// polls a running launch's output to decide when to start the second one.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// bgRun is a `yolo` invocation running in the BACKGROUND, so a second one can be started
// while it holds the workspace lock. runCommand cannot serve here by construction: it
// waits for the process it starts.
type bgRun struct {
	name string
	out  *syncBuffer
	done chan error
}

// combined returns everything the run has printed so far (stdout and stderr interleaved
// as the terminal would show them — the notices under test are ordered relative to each
// other, so splitting the streams would lose the ordering).
func (b *bgRun) combined() string { return b.out.String() }

// wait blocks for the run to finish, returning its exit code.
func (b *bgRun) wait(t *testing.T, limit time.Duration) int {
	t.Helper()
	select {
	case err := <-b.done:
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		t.Fatalf("%s: yolo failed to run: %v\n%s", b.name, err, b.combined())
		return -1
	case <-time.After(limit):
		t.Fatalf("%s: still running after %s\n%s", b.name, limit, b.combined())
		return -1
	}
}

// startYoloBackground launches `yolo run -- bash -lc <script>` in dir without waiting for
// it, mirroring runCommand's environment exactly (TERM=dumb, repo-root propagation) so the
// two helpers cannot drift on what a spawned CLI sees.
func startYoloBackground(t *testing.T, name, dir, script string) *bgRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	args := append(jailRunArgs(), "--", "bash", "-lc", script)
	cmd := exec.CommandContext(ctx, yoloBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)
	cmd.Env = append(cmd.Env, autoCaptureEnvForSuite()...)
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("%s: starting yolo: %v", name, err)
	}
	r := &bgRun{name: name, out: out, done: make(chan error, 1)}
	go func() { r.done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(30 * time.Second):
		}
	})
	return r
}

// lockIsHeld reports whether some OTHER process holds the workspace flock. The probe
// takes the lock non-blockingly and drops it again on success, so it never becomes the
// contention it is watching for.
func lockIsHeld(t *testing.T, lockPath string) bool {
	t.Helper()
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening %s: %v", lockPath, err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// TestConcurrentLaunchesInOneWorkspace: two launches, one workspace. The second must say
// it is waiting, wait, and then attach to the container the first created.
//
// The sequencing is what makes the assertion deterministic rather than hopeful. The second
// launch is started only once the lock is observably HELD, which is a window the first
// launch keeps open for its whole image-load-and-create phase (the lock is released from
// on_started, after the container is running). Starting both at once instead would leave
// the notice to a coin flip: a second launch that arrives after the container exists never
// contends at all, it just attaches.
func TestConcurrentLaunchesInOneWorkspace(t *testing.T) {
	requireJail(t)

	// Empty config: no packs, so neither launch installs an agent CLI. This test is about
	// the launcher's serialisation, and every second of provisioning it can avoid is a
	// second off both launches.
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)
	lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".lock")

	// The first launch holds its container open until the test releases it, via the live
	// /workspace bind — the same directory this process writes the sentinel into.
	const releaseName = "release-first-launch"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-JAIL-UP; `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done; `+
			`echo FIRST-JAIL-DONE`)
	release := func() { _ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644) }
	t.Cleanup(release)

	// Wait for the first launch to own the lock.
	deadline := time.Now().Add(jailTimeout())
	for !lockIsHeld(t, lockPath) {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before taking the workspace lock:\n%s",
				err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first launch never took the workspace lock %s within %s:\n%s",
				lockPath, jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Second launch, into the same workspace, while the lock is held.
	second := startYoloBackground(t, "second", dir, `echo SECOND-JAIL-COMMAND-RAN`)
	rc := second.wait(t, jailTimeout())
	got := second.combined()

	// (1) The waiting notice — the defect this test exists for. Before the non-blocking
	// probe went in, the second launch printed NOTHING here and read as a hang.
	var missing []string
	for _, want := range []string{
		"Waiting for concurrent jail launch",
		dir,             // which workspace is ahead of you
		cname + ".lock", // and which lock file to look at
	} {
		if !strings.Contains(got, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Errorf("second launch's waiting notice is missing %q (the silent-wait defect: "+
			"it blocked on the workspace flock and said nothing):\n%s", missing, got)
	}

	// (2) Race resolution + graceful attachment: the loser reports the attach rather than
	// doing it silently, and its command actually runs in the winner's container.
	if !strings.Contains(got, "Attaching to jail started by another process") {
		t.Errorf("second launch did not report attaching to the first launch's jail:\n%s", got)
	}
	if !strings.Contains(got, "SECOND-JAIL-COMMAND-RAN") {
		t.Errorf("second launch's command did not run in the attached jail:\n%s", got)
	}
	if rc != 0 {
		t.Errorf("second launch rc = %d, want 0:\n%s", rc, got)
	}

	// (3) One workspace, one container: the second launch must not have created a rival.
	if n := runningContainers(t, cname); n != 1 {
		t.Errorf("%d containers named %s are running, want exactly 1 (the race guard "+
			"exists to keep two launches from creating two jails)", n, cname)
	}

	// (4) The first launch is unharmed by having been attached to.
	release()
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("first launch rc = %d, want 0:\n%s", rc, first.combined())
	}
	if out := first.combined(); !strings.Contains(out, "FIRST-JAIL-UP") ||
		!strings.Contains(out, "FIRST-JAIL-DONE") {
		t.Errorf("first launch did not run its command to completion:\n%s", out)
	}
}

// runningContainers counts running containers with exactly this name.
func runningContainers(t *testing.T, cname string) int {
	t.Helper()
	rt := detectRuntime()
	if rt == "" {
		t.Fatal("no container runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, rt, "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("%s ps: %v", rt, err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == cname {
			n++
		}
	}
	return n
}
