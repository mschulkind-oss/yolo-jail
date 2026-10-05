package cli

// patchedactinterrupt_test.go pins AN ACT'S INTERRUPT (run.ActInterrupt; docs/design/patched-forks.md
// PF-D57) at every act that runs several advances: one Ctrl-C, landing in the first patched build an
// act waits for, ends the act's whole wait — a jail launch's later patched forks, its plain forks'
// builds and its patched extensions; `yolo host -- <bin>`'s program after its extensions; and `yolo
// host apply --assert`'s floor stage after its extensions — each later item handed the good build it
// has, with no check and no build, and one with nothing to hand none.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// countedCtrlC stands a build child in that takes the user's one Ctrl-C on the first build and
// counts every build begun, so a test can see that nothing waits after it.
func countedCtrlC(t *testing.T) *int {
	t.Helper()
	calls := 0
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _, _ io.Writer, _ bool) (int, bool) {
		calls++
		if calls == 1 {
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
				t.Error("the Ctrl-C did not reach the advance")
			}
			return 130, false
		}
		return 0, false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	return &calls
}

// interruptedAct is an act an earlier advance's Ctrl-C has reached.
func interruptedAct(t *testing.T) *run.ActInterrupt {
	t.Helper()
	act := &run.ActInterrupt{}
	act.Scope(func(ctx context.Context) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			t.Error("the Ctrl-C did not reach the scope")
		}
	})
	if !act.Interrupted() {
		t.Fatal("the act does not read as interrupted after its scope's Ctrl-C")
	}
	return act
}

// addSecondPatchedFork adds forkpack2, a patched fork of basepack2's tool2 over the fixture's own
// upstream and series, to the selection, and makes the fake build jail leave tool2 for its builds.
func addSecondPatchedFork(t *testing.T, fx *patchedAdvanceFixture) {
	t.Helper()
	second := filepath.Join(fx.packs, "forkpack2")
	ents, err := os.ReadDir(filepath.Join(fx.forkDir, "patches"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		writeFile(t, filepath.Join(second, "patches", e.Name()), mustRead(t, filepath.Join(fx.forkDir, "patches", e.Name())))
	}
	writeFile(t, filepath.Join(second, "pack.json"), `{"name":"forkpack2","contributes":[{"kind":"program","bin":"tool2",`+
		`"via":"source","fork_of":"basepack2","source":"git+file://`+fx.repo+`?ref=main","patches":"patches",`+
		`"build":"sh build.sh","produces":[".local/bin/tool2"]}]}`)
	writeFile(t, filepath.Join(fx.packs, "basepack2", "pack.json"),
		`{"name":"basepack2","contributes":[{"kind":"program","bin":"tool2","via":"npm","package":"tool2"}]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(fx.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+fx.forkDir+`","name":"forkpack"},`+
		`{"source":"file://`+filepath.Join(fx.packs, "basepack2")+`","name":"basepack2"},`+
		`{"source":"file://`+second+`","name":"forkpack2"}]}`)
	base := fx.buildJail(t)
	withFakeCaptureJail(t, func(o run.Options) int {
		rc := base(o)
		if rc != 0 || !slices.Contains(o.OnlyPacks, "forkpack2") {
			return rc
		}
		out := filepath.Join(o.Workspace, captureOutLeaf)
		bin := filepath.Join(capture.TreeDir(out), ".local", "bin")
		if err := os.Rename(filepath.Join(bin, "tool"), filepath.Join(bin, "tool2")); err != nil {
			t.Fatal(err)
		}
		m, err := capture.ReadManifest(out)
		if err != nil {
			t.Fatal(err)
		}
		m.Entries[2].Path = ".local/bin/tool2"
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	})
}

// forkLaunchRequest is a jail launch's fork-build request for every fork in the selection.
func forkLaunchRequest(act *run.ActInterrupt) run.ForkBuildRequest {
	var pins []packload.ForkPin
	for _, f := range packload.Forks(selectConfiguredHostPacks().packs) {
		pins = append(pins, packload.ForkPin{Fork: f, Reason: packload.PatchedForkPinReason})
	}
	return run.ForkBuildRequest{Pins: pins, Platform: patchedTestPlatform, Runtime: "podman", Workspace: "/ws",
		Interrupt: act}
}

// ONE CTRL-C ENDS EVERY PATCHED FORK'S WAIT IN A JAIL LAUNCH (PF-D57): with two patched forks each
// serving a good build and pending a newer upstream, the Ctrl-C in the first one's build starts one
// build jail, not two; the second is handed its good build with no check, and says so. Red if the
// advance stops reading the act, or the launch's fork builds stop handing it the request's.
func TestOneCtrlCEndsEveryPatchedForksWaitInALaunch(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	addSecondPatchedFork(t, fx)
	var out syncBuffer
	first := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out, &out, false)
	if first["tool"].Key == "" || first["tool2"].Key == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out.String())
	}
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	calls := countedCtrlC(t)
	var out2 syncBuffer
	got := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out2, &out2, false)
	printed := out2.String()
	if *calls != 1 {
		t.Errorf("after one Ctrl-C the launch began %d build jails, want the one it ended\n%s", *calls, printed)
	}
	if got["tool"].Key != first["tool"].Key || got["tool2"].Key != first["tool2"].Key {
		t.Errorf("after the Ctrl-C the jail is handed %+v, want both good builds %+v\n%s", got, first, printed)
	}
	if strings.Contains(printed, "checking fork forkpack2/tool2's upstream") {
		t.Errorf("after the Ctrl-C the launch checked the second fork's upstream:\n%s", printed)
	}
	if !strings.Contains(printed, "fork forkpack2/tool2: not checked — a Ctrl-C ended this launch's wait for its "+
		"patched builds; this jail starts on the good build v1.1.0 ("+shortSHA(v11)+") + 2 patches, and the next "+
		"fresh launch checks it") {
		t.Errorf("the second fork does not say why it was not checked and what this jail starts on:\n%s", printed)
	}
}

