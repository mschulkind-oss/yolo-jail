package cli

// forkbuildchildmac_test.go pins the fork-build CHILD's half of a build on a Mac (FP-D24, PF-D97): a
// launch's build — a plain fork's from its slot, a patched fork's from its advance — runs as
// `yolo internal fork-build-jail`, which must be told the build's id and its runtime for its sealed
// macos-user arm to run the fork-build act, as forkBuildRunJail's seal does in-process.

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// childArgvOf runs every build child through the real runForkBuildChild, recording the build it was
// handed and the argv its command was made with, and stands a child in that exits at once: the
// argv is what the test then runs, as `yolo internal fork-build-jail` would.
func childArgvOf(t *testing.T) (builds *[]forkBuild, argvs *[][]string) {
	t.Helper()
	var bs []forkBuild
	var as [][]string
	prevChild, prevCommand := forkBuildChild, forkBuildChildCommand
	forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		bs = append(bs, b)
		return runForkBuildChild(ctx, bound, staging, b, s, color)
	}
	forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
		as = append(as, argv)
		return exec.Command("false"), nil
	}
	t.Cleanup(func() { forkBuildChild, forkBuildChildCommand = prevChild, prevCommand })
	return &bs, &as
}

// runChildArgvOnMac runs argv's build jail as the child does, and its sealed macos-user arm as the
// run pipeline does on that backend, returning what the pipeline was handed and the fork-build
// act's options.
func runChildArgvOnMac(t *testing.T, argv []string) (run.Options, []macosuser.ForkBuildOptions) {
	t.Helper()
	if len(argv) < 2 || argv[0] != "internal" || argv[1] != forkBuildJailVerb {
		t.Fatalf("argv = %q", argv)
	}
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
	got := stubMacForkBuildAct(t, 0)
	runForkBuildJail(argv[2:], io.Discard, io.Discard)
	if rc := runSealedMacArm(t, seen, seen.Workspace); rc != 0 {
		t.Fatalf("the child's sealed macos-user arm returned %d", rc)
	}
	return seen, *got
}

// A macos-user LAUNCH'S PLAIN FORK BUILD, RUN AS THE CHILD, REACHES THE FORK-BUILD ACT (FP-D24): the
// child is told the build's id and the runtime, so its seal carries the fork's line and the darwin
// build's id, and its jail boots on macos-user. Drop either from forkBuildChildArgv, or the seal's
// build from runForkBuildJail, and the arm refuses.
func TestAMacosUserLaunchPlainForkBuildChildReachesTheForkBuildAct(t *testing.T) {
	f := forkBuildHome(t)
	builds, argvs := childArgvOf(t)
	stream := &launchStream{}
	buildForksForLaunch(run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: forkTestCommit}},
		Platform: "darwin/" + goruntime.GOARCH, Runtime: "macos-user", Workspace: t.TempDir(), Stderr: stream},
		stream, stream, false)
	if len(*argvs) != 1 || len(*builds) != 1 {
		t.Fatalf("the launch ran %d build children (%d builds), want one\n%s", len(*argvs), len(*builds),
			stream.terminal())
	}
	b := (*builds)[0]
	if b.Platform != "darwin/"+goruntime.GOARCH {
		t.Errorf("the build is filed under %q, want darwin", b.Platform)
	}
	argv := (*argvs)[0]
	if !slices.Contains(argv, "--runtime=macos-user") || !slices.Contains(argv, "--build-id="+b.id()) {
		t.Errorf("the child is not told the runtime and the build's id: %q", argv)
	}
	seen, got := runChildArgvOnMac(t, argv)
	if pipelineBackend(seen) != "macos-user" {
		t.Error("the child's build jail was not booted on macos-user")
	}
	if len(got) != 1 || got[0].Build != f.Build || got[0].BuildID != b.id() ||
		got[0].Source != filepath.Join(seen.Workspace, forkSourceLeaf) {
		t.Errorf("the act was handed %+v, want build %q id %q in %s/src", got, f.Build, b.id(), seen.Workspace)
	}
}

