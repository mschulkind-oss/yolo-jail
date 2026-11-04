package cli

// patchedforkmac_test.go pins the call sites of a PATCHED fork's build on a Mac
// (docs/reference/patched-forks.md PF-D97, docs/design/forked-programs-as-packs.md FP-D25): the seal
// forkBuildRunJail gives it, the sealed macos-user arm that runs it through the fork-build act as a
// plain fork's (FP-D24) — a patched extension's too, with its tree's reserved directory (PPX-D5) —
// and the platform and runtime `yolo capture <patched bin>` names to its advance.

import (
	"bytes"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// stubMacForkBuildAct substitutes the fork-build act, recording each call's options.
func stubMacForkBuildAct(t *testing.T, rc int) *[]macosuser.ForkBuildOptions {
	t.Helper()
	var got []macosuser.ForkBuildOptions
	orig := macForkBuildAct
	macForkBuildAct = func(_ macosuser.Deps, o macosuser.ForkBuildOptions, _, _ string, _ bool) int {
		got = append(got, o)
		return rc
	}
	t.Cleanup(func() { macForkBuildAct = orig })
	return &got
}

// runSealedMacArm runs the macos-user arm the pipeline was handed, as the run pipeline does on that
// backend.
func runSealedMacArm(t *testing.T, seen run.Options, workspace string) int {
	t.Helper()
	if seen.MacosUserRun == nil || !seen.Sealed {
		t.Fatal("the build jail carries no sealed macos-user arm")
	}
	return seen.MacosUserRun(jsonx.NewOrderedMap(), workspace, nil, nil, "/flake", "/packs", macosuser.HomeOverlay{},
		macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
}

// ON macos-user A PATCHED FORK'S SEALED BUILD REACHES THE FORK-BUILD ACT (PF-D97), as a plain fork's
// does (FP-D24): forkBuildRunJail seals it with the fork's build line and the darwin build's id, and
// the arm hands the act the checkout in the workspace's src/, where buildFork has already replayed the
// series. Drop the patched fork from forkBuildRunJail's seal and the arm refuses it.
func TestAPatchedForkBuildOnMacosUserReachesTheForkBuildAct(t *testing.T) {
	f := forkBuildHome(t)
	f.Patches = "patches"
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
	got := stubMacForkBuildAct(t, 0)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: "darwin/" + goruntime.GOARCH,
		Series: &packsrc.Series{Dir: "patches", Digest: "series-digest"}}
	ws := t.TempDir()
	var out, errw bytes.Buffer
	forkBuildRunJail(ws, b, captureStreams{out: &out, errw: &errw}, false, captureAct{runtime: "macos-user"})
	if pipelineBackend(seen) != "macos-user" {
		t.Fatal("the patched fork's build jail was not handed the macos-user runtime")
	}
	if rc := runSealedMacArm(t, seen, ws); rc != 0 || len(*got) != 1 {
		t.Fatalf("the sealed macos-user arm returned %d and ran the fork-build act %d times, want 0 and once\n%s",
			rc, len(*got), errw.String())
	}
	o := (*got)[0]
	if o.Build != f.Build || o.BuildID != b.id() || o.Bin != "probetool" {
		t.Errorf("the act was handed build %q id %q for %q, want %q and the darwin build's id %q", o.Build,
			o.BuildID, o.Bin, f.Build, b.id())
	}
	if want := filepath.Join(ws, forkSourceLeaf); o.Source != want {
		t.Errorf("the act builds %q, want the replayed checkout %q", o.Source, want)
	}
}

// ON macos-user A PATCHED EXTENSION'S SEALED BUILD REACHES THE FORK-BUILD ACT (PPX-D5), with its
// tree's reserved directory (TreeDir) for the act's final copy: forkBuildRunJail seals it with the
// tree's build line — "" included, a tree's line being optional (PPX-D3) — and the darwin build's
// id, and the arm hands the act the checkout in the workspace's src/. Drop the line and id from
// forkBuildRunJail's seal for a tree, or TreeDir from the arm, and this fails.
func TestAPatchedExtensionBuildOnMacosUserReachesTheForkBuildActWithItsTree(t *testing.T) {
	for _, build := range []string{"npm ci", ""} {
		forkBuildHome(t)
		var seen run.Options
		withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
		got := stubMacForkBuildAct(t, 0)
		tree := packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".pi/extensions/tool-ext",
			Source: forkTestSource, Build: build}
		b := forkBuild{Fork: tree, Commit: forkTestCommit, Platform: "darwin/" + goruntime.GOARCH,
			Series: &packsrc.Series{Dir: "patches", Digest: "series-digest"}}
		ws := t.TempDir()
		var out, errw bytes.Buffer
		forkBuildRunJail(ws, b, captureStreams{out: &out, errw: &errw}, false, captureAct{runtime: "macos-user"})
		if seen.SealedTree != "tool-ext" {
			t.Errorf("build %q: the build jail was told it builds tree %q, want tool-ext", build, seen.SealedTree)
		}
		if rc := runSealedMacArm(t, seen, ws); rc != 0 || len(*got) != 1 {
			t.Fatalf("build %q: the sealed macos-user arm returned %d and ran the fork-build act %d times, "+
				"want 0 and once\n%s", build, rc, len(*got), errw.String())
		}
		o := (*got)[0]
		if o.TreeDir != packdecl.TreeReservedDir("tool-ext") || o.Build != b.buildLine() || o.BuildID != b.id() ||
			o.Bin != "tool-ext" {
			t.Errorf("build %q: the act was handed tree dir %q build %q id %q for %q, want %q, %q, %q for tool-ext",
				build, o.TreeDir, o.Build, o.BuildID, o.Bin, packdecl.TreeReservedDir("tool-ext"), b.buildLine(), b.id())
		}
		if want := filepath.Join(ws, forkSourceLeaf); o.Source != want {
			t.Errorf("build %q: the act builds %q, want the replayed checkout %q", build, o.Source, want)
		}
	}
}

