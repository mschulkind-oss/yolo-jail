package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestForkBuildChildRecordsRuntimeForEveryBuildKind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name  string
		fork  packload.Fork
		build forkBuild
	}{
		{
			name: "plain",
			fork: packload.Fork{Pack: "forkpack", Bin: "tool", Build: "make install"},
		},
		{
			name:  "patched",
			fork:  packload.Fork{Pack: "forkpack", Bin: "tool", Build: "make install"},
			build: forkBuild{Series: packsrc.EmptySeries()},
		},
		{
			name: "tree with command",
			fork: packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Build: "make install"},
		},
		{
			name: "tree without command",
			fork: packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			b := tc.build
			b.Fork = tc.fork
			if b.Fork.Build != "" {
				b.Commit = forkTestCommit
			}
			argv := forkBuildChildArgv(workspace, b, false)
			var resolvedOpts run.Options
			withFakeCaptureJail(t, func(o run.Options) int {
				resolvedOpts = o
				return 0
			})
			if got := runForkBuildJail(argv[2:], io.Discard, io.Discard); got != 0 {
				t.Fatalf("hidden child returned %d", got)
			}
			if resolvedOpts.OnRuntimeResolved == nil {
				t.Fatal("actual hidden build child did not wire a runtime persistence callback")
			}
			if err := resolvedOpts.OnRuntimeResolved("podman"); err != nil {
				t.Fatalf("record resolved runtime: %v", err)
			}
			if got, err := readForkBuildRuntime(workspace); err != nil || got != "podman" {
				t.Fatalf("persisted runtime = %q, %v; want podman", got, err)
			}
			if got := resolvedOpts.SealedTree; tc.fork.IsTree() && got != tc.fork.Bin {
				t.Errorf("sealed tree = %q, want %q", got, tc.fork.Bin)
			}
		})
	}
}

func TestDirectForkBuildCaptureRecordsRuntimeForEveryBuildKind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name string
		fork packload.Fork
		b    forkBuild
	}{
		{name: "plain", fork: packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Build: "make install"}},
		{name: "patched", fork: packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Build: "make install"}, b: forkBuild{Series: packsrc.EmptySeries()}},
		{name: "tree with command", fork: packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Build: "make install"}},
		{name: "tree without command", fork: packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			b := tc.b
			b.Fork = tc.fork
			var seen run.Options
			withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 0 })
			if rc := forkBuildRunJail(workspace, b, captureStreams{}, false); rc != 0 {
				t.Fatalf("direct capture pipeline returned %d", rc)
			}
			if seen.OnRuntimeResolved == nil {
				t.Fatal("direct build capture did not wire runtime persistence")
			}
			if err := seen.OnRuntimeResolved("container"); err != nil {
				t.Fatalf("record resolved runtime: %v", err)
			}
			if got, err := readForkBuildRuntime(workspace); err != nil || got != "container" {
				t.Fatalf("persisted runtime = %q, %v; want container", got, err)
			}
		})
	}
}

func TestBuildCapturePersistsConfinementSelectedGuestRuntimeBeforeDispatch(t *testing.T) {
	workspace := forkBuildRuntimeWorkspace(t, `{"confinement":"guest"}`)
	var dispatched bool
	rc, stderr := runActualBuildChild(t, workspace, "make install", false, func(o *run.Options) {
		o.IsMacOS, o.IsLinux = true, false
		o.DryRun = true
		o.Getenv = func(string) string { return "" }
		o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
		o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
			_ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap,
			_ []packload.BlockedTool, _ macosuser.JailDaemons) int {
			dispatched = true
			if got, err := readForkBuildRuntime(workspace); err != nil || got != "macos-user" {
				t.Errorf("runtime record at native backend dispatch = %q, %v; want macos-user", got, err)
			}
			return 0
		}
	})
	if rc != 0 || !dispatched {
		t.Fatalf("confinement-selected native build child rc=%d dispatched=%v; stderr:\n%s", rc, dispatched, stderr)
	}
	if got, err := readForkBuildRuntime(workspace); err != nil || got != "macos-user" {
		t.Fatalf("resolved guest runtime record = %q, %v; want macos-user", got, err)
	}
}

