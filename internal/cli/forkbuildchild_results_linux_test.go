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

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const (
	forkBuildResultFixtureWorkspaceEnv  = "YOLO_TEST_FORK_BUILD_RESULT_WORKSPACE"
	forkBuildResultFixtureModeEnv       = "YOLO_TEST_FORK_BUILD_RESULT_MODE"
	forkBuildResultFixtureReadyEnv      = "YOLO_TEST_FORK_BUILD_RESULT_READY"
	forkBuildResultFixtureDescEnv       = "YOLO_TEST_FORK_BUILD_RESULT_DESCENDANT"
	forkBuildResultFixtureSignalEnv     = "YOLO_TEST_FORK_BUILD_RESULT_SIGNALED"
	forkBuildResultFixtureSignalNameEnv = "YOLO_TEST_FORK_BUILD_RESULT_SIGNAL_NAME"
	forkBuildResultFixtureKeeperLockEnv = "YOLO_TEST_FORK_KEEPER_LOCK"
	forkBuildResultFixturePIDEnv        = "YOLO_TEST_FORK_BUILD_RESULT_PID"
)

func TestExternallySignalledForkBuildRetainsAndFencesThroughProductionCaller(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	agentState := filepath.Join(paths.AgentsDir(), cname)
	keeperReady := filepath.Join(t.TempDir(), "keeper-ready")
	keeperProcess := startForkBuildDetachedKeeper(t, keeperReady)
	descendantPIDPath := filepath.Join(t.TempDir(), "descendant.pid")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	t.Cleanup(func() {
		if data, err := os.ReadFile(descendantPIDPath); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	installForkBuildResultFixtureWithPaths(t, "external-kill", filepath.Join(staging, "child-ready"),
		descendantPIDPath, "", childPIDPath)
	withBoundedProductionForkBuildChild(t, time.Hour)
	prevProbe := probeForkBuildContainer
	probes := 0
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		probes++
		if gotName != cname || gotRuntime != "podman" {
			t.Errorf("retry probed %q on %q; want the original podman backend for %q", gotName, gotRuntime, cname)
		}
		return true, true // the original-backend witness still sees the detached keeper's jail.
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })

	ctx := context.Background() // Deliberately not cancelled: only an external signal ends the child.
	boundHit := false
	buildDone := make(chan error, 1)
	go func() {
		_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
			runJail: childJail(ctx, false, &boundHit)}, io.Discard, io.Discard, false)
		buildDone <- err
	}()
	waitForLifecycleFile(t, filepath.Join(staging, "child-ready"), 3*time.Second)
	pidData, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("read hidden child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatalf("parse hidden child pid %q: %v", pidData, err)
	}
	if err := syscall.Kill(childPID, syscall.SIGKILL); err != nil {
		t.Fatalf("externally signal the hidden child: %v", err)
	}
	select {
	case err := <-buildDone:
		if err == nil {
			t.Fatal("externally killed build unexpectedly succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("production fork-build caller did not return after the externally signalled child")
	}
	if ctx.Err() != nil || boundHit {
		t.Fatalf("external child signal was misclassified: context error=%v, boundHit=%v", ctx.Err(), boundHit)
	}

	for _, path := range []string{staging, agentState, filepath.Join(paths.ContainerDir(), cname), forkBuildRuntimeRecordPath(staging)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("externally signalled child cleanup removed uncertain state %s: %v", path, err)
		}
	}
	if got, err := readForkBuildRuntime(staging); err != nil || got != "podman" {
		t.Fatalf("retained original runtime = %q, %v; want podman", got, err)
	}
	if keys, _ := store.EntryKeys(); len(keys) != 0 {
		t.Fatalf("signalled child admitted its complete-looking manifest: %v", keys)
	}
	if data, err := os.ReadFile(descendantPIDPath); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			t.Fatalf("fixture descendant pid %q: %v", data, parseErr)
		}
		if err := syscall.Kill(pid, 0); err == nil {
			t.Errorf("the killed launch child left its same-group descendant %d alive", pid)
		}
		_ = syscall.Kill(pid, syscall.SIGKILL) // Cleanup if a failure left the test fixture behind.
	} else {
		t.Fatalf("fixture did not record a descendant in the child process group: %v", err)
	}

	if err := keeperProcess.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("detached keeper did not outlive the build-child process group: %v", err)
	}
	_, err = buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(string, forkBuild, captureStreams) int {
			t.Error("same-ID retry reached build dispatch while the original backend is present")
			return 1
		}}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "still present") || probes != 1 {
		t.Fatalf("same-ID retry result=%v probes=%d; want original-backend refusal", err, probes)
	}
	for _, path := range []string{staging, agentState, filepath.Join(paths.ContainerDir(), cname), forkBuildRuntimeRecordPath(staging)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("refused retry removed retained state %s: %v", path, err)
		}
	}
}

