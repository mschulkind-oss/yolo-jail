//go:build linux

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"golang.org/x/sys/unix"
)

const buildPoolCancelRootEnv = "YOLO_TEST_BUILD_POOL_CANCEL_ROOT"

// TestTheLaunchBuildPoolStopsCompilerDescendantsOnOneInterrupt drives the real startup build pool
// and fork-build child runner under a controlling pty. Five compiler fixtures are scheduled with
// four active slots; one ^C must stop and reap the active process trees without starting the fifth.
func TestTheLaunchBuildPoolStopsCompilerDescendantsOnOneInterrupt(t *testing.T) {
	root := t.TempDir()
	blocked := `#!/bin/sh
set -eu
D=$1
printf R >&5
exec 3>&- 4>&- 5>&-
printf started > "$D/started"
echo "partial output" >> "$D/output"
trap 'echo interrupted > "$D/interrupted"; exit 0' INT
sh -c 'echo $$ > "$1/descendant.pid"; exec sleep 120' sh "$D"
printf success > "$D/success"
`
	for i := 0; i < 5; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pi-extension-%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compile.sh"), []byte(blocked), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	master, slave := openBuildPoolPTY(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestForkBuildChildCancellationPTYHelper$")
	cmd.Env = append(os.Environ(), buildPoolCancelRootEnv+"="+root)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	output := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(master)
		output <- string(b)
	}()
	wait := make(chan error, 1)
	waitDone := make(chan struct{})
	go func() {
		wait <- cmd.Wait()
		close(waitDone)
	}()
	t.Cleanup(func() {
		select {
		case <-waitDone:
		default:
			_ = cmd.Process.Kill()
			<-waitDone
		}
		_ = master.Close()
	})

	ready := filepath.Join(root, "pool-ready")
	waitForFixtureFile(t, ready, 8*time.Second)
	started := time.Now()
	if _, err := master.Write([]byte{0x03}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waitDone:
		err := <-wait
		if err != nil {
			select {
			case text := <-output:
				t.Fatalf("startup build cancellation helper failed: %v\n%s", err, text)
			case <-time.After(time.Second):
				t.Fatalf("startup build cancellation helper failed: %v", err)
			}
		}
	case <-time.After(4 * time.Second):
		t.Fatalf("the startup build pool did not return promptly after one terminal Ctrl-C (%s)", time.Since(started))
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("startup cancellation took %s, want at most 2s", elapsed)
	}
	select {
	case text := <-output:
		if strings.Contains(text, "--- FAIL:") || strings.Contains(text, "FAIL\t") {
			t.Errorf("the helper test failed:\n%s", text)
		}
	case <-time.After(time.Second):
		t.Error("pty stdout/stderr did not drain after the helper exited")
	}
}