// A FORK'S BUILD names no tree to the act: TreeDir stays "", so the act runs the fork's build alone.
func TestAForkBuildOnMacosUserNamesNoTreeDir(t *testing.T) {
	f := forkBuildHome(t)
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
	got := stubMacForkBuildAct(t, 0)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: "darwin/" + goruntime.GOARCH}
	ws := t.TempDir()
	forkBuildRunJail(ws, b, captureStreams{out: &bytes.Buffer{}, errw: &bytes.Buffer{}}, false,
		captureAct{runtime: "macos-user"})
	if rc := runSealedMacArm(t, seen, ws); rc != 0 || len(*got) != 1 || (*got)[0].TreeDir != "" {
		t.Fatalf("rc=%d acts=%d %+v, want one act with no tree dir", rc, len(*got), *got)
	}
}

// A TREE'S SEAL HANDED NO BUILD ID is refused on macos-user rather than run: the act's staging tree
// and its provenance are keyed by the id. A tree's seal with no build line but an id is not (above).
func TestATreeSealWithNoBuildIDOnMacosUserIsRefused(t *testing.T) {
	forkBuildHome(t)
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
	got := stubMacForkBuildAct(t, 0)
	var out, errw bytes.Buffer
	runCaptureJail(t.TempDir(), "tool-ext", []string{"true"}, &captureSeal{only: []string{"treepack"},
		tree: "tool-ext", build: "npm ci"}, captureStreams{out: &out, errw: &errw}, false)
	errw.Reset()
	if rc := runSealedMacArm(t, seen, ""); rc == 0 || len(*got) != 0 {
		t.Fatalf("a tree's seal with no id ran the fork-build act: rc=%d acts=%d", rc, len(*got))
	}
	if !strings.Contains(errw.String(), "YOLO_SETUP=podman-self yolo -- tool-ext") {
		t.Errorf("the refusal names no next step:\n%s", errw.String())
	}
}

// A FORK'S SEAL HANDED NO BUILD LINE is refused on macos-user rather than run as an empty build: the
// act runs only the line and id the seal carries.
func TestAForkSealWithNoBuildLineOnMacosUserIsRefused(t *testing.T) {
	forkBuildHome(t)
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
	got := stubMacForkBuildAct(t, 0)
	var out, errw bytes.Buffer
	runCaptureJail(t.TempDir(), "probetool", []string{"true"}, &captureSeal{only: []string{"forkpack", "basepack"}},
		captureStreams{out: &out, errw: &errw}, false)
	errw.Reset()
	if rc := runSealedMacArm(t, seen, ""); rc == 0 || len(*got) != 0 {
		t.Fatalf("a seal with no build line ran the fork-build act: rc=%d acts=%d", rc, len(*got))
	}
	if !strings.Contains(errw.String(), "YOLO_SETUP=podman-self yolo -- probetool") {
		t.Errorf("the refusal names no next step:\n%s", errw.String())
	}
}

// `yolo capture <patched bin>` UNDER macos-user ADVANCES THE DARWIN BUILD (PF-D97), as a plain fork's
// capture builds it (FP-D24): its advance is handed darwin on this machine's architecture and the
// macos-user runtime. Any other runtime keeps the container jail's platform and names none. A patched
// extension's capture (`yolo capture <pack>/<name>`) does the same (PPX-D5): restore the tree
// exclusion in capturePatchedFork and the tree's macos-user case fails.
func TestACaptureOfAPatchedForkOnMacosUserAdvancesTheDarwinBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		runtime, platform, named string
		tree                     bool
	}{
		"macos-user":      {"macos-user", "darwin/" + goruntime.GOARCH, "macos-user", false},
		"podman":          {"podman", captureJailPlatform(), "", false},
		"tree macos-user": {"macos-user", "darwin/" + goruntime.GOARCH, "macos-user", true},
		"tree podman":     {"podman", captureJailPlatform(), "", true},
	} {
		t.Run(name, func(t *testing.T) {
			f := forkBuildHome(t)
			f.Patches = "patches"
			if tc.tree {
				f = packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".pi/extensions/tool-ext",
					Source: forkTestSource, Build: "npm ci", Patches: "patches"}
				if !f.IsTree() {
					t.Fatal("the fixture is no tree")
				}
			}
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_SETUP", config.SetupForRemovedRuntime(tc.runtime))
			var seen []advanceOptions
			orig := capturePatchedAdvance
			capturePatchedAdvance = func(_ packload.Fork, o advanceOptions) advanceResult {
				seen = append(seen, o)
				return advanceResult{built: true}
			}
			t.Cleanup(func() { capturePatchedAdvance = orig })
			var out, errw bytes.Buffer
			if rc := capturePatchedFork(f, &out, &errw, false); rc != 0 || len(seen) != 1 {
				t.Fatalf("rc=%d advances=%d\n%s", rc, len(seen), errw.String())
			}
			if seen[0].platform != tc.platform || seen[0].runtime != tc.named || !seen[0].force {
				t.Errorf("the advance was handed platform %q runtime %q force %v, want %q %q true",
					seen[0].platform, seen[0].runtime, seen[0].force, tc.platform, tc.named)
			}
		})
	}
}
