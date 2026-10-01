package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// concurrentlaunch_test.go covers what two `yolo` processes do to each other in ONE
// workspace — the workspace flock, the notice it prints while blocked, and the attach
// that ends the wait — and what several do to each other across workspaces through the
// one lock they share machine-wide, the image-copy lock
// (TestImageCopyLockSerializesConcurrentLaunches).
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
	// pid is the launcher's own process, for a test that kills it outright.
	pid  int
	out  *syncBuffer
	done chan error
	// exited is closed once the process has been reaped. done carries the ONE result, which a
	// caller consumes; exited is what the cleanup waits on, because it stays readable after that.
	exited chan struct{}
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
//
// env is withEnv's twin: whole KEY=VALUE strings appended to the LAUNCHER's environment,
// last, so a pair here wins over the inherited one.
func startYoloBackground(t *testing.T, name, dir, script string, env ...string) *bgRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	args := append(jailRunArgs(), "--", "bash", "-lc", script)
	cmd := exec.CommandContext(ctx, yoloBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)
	cmd.Env = append(cmd.Env, autoCaptureEnvForSuite()...)
	cmd.Env = append(cmd.Env, env...)
	awaitDetachedWriters(t, dir, launchHome(cmd.Env))
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("%s: starting yolo: %v", name, err)
	}
	r := &bgRun{name: name, pid: cmd.Process.Pid, out: out, done: make(chan error, 1), exited: make(chan struct{})}
	go func() {
		r.done <- cmd.Wait() // buffered: never blocks, whether or not anyone reads it
		close(r.exited)
	}()
	// The cleanup waits on EXITED, not on done. It used to receive from done, the channel
	// wait() had already drained — so every run a test had waited for sat out the full 30s
	// here instead of returning at once. Measured: 30s of TestAnAttachKeepsThePackTreeTheJailBootedWith's
	// 33.4s, 90s of TestImageCopyLockSerializesConcurrentLaunches' 98.5s (three runs, three
	// sequential cleanups), 60s of TestConcurrentLaunchesInOneWorkspace. The 30s bound still
	// covers the case it is for: a run nobody waited for, killed by cancel(), taking a moment
	// to be reaped.
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.exited:
		case <-time.After(30 * time.Second):
		}
	})
	return r
}

// lockIsHeld reports whether some OTHER process holds the workspace flock. The probe
// takes the lock non-blockingly and drops it again on success, so it never becomes the
// contention it is watching for.
//
// ⚠ "NOT THERE YET" IS NOT AN ERROR HERE, AND TREATING IT AS ONE MADE THIS TEST FAIL ON A
// SLOW MACHINE. This used to t.Fatalf on any open error — inside a poll loop whose whole
// purpose is to wait for that path to appear. The lock lives under
// `<storage>/locks/`, a directory the LAUNCH creates; until the first launch gets that
// far, O_CREATE cannot make the file because its parent does not exist, and the probe
// killed the test on the first iteration.
//
// Measured on the macOS nightly 2026-09-15, shard 9 of run 34995936829:
//
//	opening …/.local/share/yolo-jail/locks/yolo-002-f954046a.lock:
//	open …: no such file or directory
//
// It failed in 0.51s — before the first launch could plausibly have started a container —
// while the same test passes in ~124s on an unloaded machine, which is why it reads as a
// concurrency bug and is really a startup race. The caller already has a deadline and a
// message for "never took the lock"; this returns false so that deadline is what decides.
func lockIsHeld(t *testing.T, lockPath string) bool {
	t.Helper()
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrNotExist) {
		// The launch has not created <storage>/locks/ yet. Not held, and not a failure.
		return false
	}
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

// awaitLaunchLockReleased blocks until the background launch first has released the
// workspace's launch lock, so the next launch of dir attaches without waiting.
//
// A session's output is NOT that sync point. A fresh launch releases the lock from onStarted,
// once `podman ps` shows its container running (awaitRunningContainer's poll), and the
// container's command can print before that poll answers. A launch started on the session's
// marker alone can therefore find the lock still held, wait for it, and attach through the
// raced arm ("Attaching to jail started by another process"), so a test asserting "Attaching to
// existing jail" fails by timing. Measured in two full-suite runs on 2026-09-28: both
// TestAttachDeliversTheSelectedProfile and TestAnAttachKeepsThePackTreeTheJailBootedWith in the
// first, the profile test again in the second, and each passing when run alone.
func awaitLaunchLockReleased(t *testing.T, dir string, first *bgRun) {
	t.Helper()
	lockPath := filepath.Join(paths.GlobalStorage(), "locks", naming.FromWorkspace(dir)+".lock")
	deadline := time.Now().Add(jailTimeout())
	for lockIsHeld(t, lockPath) {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) while still holding the workspace lock:\n%s",
				err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first launch still holds the workspace lock %s after %s:\n%s",
				lockPath, jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
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
	return runningContainersOn(t, rt, cname)
}

