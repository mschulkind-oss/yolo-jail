package run

// forkdelivery_test.go pins the fork build TRIGGER at its call site, from Run's own entry point
// (docs/design/forked-programs-as-packs.md OQ-FP4, FP-D8, FP-D3), as autocapture_test.go pins
// auto-capture's: a launch carrying a pinned fork asks the injected build act for it, hands the
// jail the answer beside the store, and does neither in a capture or build jail; a macos-user
// launch says it delivers no program.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// pinFork writes forkLaunchHome's fork pinned at commit into the fork lock.
func pinFork(t *testing.T, commit string) {
	t.Helper()
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/tool", Source: forkPinSource, Ref: "main", Commit: commit})
	if err := l.Save(packsrc.ForkLockPath(paths.UserConfigPath())); err != nil {
		t.Fatal(err)
	}
}

// fakePodmanLaunch runs one podman launch through Run with a fake podman on PATH that records the
// `run` argv, and returns that argv (nil when the runtime was never run) and what was printed.
func fakePodmanLaunch(t *testing.T, mutate func(*Options)) ([]string, string) {
	t.Helper()
	ws := t.TempDir()
	bin, rec := t.TempDir(), t.TempDir()
	argvFile := filepath.Join(rec, "argv")
	script := "#!/bin/sh\nif [ \"$1\" = run ]; then printf '%s\\n' \"$@\" > '" + argvFile + "'; fi\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	store := t.TempDir()
	o.CapturesDir = func() string { return store }
	mutate(o)
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })
	Run(*o)
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		return nil, stdout.String() + stderr.String()
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n"), stdout.String() + stderr.String()
}

// forkBuildsInArgv decodes the argv's ForkBuildsEnv pair, nil when there is none.
func forkBuildsInArgv(t *testing.T, argv []string) map[string]entrypoint.ForkDelivery {
	t.Helper()
	for _, e := range argvValues(argv, "-e") {
		if v, ok := strings.CutPrefix(e, entrypoint.ForkBuildsEnv+"="); ok {
			var d map[string]entrypoint.ForkDelivery
			if err := json.Unmarshal([]byte(v), &d); err != nil {
				t.Fatalf("%s is not JSON: %v", entrypoint.ForkBuildsEnv, err)
			}
			return d
		}
	}
	return nil
}

// TestALaunchBuildsItsPinnedForkAndHandsTheJailTheKey: the trigger fires with the pinned fork and
// the JAIL's platform, and the argv carries the answer beside the store. Red if Run stops calling
// forkDeliveriesFor, or assembly stops emitting the pair.
func TestALaunchBuildsItsPinnedForkAndHandsTheJailTheKey(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	commit := strings.Repeat("ab", 20)
	pinFork(t, commit)
	var gotPins []packload.ForkPin
	var gotPlatform string
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(req ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			gotPins, gotPlatform = req.Pins, req.Platform
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "k1"}}
		}
	})
	if len(gotPins) != 1 || gotPins[0].Commit != commit || gotPins[0].Fork.Key() != "forkpack/tool" {
		t.Fatalf("the trigger was handed %+v, want the pinned fork\n%s", gotPins, printed)
	}
	if want := "linux/" + goruntime.GOARCH; gotPlatform != want {
		t.Errorf("trigger platform = %q, want the jail's %q", gotPlatform, want)
	}
	if d := forkBuildsInArgv(t, argv); d["tool"].Key != "k1" {
		t.Errorf("the jail is handed %+v, want tool's key k1", d)
	}
}

// A fork the launch could not pin is never handed to the build act: its reason crosses instead.
func TestAnUnpinnableForkReachesTheJailAsItsReason(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	called := false
	argv, _ := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { called = true; return nil }
	})
	if called {
		t.Error("the build act was asked to build a fork with no pin")
	}
	if d := forkBuildsInArgv(t, argv); !strings.Contains(d["tool"].Reason, "it has no pin, and pinning it failed") {
		t.Errorf("the jail is handed %+v, want the pin's reason", d)
	}
}

// NEVER IN A CAPTURE OR BUILD JAIL: the switch that suppresses the store mount suppresses the
// trigger, so a build cannot trigger a build.
func TestACaptureJailTriggersNoForkBuild(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	pinFork(t, strings.Repeat("cd", 20))
	called := false
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.CapturesDir = func() string { return "" }
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { called = true; return nil }
	})
	if called || forkBuildsInArgv(t, argv) != nil {
		t.Errorf("a capture jail triggered a fork build (called %v) or was handed decisions", called)
	}
	// Nor does it repeat the pin line the launch that started it already said.
	if strings.Contains(printed, "Forks this launch") {
		t.Errorf("a capture jail repeated the fork pin line:\n%s", printed)
	}
}

// BELOW APPLE CONTAINER'S READ-ONLY FLOOR no store is mounted, so nothing is built and each fork is
// told why.
func TestNoForkIsBuiltBelowTheAppleContainerFloor(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	o.CapturesDir = func() string { return "/store" }
	called := false
	o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { called = true; return nil }
	o.forkPinned = []packload.ForkPin{{Fork: packload.Fork{Pack: "forkpack", Bin: "tool"}, Commit: "c"}}
	got := o.forkDeliveriesFor("container")
	if called || !strings.Contains(got["tool"].Reason, "mounts no capture store") {
		t.Errorf("below the floor: called %v, answered %+v", called, got)
	}
}

// FP-D3's line: a macos-user launch carrying a fork says it delivers no program, and why.
func TestAMacosUserLaunchSaysItDeliversNoFork(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	pinFork(t, strings.Repeat("ef", 20))
	out := launchToDispatch(t)
	if !strings.Contains(out, "tool is not delivered on macos-user") || !strings.Contains(out, "hand-off H4") {
		t.Errorf("the macos-user launch does not name the undelivered fork:\n%s", out)
	}
}

// AN ATTACH BUILDS NOTHING. The jail it enters baked its fork decisions at boot (the launcher
// reads ForkBuildsEnv once), so a build now could not reach it: it would only hold the attach —
// up to forkBuildWaitBound behind another launch's build — for bytes the next fresh launch builds
// anyway. The trigger belongs to the fresh launch, below the attach decision.
func TestAnAttachTriggersNoForkBuild(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	pinFork(t, strings.Repeat("12", 20))
	called := false
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		cname := yoloruntime.FromWorkspace(o.Workspace)
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { called = true; return nil }
	})
	if !strings.Contains(printed, "Attaching to existing jail") {
		t.Fatalf("the fixture did not attach, so the attach path is unexercised:\n%s", printed)
	}
	if called {
		t.Errorf("an attach built a fork its running jail cannot receive:\n%s", printed)
	}
}