func TestForkBuildTimeoutZeroExitCannotAdmitThroughLaunchCaller(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	ready := filepath.Join(t.TempDir(), "child-ready")
	signalled := filepath.Join(t.TempDir(), "sigint-seen")
	installForkBuildResultFixtureWithPaths(t, "bound-zero", ready, "", signalled, "")
	withBoundedProductionForkBuildChild(t, time.Second)

	var report bytes.Buffer
	deliveries := buildForksForLaunch(run.ForkBuildRequest{
		Pins:      []packload.ForkPin{{Fork: f, Commit: forkTestCommit}},
		Platform:  captureJailPlatform(),
		Runtime:   "podman",
		Workspace: t.TempDir(),
		Stderr:    &report,
		Interrupt: &run.ActInterrupt{},
	}, io.Discard, &report, false)
	if _, err := os.Stat(signalled); err != nil {
		t.Fatalf("zero-exit child did not handle the bound SIGINT: %v", err)
	}
	if deliveries[f.Bin].Key != "" {
		t.Fatalf("launch build reported success for the bound-stopped child: %+v\n%s", deliveries[f.Bin], report.String())
	}
	if !strings.Contains(report.String(), "bound and was stopped") {
		t.Errorf("launch build did not report the stopped bound: %s", report.String())
	}
	if keys, _ := store.EntryKeys(); len(keys) != 0 {
		t.Fatalf("bound-stopped child admitted its complete-looking manifest: %v", keys)
	}
	for _, path := range []string{staging, filepath.Join(paths.AgentsDir(), cname), filepath.Join(paths.ContainerDir(), cname), forkBuildRuntimeRecordPath(staging)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("timed-out build lost fenced state %s: %v", path, err)
		}
	}
}

func TestForkBuildChildOrdinarySuccessStillAdmitsThroughLaunchCaller(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	ready := filepath.Join(t.TempDir(), "child-ready")
	installForkBuildResultFixtureWithPaths(t, "ordinary-success", ready, "", "", "")
	withBoundedProductionForkBuildChild(t, time.Hour)

	deliveries := buildForksForLaunch(run.ForkBuildRequest{
		Pins:      []packload.ForkPin{{Fork: f, Commit: forkTestCommit}},
		Platform:  captureJailPlatform(),
		Runtime:   "podman",
		Workspace: t.TempDir(),
		Stderr:    io.Discard,
		Interrupt: &run.ActInterrupt{},
	}, io.Discard, io.Discard, false)
	if deliveries[f.Bin].Key == "" {
		t.Fatalf("ordinary completed child did not deliver its build: %+v", deliveries[f.Bin])
	}
	if _, err := store.Resolve(deliveries[f.Bin].Key); err != nil {
		t.Fatalf("ordinary child output was not admitted: %v", err)
	}
	for _, path := range []string{staging, filepath.Join(paths.AgentsDir(), cname), filepath.Join(paths.ContainerDir(), cname), forkBuildRuntimeRecordPath(staging)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("ordinary success did not clean build workspace state %s: %v", path, err)
		}
	}
}

func installForkBuildResultFixtureWithPaths(t *testing.T, mode, ready, descendant, signalled, pidFile string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	prevCommand := forkBuildChildCommand
	forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
		workspace := ""
		for _, arg := range argv {
			if strings.HasPrefix(arg, "--workspace=") {
				workspace = strings.TrimPrefix(arg, "--workspace=")
			}
		}
		if workspace == "" {
			return nil, fmt.Errorf("fork-build child argv has no workspace: %q", argv)
		}
		cmd := exec.Command(exe, "-test.run=^TestForkBuildResultChildFixture$")
		cmd.Env = append(os.Environ(), forkBuildResultFixtureWorkspaceEnv+"="+workspace,
			forkBuildResultFixtureModeEnv+"="+mode, forkBuildResultFixtureReadyEnv+"="+ready,
			forkBuildResultFixtureDescEnv+"="+descendant, forkBuildResultFixtureSignalEnv+"="+signalled,
			forkBuildResultFixturePIDEnv+"="+pidFile)
		return cmd, nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prevCommand })
}

func withBoundedProductionForkBuildChild(t *testing.T, bound time.Duration) {
	t.Helper()
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		return runForkBuildChild(ctx, bound, staging, b, s, color)
	}
	t.Cleanup(func() { forkBuildChild = prev })
}

func startForkBuildDetachedKeeper(t *testing.T, ready string) *os.Process {
	return startForkBuildDetachedKeeperWithLock(t, ready, "")
}