// runningContainersOn is runningContainers on the runtime rt. Apple Container has no `ps` and no
// Go-template format: its `container ls` is a table of running containers whose first column is
// the name yolo gave it, which runtime.ParseContainerLsLive reads as the launch's own probes do.
func runningContainersOn(t *testing.T, rt, cname string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if rt == "container" {
		out, err := exec.CommandContext(ctx, rt, "ls").Output()
		if err != nil {
			t.Fatalf("%s ls: %v", rt, err)
		}
		if _, ok := naming.ParseContainerLsLive(string(out))[cname]; ok {
			return 1
		}
		return 0
	}
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

// The strings TestImageCopyLockSerializesConcurrentLaunches reads a launch's output for,
// spelled literally rather than imported: this suite observes the shipped artifact rather than
// agreeing with the code under test. Each was checked against the tree when this test was
// written; the source line is named beside it.
const (
	// internal/image/copylock.go, lockImageCopy: printed when the LOCK_NB probe finds the
	// machine-wide lock held, before the blocking acquire.
	copyLockWaitNotice = "Waiting for another launch to finish copying an image"
	// internal/image/autoload.go, the peerDelivered report: the re-inspect under the lock found
	// the content ref the peer delivered, so this launch copied nothing.
	copyLockPeerNotice = "was delivered by a concurrent launch while this one waited for the image-copy lock"
	// internal/image/storewrite.go, StoreWriteNote: every one of its three variants starts with
	// this, and it is printed only on the containers-storage copy arm, immediately before the
	// copy runs. So it is the mark of a launch that REALLY COPIED.
	copyLockRealCopyNote = "  Store write:"
	// copyLockHoldRepoPrefix names the images this test keeps alive while it removes their
	// jail-repo names — see evictJailImageRefs. Its own repository, so no yolo reaper (they all
	// filter on the jail repository) and no imageExists probe ever sees it. The repository a run
	// actually uses carries its PID (copyLockHoldRepo), and a run sweeps the names of runs whose
	// PID is gone (sweepStaleHoldNames).
	copyLockHoldRepoPrefix = "localhost/yolo-integration-copylock-hold"
)

// copyLockHoldRepo is THIS process's hold repository: the prefix plus its PID.
//
// PER PROCESS, because one shared name was a live defect. Two runs that both evicted names
// tagged the same image under the same `<prefix>:<id>`, and whichever finished first untagged
// it — measured 2026-09-28 as `could not drop the hold name …: tag not known` in the run that
// finished second, and worse in the other order: with the only surviving name gone, a run
// still mid-test holds an image nothing names, which a reaper may take. The machine lock
// (machinelock_test.go) keeps two of these tests from overlapping at all now; the per-process
// name is what keeps one run's cleanup from ever touching a name another run made, overlap or
// not — a run of an older tree does not take the lock.
func copyLockHoldRepo() string {
	return copyLockHoldRepoPrefix + "-" + strconv.Itoa(os.Getpid())
}

// staleHoldNames returns the hold names in refs whose owning run is gone: a name under
// copyLockHoldRepoPrefix-<pid> whose pid alive() reports dead. A name without a PID suffix —
// what trees before the per-process name wrote — is NOT returned: an older run cannot be told
// apart from a live one, so its names are left for a human (`podman untag` them by name).
func staleHoldNames(refs []string, alive func(pid int) bool) []string {
	var stale []string
	for _, ref := range refs {
		repo, _, ok := strings.Cut(ref, ":")
		if !ok {
			continue
		}
		pidText, ok := strings.CutPrefix(repo, copyLockHoldRepoPrefix+"-")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid <= 0 || alive(pid) {
			continue
		}
		stale = append(stale, ref)
	}
	return stale
}

// pidAlive reports whether pid names a process: signal 0 delivers nothing, and EPERM is an
// answer too (the process exists and belongs to someone else).
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// sweepStaleHoldNames untags the hold names a killed run left behind. A left-behind hold name
// pins a whole jail image that no yolo reaper will ever consider (they filter on the jail
// repository), so a per-process name without this sweep would trade one run's clobbered name
// for a disk leak per killed run.
func sweepStaleHoldNames(t *testing.T, rt string) {
	t.Helper()
	out, err := imageCmd(rt, "images", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		t.Logf("listing images to sweep stale hold names: %v\n%s", err, out)
		return
	}
	for _, ref := range staleHoldNames(strings.Fields(out), pidAlive) {
		if o, err := imageCmd(rt, "untag", ref); err != nil {
			t.Logf("could not untag the stale hold name %s (a killed run's): %v\n%s", ref, err, o)
		} else {
			t.Logf("untagged %s, a hold name a killed run left behind", ref)
		}
	}
}

// podmanRootless logs and returns `podman info --format '{{.Host.Security.Rootless}}'`: "true",
// "false", or "unknown (<why>)". TestOpenAIAuthBrokerRoundTripsAnImportedToken uses it too.
//
// It exists for the tests whose green means something only on a ROOTLESS host — the rootless
// store write, and anything reached across the host loopback. A nested jail's podman is rootful
// by construction (AGENTS.md's carve-outs), so the same test passing there settles nothing, and
// AGENTS.md asks that such a claim be reported WITH this value. Logging it from the test itself
// puts the answer in the run that made the claim. It never fails a test: it is a witness.
func podmanRootless(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "podman", "info", "--format", "{{.Host.Security.Rootless}}").Output()
	v := strings.TrimSpace(string(out))
	if err != nil || v == "" {
		v = fmt.Sprintf("unknown (%v)", err)
	}
	t.Logf("podman info --format '{{.Host.Security.Rootless}}' = %s", v)
	return v
}

