//go:build linux

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestHandledForkBuildSignalRetainsUntilKeeperAndOriginalBackendAreGone(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{
		{name: "INT", sig: syscall.SIGINT},
		{name: "HUP", sig: syscall.SIGHUP},
		{name: "TERM", sig: syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := forkBuildHome(t)
			b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
			store := &capture.Store{Dir: paths.CapturesDir()}
			staging := store.StagingDir("fork-" + b.id())
			cname := runtime.FromWorkspace(staging)
			lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
			keeperReady := filepath.Join(t.TempDir(), "keeper-ready")
			keeper := startForkBuildDetachedKeeperWithLock(t, keeperReady, lockPath)
			descendantPIDPath := filepath.Join(t.TempDir(), "descriptor-free-helper.pid")
			childPIDPath := filepath.Join(t.TempDir(), "child.pid")
			signalSeen := filepath.Join(t.TempDir(), "handled-signal.txt")
			t.Setenv(forkBuildResultFixtureSignalNameEnv, tc.name)
			installForkBuildResultFixtureWithPaths(t, "handled-signal", filepath.Join(staging, "child-ready"),
				descendantPIDPath, signalSeen, childPIDPath)
			withBoundedProductionForkBuildChild(t, time.Hour)

			probes := 0
			previousProbe := probeForkBuildContainer
			probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
				probes++
				if gotName != cname || gotRuntime != "podman" {
					t.Errorf("original-backend probe = (%q,%q), want (%q,podman)", gotName, gotRuntime, cname)
				}
				return false, true
			}
			t.Cleanup(func() { probeForkBuildContainer = previousProbe })

			ctx := context.Background() // The child alone receives the external, handled signal.
			buildDone := make(chan error, 1)
			go func() {
				_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
					runJail: childJail(ctx, false, nil)}, io.Discard, io.Discard, false)
				buildDone <- err
			}()
			waitForLifecycleFile(t, filepath.Join(staging, "child-ready"), 3*time.Second)
			childData, err := os.ReadFile(childPIDPath)
			if err != nil {
				t.Fatal(err)
			}
			childPID, err := strconv.Atoi(strings.TrimSpace(string(childData)))
			if err != nil {
				t.Fatalf("child pid %q: %v", childData, err)
			}
			before := snapshotForkBuildLifetimeState(t, staging, cname)
			if len(before) == 0 {
				t.Fatal("no stable pre-signal workspace evidence was captured")
			}
			if err := syscall.Kill(childPID, tc.sig); err != nil {
				t.Fatalf("deliver %s to only the build child: %v", tc.name, err)
			}
			select {
			case err := <-buildDone:
				wantStatus := 128 + int(tc.sig)
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exited %d", wantStatus)) {
					t.Fatalf("handled %s reached build caller as %v; want ordinary exit status %d", tc.name, err, wantStatus)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("build caller did not return after handled %s", tc.name)
			}
			if ctx.Err() != nil {
				t.Fatalf("parent context was cancelled: %v", ctx.Err())
			}
			handledText := map[string]string{"INT": "interrupt", "HUP": "hangup", "TERM": "terminated"}[tc.name]
			if got, err := os.ReadFile(signalSeen); err != nil || !strings.Contains(string(got), handledText+" handled") {
				t.Fatalf("fixture did not record actual handled %s delivery: %q (%v)", tc.name, got, err)
			}
			if forkBuildRunReturned(staging) {
				t.Fatal("handled os.Exit path wrote the normal run-return witness")
			}
			if after := snapshotForkBuildLifetimeState(t, staging, cname); !reflect.DeepEqual(before, after) {
				t.Fatal("handled signal first return changed retained staging/home/tracking/runtime bytes")
			}
			if keys, _ := store.EntryKeys(); len(keys) != 0 {
				t.Fatalf("handled signal admitted its complete-looking manifest: %v", keys)
			}
			helperData, err := os.ReadFile(descendantPIDPath)
			if err != nil {
				t.Fatalf("same-group descriptor-free helper did not start: %v", err)
			}
			helperPID, err := strconv.Atoi(strings.TrimSpace(string(helperData)))
			if err != nil {
				t.Fatalf("helper pid %q: %v", helperData, err)
			}
			assertLinuxProcessStopped(t, helperPID)

			// Retry hits the actual keeper gate before the original-backend probe or Stage.
			_, err = buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
				runJail: func(string, forkBuild, captureStreams) int {
					t.Error("same-ID retry reached Stage/build dispatch while keeper still owns the workspace")
					return 1
				}}, io.Discard, io.Discard, false)
			if err == nil || !strings.Contains(err.Error(), "keeper") || probes != 0 {
				t.Fatalf("live-keeper retry err=%v probes=%d; want keeper refusal before probe/Stage", err, probes)
			}
			if after := snapshotForkBuildLifetimeState(t, staging, cname); !reflect.DeepEqual(before, after) {
				t.Fatal("keeper refusal changed retained staging/home/tracking/runtime evidence")
			}

			if err := keeper.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("stop fixture keeper: %v", err)
			}
			_, _ = keeper.Wait()
			_, err = buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "container",
				runJail: func(string, forkBuild, captureStreams) int { return 17 }}, io.Discard, io.Discard, false)
			if err == nil || probes != 1 {
				t.Fatalf("retry after keeper exit err=%v probes=%d; want a new build after one absent podman probe", err, probes)
			}
			if _, statErr := os.Stat(staging); !os.IsNotExist(statErr) {
				t.Errorf("known-absent original backend did not permit safe cleanup: %v", statErr)
			}
		})
	}
}