func startForkBuildDetachedKeeperWithLock(t *testing.T, ready, lockPath string) *os.Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	keeper := exec.Command(exe, "-test.run=^TestForkBuildDetachedKeeperFixture$")
	keeper.Env = append(os.Environ(), "YOLO_TEST_FORK_KEEPER_READY="+ready)
	if lockPath != "" {
		keeper.Env = append(keeper.Env, forkBuildResultFixtureKeeperLockEnv+"="+lockPath)
	}
	keeper.Stdout, keeper.Stderr = io.Discard, io.Discard
	keeper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := keeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = keeper.Process.Signal(syscall.SIGTERM)
		_ = keeper.Wait()
	})
	waitForLifecycleFile(t, ready, 2*time.Second)
	return keeper.Process
}

// TestForkBuildResultChildFixture is the inert hidden-child process used by Contract B tests. It
// writes a valid would-be fork entry, then either exits normally, handles bound SIGINT with status 0,
// or remains live for an external signal. The build runner and build/admission callers stay real.
func TestForkBuildResultChildFixture(t *testing.T) {
	workspace := os.Getenv(forkBuildResultFixtureWorkspaceEnv)
	if workspace == "" {
		return
	}
	mode := os.Getenv(forkBuildResultFixtureModeEnv)
	var handled chan os.Signal
	var handledSignal syscall.Signal
	if mode == "handled-signal" {
		switch os.Getenv(forkBuildResultFixtureSignalNameEnv) {
		case "INT":
			handledSignal = syscall.SIGINT
		case "HUP":
			handledSignal = syscall.SIGHUP
		case "TERM":
			handledSignal = syscall.SIGTERM
		default:
			t.Fatalf("unknown handled signal %q", os.Getenv(forkBuildResultFixtureSignalNameEnv))
		}
		handled = make(chan os.Signal, 1)
		signal.Notify(handled, handledSignal)
	}
	cname := runtime.FromWorkspace(workspace)
	agentState := filepath.Join(paths.AgentsDir(), cname)
	if err := os.MkdirAll(agentState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentState, "result-fixture"), []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteContainerTracking(cname, workspace); err != nil {
		t.Fatal(err)
	}
	if err := writeForkBuildRuntime(workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, forkToolchainLeaf), []byte("image-identity-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(workspace, captureOutLeaf)
	tree := capture.TreeDir(out)
	program := filepath.Join(tree, ".local", "bin", "probetool")
	if err := os.MkdirAll(filepath.Dir(program), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, bytes.Repeat([]byte("x"), 32), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := capture.WriteManifest(out, &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: captureJailPlatform(),
		Surfaces: []string{".npm-global", ".local", "go"}, Excluded: capture.DefaultExcludes(), Entries: probetoolBuilt,
	}); err != nil {
		t.Fatal(err)
	}

	for _, fd := range []int{3, 4, 5} {
		syscall.CloseOnExec(fd)
	}
	var interrupt chan os.Signal
	if mode == "bound-zero" {
		interrupt = make(chan os.Signal, 1)
		signal.Notify(interrupt, syscall.SIGINT)
		defer signal.Stop(interrupt)
	}
	if mode == "external-kill" || mode == "handled-signal" {
		pidFile := os.Getenv(forkBuildResultFixtureDescEnv)
		descendant := exec.Command("sh", "-c", "exec sleep 120")
		descendant.Stdout, descendant.Stderr = io.Discard, io.Discard
		if err := descendant.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(descendant.Process.Pid)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if path := os.Getenv(forkBuildResultFixturePIDEnv); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if ready := os.Getenv(forkBuildResultFixtureReadyEnv); ready != "" {
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, fd := range []uintptr{3, 4} {
		_ = os.NewFile(fd, "build-jail-stream").Close()
	}
	readyFD := os.NewFile(childJailReadyFD, "build-jail-ready")
	if _, err := readyFD.Write([]byte{'R'}); err != nil {
		t.Fatal(err)
	}
	_ = readyFD.Close()

	switch mode {
	case "ordinary-success":
		if err := writeForkBuildRunReturned(workspace); err != nil {
			t.Fatal(err)
		}
		return
	case "ordinary-failure":
		if err := writeForkBuildRunReturned(workspace); err != nil {
			t.Fatal(err)
		}
		os.Exit(17)
	case "handled-signal":
		got := (<-handled).(syscall.Signal)
		signal.Stop(handled)
		if path := os.Getenv(forkBuildResultFixtureSignalEnv); path != "" {
			if err := os.WriteFile(path, []byte(got.String()+" handled\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(128 + int(got))
	case "bound-zero":
		<-interrupt
		if path := os.Getenv(forkBuildResultFixtureSignalEnv); path != "" {
			if err := os.WriteFile(path, []byte("SIGINT handled with exit 0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return
	case "external-kill":
		select {}
	default:
		t.Fatalf("unknown result fixture mode %q", mode)
	}
}
