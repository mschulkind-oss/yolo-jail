package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

type forkRecoveryEvidence string

const (
	forkRecoveryMissing    forkRecoveryEvidence = "missing"
	forkRecoveryPodman     forkRecoveryEvidence = "podman"
	forkRecoveryContainer  forkRecoveryEvidence = "container"
	forkRecoveryMalformed  forkRecoveryEvidence = "malformed"
	forkRecoveryUnreadable forkRecoveryEvidence = "unreadable"
	forkRecoveryUnknown    forkRecoveryEvidence = "unknown-runtime"
)

func TestForkBuildRetryJoinsPersistedRuntimeEvidenceBeforeReuse(t *testing.T) {
	tests := []struct {
		name          string
		sidecar       forkRecoveryEvidence
		keeper        forkRecoveryEvidence
		parentRuntime string
		probePresent  bool
		probeKnown    bool
		wantRuntime   string
		wantProbe     bool
		wantRun       bool
		wantError     string
	}{
		{
			name:    "equal persisted records use original backend despite changed parent selection",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryPodman, parentRuntime: "container",
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid container sidecar records its own runtime",
			sidecar: forkRecoveryContainer, keeper: forkRecoveryMissing,
			probeKnown: true, wantRuntime: "container", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid sidecar is sufficient when keeper record is missing",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryMissing,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid sidecar is sufficient when keeper record is malformed",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryMalformed,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid sidecar is sufficient when keeper record is unreadable",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryUnreadable,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid keeper record falls back when sidecar is missing",
			sidecar: forkRecoveryMissing, keeper: forkRecoveryPodman,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid keeper record falls back when sidecar is malformed",
			sidecar: forkRecoveryMalformed, keeper: forkRecoveryPodman,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid keeper record falls back when sidecar is unreadable",
			sidecar: forkRecoveryUnreadable, keeper: forkRecoveryPodman,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid keeper record falls back when sidecar contains an unrecognized runtime",
			sidecar: forkRecoveryUnknown, keeper: forkRecoveryPodman,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "valid sidecar remains authoritative over an unrecognized keeper runtime",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryUnknown,
			probeKnown: true, wantRuntime: "podman", wantProbe: true, wantRun: true,
		},
		{
			name:    "two valid conflicting records refuse before runtime probe",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryContainer,
			wantError: "disagrees with the keeper's original backend",
		},
		{
			name:    "no sidecar or keeper record is not evidence",
			sidecar: forkRecoveryMissing, keeper: forkRecoveryMissing,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "malformed sidecar and no keeper record are not evidence",
			sidecar: forkRecoveryMalformed, keeper: forkRecoveryMissing,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "unreadable sidecar and no keeper record are not evidence",
			sidecar: forkRecoveryUnreadable, keeper: forkRecoveryMissing,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "missing sidecar and malformed keeper record are not evidence",
			sidecar: forkRecoveryMissing, keeper: forkRecoveryMalformed,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "missing sidecar and unreadable keeper record are not evidence",
			sidecar: forkRecoveryMissing, keeper: forkRecoveryUnreadable,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "unrecognized sidecar and no keeper record are not evidence",
			sidecar: forkRecoveryUnknown, keeper: forkRecoveryMissing,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "unrecognized keeper runtime is not probeable evidence",
			sidecar: forkRecoveryMissing, keeper: forkRecoveryUnknown,
			wantError: "original capture runtime cannot be established",
		},
		{
			name:    "known original backend presence retains the old workspace",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryPodman,
			probePresent: true, probeKnown: true, wantRuntime: "podman", wantProbe: true,
			wantError: "still present",
		},
		{
			name:    "original backend probe error is unknown, not absence",
			sidecar: forkRecoveryPodman, keeper: forkRecoveryPodman,
			probeKnown: false, wantRuntime: "podman", wantProbe: true,
			wantError: "could not confirm",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if tc.parentRuntime != "" {
				t.Setenv("YOLO_RUNTIME", tc.parentRuntime)
			}
			f := forkBuildHome(t)
			b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
			staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
			if err := runtime.WriteContainerTracking(cname, staging); err != nil {
				t.Fatal(err)
			}
			sidecarPath := forkBuildRuntimeRecordPath(staging)
			setForkRecoverySidecar(t, sidecarPath, tc.sidecar)
			keeperPath := forkBuildKeeperRecordPath(cname)
			setForkRecoveryKeeperRecord(t, keeperPath, tc.keeper)
			if tc.keeper == forkRecoveryPodman || tc.keeper == forkRecoveryContainer || tc.keeper == forkRecoveryUnknown {
				want := recoveryEvidenceRuntime(tc.keeper)
				if got, ok := run.LaunchedRuntime(staging); !ok || got != want {
					t.Fatalf("persisted keeper start record reads as %q (%v), want %q", got, ok, want)
				}
			}

			previousProbe := probeForkBuildContainer
			probeCalls := 0
			var probeRuntimes []string
			probeRuntime := ""
			probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
				probeCalls++
				if gotName != cname {
					t.Errorf("probe cname = %q, want retained build jail %q", gotName, cname)
				}
				probeRuntime = gotRuntime
				probeRuntimes = append(probeRuntimes, gotRuntime)
				return tc.probePresent, tc.probeKnown
			}
			t.Cleanup(func() { probeForkBuildContainer = previousProbe })

			var seen run.Options
			fake := fakeBuildJail(t, &seen, probetoolBuilt)
			runCalls := 0
			newRuntime := tc.parentRuntime
			if newRuntime == "" {
				newRuntime = "podman"
			}
			entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: tc.parentRuntime,
				runJail: func(workspace string, _ forkBuild, _ captureStreams) int {
					runCalls++
					return fake(run.Options{Workspace: workspace, Getenv: func(key string) string {
						if key == "YOLO_RUNTIME" {
							return newRuntime
						}
						return ""
					}, OnRuntimeResolved: func(rt string) error {
						return writeForkBuildRuntime(workspace, rt)
					}})
				}}, io.Discard, io.Discard, false)

			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("retry error = %v, want text %q", err, tc.wantError)
				}
				if entry != nil || runCalls != boolInt(tc.wantRun) {
					t.Fatalf("refused retry entry=%v, runCalls=%d; wanted no build dispatch", entry, runCalls)
				}
				if probeCalls != boolInt(tc.wantProbe) {
					t.Errorf("refused retry probed %d times, want %d", probeCalls, boolInt(tc.wantProbe))
				}
				if tc.wantProbe && probeRuntime != tc.wantRuntime {
					t.Errorf("probe runtime = %q, want original backend %q", probeRuntime, tc.wantRuntime)
				}
				assertForkRecoveryStateRetained(t, staging, cname, marker)
				assertForkRecoveryEvidenceRetained(t, sidecarPath, tc.sidecar, keeperPath, tc.keeper)
				return
			}
			if err != nil || entry == nil || runCalls != boolInt(tc.wantRun) {
				t.Fatalf("retry with trusted absent-backend evidence: entry=%v runCalls=%d err=%v", entry, runCalls, err)
			}
			if !tc.wantProbe || probeCalls != 2 || len(probeRuntimes) != 2 || probeRuntimes[0] != tc.wantRuntime ||
				probeRuntimes[1] != newRuntime {
				t.Errorf("original-backend probes = %v; want reuse probe %q then completed-build probe %q",
					probeRuntimes, tc.wantRuntime, newRuntime)
			}
			if _, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(entry.Key); err != nil {
				t.Errorf("successful retry did not admit its build: %v", err)
			}
			for _, path := range []string{staging, filepath.Join(paths.AgentsDir(), cname),
				filepath.Join(paths.ContainerDir(), cname), sidecarPath} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("known-absent original backend did not clean old workspace evidence %s: %v", path, err)
				}
			}
		})
	}
}