func TestHandledForkBuildSignalWithoutKeeperStillRetainsUnknownRunLifetime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	ready := filepath.Join(t.TempDir(), "child-ready")
	descendantPIDPath := filepath.Join(t.TempDir(), "descriptor-free-helper.pid")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	signalSeen := filepath.Join(t.TempDir(), "handled-signal.txt")
	t.Setenv(forkBuildResultFixtureSignalNameEnv, "TERM")
	installForkBuildResultFixtureWithPaths(t, "handled-signal", ready, descendantPIDPath, signalSeen, childPIDPath)
	withBoundedProductionForkBuildChild(t, time.Hour)

	probes := 0
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		probes++
		if gotName != cname || gotRuntime != "podman" {
			t.Errorf("original-backend probe = (%q,%q), want (%q,podman)", gotName, gotRuntime, cname)
		}
		return false, true // Backend absence cannot replace the missing run-return witness.
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })

	ctx := context.Background()
	buildDone := make(chan error, 1)
	go func() {
		_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
			runJail: childJail(ctx, false, nil)}, io.Discard, io.Discard, false)
		buildDone <- err
	}()
	waitForLifecycleFile(t, ready, 3*time.Second)
	pidData, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatalf("child pid %q: %v", pidData, err)
	}
	before := snapshotForkBuildLifetimeState(t, staging, cname)
	if len(before) == 0 {
		t.Fatal("no stable pre-signal workspace evidence was captured")
	}
	if err := syscall.Kill(childPID, syscall.SIGTERM); err != nil {
		t.Fatalf("deliver handled TERM to only the build child: %v", err)
	}
	select {
	case err := <-buildDone:
		if err == nil || !strings.Contains(err.Error(), "exited 143") {
			t.Fatalf("handled TERM reached build caller as %v; want ordinary exit status 143", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("build caller did not return after handled TERM")
	}
	if ctx.Err() != nil {
		t.Fatalf("parent context was cancelled: %v", ctx.Err())
	}
	if forkBuildRunReturned(staging) || probes != 0 {
		t.Fatalf("handled TERM was mistaken for lifetime completion: returned=%v probes=%d", forkBuildRunReturned(staging), probes)
	}
	if after := snapshotForkBuildLifetimeState(t, staging, cname); !reflect.DeepEqual(before, after) {
		t.Fatal("handled TERM first return changed retained staging/home/tracking/runtime bytes")
	}
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("handled TERM with no keeper removed the uncertain workspace: %v", err)
	}
	if keys, _ := store.EntryKeys(); len(keys) != 0 {
		t.Fatalf("handled TERM admitted output: %v", keys)
	}
}