// TestForkBuildChildCancellationPTYHelper is the subprocess whose foreground pty receives ^C. It
// uses real runBuildPool scheduling, real child processes, and harmless shell compiler fixtures.
func TestForkBuildChildCancellationPTYHelper(t *testing.T) {
	root := os.Getenv(buildPoolCancelRootEnv)
	if root == "" {
		return
	}
	const (
		keys   = 5
		active = 4
	)
	keyDirs := make(map[string]string, keys)
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("pi-extension-%d", i)
		keyDirs[key] = filepath.Join(root, key)
	}

	prevCommand := forkBuildChildCommand
	forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
		key := ""
		for _, arg := range argv {
			if strings.HasPrefix(arg, "--bin=") {
				key = strings.TrimPrefix(arg, "--bin=")
			}
		}
		dir, ok := keyDirs[key]
		if !ok {
			return nil, fmt.Errorf("unknown fixture key %q in %q", key, argv)
		}
		return exec.Command("sh", filepath.Join(dir, "compile.sh"), dir), nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prevCommand })
	t.Cleanup(func() {
		for _, dir := range keyDirs {
			if b, err := os.ReadFile(filepath.Join(dir, "descendant.pid")); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
	})
	prevDrain := forkBuildChildDrain
	forkBuildChildDrain = 300 * time.Millisecond
	t.Cleanup(func() { forkBuildChildDrain = prevDrain })
	act := &run.ActInterrupt{}
	pool := newBuildPool(io.Discard, progress.Config{}, false, root, "podman", keys, active, act)
	statuses := make(chan int, keys)
	for key := range keyDirs {
		key := key
		pool.add(key, func(it *poolItem) {
			release, ok := it.build()
			if !ok {
				statuses <- 130
				return
			}
			defer release()
			code, _ := runForkBuildChild(it.context(), time.Hour, root,
				forkBuild{Fork: packload.Fork{Pack: "pi", Bin: key}},
				captureStreams{out: it.stream(), errw: it.stream(), jailOut: io.Discard, jailErr: io.Discard}, false)
			statuses <- code
		})
	}
	done := make(chan struct{})
	go func() { pool.run(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		started := 0
		for _, dir := range keyDirs {
			if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
				started++
			}
		}
		if started == active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	started := 0
	for _, dir := range keyDirs {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			started++
			waitForFixtureFile(t, filepath.Join(dir, "descendant.pid"), time.Second)
		}
	}
	if started != active {
		t.Fatalf("the launch pool started %d compiler fixtures concurrently, want %d", started, active)
	}
	if err := os.WriteFile(filepath.Join(root, "pool-ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("launch build pool did not return promptly after one Ctrl-C")
	}
	if !act.Interrupted() {
		t.Fatal("the launch act did not record its Ctrl-C")
	}
	for i := 0; i < keys; i++ {
		select {
		case got := <-statuses:
			if got != 130 {
				t.Errorf("interrupted or queued build returned status %d, want 130", got)
			}
		default:
			t.Fatal("the build pool returned before every key ended")
		}
	}
	for _, dir := range keyDirs {
		_, startErr := os.Stat(filepath.Join(dir, "started"))
		if startErr == nil {
			if _, err := os.Stat(filepath.Join(dir, "success")); !os.IsNotExist(err) {
				t.Errorf("interrupted compiler %s was marked successful (err=%v)", dir, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "interrupted")); err != nil {
				t.Errorf("compiler %s did not observe the interrupt: %v", dir, err)
			}
		} else if !os.IsNotExist(startErr) {
			t.Errorf("checking queued compiler fixture %s: %v", dir, startErr)
		} else if _, err := os.Stat(filepath.Join(dir, "success")); !os.IsNotExist(err) {
			t.Errorf("queued compiler %s started or published output after cancellation (err=%v)", dir, err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "descendant.pid"))
		if err != nil {
			continue // The compiler was queued and never spawned.
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			t.Errorf("compiler %s recorded invalid descendant pid %q", dir, b)
			continue
		}
		if err := syscall.Kill(pid, 0); err == nil {
			t.Errorf("compiler descendant %d from %s is still alive after the pool returned", pid, dir)
		}
	}
	before := make([]string, len(pool.items))
	for i, item := range pool.items {
		before[i] = item.buf.String()
	}
	time.Sleep(100 * time.Millisecond)
	for i, item := range pool.items {
		if got := item.buf.String(); got != before[i] {
			t.Errorf("build output %d changed after the pool returned: before %q, after %q", i, before[i], got)
		}
	}

	// An ordinary subsequent start safely rebuilds all five keys; partial output from the cancelled
	// startup was never promoted to success.
	success := "#!/bin/sh\nprintf R >&5\nexec 3>&- 4>&- 5>&-\nprintf success > \"$1/success\"\n"
	for _, dir := range keyDirs {
		for _, name := range []string{"descendant.pid", "started", "interrupted"} {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "compile.sh"), []byte(success), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	retry := newBuildPool(io.Discard, progress.Config{}, false, root, "podman", keys, active, &run.ActInterrupt{})
	for key := range keyDirs {
		key := key
		retry.add(key, func(it *poolItem) {
			release, ok := it.build()
			if !ok {
				t.Errorf("retry for %s was canceled before it started", key)
				return
			}
			defer release()
			code, _ := runForkBuildChild(it.context(), time.Hour, root,
				forkBuild{Fork: packload.Fork{Pack: "pi", Bin: key}},
				captureStreams{out: it.stream(), errw: it.stream(), jailOut: io.Discard, jailErr: io.Discard}, false)
			if code != 0 {
				t.Errorf("safe retry for %s failed with status %d", key, code)
			}
		})
	}
	retry.run()
	for _, dir := range keyDirs {
		if _, err := os.Stat(filepath.Join(dir, "success")); err != nil {
			t.Errorf("safe retry did not publish success for %s: %v", dir, err)
		}
	}
}

// TestTheForkBuildChildEscalatesAnIgnoredCompilerDescendant checks forced cleanup after the grace.
func TestTheForkBuildChildEscalatesAnIgnoredCompilerDescendant(t *testing.T) {
	dir := t.TempDir()
	body := `#!/bin/sh
printf R >&5
exec 3>&- 4>&- 5>&-
sh -c 'trap "" INT TERM; echo $$ > "$1/descendant.pid"; exec sleep 120' sh "$1" &
child=$!
echo "$child" > "$1/descendant.pid"
trap 'exit 130' INT
wait "$child"
`
	if err := os.WriteFile(filepath.Join(dir, "compile.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prevCommand := forkBuildChildCommand
	forkBuildChildCommand = func([]string) (*exec.Cmd, error) {
		return exec.Command("sh", filepath.Join(dir, "compile.sh"), dir), nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prevCommand })
	prevDrain := forkBuildChildDrain
	forkBuildChildDrain = 300 * time.Millisecond
	t.Cleanup(func() { forkBuildChildDrain = prevDrain })
	prevGrace := forkBuildChildGrace
	forkBuildChildGrace = 300 * time.Millisecond
	t.Cleanup(func() { forkBuildChildGrace = prevGrace })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		code     int
		timedOut bool
	}
	done := make(chan result, 1)
	go func() {
		code, timedOut := runForkBuildChild(ctx, time.Hour, dir,
			forkBuild{Fork: packload.Fork{Pack: "pi", Bin: "stubborn"}}, discardStreams, false)
		done <- result{code: code, timedOut: timedOut}
	}()
	waitForFixtureFile(t, filepath.Join(dir, "descendant.pid"), 2*time.Second)
	start := time.Now()
	cancel()
	select {
	case got := <-done:
		if got.code != 130 || got.timedOut {
			t.Fatalf("interrupted compiler = status %d, timed out %v; want 130 and no timeout", got.code, got.timedOut)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ignored compiler descendant outlived the bounded cancellation")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("ignored compiler descendant delayed cancellation %s, want at most 2s", time.Since(start))
	}
	b, err := os.ReadFile(filepath.Join(dir, "descendant.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("compiler descendant %d survived forced group cleanup", pid)
	}
}

// TestTheForkBuildChildBoundsOutputDrainAndStopsDescendants makes a successful root command leave
// stdout/stderr open in a same-group descendant. The runner must bound the drain, fail the build,
// and stop the process group before returning.
func TestTheForkBuildChildBoundsOutputDrainAndStopsDescendants(t *testing.T) {
	dir := t.TempDir()
	body := `#!/bin/sh
printf R >&5
exec 3>&- 4>&- 5>&-
sh -c 'echo $$ > "$1/descendant.pid"; exec sleep 120' sh "$1" &
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "compile.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prevCommand := forkBuildChildCommand
	forkBuildChildCommand = func([]string) (*exec.Cmd, error) {
		return exec.Command("sh", filepath.Join(dir, "compile.sh"), dir), nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prevCommand })
	prevDrain := forkBuildChildDrain
	forkBuildChildDrain = 300 * time.Millisecond
	t.Cleanup(func() { forkBuildChildDrain = prevDrain })
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(dir, "descendant.pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	start := time.Now()
	code, timedOut := runForkBuildChild(t.Context(), time.Hour, dir,
		forkBuild{Fork: packload.Fork{Pack: "pi", Bin: "incomplete-output"}}, discardStreams, false)
	if code != 1 || timedOut {
		t.Fatalf("compiler with an incomplete output drain = status %d, timed out %v; want failure and no build bound", code, timedOut)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("inherited output delayed the build return %s, want at most 1s", elapsed)
	}
	b, err := os.ReadFile(filepath.Join(dir, "descendant.pid"))
	if err != nil {
		t.Fatalf("fixture did not leave a descendant holding stdout/stderr: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("fixture recorded invalid descendant pid %q: %v", b, err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("same-group descendant %d survived the bounded output drain", pid)
	}
}

// The same production child runner: an exit failure remains a failed build rather than an interrupt.
func TestTheForkBuildChildPreservesAnOrdinaryCompilerFailure(t *testing.T) {
	prev := forkBuildChildCommand
	forkBuildChildCommand = func([]string) (*exec.Cmd, error) {
		return exec.Command("sh", "-c", "printf R >&5; exit 17"), nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prev })
	code, timedOut := runForkBuildChild(t.Context(), time.Hour, t.TempDir(), forkBuild{Fork: packload.Fork{Pack: "pi", Bin: "failed"}},
		discardStreams, false)
	if code != 17 || timedOut {
		t.Fatalf("ordinary compiler failure = status %d, timed out %v; want status 17 and no timeout", code, timedOut)
	}
}

// TestCancelledForkBuildKeepsWorkspaceUntilItsDetachedKeeperStops exercises the production fork
// build, child runner, deferred cleanup and same-ID retry path. The detached keeper is a real process
// in its own session; its runtime-presence fixture remains positive until the test observes its
// delayed stop, so an early retry must refuse before Store.Stage can clear any live jail state.
func TestCancelledForkBuildKeepsWorkspaceUntilItsDetachedKeeperStops(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	marker := filepath.Join(paths.AgentsDir(), cname, "detached-keeper-live")

	keeperReady := filepath.Join(t.TempDir(), "keeper-ready")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	keeper := exec.Command(exe, "-test.run=^TestForkBuildDetachedKeeperFixture$")
	keeper.Env = append(os.Environ(), "YOLO_TEST_FORK_KEEPER_READY="+keeperReady)
	keeper.Stdout, keeper.Stderr = io.Discard, io.Discard
	keeper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := keeper.Start(); err != nil {
		t.Fatal(err)
	}
	keeperDone := false
	t.Cleanup(func() {
		if !keeperDone {
			_ = keeper.Process.Signal(syscall.SIGTERM)
			_ = keeper.Wait()
		}
	})
	waitForFixtureFile(t, keeperReady, 2*time.Second)

	prevProbe := probeForkBuildContainer
	keeperPresent, keeperKnown := true, true
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		if gotName != cname || gotRuntime != "podman" {
			t.Errorf("retry probed container %q on %q; want %q on podman", gotName, gotRuntime, cname)
		}
		return keeperPresent, keeperKnown
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })
	prevCommand := forkBuildChildCommand
	forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
		workspace := ""
		for _, arg := range argv {
			if strings.HasPrefix(arg, "--workspace=") {
				workspace = strings.TrimPrefix(arg, "--workspace=")
			}
		}
		if workspace == "" {
			return nil, fmt.Errorf("build child argv has no workspace: %q", argv)
		}
		return &exec.Cmd{Path: exe, Args: []string{exe, "-test.run=^TestForkBuildCancelledBuildChildFixture$"},
			Env: append(os.Environ(), "YOLO_TEST_FORK_CHILD_WORKSPACE="+workspace,
				"YOLO_TEST_FORK_CHILD_CNAME="+cname)}, nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prevCommand })

	prevRunner := forkBuildChild
	forkBuildChild = runForkBuildChild
	t.Cleanup(func() { forkBuildChild = prevRunner })

	ctx, cancel := context.WithCancel(t.Context())
	buildDone := make(chan error, 1)
	var childOut, childErr bytes.Buffer
	go func() {
		_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
			runJail: childJail(ctx, false, nil)}, &childOut, &childErr, false)
		buildDone <- err
	}()
	childReady := filepath.Join(staging, "child-ready")
	readyDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(readyDeadline) {
		if _, err := os.Stat(childReady); err == nil {
			break
		}
		select {
		case err := <-buildDone:
			t.Fatalf("build returned before its child became ready: %v\nstdout:\n%s\nstderr:\n%s", err, childOut.String(), childErr.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(childReady); err != nil {
		t.Fatalf("fork build child never reached its fixture (build may still be running): %v", err)
	}
	cancel()
	select {
	case err := <-buildDone:
		if err == nil {
			t.Fatal("cancelled build unexpectedly succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled build did not return after the child process group stopped")
	}

	for _, path := range []string{staging, marker, filepath.Join(paths.ContainerDir(), cname)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("interrupted build removed live jail state %s: %v", path, err)
		}
	}
	if err := keeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("detached keeper did not survive build-child cancellation: %v", err)
	}
	if entries, _ := store.EntryKeys(); len(entries) != 0 {
		t.Fatalf("interrupted build admitted partial output: %v", entries)
	}

	runAgain := func() (*capture.Entry, error) {
		return buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
			runJail: func(string, forkBuild, captureStreams) int {
				t.Error("retry reached the build jail before keeper teardown was confirmed")
				return 1
			}}, io.Discard, io.Discard, false)
	}
	// An unavailable runtime is not evidence of absence either: keep the mounted state and refuse.
	keeperKnown = false
	if _, err := runAgain(); err == nil || !strings.Contains(err.Error(), "could not confirm") {
		t.Fatalf("retry with unknown runtime liveness returned %v, want a fail-closed refusal", err)
	}
	for _, path := range []string{staging, marker, filepath.Join(paths.ContainerDir(), cname)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unknown-liveness retry removed live jail state %s: %v", path, err)
		}
	}

	// A same-key retry while the detached keeper is still present must not stage or clean the old
	// paths. This is the race the build lock alone cannot prevent after the interrupted caller exits.
	keeperKnown = true
	if _, err := runAgain(); err == nil || !strings.Contains(err.Error(), "still present") {
		t.Fatalf("retry with the detached keeper live returned %v, want a safe refusal", err)
	}
	for _, path := range []string{staging, marker, filepath.Join(paths.ContainerDir(), cname)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("refused retry removed live jail state %s: %v", path, err)
		}
	}

	// The runtime's stop completes only when the detached keeper exits. Once it is reaped, a retry
	// may clear that build's old workspace and safely create a fresh one at the same ID.
	if err := keeper.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := keeper.Wait(); err != nil {
		t.Fatalf("detached keeper stop: %v", err)
	}
	keeperDone, keeperPresent = true, false
	var seen run.Options
	fake := fakeBuildJail(t, &seen, probetoolBuilt)
	var retried bool
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(workspace string, _ forkBuild, _ captureStreams) int {
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("confirmed-gone keeper marker survived safe workspace cleanup: %v", err)
			}
			retried = true
			if err := writeForkBuildRuntime(workspace, "podman"); err != nil {
				t.Fatalf("record retry runtime: %v", err)
			}
			rc := fake(run.Options{Workspace: workspace})
			if err := writeForkBuildRunReturned(workspace); err != nil {
				t.Fatalf("record retry runner completion: %v", err)
			}
			return rc
		}}, io.Discard, io.Discard, false)
	if err != nil || entry == nil || !retried {
		t.Fatalf("retry after confirmed keeper teardown: entry=%v err=%v ran=%v", entry, err, retried)
	}
	if _, err := os.Stat(filepath.Join(paths.AgentsDir(), cname)); !os.IsNotExist(err) {
		t.Errorf("successful retry left stale jail home: %v", err)
	}
}

// TestForkBuildCancelledBuildChildFixture is the child process that leaves workspace/runtime
// markers before the production child runner sends it SIGINT.
func TestForkBuildCancelledBuildChildFixture(t *testing.T) {
	workspace := os.Getenv("YOLO_TEST_FORK_CHILD_WORKSPACE")
	if workspace == "" {
		return
	}
	cname := os.Getenv("YOLO_TEST_FORK_CHILD_CNAME")
	if cname == "" {
		t.Fatal("fixture has no container name")
	}
	if err := os.MkdirAll(filepath.Join(paths.AgentsDir(), cname), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.AgentsDir(), cname, "detached-keeper-live"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteContainerTracking(cname, workspace); err != nil {
		t.Fatal(err)
	}
	if err := writeForkBuildRuntime(workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "child-ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(childJailReadyFD, "jail-ready")
	if _, err := ready.Write([]byte{'R'}); err != nil {
		t.Fatal(err)
	}
	_ = ready.Close()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT)
	defer signal.Stop(interrupt)
	<-interrupt
}

// TestForkBuildDetachedKeeperFixture is an inert delayed-stop keeper in its own process session.
func TestForkBuildDetachedKeeperFixture(t *testing.T) {
	ready := os.Getenv("YOLO_TEST_FORK_KEEPER_READY")
	if ready == "" {
		return
	}
	if lockPath := os.Getenv(forkBuildResultFixtureKeeperLockEnv); lockPath != "" {
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
			t.Fatal(err)
		}
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = lock.Close()
			t.Fatal(err)
		}
		defer func() {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			_ = lock.Close()
		}()
	}
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
}

func openBuildPoolPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		_ = m.Close()
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		_ = m.Close()
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = m.Close()
		t.Skipf("open slave: %v", err)
	}
	return m, s
}

func waitForFixtureFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture did not create %s", path)
}
