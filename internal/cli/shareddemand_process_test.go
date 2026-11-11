package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func sharedDemandTestProcess(t *testing.T, mode string, extra ...string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestSharedDemandProcessHelper$", "-test.short=true", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "YOLO_TEST_DEMAND_MODE="+mode)
	cmd.Env = append(cmd.Env, extra...)
	return cmd
}

// passSharedDemandHelperAlive hands the helper cmd will start a liveness lock: a fresh file in
// YOLO_TEST_DEMAND_ALIVE (set by newSharedDemandProcessFixture), flocked here and passed as an
// inherited descriptor. A flock belongs to the open file, so from the fork on the helper holds it
// (and so does any child it starts that inherits the descriptor); the kernel drops it only when
// every holder has exited. The caller calls the returned func once Start has returned, to close
// its own copy. Taking the lock in the helper itself would leave a gap between the spawn and the
// helper's own first step, in which awaitSharedDemandHelpers could not see a helper exists.
func passSharedDemandHelperAlive(t *testing.T, cmd *exec.Cmd, mode string) func() {
	t.Helper()
	dir := os.Getenv("YOLO_TEST_DEMAND_ALIVE")
	if dir == "" {
		return func() {}
	}
	f, err := os.CreateTemp(dir, mode+"-*.lock")
	if err != nil {
		t.Fatalf("helper liveness lock: %v", err)
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("helper liveness lock: %v", err)
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	return func() { _ = f.Close() }
}

// awaitSharedDemandHelpers waits until every helper process that took a liveness lock in dir has
// exited, so none still writes under the test's home when its TempDir cleanup removes it. THE
// FLAKE IT CLOSES (a loaded Mac run, 2026-11-03: `TempDir RemoveAll cleanup: unlinkat
// …/.local/share/yolo-jail: directory not empty`): every requester spawns its own detached worker
// (registerSharedDemand), which nothing waits for, so with two requesters a second worker runs
// after the first. A test that saw worker.lock free saw the first worker's release, before the
// second (slow to exec under load) had taken it, and the second then ran — re-creating the
// demand dir and its locks (pidlock.Acquire's MkdirAll and O_CREATE) under the tree the cleanup
// was removing. A helper still alive at the bound is reported.
func awaitSharedDemandHelpers(t *testing.T, dir string, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("reading helper liveness locks: %v", err)
			return
		}
		var alive []string
		for _, e := range entries {
			if pidlock.Held(filepath.Join(dir, e.Name())) {
				alive = append(alive, e.Name())
			}
		}
		if len(alive) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("helper processes still running %s after the test (their liveness locks): %v", bound, alive)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Only external execution and the sealed synthetic child are fixtures. The requester reaches
// hostMain --update; the detached process resolves user config and runs the production pipeline;
// runForkBuildChild owns/signals/drains a real child process. No agent is started.
func TestSharedDemandProcessHelper(t *testing.T) {
	mode := os.Getenv("YOLO_TEST_DEMAND_MODE")
	if mode == "" {
		return
	}
	switch mode {
	case "requester":
		withForkFloor(t)
		forkPinHomeSeams(t)
		prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
		got := captureHostExec(t)
		sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
			cmd := sharedDemandTestProcess(t, "worker", "YOLO_TEST_DEMAND_KEY="+d.Key, "YOLO_TEST_DEMAND_RUNTIME="+d.Runtime, "YOLO_TEST_DEMAND_PLATFORM="+d.Platform)
			log, err := notty.OpenDetachLog(backgroundAdvanceLog())
			if err != nil {
				return err
			}
			defer log.Close()
			cmd.Stdout = log
			cmd.Stderr = log
			defer passSharedDemandHelperAlive(t, cmd, "worker")()
			return notty.StartDetached(cmd)
		}
		code := hostMain([]string{"--update", "--", "tool"}, os.Stdout, os.Stderr, false, nil)
		if err := writeDemandJSON(os.Getenv("YOLO_TEST_DEMAND_RC"), struct {
			Code   int
			Target string
		}{code, got.target}); err != nil {
			t.Fatal(err)
		}
	case "worker":
		forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, b forkBuild, s captureStreams, color bool) (int, bool) {
			return runForkBuildChild(ctx, bound, staging, b, s, color)
		}
		forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
			staging := ""
			for _, arg := range argv {
				if value, ok := strings.CutPrefix(arg, "--workspace="); ok {
					staging = value
				}
			}
			return sharedDemandTestProcess(t, "child", "YOLO_TEST_DEMAND_STAGING="+staging), nil
		}
		removeBuildJail = func(name, rt string) bool {
			_ = os.WriteFile(os.Getenv("YOLO_TEST_DEMAND_CLEANED"), []byte(name+" "+rt), 0600)
			return true
		}
		code := runSharedPreparationCommand(backgroundArgs{keys: []string{os.Getenv("YOLO_TEST_DEMAND_KEY")}, runtime: os.Getenv("YOLO_TEST_DEMAND_RUNTIME"), platform: os.Getenv("YOLO_TEST_DEMAND_PLATFORM")}, os.Stdout)
		if code != 0 {
			t.Fatalf("worker exit=%d", code)
		}
	case "child":
		staging := os.Getenv("YOLO_TEST_DEMAND_STAGING")
		if err := writeForkBuildRuntime(staging, "podman", ""); err != nil {
			t.Fatal(err)
		}
		conn, err := net.Dial("unix", os.Getenv("YOLO_TEST_DEMAND_GATE"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = fmt.Fprintln(conn, os.Getpid())
		var release [1]byte
		if _, err = io.ReadFull(conn, release[:]); err != nil {
			t.Fatal(err)
		}
		body := "#!/bin/sh\nexit 0\n"
		out := filepath.Join(staging, captureOutLeaf)
		writeFile(t, filepath.Join(staging, forkToolchainLeaf), "synthetic-image\n")
		tool := filepath.Join(capture.TreeDir(out), ".local", "bin", "tool")
		writeFile(t, tool, body)
		if err = os.Chmod(tool, 0755); err != nil {
			t.Fatal(err)
		}
		manifest := &capture.Manifest{Schema: capture.ManifestSchema, Home: "/home/agent", Platform: os.Getenv("YOLO_TEST_DEMAND_PLATFORM"), Surfaces: []string{".local"}, Excluded: capture.DefaultExcludes(), RefScan: capture.RefScanFull, Relocatable: true,
			Entries: []capture.ManifestEntry{{Path: ".local", Kind: capture.KindDir, Mode: "0755"}, {Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"}, {Path: ".local/bin/tool", Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))}}}
		if err = capture.WriteManifest(out, manifest); err != nil {
			t.Fatal(err)
		}
		if err = writeForkBuildRunReturned(staging); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown helper mode %s", mode)
	}
}

type sharedDemandProcessFixture struct {
	fx            *patchedAdvanceFixture
	listener      *net.UnixListener
	gate, cleaned string
	alive         string // the helpers' liveness locks (passSharedDemandHelperAlive)
}

func newSharedDemandProcessFixture(t *testing.T) *sharedDemandProcessFixture {
	t.Helper()
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	seed, out, _ := fx.launch(t, "podman")
	if seed.delivery.Key == "" {
		t.Fatalf("container fixture did not seed its own admitted build: %+v\n%s", seed, out)
	}
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("host-floor fixture seed rc=%d\n%s", rc, out)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	// A short owned temporary path keeps AF_UNIX usable on both hosts; resolve where minted.
	dir, err := os.MkdirTemp("", "yolo-demand-")
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	gate := filepath.Join(real, "gate")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: gate, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cleaned := filepath.Join(real, "cleaned")
	alive := filepath.Join(real, "alive")
	if err := os.Mkdir(alive, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_TEST_DEMAND_GATE", gate)
	t.Setenv("YOLO_TEST_DEMAND_CLEANED", cleaned)
	t.Setenv("YOLO_TEST_DEMAND_ALIVE", alive)
	// Registered last, so it runs FIRST: before the gate closes, before the scratch dir goes, and
	// before the fixture's home (patchedFloorFixture's TempDir) is removed under a helper still
	// writing it.
	t.Cleanup(func() { awaitSharedDemandHelpers(t, alive, 30*time.Second) })
	return &sharedDemandProcessFixture{fx, listener, gate, cleaned, alive}
}
func (f *sharedDemandProcessFixture) requester(t *testing.T) (*exec.Cmd, string, *syncBuffer) {
	t.Helper()
	result := filepath.Join(t.TempDir(), "result.json")
	cmd := sharedDemandTestProcess(t, "requester", "YOLO_TEST_DEMAND_RC="+result)
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, result, out
}
func (f *sharedDemandProcessFixture) child(t *testing.T) (net.Conn, int) {
	t.Helper()
	_ = f.listener.SetDeadline(time.Now().Add(10 * time.Second))
	conn, err := f.listener.Accept()
	if err != nil {
		t.Fatalf("no synthetic child: %v; worker log: %s", err, mustRead(t, backgroundAdvanceLog()))
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	return conn, pid
}
func readSharedRequester(t *testing.T, cmd *exec.Cmd, path string, out *syncBuffer, want int) {
	t.Helper()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("requester process: %v\n%s", err, out.String())
	}
	var result struct {
		Code   int
		Target string
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("requester result: %v\n%s", err, out.String())
	}
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != want || want != 0 && result.Target != "" || want == 0 && result.Target == "" {
		t.Fatalf("requester result=%+v want code=%d\n%s", result, want, out.String())
	}
}

func TestSharedDemandSchedulerStopDetachesBeforeDemandStateUnlockWithProcessChild(t *testing.T) {
	f := newSharedDemandProcessFixture(t)
	f.fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
	fork := f.fx.fork(t)
	request := forkRequest(t, f.fx, "podman")
	var launchOutput syncBuffer
	got, _ := runBuildSlot(run.BuildSlotRequest{Forks: &request, MaxBuilds: 1}, &launchOutput, false)
	if got[fork.Bin].Key == "" {
		t.Fatalf("next-launch registration did not serve its current build:\n%s", launchOutput.String())
	}
	if _, err := readPreparationSnapshot(fork.Key()); err != nil {
		t.Fatalf("launch did not register scheduler demand: %v", err)
	}
	f.fx.later(2 * time.Hour)
	before := f.fx.record(t).Good.Entry

	previousSpawn := sharedPreparationSpawn
	var spawnOnce sync.Once
	var worker *exec.Cmd
	workerDone := make(chan error, 1)
	workerFinished := false
	var spawnErr error
	sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
		spawnOnce.Do(func() {
			worker = sharedDemandTestProcess(t, "worker", "YOLO_TEST_DEMAND_KEY="+d.Key,
				"YOLO_TEST_DEMAND_RUNTIME="+d.Runtime, "YOLO_TEST_DEMAND_PLATFORM="+d.Platform)
			log, err := os.OpenFile(backgroundAdvanceLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				spawnErr = err
				return
			}
			worker.Stdout, worker.Stderr = log, log
			if err = worker.Start(); err != nil {
				spawnErr = err
				_ = log.Close()
				return
			}
			_ = log.Close()
			go func() { workerDone <- worker.Wait() }()
		})
		return spawnErr
	}
	var childConn net.Conn
	t.Cleanup(func() {
		sharedPreparationSpawn = previousSpawn
		if childConn != nil {
			_, _ = childConn.Write([]byte{1})
			_ = childConn.Close()
		}
		if worker != nil && !workerFinished {
			select {
			case <-workerDone:
			default:
				_ = worker.Process.Kill()
				<-workerDone
			}
		}
	})

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ticks := make(chan time.Time, 1)
	schedulerDone := make(chan error, 1)
	ownerTicks := make(chan time.Time, 1)
	// The owner's own ready event, not a probe of worker.lock: a probe takes that lock to read it
	// free, and the owner's no-wait acquire then loses to its own observer on a loaded machine.
	ownerReady := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		schedulerDone <- runPreparationWorkerReady(ctx, preparationRoot(), run.SelfExecPath(), ownerTicks,
			func(ownerCtx context.Context) error { return runPreparationScheduler(ownerCtx, ticks, io.Discard) },
			func(preparationWorker) { close(ownerReady) })
	}()
	// Every bound below is a hang guard that turns a stuck event into a red test, never a budget
	// the code has to beat.
	hangGuard := testsupport.ReadinessBudget(t)
	t.Cleanup(func() {
		stop()
		select {
		case <-schedulerDone:
		case <-time.After(hangGuard):
			t.Error("preparation owner did not exit after cancellation")
		}
	})
	select {
	case <-ownerReady:
	case err := <-schedulerDone:
		t.Fatalf("preparation owner exited before it was ready: %v", err)
	case <-time.After(hangGuard):
		t.Fatal("preparation owner did not become ready")
	}
	ownerTicks <- time.Now()
	ticks <- f.fx.now
	childConn, pid := f.child(t)
	freshAfter, err := patchedAdvanceStore(true).CheckBoundary(fork.Key())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := freshPreparationIdentity(fork, patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	var requesterOutput syncBuffer
	requesterDone := make(chan advanceResult, 1)
	go func() {
		requesterDone <- advancePatchedFork(fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
			ctx: context.Background(), out: &requesterOutput, errw: &requesterOutput, freshCheck: true,
			freshIdentity: identity, freshBoundary: freshAfter})
	}()
	dir := sharedDemandDir(fork.Key(), "podman", patchedTestPlatform)
	waitFor(t, "fresh requester attached beside scheduler demand", func() bool {
		var live []livePreparationDemand
		err := withDemandState(context.Background(), dir, false, func() error { var e error; live, e = livePreparationDemands(dir); return e })
		return err == nil && len(live) == 2
	})
	state, err := pidlock.Acquire(filepath.Join(dir, "state.lock"), pidlock.Mode{Wait: true, Bound: 5 * time.Second}, nil)
	if err != nil {
		t.Fatalf("hold demand state across cleanup deadline: %v", err)
	}
	defer state.Release()
	stop()
	// The pass condition is the scheduler's own return while this test still holds state.lock.
	// Its cleanup deadline (sharedDemandCleanupBound) is what ends its wait for that lock, so the
	// return cannot come sooner than that bound and, on a loaded machine, comes well after it:
	// how long it takes is not asserted. Without the deadline the scheduler waits on the lock
	// for as long as it is held, and only the hang guard ends the test.
	select {
	case err := <-schedulerDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("scheduler stop: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("scheduler did not detach while demand state stayed locked: its cleanup deadline never ended the wait")
	}
	if !pidlock.Held(filepath.Join(dir, "state.lock")) {
		t.Fatal("demand state was unlocked before the scheduler returned, so its return proves no detach")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("scheduler stop killed the original child still owned by the fresh requester: %v", err)
	}
	if got := f.fx.record(t).Good.Entry; got != before {
		t.Fatalf("unreadable demand state changed Good from %q to %q", before, got)
	}
	state.Release()
	if _, err := childConn.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-requesterDone:
		if result.delivery.Key == "" || result.freshFailure() != "" {
			t.Fatalf("fresh requester did not settle through the valid demand guard: %+v\n%s", result, requesterOutput.String())
		}
	case <-time.After(hangGuard):
		t.Fatal("fresh requester did not settle after the valid demand became readable")
	}
	select {
	case err := <-workerDone:
		workerFinished = true
		if err != nil {
			t.Fatalf("shared worker exited: %v\n%s", err, mustRead(t, backgroundAdvanceLog()))
		}
	case <-time.After(hangGuard):
		t.Fatal("shared worker did not finish after its child was released")
	}
}

func TestSharedDemandPublicHostProcessesProducerInterruptSecondRemains(t *testing.T) {
	f := newSharedDemandProcessFixture(t)
	first, firstRC, firstOut := f.requester(t)
	conn, pid := f.child(t)
	defer conn.Close()
	second, secondRC, secondOut := f.requester(t)
	waitFor(t, "second public host attached", func() bool {
		dir := sharedDemandDir("forkpack/tool", "podman", floorPatchedPlatform())
		var live []livePreparationDemand
		err := withDemandState(context.Background(), dir, false, func() error { var e error; live, e = livePreparationDemands(dir); return e })
		return err == nil && len(live) == 2
	})
	if err := first.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	readSharedRequester(t, first, firstRC, firstOut, 130)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("second requester lost the original child: %v\nfirst=%s\nsecond=%s\nworker=%s", err, firstOut.String(), secondOut.String(), mustRead(t, backgroundAdvanceLog()))
	}
	_, _ = conn.Write([]byte{1})
	readSharedRequester(t, second, secondRC, secondOut, 0)
	// STARTED by a later requester may require a follow-up check, never a second identical build.
	if got := f.fx.record(t).Good; got == nil || got.Entry == "" {
		t.Fatal("no guarded admission")
	}
	dir := sharedDemandDir("forkpack/tool", "podman", floorPatchedPlatform())
	// Every worker PROCESS, not worker.lock: each requester spawned its own, and the lock reads
	// free between the first's release and the second's acquire (awaitSharedDemandHelpers), so a
	// child the second worker started after that would escape the check below.
	awaitSharedDemandHelpers(t, f.alive, 30*time.Second)
	if pidlock.Held(filepath.Join(dir, "worker.lock")) {
		t.Fatal("worker.lock is held with every worker gone")
	}
	_ = f.listener.SetDeadline(time.Now())
	if extra, err := f.listener.Accept(); err == nil {
		extra.Close()
		t.Fatal("a second child replaced the shared producer")
	}
}

func TestSharedDemandPublicHostProcessExitZeroDemandCleansOwnedChild(t *testing.T) {
	f := newSharedDemandProcessFixture(t)
	before := f.fx.record(t).Good.Entry
	first, _, _ := f.requester(t)
	conn, pid := f.child(t)
	defer conn.Close()
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	waitFor(t, "requester exit tears down owned runtime", func() bool { _, err := os.Stat(f.cleaned); return err == nil })
	dir := sharedDemandDir("forkpack/tool", "podman", floorPatchedPlatform())
	waitFor(t, "zero-demand worker ends", func() bool { return !pidlock.Held(filepath.Join(dir, "worker.lock")) })
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatal("owned child survived requester exit")
	}
	if got := f.fx.record(t).Good.Entry; got != before {
		t.Fatalf("zero-demand exit admitted %s instead of retaining %s", got, before)
	}
}