func TestForkBuildDoesNotAdmitMutableOutputOrDeleteWorkspaceAfterUnknownKeeperWait(t *testing.T) {
	for _, route := range []string{"child", "direct"} {
		for _, rc := range []int{0, 17} {
			for _, keeperState := range []string{"live", "unknown", "backend-present", "backend-unknown"} {
				t.Run(fmt.Sprintf("%s/exit-%d/keeper-%s", route, rc, keeperState), func(t *testing.T) {
					t.Setenv("HOME", t.TempDir())
					f := forkBuildHome(t)
					b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
					store := &capture.Store{Dir: paths.CapturesDir()}
					staging := store.StagingDir("fork-" + b.id())
					cname := runtime.FromWorkspace(staging)
					ready := filepath.Join(t.TempDir(), "keeper-ready")
					var keeper *os.Process
					if keeperState == "live" {
						lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
						keeper = startForkBuildDetachedKeeperWithLock(t, ready, lockPath)
					} else if keeperState == "unknown" {
						lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
						if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(lockPath, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					withForkBuildGoneWait(t, 0)
					probePresent, probeKnown := false, true
					if keeperState == "backend-present" {
						probePresent = true
					} else if keeperState == "backend-unknown" {
						probeKnown = false
					}
					probeCalls := 0
					previousProbe := probeForkBuildContainer
					probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
						probeCalls++
						if gotName != cname || gotRuntime != "podman" {
							t.Errorf("lifetime probe = (%q,%q), want (%q,podman)", gotName, gotRuntime, cname)
						}
						return probePresent, probeKnown
					}
					t.Cleanup(func() { probeForkBuildContainer = previousProbe })

					if route == "child" {
						mode := "ordinary-success"
						if rc != 0 {
							mode = "ordinary-failure"
						}
						installForkBuildResultFixtureWithPaths(t, mode, filepath.Join(staging, "child-ready"), "", "", "")
						withBoundedProductionForkBuildChild(t, time.Hour)
						_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
							runJail: childJail(context.Background(), false, nil)}, io.Discard, io.Discard, false)
						if err == nil {
							t.Fatal("child with unresolved keeper lifetime unexpectedly succeeded")
						}
					} else {
						withFakeCaptureJail(t, func(o run.Options) int {
							if err := o.OnRuntimeResolved("podman"); err != nil {
								t.Fatalf("persist direct runtime: %v", err)
							}
							if err := runtime.WriteContainerTracking(cname, o.Workspace); err != nil {
								t.Fatal(err)
							}
							if err := os.MkdirAll(filepath.Join(paths.AgentsDir(), cname), 0o755); err != nil {
								t.Fatal(err)
							}
							writeFile(t, filepath.Join(paths.AgentsDir(), cname, "lifetime-fixture"), "owner")
							writeForkBuildFixtureOutput(t, o.Workspace)
							writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "fixture-image\n")
							return rc
						})
						_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman"}, io.Discard, io.Discard, false)
						if err == nil {
							t.Fatal("direct capture with nonzero status or unresolved keeper lifetime unexpectedly succeeded")
						}
					}
					if keys, _ := store.EntryKeys(); len(keys) != 0 {
						t.Fatalf("unresolved keeper output was admitted: %v", keys)
					}
					for _, path := range []string{staging, filepath.Join(paths.AgentsDir(), cname),
						filepath.Join(paths.ContainerDir(), cname), forkBuildRuntimeRecordPath(staging)} {
						if _, err := os.Stat(path); err != nil {
							t.Errorf("unknown/live keeper completion deleted %s: %v", path, err)
						}
					}
					wantProbe := keeperState == "backend-present" || keeperState == "backend-unknown"
					if (probeCalls > 0) != wantProbe {
						t.Errorf("backend probe calls=%d, want called=%v", probeCalls, wantProbe)
					}
					if keeper != nil {
						_ = keeper.Signal(syscall.SIGTERM)
						_, _ = keeper.Wait()
					}
				})
			}
		}
	}
}