// jailImageTag is one name under the jail image repository and the image ID it points at.
type jailImageTag struct{ ref, id string }

// imageCmd runs one runtime command with a bound, returning its combined output.
func imageCmd(rt string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, rt, args...).CombinedOutput()
	return string(out), err
}

// listJailImageTags returns every `localhost/yolo-jail:<tag>` the runtime holds, with its image
// ID. The prefix is re-checked here, anchored at the tag separator, so a neighbouring repository
// such as `yolo-jail-builder` can never be taken for one of these.
func listJailImageTags(t *testing.T, rt string) []jailImageTag {
	t.Helper()
	repo := "localhost/" + jailImageRepo
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, rt, "images", "--format",
		"{{.Repository}}:{{.Tag}} {{.ID}}", repo).Output()
	if err != nil {
		t.Fatalf("listing %s images: %v", repo, err)
	}
	var tags []jailImageTag
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.Contains(f[0], "<none>") || !strings.HasPrefix(f[0], repo+":") {
			continue
		}
		tags = append(tags, jailImageTag{ref: f[0], id: f[1]})
	}
	return tags
}

// evictJailImageRefs removes every NAME the jail image repository holds while keeping the
// IMAGES, and restores the exact name-to-image mapping when the test ends.
//
// NAMES, BECAUSE THEY ARE WHAT A LAUNCH ASKS FOR. A launch skips the build when its stock tag
// is present and skips the copy when its content-addressed ref is (internal/image/autoload.go),
// so only with both gone does it reach the image-copy lock at all. `:latest` goes too, so no
// name in the repository survives to satisfy anything.
//
// IMAGES KEPT, which is the difference from the roadmap row's "cold store", stated rather than
// hidden: each image first gets a name in copyLockHoldRepo, so its layers stay in the store and
// the one real copy negotiates them all as already present. What this test measures — that N
// launches take turns at the lock and that every launch after the first re-inspects and copies
// nothing — does not depend on how many bytes the first copy moves; a cold layer store only
// makes that one copy longer. Keeping the layers is also what makes the restore safe: every
// name goes back onto an image that never left, with no rebuild and no re-copy.
//
// Never `rmi`: with the hold name in place it would only untag, but without it (a tag call that
// silently failed) it would delete the image, and `untag <id> <name>` removes exactly the one
// name it is given.
func evictJailImageRefs(t *testing.T, rt string) []jailImageTag {
	t.Helper()
	tags := listJailImageTags(t, rt)
	ids := map[string]bool{}
	for _, tg := range tags {
		ids[tg.id] = true
	}
	hold := copyLockHoldRepo()
	var held []string
	for id := range ids {
		if out, err := imageCmd(rt, "tag", id, hold+":"+id); err != nil {
			t.Fatalf("keeping image %s alive under %s before evicting its jail names: %v\n%s",
				id, hold, err, out)
		}
		held = append(held, id)
	}
	// Registered BEFORE the first untag, so a failure half-way through the eviction still
	// restores what it had already removed.
	t.Cleanup(func() {
		for _, tg := range tags {
			if out, err := imageCmd(rt, "tag", tg.id, tg.ref); err != nil {
				t.Errorf("RESTORE FAILED: could not point %s back at image %s, so later tests "+
					"may rebuild or re-copy the jail image: %v\n%s", tg.ref, tg.id, err, out)
			}
		}
		for _, id := range held {
			if out, err := imageCmd(rt, "untag", id, hold+":"+id); err != nil {
				t.Errorf("could not drop the hold name %s:%s (remove it by hand): %v\n%s",
					hold, id, err, out)
			}
		}
	})
	for _, tg := range tags {
		if out, err := imageCmd(rt, "untag", tg.id, tg.ref); err != nil {
			t.Fatalf("evicting %s from image %s: %v\n%s", tg.ref, tg.id, err, out)
		}
	}
	if left := listJailImageTags(t, rt); len(left) != 0 {
		t.Fatalf("eviction left %v in the jail repository — a launch finding its stock or content "+
			"ref would skip the copy and never contend for the lock", left)
	}
	return tags
}