func TestSharedDemandPublicHostProcessesRequesterExitSecondRemains(t *testing.T) {
	f := newSharedDemandProcessFixture(t)
	first, _, _ := f.requester(t)
	conn, pid := f.child(t)
	defer conn.Close()
	second, secondRC, secondOut := f.requester(t)
	waitFor(t, "second public host attached", func() bool {
		dir := sharedDemandDir("forkpack/tool", "podman", floorPatchedPlatform())
		var live []livePreparationDemand
		err := withDemandState(context.Background(), dir, false, func() error { var e error; live, e = livePreparationDemands(dir); return e })
		return err == nil && len(live) == 2
	})
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("exiting first requester killed shared child: %v", err)
	}
	_, _ = conn.Write([]byte{1})
	readSharedRequester(t, second, secondRC, secondOut, 0)
}

func TestSharedDemandPublicHostRejectsLateSourceRecipeAndSelectionChanges(t *testing.T) {
	for _, change := range []string{"recipe", "source", "selection"} {
		t.Run(change, func(t *testing.T) {
			f := newSharedDemandProcessFixture(t)
			before := f.fx.record(t).Good.Entry
			requester, result, out := f.requester(t)
			conn, _ := f.child(t)
			defer conn.Close()
			switch change {
			case "recipe":
				b, err := os.ReadFile(f.fx.manifest)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, f.fx.manifest, strings.Replace(string(b), "sh build.sh", "sh changed-build.sh", 1))
			case "source":
				f.fx.writeManifest(t, "different-source-ref", "")
			case "selection":
				writeFile(t, filepath.Join(f.fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[]}`)
			}
			// Releasing the child races the watcher intentionally: the late admission guard must still
			// reject the output if the watcher has not yet observed revocation.
			_, _ = conn.Write([]byte{1})
			readSharedRequester(t, requester, result, out, 1)
			if got := f.fx.record(t).Good.Entry; got != before {
				t.Fatalf("late %s change admitted stale output: %s", change, got)
			}
		})
	}
}