func TestBuildCapturePersistsAutomaticAppleToPodmanFallback(t *testing.T) {
	workspace := forkBuildRuntimeWorkspace(t, "{}")
	rc, stderr := runActualBuildChild(t, workspace, "make install", false, func(o *run.Options) {
		o.IsMacOS, o.IsLinux = true, false
		o.DryRun = true // The resolver callback must run before the dry-run/backend gate.
		o.Getenv = func(string) string { return "" }
		o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
		o.LookPath = func(name string) (string, bool) {
			return "/usr/bin/" + name, name == "container" || name == "podman"
		}
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) run.ExecResult {
			if len(argv) >= 2 && argv[0] == "/usr/bin/container" && argv[1] == "--version" {
				return run.ExecResult{Ran: true, Stdout: "Apple container CLI version 1.0"}
			}
			if len(argv) >= 3 && argv[0] == "container" && argv[1] == "system" && argv[2] == "status" {
				return run.ExecResult{Ran: true, RC: 1, Stderr: "not running"}
			}
			return run.ExecResult{Ran: false}
		}
		o.PodmanReadiness = yoloruntime.ReadySeams{
			Attempt: func([]string, time.Time, <-chan struct{}) yoloruntime.Attempt {
				return yoloruntime.Attempt{Exited: true, RC: 0, Stdout: `{}`, Pid: 1}
			},
			Sleep: func(time.Duration, <-chan struct{}) bool { return true },
		}
	})
	if rc != 1 {
		t.Fatalf("automatic fallback fixture rc=%d, stderr=%s; want it to stop after runtime resolution", rc, stderr)
	}
	if got, err := readForkBuildRuntime(workspace); err != nil || got != "podman" {
		t.Fatalf("fallback runtime record = %q, %v; want actual Podman selection", got, err)
	}
}

func TestBuildCaptureRuntimeRecordWriteFailureStopsBeforeNativeDispatch(t *testing.T) {
	workspace := forkBuildRuntimeWorkspace(t, `{"confinement":"guest"}`)
	if err := os.Mkdir(forkBuildRuntimeRecordPath(workspace), 0o755); err != nil {
		t.Fatal(err)
	}
	dispatched := false
	rc, stderr := runActualBuildChild(t, workspace, "make install", false, func(o *run.Options) {
		o.IsMacOS, o.IsLinux = true, false
		o.DryRun = true
		o.Getenv = func(string) string { return "" }
		o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
		o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
			_ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap,
			_ []packload.BlockedTool, _ macosuser.JailDaemons) int {
			dispatched = true
			return 0
		}
	})
	if rc != 1 || dispatched || !strings.Contains(stderr, "Cannot record the resolved capture runtime") {
		t.Fatalf("runtime-record write failure rc=%d dispatched=%v stderr=%s; want refusal before backend dispatch", rc, dispatched, stderr)
	}
}

func forkBuildRuntimeWorkspace(t *testing.T, config string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func runActualBuildChild(t *testing.T, workspace, build string, isTree bool, tweak func(*run.Options)) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	withFakeCaptureJail(t, func(o run.Options) int {
		if o.OnRuntimeResolved == nil {
			t.Fatal("build capture did not wire its resolved-runtime persistence callback")
		}
		o.AcceptConfigChanges = true
		if tweak != nil {
			tweak(&o)
		}
		return run.Run(o)
	})
	fork := packload.Fork{Pack: "forkpack", Bin: "tool", Build: build}
	if isTree {
		fork = packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Build: build}
	}
	argv := forkBuildChildArgv(workspace, forkBuild{Fork: fork}, false)
	rc := runForkBuildJail(argv[2:], io.Discard, &stderr)
	return rc, stderr.String()
}