// holdImageCopyLock takes the machine-wide image-copy lock at lockPath from THIS process and
// returns its release (idempotent).
//
// IT WAITS for a holder rather than failing, and it used to fail. A held lock is some other
// launch on this machine copying an image — an ordinary event, and on a machine running two
// suites at once a frequent one — and the right response is the one every launch makes: wait
// for it to finish. The wait is bounded by jailTimeout(), the budget of the one command that
// can be holding it, and says how long it took when that was noticeable.
func holdImageCopyLock(t *testing.T, lockPath string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("creating the lock directory: %v", err)
	}
	start := time.Now()
	f, err := flockWait(lockPath, syscall.LOCK_EX, start.Add(jailTimeout()))
	if err != nil {
		t.Fatalf("could not take the image-copy lock %s within %s (%v) — some other launch on "+
			"this machine has been copying an image for that long, and this test needs to be "+
			"the holder", lockPath, jailTimeout(), err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Logf("waited %s for another launch's image copy to release %s",
			waited.Round(100*time.Millisecond), lockPath)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = f.Close() }) } // closing the fd drops the flock
	t.Cleanup(release)
	return release
}

// spanEnds returns the durations, in seconds, of every `end <name>` line in a launch's
// <workspace>/.yolo/host-perf.log (perfEndRe is the line shape perf.formatLine writes).
func spanEnds(t *testing.T, ws, name string) []float64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".yolo", "host-perf.log"))
	if err != nil {
		t.Errorf("%s: no host-perf.log, although the launch ran with %s=1: %v", ws, paths.TimingEnv, err)
		return nil
	}
	var durs []float64
	for _, m := range perfEndRe.FindAllStringSubmatch(string(b), -1) {
		if m[1] != name {
			continue
		}
		d, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Errorf("%s: unparseable duration %q for %s", ws, m[2], name)
			continue
		}
		durs = append(durs, d)
	}
	return durs
}