func TestNativeBuildUnknownKeeperRefusesAdmissionAndRetainsOriginalState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	probes := 0
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		probes++
		t.Error("native admission must not invent a container probe")
		return false, false
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })

	var original map[string]string
	withFakeCaptureJail(t, func(o run.Options) int {
		if err := o.OnRuntimeResolved("macos-user"); err != nil {
			t.Fatalf("persist actual native backend: %v", err)
		}
		if err := runtime.WriteContainerTracking(cname, o.Workspace); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(paths.AgentsDir(), cname), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(paths.AgentsDir(), cname, "lifetime-fixture"), "owner")
		writeForkBuildFixtureOutput(t, o.Workspace)
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "native-fixture\n")
		if err := writeForkBuildRunReturned(o.Workspace); err != nil {
			t.Fatal(err)
		}
		original = snapshotForkBuildLifetimeState(t, staging, cname)
		return 0
	})
	var stderr bytes.Buffer
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "macos-user"}, io.Discard, &stderr, false)
	if err == nil || entry != nil || !strings.Contains(stderr.String(), "no output was admitted") {
		t.Fatalf("native build with unknown keeper ownership: entry=%v err=%v stderr=%s; want admission refusal", entry, err, stderr.String())
	}
	if len(original) == 0 || !reflect.DeepEqual(original, snapshotForkBuildLifetimeState(t, staging, cname)) {
		t.Fatal("unknown-keeper refusal changed the original staging/home/tracking/runtime bytes")
	}
	if keys, _ := store.EntryKeys(); len(keys) != 0 {
		t.Fatalf("native output with unknown ownership was admitted: %v", keys)
	}
	if probes != 0 {
		t.Fatalf("native admission made %d container probes, want none", probes)
	}

	_, err = buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "macos-user",
		runJail: func(string, forkBuild, captureStreams) int {
			t.Error("same-ID retry reached build dispatch while keeper ownership is unknown")
			return 1
		}}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "keeper") {
		t.Fatalf("retry with unknown native keeper returned %v; want fail-closed keeper refusal", err)
	}
	if !reflect.DeepEqual(original, snapshotForkBuildLifetimeState(t, staging, cname)) {
		t.Fatal("unknown-keeper retry changed the original staging/home/tracking/runtime bytes")
	}
	if probes != 0 {
		t.Fatalf("unknown native ownership caused %d container probes, want none", probes)
	}
}

func TestNativePlainBuildWithKnownOwnershipStillAdmitsWithoutContainerProbe(t *testing.T) {
	result := runNativeForkBuildForOwnershipTest(t, nil)
	if result.err != nil || result.entry == nil {
		t.Fatalf("synchronous plain native success was rejected: entry=%v err=%v stderr=%s", result.entry, result.err, result.stderr)
	}
	if _, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(result.entry.Key); err != nil {
		t.Fatalf("plain native output was not admitted: %v", err)
	}
	if result.probeCalls != 0 {
		t.Fatalf("plain native success made %d container probes, want none", result.probeCalls)
	}
	for _, path := range []string{result.staging, filepath.Join(paths.AgentsDir(), result.cname),
		filepath.Join(paths.ContainerDir(), result.cname), forkBuildRuntimeRecordPath(result.staging)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("known-complete plain native success did not clean %s: %v", path, err)
		}
	}
}

func TestNativeBuildHeldKeeperAndWorkspaceLocksRefuseAdmission(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
	}{
		{name: "real held keeper flock", owner: "keeper-held"},
		{name: "busy workspace launch flock", owner: "workspace-busy"},
		{name: "unknown workspace lock path", owner: "workspace-unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runNativeForkBuildForOwnershipTest(t, func(cname string) {
				switch tc.owner {
				case "keeper-held":
					holdForkBuildFileLock(t, filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper"))
				case "workspace-busy":
					holdForkBuildFileLock(t, filepath.Join(paths.GlobalStorage(), "locks", cname+".lock"))
				case "workspace-unknown":
					lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".lock")
					if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(lockPath, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			})
			assertNativeOwnershipRefusal(t, result)
		})
	}
}