func TestForkBuildRetryRuntimeCommandErrorIsUnknownThroughProductionProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	if err := runtime.WriteContainerTracking(cname, staging); err != nil {
		t.Fatal(err)
	}
	keeperPath := forkBuildKeeperRecordPath(cname)
	setForkRecoveryKeeperRecord(t, keeperPath, forkRecoveryPodman)
	trace := filepath.Join(t.TempDir(), "probe-commands")
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$YOLO_TEST_RUNTIME_TRACE"
exit 37
`
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(cname, rt string, timeout time.Duration) (bool, bool) {
		return run.ProbeExistingContainer(cname, rt, timeout)
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	t.Setenv("YOLO_TEST_RUNTIME_TRACE", trace)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	runs := 0
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "container",
		runJail: func(string, forkBuild, captureStreams) int { runs++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "could not confirm") {
		t.Fatalf("retry after original runtime command failure = %v, want unknown-liveness refusal", err)
	}
	if runs != 0 {
		t.Errorf("build ran %d times after original runtime probe errored", runs)
	}
	commands, readErr := os.ReadFile(trace)
	if readErr != nil || !strings.Contains(string(commands), "ps -a -q --filter name=^/"+cname+"$") {
		t.Errorf("production probe did not query the persisted podman backend: %q (%v)", commands, readErr)
	}
	assertForkRecoveryStateRetained(t, staging, cname, marker)
	assertForkRecoveryEvidenceRetained(t, forkBuildRuntimeRecordPath(staging), forkRecoveryPodman,
		keeperPath, forkRecoveryPodman)
}

func TestForkBuildRetryWorkspaceLockOpenFailureRetainsEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	if err := runtime.WriteContainerTracking(cname, staging); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		t.Error("runtime probe ran after workspace lock open failed")
		return false, true
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })

	runs := 0
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "container",
		runJail: func(string, forkBuild, captureStreams) int { runs++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "workspace launch ownership") {
		t.Fatalf("retry with unopenable workspace lock = %v, want ownership refusal", err)
	}
	if runs != 0 {
		t.Errorf("build ran %d times after workspace lock failure", runs)
	}
	assertForkRecoveryStateRetained(t, staging, cname, marker)
	assertForkRecoveryEvidenceRetained(t, forkBuildRuntimeRecordPath(staging), forkRecoveryPodman,
		forkBuildKeeperRecordPath(cname), forkRecoveryMissing)
}

func TestForkBuildRetryUncheckableKeeperLockRetainsEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	if err := runtime.WriteContainerTracking(cname, staging); err != nil {
		t.Fatal(err)
	}
	keeperLock := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
	if err := os.MkdirAll(filepath.Dir(keeperLock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(keeperLock, 0o700); err != nil {
		t.Fatal(err)
	}
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		t.Error("runtime probe ran while keeper liveness was unknown")
		return false, true
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })

	runs := 0
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(string, forkBuild, captureStreams) int { runs++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "keeper") || !strings.Contains(err.Error(), "retaining staging") {
		t.Fatalf("retry with uncheckable keeper liveness = %v, want fail-closed keeper refusal", err)
	}
	if runs != 0 {
		t.Errorf("build ran %d times with unknown keeper liveness", runs)
	}
	assertForkRecoveryStateRetained(t, staging, cname, marker)
	assertForkRecoveryEvidenceRetained(t, forkBuildRuntimeRecordPath(staging), forkRecoveryPodman,
		forkBuildKeeperRecordPath(cname), forkRecoveryMissing)
}

func setForkRecoverySidecar(t *testing.T, path string, state forkRecoveryEvidence) {
	t.Helper()
	_ = os.RemoveAll(path)
	switch state {
	case forkRecoveryMissing:
	case forkRecoveryPodman, forkRecoveryContainer:
		if err := writeForkBuildRuntime(strings.TrimSuffix(path, forkBuildRuntimeRecordSuffix), recoveryEvidenceRuntime(state)); err != nil {
			t.Fatal(err)
		}
	case forkRecoveryUnknown:
		if err := os.WriteFile(path, []byte("buildkit-forged\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	case forkRecoveryMalformed:
		if err := os.WriteFile(path, []byte("not a runtime record\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	case forkRecoveryUnreadable:
		// A link to a directory is present to Lstat but cannot be read as a runtime record. The
		// recovery cleanup removes the link itself without a chmod-dependent test identity.
		target := path + ".directory-target"
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported sidecar fixture state %q", state)
	}
}

func setForkRecoveryKeeperRecord(t *testing.T, path string, state forkRecoveryEvidence) {
	t.Helper()
	_ = os.RemoveAll(path)
	switch state {
	case forkRecoveryMissing:
	case forkRecoveryPodman, forkRecoveryContainer, forkRecoveryUnknown:
		record := struct {
			PID     int    `json:"pid"`
			Runtime string `json:"runtime"`
		}{PID: 987654321, Runtime: recoveryEvidenceRuntime(state)}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	case forkRecoveryMalformed:
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	case forkRecoveryUnreadable:
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		target := path + ".directory-target"
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported keeper fixture state %q", state)
	}
}

func forkBuildKeeperRecordPath(cname string) string {
	return filepath.Join(paths.GlobalStorage(), "owners", cname+".keeper.json")
}

func recoveryEvidenceRuntime(state forkRecoveryEvidence) string {
	switch state {
	case forkRecoveryPodman:
		return "podman"
	case forkRecoveryContainer:
		return "container"
	case forkRecoveryUnknown:
		return "buildkit-forged"
	default:
		return ""
	}
}

func assertForkRecoveryEvidenceRetained(t *testing.T, sidecarPath string, sidecar forkRecoveryEvidence,
	keeperPath string, keeper forkRecoveryEvidence) {
	t.Helper()
	for _, item := range []struct {
		path string
		want bool
	}{{sidecarPath, sidecar != forkRecoveryMissing}, {keeperPath, keeper != forkRecoveryMissing}} {
		_, err := os.Lstat(item.path)
		if item.want && err != nil {
			t.Errorf("refused retry removed persisted evidence %s: %v", item.path, err)
		}
		if !item.want && !os.IsNotExist(err) {
			t.Errorf("missing evidence path %s unexpectedly exists: %v", item.path, err)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func assertForkRecoveryStateRetained(t *testing.T, staging, cname, marker string) {
	t.Helper()
	for _, path := range []string{staging, marker, filepath.Join(paths.AgentsDir(), cname),
		filepath.Join(paths.ContainerDir(), cname)} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("refused retry removed or lost retained state %s: %v", path, err)
		}
	}
}

// A macos-user build whose run returned has the same proof forkBuildWorkspaceReclaimable accepts, so a
// retry reuses its workspace without asking the native backend, which can only answer unknown.
func TestAMacosUserBuildThatReturnedIsReusedOnRetry(t *testing.T) {
	tests := []struct {
		name     string
		returned bool
	}{
		{name: "returned", returned: true},
		{name: "not returned"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := forkBuildHome(t)
			b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
			staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
			if err := runtime.WriteContainerTracking(cname, staging); err != nil {
				t.Fatal(err)
			}
			if err := writeForkBuildRuntime(staging, "macos-user"); err != nil {
				t.Fatal(err)
			}
			if tc.returned {
				if err := writeForkBuildRunReturned(staging); err != nil {
					t.Fatal(err)
				}
			}

			previousProbe := probeForkBuildContainer
			probeByRuntime := map[string]int{}
			probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
				if gotName != cname {
					t.Errorf("probe cname = %q, want %q", gotName, cname)
				}
				probeByRuntime[gotRuntime]++
				// The new run's own completion check on its runtime may answer known-absent; native stays unknown.
				return false, gotRuntime != "macos-user"
			}
			t.Cleanup(func() { probeForkBuildContainer = previousProbe })

			var seen run.Options
			fake := fakeBuildJail(t, &seen, probetoolBuilt)
			runCalls := 0
			entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
				runJail: func(workspace string, _ forkBuild, _ captureStreams) int {
					runCalls++
					return fake(run.Options{Workspace: workspace, Getenv: func(key string) string {
						if key == "YOLO_RUNTIME" {
							return "podman"
						}
						return ""
					}, OnRuntimeResolved: func(rt string) error {
						return writeForkBuildRuntime(workspace, rt)
					}})
				}}, io.Discard, io.Discard, false)

			if probeByRuntime["macos-user"] != 0 && tc.returned {
				t.Errorf("returned macos-user build was probed on macos-user %d times; the run-returned proof must skip it", probeByRuntime["macos-user"])
			}
			if !tc.returned {
				nativeTree := macosuser.ForkBuildStagingRoot("", b.id())
				if err == nil || !strings.Contains(err.Error(), "cannot prove macos-user build") ||
					!strings.Contains(err.Error(), nativeTree) {
					t.Fatalf("unreturned macos-user retry error = %v, want refusal naming native tree %s", err, nativeTree)
				}
				if entry != nil || runCalls != 0 {
					t.Fatalf("refused retry entry=%v runCalls=%d; want no dispatch", entry, runCalls)
				}
				if probeByRuntime["macos-user"] != 1 {
					t.Errorf("unreturned macos-user probed %d times, want 1", probeByRuntime["macos-user"])
				}
				assertForkRecoveryStateRetained(t, staging, cname, marker)
				return
			}
			if err != nil || entry == nil || runCalls != 1 {
				t.Fatalf("returned macos-user retry: entry=%v runCalls=%d err=%v; want one dispatch and an entry", entry, runCalls, err)
			}
		})
	}
}

// The run-returned witness skips the probe for macos-user alone: a container backend's build that
// returned is still probed, so a jail still present there keeps its workspace.
func TestARetainedContainerBuildThatReturnedIsStillProbed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	if err := runtime.WriteContainerTracking(cname, staging); err != nil {
		t.Fatal(err)
	}
	if err := writeForkBuildRuntime(staging, "podman"); err != nil {
		t.Fatal(err)
	}
	if err := writeForkBuildRunReturned(staging); err != nil {
		t.Fatal(err)
	}
	previousProbe := probeForkBuildContainer
	probes := 0
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		probes++
		return true, true // still present
	}
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	runCalls := 0
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(string, forkBuild, captureStreams) int { runCalls++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "still present") || entry != nil || runCalls != 0 || probes != 1 {
		t.Fatalf("retry of a returned podman build whose jail is present: entry=%v runCalls=%d probes=%d err=%v; "+
			"want one probe and the still-present refusal", entry, runCalls, probes, err)
	}
	assertForkRecoveryStateRetained(t, staging, cname, marker)
}
