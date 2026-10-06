package cli

// forkbuildmac_test.go pins the call sites of a fork's build on a Mac (docs/design/
// forked-programs-as-packs.md FP-D24) that the act-level tests in hostfloorfork_test.go and
// capturenextstep_test.go stand in for: which runtime a `yolo capture <forked bin>` names to its
// build jail, the refusals a Mac's floor gives before it builds, and the workspace a macos-user
// build's links are read against.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A CONTAINER BACKEND'S `yolo capture <forked bin>` NAMES NO RUNTIME TO ITS BUILD JAIL: the run
// pipeline resolves it, as for any launch — skipping a `container` that is not Apple's or does not
// answer, honoring the guest notch — and a refusal there names what the user set, never a
// YOLO_RUNTIME they did not. Only macos-user is named, because its build is of darwin.
func TestACaptureOfAForkOnAContainerBackendNamesNoRuntime(t *testing.T) {
	for name, machine := range map[string]func(t *testing.T){
		"nothing on PATH": func(t *testing.T) { t.Setenv("PATH", t.TempDir()) },
		"podman on PATH":  func(t *testing.T) { stubBins(t, "podman") },
	} {
		t.Run(name, func(t *testing.T) {
			forkBuildHome(t)
			t.Setenv("YOLO_RUNTIME", "")
			machine(t)
			var seen run.Options
			withFakeCaptureJail(t, fakeBuildJail(t, &seen, probetoolBuilt))
			var out, errw bytes.Buffer
			if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
				t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
			}
			if seen.Getenv != nil {
				if rt := seen.Getenv("YOLO_RUNTIME"); rt != "" {
					t.Errorf("the build jail was handed YOLO_RUNTIME=%q, which nobody set", rt)
				}
			}
		})
	}
}

// A MAC'S FLOOR REFUSES A FORK'S BUILD IT CANNOT RUN, AT ITS CALL SITE (FP-D24): with the macos-user
// act unable to run here, `yolo host -- forkcli` says why as no floor entry, naming the step, runs the
// PATH copy, and builds nothing. Drop macBuildBlocked from the Mac's floor wiring and the act runs.
func TestAMacFloorThatCannotRunTheBuildActRunsThePathCopy(t *testing.T) {
	for name, tc := range map[string]struct {
		mac  macSetup
		want string
	}{
		"before macos-setup": {macSetup{terminal: true}, "the sandbox account " + macosuser.SandboxUser +
			", which a fork's build on a Mac runs as, does not exist — run the one-time setup, " +
			"`yolo macos-setup`, and the next `yolo host` launch builds it"},
		"as root": {macSetup{root: true, account: true, terminal: true},
			"yolo is running as root, and a fork's build on a Mac runs as your own user"},
		"with no terminal for sudo": {macSetup{account: true},
			"a fork's build on a Mac asks for sudo, and this launch has no terminal to ask on — run " +
				"`YOLO_RUNTIME=macos-user yolo capture forkcli` once in a terminal"},
	} {
		t.Run(name, func(t *testing.T) {
			forkFloorHome(t)
			var out, errw bytes.Buffer
			if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
				t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
			}
			withMacForkFloor(t)
			withMac(t, tc.mac)
			origAct := macForkBuildAct
			macForkBuildAct = func(macosuser.Deps, macosuser.ForkBuildOptions, string, string, bool) int {
				t.Error("the fork-build act ran on a Mac that cannot run it")
				return 1
			}
			t.Cleanup(func() { macForkBuildAct = origAct })
			withFakeCaptureJail(t, func(run.Options) int { t.Error("a build jail launched"); return 1 })
			stub := filepath.Join(stubBins(t, "forkcli"), "forkcli")
			got := captureHostExec(t)
			errw.Reset()
			if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != stub {
				t.Fatalf("rc=%d target=%s, want the PATH copy %s\n%s", rc, got.target, stub, errw.String())
			}
			if !strings.Contains(errw.String(), "yolo has no copy of forkcli on this Mac") ||
				!strings.Contains(errw.String(), tc.want) {
				t.Errorf("the no-copy line does not say %q:\n%s", tc.want, errw.String())
			}
			if keys, _ := (&capture.Store{Dir: paths.CapturesDir()}).EntryKeys(); len(keys) != 0 {
				t.Errorf("entries were admitted: %v", keys)
			}
		})
	}
}

// A MACOS-USER BUILD'S LINKS ARE READ AGAINST ITS OWN WORKSPACE, at buildFork's call site: a build
// that leaves its program as a link into its checkout (<capture root>/fork-<id>/src, deleted when the
// build ends) is refused and stores nothing, as a container build's link into /workspace is. Read
// the links against /workspace on this backend and the dangling program is admitted.
func TestAMacosUserBuildLeavingALinkIntoItsCheckoutStoresNothing(t *testing.T) {
	forkBuildHome(t)
	t.Setenv("YOLO_RUNTIME", "macos-user")
	origAct := macForkBuildAct
	macForkBuildAct = func(_ macosuser.Deps, o macosuser.ForkBuildOptions, dest, toolchain string, _ bool) int {
		root := macosuser.ForkBuildStagingRoot("", o.BuildID)
		target := filepath.Join(macosuser.ForkBuildSourceDir(root), "bin", "probetool")
		entries := []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/probetool", Kind: capture.KindSymlink, Target: target},
		}
		tree := capture.TreeDir(dest)
		if err := os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(tree, ".local", "bin", "probetool")); err != nil {
			t.Fatal(err)
		}
		m := &capture.Manifest{
			Schema: capture.ManifestSchema, Home: macosuser.CaptureStagingHome(root), Platform: capture.Platform(),
			Surfaces: []string{".npm-global", ".local", "go"}, Excluded: capture.DefaultExcludes(), Entries: entries,
		}
		if err := capture.WriteManifest(dest, m); err != nil {
			t.Fatal(err)
		}
		writeFile(t, toolchain, "yolo test")
		return 0
	}
	t.Cleanup(func() { macForkBuildAct = origAct })
	withFakeCaptureJail(t, func(o run.Options) int {
		return o.MacosUserRun(jsonx.NewOrderedMap(), o.Workspace, nil, o.Args, "/flake", "", macosuser.HomeOverlay{},
			macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
	})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatalf("a build leaving a link into its checkout succeeded\n%s\n%s", out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "a link into its own workspace") {
		t.Errorf("the refusal does not name the link into the build's workspace:\n%s", errw.String())
	}
	if keys, _ := (&capture.Store{Dir: paths.CapturesDir()}).EntryKeys(); len(keys) != 0 {
		t.Errorf("entries were admitted: %v", keys)
	}
}