type nativeBuildOwnershipResult struct {
	entry      *capture.Entry
	err        error
	stderr     string
	staging    string
	cname      string
	original   map[string]string
	probeCalls int
}

func runNativeForkBuildForOwnershipTest(t *testing.T, setup func(string)) nativeBuildOwnershipResult {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	if setup != nil {
		setup(cname)
	}
	previousProbe := probeForkBuildContainer
	probes := 0
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		probes++
		t.Errorf("native completion invented a container probe")
		return false, false
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })

	var original map[string]string
	withFakeCaptureJail(t, func(o run.Options) int {
		if err := o.OnRuntimeResolved("macos-user"); err != nil {
			t.Fatalf("persist native runtime: %v", err)
		}
		if err := runtime.WriteContainerTracking(cname, o.Workspace); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(paths.AgentsDir(), cname), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(paths.AgentsDir(), cname, "native-owner"), "fixture-owner-bytes")
		writeForkBuildFixtureOutput(t, o.Workspace)
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "native-fixture\n")
		if err := writeForkBuildRunReturned(o.Workspace); err != nil {
			t.Fatal(err)
		}
		original = snapshotForkBuildLifetimeState(t, staging, cname)
		return 0
	})
	var stderr bytes.Buffer
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "macos-user"}, io.Discard, &stderr, false)
	return nativeBuildOwnershipResult{entry: entry, err: err, stderr: stderr.String(), staging: staging,
		cname: cname, original: original, probeCalls: probes}
}

func assertNativeOwnershipRefusal(t *testing.T, result nativeBuildOwnershipResult) {
	t.Helper()
	if result.err == nil || result.entry != nil || !strings.Contains(result.stderr, "no output was admitted") {
		t.Fatalf("native owner uncertainty: entry=%v err=%v stderr=%s; want refusal", result.entry, result.err, result.stderr)
	}
	if len(result.original) == 0 || !reflect.DeepEqual(result.original,
		snapshotForkBuildLifetimeState(t, result.staging, result.cname)) {
		t.Fatal("native owner refusal changed original staging/home/tracking/runtime bytes")
	}
	if keys, _ := (&capture.Store{Dir: paths.CapturesDir()}).EntryKeys(); len(keys) != 0 {
		t.Fatalf("native output with unknown ownership was admitted: %v", keys)
	}
	if result.probeCalls != 0 {
		t.Fatalf("native owner check made %d container probes, want none", result.probeCalls)
	}
}

func writeForkBuildFixtureOutput(t *testing.T, workspace string) {
	t.Helper()
	out := filepath.Join(workspace, captureOutLeaf)
	program := filepath.Join(capture.TreeDir(out), ".local", "bin", "probetool")
	if err := os.MkdirAll(filepath.Dir(program), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte(strings.Repeat("x", 32)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := capture.WriteManifest(out, &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: captureJailPlatform(),
		Surfaces: []string{".npm-global", ".local", "go"}, Excluded: capture.DefaultExcludes(), Entries: probetoolBuilt,
	}); err != nil {
		t.Fatal(err)
	}
}

func snapshotForkBuildLifetimeState(t *testing.T, staging, cname string) map[string]string {
	t.Helper()
	roots := []string{staging, filepath.Join(paths.AgentsDir(), cname), filepath.Join(paths.ContainerDir(), cname),
		forkBuildRuntimeRecordPath(staging), forkBuildRunReturnedPath(staging)}
	out := make(map[string]string)
	for _, root := range roots {
		info, err := os.Lstat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		if !info.IsDir() {
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(root)
				if err != nil {
					t.Fatal(err)
				}
				out[root] = "symlink:" + link
			} else {
				data, err := os.ReadFile(root)
				if err != nil {
					t.Fatal(err)
				}
				out[root] = fmt.Sprintf("%s:%s", info.Mode(), data)
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				out[path] = "symlink:" + link
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = fmt.Sprintf("%s:%s", info.Mode(), data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func assertLinuxProcessStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err == nil {
			if close := strings.LastIndexByte(string(data), ')'); close >= 0 {
				fields := strings.Fields(string(data)[close+1:])
				if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("same-group descriptor-free helper %d remained active after caller return", pid)
}
