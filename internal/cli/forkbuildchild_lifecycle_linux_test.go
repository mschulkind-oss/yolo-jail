//go:build linux

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestCancelledForkBuildRetainsWorkspaceThroughProductionCaller is deliberately compatible with
// the pre-repair production APIs so this exact regression can run against pristine baseline source.
// It enters buildFork -> buildForkUnderLock -> captureStaged and the real child runner; a detached
// keeper in a separate session remains alive after that child group receives SIGINT.
func TestCancelledForkBuildRetainsWorkspaceThroughProductionCaller(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	agentState := filepath.Join(paths.AgentsDir(), cname)
	marker := filepath.Join(agentState, "detached-keeper-retention")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	keeperReady := filepath.Join(t.TempDir(), "keeper-ready")
	keeper := exec.Command(exe, "-test.run=^TestForkBuildLifecycleKeeperFixture$")
	keeper.Env = append(os.Environ(), "YOLO_TEST_FORK_LIFECYCLE_KEEPER="+keeperReady)
	keeper.Stdout, keeper.Stderr = io.Discard, io.Discard
	keeper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := keeper.Start(); err != nil {
		t.Fatal(err)
	}
	keeperWaited := false
	t.Cleanup(func() {
		if !keeperWaited {
			_ = keeper.Process.Signal(syscall.SIGTERM)
			_ = keeper.Wait()
		}
	})
	waitForLifecycleFile(t, keeperReady, 2*time.Second)

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
		return &exec.Cmd{Path: exe, Args: []string{exe, "-test.run=^TestForkBuildLifecycleChildFixture$"},
			Env: append(os.Environ(), "YOLO_TEST_FORK_LIFECYCLE_WORKSPACE="+workspace,
				"YOLO_TEST_FORK_LIFECYCLE_CNAME="+cname)}, nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prevCommand })
	prevRunner := forkBuildChild
	forkBuildChild = runForkBuildChild
	t.Cleanup(func() { forkBuildChild = prevRunner })

	ctx, cancel := context.WithCancel(t.Context())
	buildDone := make(chan error, 1)
	go func() {
		_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
			runJail: childJail(ctx, false, nil)}, io.Discard, io.Discard, false)
		buildDone <- err
	}()
	ready := filepath.Join(staging, "child-ready")
	waitForLifecycleFile(t, ready, 3*time.Second)
	cancel()
	select {
	case err := <-buildDone:
		if err == nil {
			t.Fatal("cancelled build unexpectedly succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("production fork-build caller did not return after child-group cancellation")
	}

	if err := keeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("separate-session keeper did not outlive build-child cancellation: %v", err)
	}
	for _, path := range []string{staging, ready, agentState, marker, filepath.Join(paths.ContainerDir(), cname)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("production fork-build cleanup removed state before keeper teardown: %s: %v", path, err)
		}
	}
	if entries, _ := store.EntryKeys(); len(entries) != 0 {
		t.Fatalf("interrupted build admitted partial output: %v", entries)
	}

	if err := keeper.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := keeper.Wait(); err != nil {
		t.Fatalf("detached keeper stop: %v", err)
	}
	keeperWaited = true
}

// The build-child fixture writes runtime/home markers and then handles the parent's cancellation.
func TestForkBuildLifecycleChildFixture(t *testing.T) {
	workspace := os.Getenv("YOLO_TEST_FORK_LIFECYCLE_WORKSPACE")
	if workspace == "" {
		return
	}
	cname := os.Getenv("YOLO_TEST_FORK_LIFECYCLE_CNAME")
	if cname == "" {
		t.Fatal("fixture has no container name")
	}
	agentState := filepath.Join(paths.AgentsDir(), cname)
	if err := os.MkdirAll(agentState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentState, "detached-keeper-retention"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteContainerTracking(cname, workspace); err != nil {
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

// The keeper is an inert process in a separate session, stopped explicitly after cleanup assertions.
func TestForkBuildLifecycleKeeperFixture(t *testing.T) {
	ready := os.Getenv("YOLO_TEST_FORK_LIFECYCLE_KEEPER")
	if ready == "" {
		return
	}
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
}

func waitForLifecycleFile(t *testing.T, path string, timeout time.Duration) {
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