// THE MAC'S PATCHED ADVANCE AT A LAUNCH, ITS BUILD RUN AS THE CHILD (PF-D97): the child is told the
// patched build's darwin id and macos-user, and its arm hands the act the fork's line and the
// workspace's src/, where the series was replayed.
func TestAMacosUserPatchedAdvanceBuildChildReachesTheForkBuildAct(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	builds, argvs := childArgvOf(t)
	var out, errw syncBuffer
	advancePatchedFork(fx.fork(t), advanceOptions{platform: "darwin/" + goruntime.GOARCH, runtime: "macos-user",
		workspace: "/ws", out: &out, errw: &errw, launch: true, ctx: context.Background(),
		hand: func(string, run.HandedFork) error { return nil }})
	if len(*argvs) != 1 || len(*builds) != 1 {
		t.Fatalf("the advance ran %d build children, want one\n%s%s", len(*argvs), out.String(), errw.String())
	}
	b := (*builds)[0]
	if b.Series == nil || b.Platform != "darwin/"+goruntime.GOARCH {
		t.Fatalf("the child built %+v, want the patched darwin build", b)
	}
	argv := (*argvs)[0]
	if !slices.Contains(argv, "--runtime=macos-user") || !slices.Contains(argv, "--build-id="+b.id()) {
		t.Errorf("the child is not told the runtime and the build's id: %q", argv)
	}
	seen, got := runChildArgvOnMac(t, argv)
	if len(got) != 1 || got[0].Build != fx.fork(t).Build || got[0].BuildID != b.id() ||
		got[0].Source != filepath.Join(seen.Workspace, forkSourceLeaf) {
		t.Errorf("the act was handed %+v, want the fork's line and id %q in %s/src", got, b.id(), seen.Workspace)
	}
}

// A CONTAINER LAUNCH'S CHILD is told its runtime too, and its build jail boots on it, as the
// in-process build jail does (captureAct); a child told none resolves it as before.
func TestAContainerForkBuildChildNamesItsRuntime(t *testing.T) {
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	for _, rt := range []string{"podman", ""} {
		argv := forkBuildChildArgv(t.TempDir(), b, false, rt)
		if named := slices.Contains(argv, "--runtime="+rt); named != (rt != "") {
			t.Errorf("runtime %q: argv %q", rt, argv)
		}
		var seen run.Options
		withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
		runForkBuildJail(argv[2:], io.Discard, io.Discard)
		if rt != "" && (pipelineBackend(seen) != rt) {
			t.Errorf("runtime %q: the child's jail was not booted on it", rt)
		}
		if rt == "" && pipelineBackend(seen) != "" {
			t.Errorf("a child told no runtime named one: %q", pipelineBackend(seen))
		}
	}
}

// A PATCHED EXTENSION'S BUILD, RUN AS THE CHILD ON A MAC (PPX-D5): the child is told the tree, the
// build's id and the runtime, so its seal carries the tree's line — "" included — and the darwin
// build's id, and its arm hands the act the tree's reserved directory. Drop --build-id for a tree
// from forkBuildChildArgv, or the tree's line and id from runForkBuildJail's seal, and the arm
// refuses or the act is handed none.
func TestAMacosUserTreeBuildChildReachesTheForkBuildActWithItsTree(t *testing.T) {
	forkBuildHome(t)
	for _, build := range []string{"npm ci", ""} {
		tree := packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".pi/extensions/tool-ext",
			Source: forkTestSource, Build: build, Patches: "patches"}
		b := forkBuild{Fork: tree, Commit: forkTestCommit, Platform: "darwin/" + goruntime.GOARCH}
		argv := forkBuildChildArgv(t.TempDir(), b, false, "macos-user")
		for _, want := range []string{"--tree=tool-ext", "--build-id=" + b.id(), "--runtime=macos-user"} {
			if !slices.Contains(argv, want) {
				t.Errorf("build %q: the child's argv %q lacks %s", build, argv, want)
			}
		}
		seen, got := runChildArgvOnMac(t, argv)
		if seen.SealedTree != "tool-ext" {
			t.Errorf("build %q: the child's jail was told it builds tree %q", build, seen.SealedTree)
		}
		if len(got) != 1 || got[0].TreeDir != packdecl.TreeReservedDir("tool-ext") || got[0].BuildID != b.id() ||
			got[0].Build != build || got[0].Source != filepath.Join(seen.Workspace, forkSourceLeaf) {
			t.Errorf("build %q: the act was handed %+v, want tree dir %q id %q", build, got,
				packdecl.TreeReservedDir("tool-ext"), b.id())
		}
	}
}