// TestImageCopyLockSerializesConcurrentLaunches is the image-copy lock (internal/image/copylock.go)
// under real contention: THREE launches, in three workspaces, that each need the same image
// copied, queued behind a lock this test holds.
//
// The defect it guards was measured, not imagined: on 2026-09-14 a reboot launched 11 jails and 5
// of them copied one identical 3.45 GB image side by side, ~4 minutes each (copylock.go's header).
// The lock turns that into one copy, and the RE-INSPECT under the lock is what stops the other
// launches copying the same image one after another instead. So the assertions are:
//
//   - every launch printed the wait notice, which only a held lock prints;
//   - EXACTLY ONE launch really copied (copyLockRealCopyNote), and every other one reported the
//     image delivered by a concurrent launch (copyLockPeerNotice) — delete the re-inspect and
//     three launches copy, delete the lock and none waits;
//   - each launch's timing log has one image.copy_lock span at least as long as this test held
//     the lock after the last launch began waiting, and one image.layer_copy span. The layer_copy
//     span opens on EVERY launch that reaches the copy (it brackets the whole delivery switch,
//     peer-delivered arm included), so there are N of them and the real copy's is the long one.
//
// The DETERMINISM comes from holding the lock here rather than racing three launches: each one
// is started, and the lock released only once all three have said they are waiting, so none can
// arrive after the copy is done and skip the contention.
//
// ⚠ ONLY A ROOTLESS HOST SETTLES THE ROADMAP ROW. A rootless podman takes a different copy arm
// (`podman unshare -- <copier> copy`, internal/image/storewrite.go), and a nested jail's podman is
// rootful by construction (AGENTS.md's second carve-out). The test runs anywhere podman runs on
// Linux and says which it saw, in the log and in the CI step summary; ci.yml's `integration` job
// is rootless on both arches and is the instrument the row names.
func TestImageCopyLockSerializesConcurrentLaunches(t *testing.T) {
	// EXCLUSIVE: this test removes every name in the jail image repository, and any other
	// launch on the machine in that window would need a copy too and could be the one that
	// makes it (machinelock_test.go).
	requireJailExclusive(t, "TestImageCopyLockSerializesConcurrentLaunches evicts the jail "+
		"image names every launch looks for")
	rt := detectRuntime()
	if rt != "podman" || goruntime.GOOS != "linux" {
		t.Skipf("the image-copy lock's containers-storage arm runs on podman on Linux; this is %q "+
			"on %s. The Mac backends deliver through deliverViaArchive under the same lock, "+
			"measured by the macOS archive-delivery row, and this test's tag/untag spelling is "+
			"podman's", rt, goruntime.GOOS)
	}
	rootless := podmanRootless(t)

	const launches = 3
	// Held for this long AFTER the last launch announced its wait, so every launch's
	// image.copy_lock span has a floor the test controls.
	const holdAfterAllWaiting = 2 * time.Second

	// The lock BEFORE the eviction: a launch copying when this test arrives is waited out
	// here, so the image it delivers is one this test then evicts, instead of a name that
	// appears after the eviction and lets a launch below skip the copy.
	release := holdImageCopyLock(t, image.ImageCopyLockPath())

	sweepStaleHoldNames(t, rt)
	evicted := evictJailImageRefs(t, rt)
	t.Logf("evicted %d jail image name(s), images kept under %s: %v", len(evicted), copyLockHoldRepo(), evicted)

	// YOLO_TIMING=1 records the spans to <workspace>/.yolo/host-perf.log without printing the
	// report. YOLO_NO_AUTO_IMAGE_REAP keeps each launch's post-launch reaper out of a store this
	// test is holding in a deliberately unusual state; it is the hatch autoreapimages.go names
	// for "a launch against a store with images worth keeping".
	env := []string{paths.TimingEnv + "=1", "YOLO_NO_AUTO_IMAGE_REAP=1"}
	dirs := make([]string, launches)
	runs := make([]*bgRun, launches)
	for i := range launches {
		// An empty config: no packs, no `packages:`, so every launch wants the STOCK image and
		// all three contend for one copy of one ref.
		dirs[i] = writeProject(t, `{}`)
		runs[i] = startYoloBackground(t, fmt.Sprintf("launch-%d", i+1), dirs[i],
			`echo COPY-LOCK-$((40+2))-RAN`, env...)
	}

	deadline := time.Now().Add(jailTimeout())
	for {
		waiting := 0
		for _, r := range runs {
			select {
			case err := <-r.done:
				t.Fatalf("%s exited (%v) before it reached the image-copy lock, so nothing was "+
					"contended — did a stock or content ref survive the eviction?\n%s",
					r.name, err, r.combined())
			default:
			}
			if strings.Contains(r.combined(), copyLockWaitNotice) {
				waiting++
			}
		}
		if waiting == launches {
			break
		}
		if time.Now().After(deadline) {
			var report strings.Builder
			for _, r := range runs {
				fmt.Fprintf(&report, "--- %s (waiting: %v)\n%s\n", r.name,
					strings.Contains(r.combined(), copyLockWaitNotice), lastLines(r.combined(), 20))
			}
			t.Fatalf("only %d of %d launches reached the held image-copy lock within %s:\n%s",
				waiting, launches, jailTimeout(), report.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(holdAfterAllWaiting)
	release()

	type outcome struct {
		name           string
		rc             int
		out            string
		realCopy, peer bool
		copyLock       []float64
		layerCopy      []float64
	}
	var outcomes []outcome
	for i, r := range runs {
		rc := r.wait(t, jailTimeout())
		out := r.combined()
		outcomes = append(outcomes, outcome{
			name: r.name, rc: rc, out: out,
			realCopy:  strings.Contains(out, copyLockRealCopyNote),
			peer:      strings.Contains(out, copyLockPeerNotice),
			copyLock:  spanEnds(t, dirs[i], "image.copy_lock"),
			layerCopy: spanEnds(t, dirs[i], "image.layer_copy"),
		})
	}

	summary := []string{fmt.Sprintf("### Image-copy lock under contention: %d launches, "+
		"podman rootless=%s", launches, rootless)}
	for _, o := range outcomes {
		summary = append(summary, fmt.Sprintf("- %s: rc=%d real-copy=%v peer-delivered=%v "+
			"image.copy_lock=%v image.layer_copy=%v", o.name, o.rc, o.realCopy, o.peer,
			o.copyLock, o.layerCopy))
	}
	stepSummary(t, summary...)

	var copier *outcome
	realCopies := 0
	for i := range outcomes {
		o := &outcomes[i]
		// The target echoes an arithmetic expansion, so the match needs the shell to have run it:
		// the entrypoint's "⚡ Executing: <command>" banner shares this stream and carries the
		// command's literal text, which a plain `echo MARKER` would put in the output unrun.
		if o.rc != 0 || !strings.Contains(o.out, "COPY-LOCK-42-RAN") {
			t.Errorf("%s: rc %d, or its command never ran — every waiter must still get its "+
				"jail:\n%s", o.name, o.rc, o.out)
		}
		if o.realCopy {
			realCopies++
			copier = o
		}
		switch {
		case o.realCopy && o.peer:
			t.Errorf("%s both copied and reported a peer's delivery:\n%s", o.name, o.out)
		case !o.realCopy && !o.peer:
			t.Errorf("%s neither copied nor reported a peer's delivery — it got its image some "+
				"third way:\n%s", o.name, o.out)
		}
		if len(o.copyLock) != 1 {
			t.Errorf("%s recorded %d image.copy_lock span(s), want exactly 1", o.name, len(o.copyLock))
		} else if o.copyLock[0] < holdAfterAllWaiting.Seconds()-0.05 {
			t.Errorf("%s's image.copy_lock span is %.3fs, shorter than the %s this test held the "+
				"lock after every launch was waiting — the span no longer measures the wait",
				o.name, o.copyLock[0], holdAfterAllWaiting)
		}
		if len(o.layerCopy) != 1 {
			t.Errorf("%s recorded %d image.layer_copy span(s), want exactly 1 — the span opens on "+
				"every launch that reaches the delivery, peer-delivered or not", o.name, len(o.layerCopy))
		}
	}
	if realCopies != 1 {
		t.Fatalf("%d of %d launches really copied the image, want exactly 1: zero means the image "+
			"came from somewhere the test did not arrange, more means the re-inspect under the "+
			"lock no longer sees a peer's delivery (each queued launch copies again)", realCopies, launches)
	}
	for _, o := range outcomes {
		if o.name == copier.name || len(o.layerCopy) != 1 || len(copier.layerCopy) != 1 {
			continue
		}
		if o.layerCopy[0] >= copier.layerCopy[0] {
			t.Errorf("%s's image.layer_copy (%.3fs) is not shorter than the real copy's in %s "+
				"(%.3fs) — a launch that copied nothing should spend no time in delivery",
				o.name, o.layerCopy[0], copier.name, copier.layerCopy[0])
		}
	}
}

// TestStaleHoldNamesTakesOnlyDeadRunsNames pins the hold-name sweep, under -short: it takes a
// name only when it can prove its run is gone, and never a name without a PID.
func TestStaleHoldNamesTakesOnlyDeadRunsNames(t *testing.T) {
	refs := []string{
		copyLockHoldRepoPrefix + "-100:abc", // dead run
		copyLockHoldRepoPrefix + "-200:abc", // live run
		copyLockHoldRepoPrefix + ":abc",     // an older tree's unsuffixed name
		copyLockHoldRepoPrefix + "-x:abc",   // not a PID
		"localhost/yolo-jail:latest",
	}
	got := staleHoldNames(refs, func(pid int) bool { return pid == 200 })
	if len(got) != 1 || got[0] != copyLockHoldRepoPrefix+"-100:abc" {
		t.Errorf("staleHoldNames = %q, want only the dead run's name", got)
	}
	if !strings.HasSuffix(copyLockHoldRepo(), "-"+strconv.Itoa(os.Getpid())) {
		t.Errorf("copyLockHoldRepo() = %q, want this process's PID as its suffix", copyLockHoldRepo())
	}
}