// AFTER THE CTRL-C NO PLAIN FORK'S BUILD BEGINS (PF-D57): each would be a new wait the user had just
// declined, so the fork is told why it has no build, and the next launch builds it. Red if the fork
// builds stop asking the request's act.
func TestAfterACtrlCALaunchBeginsNoPlainForksBuild(t *testing.T) {
	f := forkBuildHome(t)
	builds := 0
	withFakeCaptureJail(t, func(run.Options) int { builds++; return 0 })
	var errw bytes.Buffer
	got := buildForksForLaunch(run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: forkTestCommit}},
		Platform: "linux/arm64", Interrupt: interruptedAct(t)}, io.Discard, &errw, false)
	if builds != 0 {
		t.Errorf("after the Ctrl-C the launch began %d plain fork builds", builds)
	}
	if d := got["probetool"]; d.Key != "" || !strings.Contains(d.Reason, "was not started: a Ctrl-C ended the launch's "+
		"wait for its fork builds — the next launch builds it") {
		t.Errorf("the plain fork is handed %+v, want why it has no build", d)
	}
	if !strings.Contains(errw.String(), "this launch continues without probetool") {
		t.Errorf("the launch does not say it continues without the plain fork:\n%s", errw.String())
	}
}

// forkAndTreeFixture is the patched-fork fixture with a patched extension of the same upstream and
// series beside it in the selection, and one fake build jail answering for both.
func forkAndTreeFixture(t *testing.T, fx *patchedAdvanceFixture, listed string) *treeFixture {
	t.Helper()
	tfx := &treeFixture{patchedFixture: fx.patchedFixture, now: fx.now}
	tfx.treeDir = filepath.Join(fx.packs, "treepack")
	ents, err := os.ReadDir(filepath.Join(fx.forkDir, "patches"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		writeFile(t, filepath.Join(tfx.treeDir, "patches", e.Name()), mustRead(t, filepath.Join(fx.forkDir, "patches", e.Name())))
	}
	writeFile(t, filepath.Join(tfx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=main","patches":"patches",`+
		`"build":"true","produces":["f.txt"]}`+listed+`]}`)
	forkJail, treeJail := fx.buildJail(t), tfx.buildJail(t)
	withFakeCaptureJail(t, func(o run.Options) int {
		if slices.Contains(o.OnlyPacks, "treepack") {
			return treeJail(o)
		}
		return forkJail(o)
	})
	return tfx
}

// ONE CTRL-C IN THE FORK SLOT ENDS THE TREE ARM'S WAIT TOO (PF-D57): the launch's patched fork takes
// the Ctrl-C in its build, and the patched extension after it is not checked or built — its good build
// is copied for the jail, as a launch with no newer upstream copies it. Red if the tree arm stops
// handing its advance the request's act.
func TestOneCtrlCInAPatchedForksBuildEndsTheTreeArmsWait(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	forkAndTreeFixture(t, fx, "")
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(fx.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+fx.forkDir+`","name":"forkpack"},`+
		`{"source":"file://`+filepath.Join(fx.packs, "treepack")+`","name":"treepack"}]}`)
	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(trees) != 1 {
		t.Fatalf("the selection carries trees %+v", trees)
	}
	launch := func(act *run.ActInterrupt, out io.Writer) (map[string]entrypoint.ForkDelivery, run.TreeDelivery) {
		forks := buildForksForLaunch(forkLaunchRequest(act), out, out, false)
		got := deliverTreesForLaunch(run.TreeBuildRequest{Trees: trees, Platform: patchedTestPlatform, Runtime: "podman",
			Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched"), Interrupt: act}, out, out, false)
		return forks, got[treeKeyCLI]
	}
	var out syncBuffer
	forks, tree := launch(&run.ActInterrupt{}, &out)
	if forks["tool"].Key == "" || tree.Dir == "" {
		t.Fatalf("the first launch delivered %+v and %+v\n%s", forks, tree, out.String())
	}
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	calls := countedCtrlC(t)
	var out2 syncBuffer
	forks2, tree2 := launch(&run.ActInterrupt{}, &out2)
	printed := out2.String()
	if *calls != 1 {
		t.Errorf("after one Ctrl-C the launch began %d build jails, want the one it ended\n%s", *calls, printed)
	}
	if forks2["tool"].Key != forks["tool"].Key || tree2.Dir == "" || tree2.Entry != tree.Entry {
		t.Errorf("after the Ctrl-C the jail is handed %+v and %+v, want both good builds\n%s", forks2, tree2, printed)
	}
	if !strings.Contains(printed, "extension "+treeKeyCLI+": not checked — a Ctrl-C ended this launch's wait for its "+
		"patched builds; this jail starts on the good build") {
		t.Errorf("the extension does not say why it was not checked:\n%s", printed)
	}
}

// hostForkAndTree is the patched-fork floor fixture with a patched extension its base pack owns: the
// base declares tool's settings surface, and the extension's list entry is on it, so `yolo host --
// tool` advances the extension, then installs the fork's program; `host_apply_on_launch` is on.
func hostForkAndTree(t *testing.T) *patchedAdvanceFixture {
	t.Helper()
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	forkAndTreeFixture(t, fx, `,{"kind":"config-list","surface":"tool/settings","path":"/packages",`+
		`"add":["~/.tool/ext/tool-ext"]}`)
	writeFile(t, filepath.Join(fx.packs, "basepack", "pack.json"), `{"name":"basepack","contributes":[`+
		`{"kind":"program","bin":"tool","via":"npm","package":"tool"},`+
		`{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(fx.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+fx.forkDir+`","name":"forkpack"},`+
		`{"source":"file://`+filepath.Join(fx.packs, "treepack")+`","name":"treepack"}],"host_apply_on_launch":true}`)
	return fx
}

// ONE CTRL-C AT `yolo host -- <bin>` ENDS THE PROGRAM'S WAIT AFTER ITS EXTENSION'S (PF-D57): the
// Ctrl-C lands in the extension's build, and the floor's patched fork is not checked or built — tool
// starts on its good build. Red if the launch stops sharing one act between the extensions' advances
// and the floor's install, or the floor's advance stops reading the act its Ensure carries.
func TestOneCtrlCAtAHostLaunchEndsTheProgramsWaitAfterItsExtensions(t *testing.T) {
	fx := hostForkAndTree(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "tool")
	rc, target, out := fx.hostLaunch(t)
	if rc != 0 || target != launcher || len(fx.builds) != 1 {
		t.Fatalf("the first launch: rc=%d target=%s fork builds=%d, want the floor's copy of one build\n%s", rc, target,
			len(fx.builds), out)
	}
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	calls := countedCtrlC(t)
	rc, target, out = fx.hostLaunch(t)
	if rc != 0 || target != launcher {
		t.Fatalf("after the Ctrl-C: rc=%d target=%s, want tool started on the floor's copy\n%s", rc, target, out)
	}
	if *calls != 1 {
		t.Errorf("after one Ctrl-C `yolo host` began %d build jails, want the one it ended\n%s", *calls, out)
	}
	if !strings.Contains(out, "fork forkpack/tool: not checked — a Ctrl-C ended `yolo host`'s wait for its patched "+
		"builds; `yolo host` starts tool on the good build v1.1.0") {
		t.Errorf("the floor's fork does not say why it was not checked:\n%s", out)
	}
}

// ONE CTRL-C AT `yolo host apply --assert` ENDS THE FLOOR STAGE'S WAIT AFTER THE EXTENSIONS' (PF-D57),
// at both spellings of the verb: the Ctrl-C lands in the extension's build, and the floor's patched
// fork is not checked or built. Red if either spelling stops handing its extensions' advances and its
// floor stage one act, or the floor stage stops carrying it into the floor's Ensure.
func TestOneCtrlCAtHostApplyEndsTheFloorStagesWaitAfterTheExtensions(t *testing.T) {
	for _, spelling := range []struct {
		name  string
		apply func(w io.Writer) int
	}{
		{"yolo host apply --assert", func(w io.Writer) int {
			return hostApply([]string{"--assert"}, w, w, false, strings.NewReader(""))
		}},
		{"yolo apply --at host --assert", func(w io.Writer) int {
			return applyMain([]string{"--at", "host", "--assert"}, w, w, false, strings.NewReader(""))
		}},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			fx := hostForkAndTree(t)
			var errw syncBuffer
			if rc := spelling.apply(&errw); rc != 0 || len(fx.builds) != 1 {
				t.Fatalf("the first apply: rc=%d, fork builds=%d\n%s", rc, len(fx.builds), errw.String())
			}
			fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
			fx.later(2 * time.Hour)
			calls := countedCtrlC(t)
			var errw2 syncBuffer
			spelling.apply(&errw2)
			if *calls != 1 {
				t.Errorf("after one Ctrl-C the apply began %d build jails, want the one it ended\n%s", *calls, errw2.String())
			}
			if !strings.Contains(errw2.String(), "fork forkpack/tool: not checked — a Ctrl-C ended `yolo host`'s wait") {
				t.Errorf("the floor stage's fork does not say why it was not checked:\n%s", errw2.String())
			}
		})
	}
}
