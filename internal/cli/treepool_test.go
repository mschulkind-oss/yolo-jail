package cli

// treepool_test.go pins THE PARALLEL ADVANCE (treepool.go; docs/design/pi-extension-store-builds.md
// §5.2, §5.4, XB-D10): a launch's extensions build at once, each under its own locks; at most one
// build jail at a time on a backend that cannot start two side by side; the lines of each key print
// in declaration order whichever ends first; a held key's build says at once that it started; and
// one Ctrl-C ends every key's wait, each handed the good build it has.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const (
	poolKeyA = "treepool/ext-a"
	poolKeyB = "treepool/ext-b"
)

// poolFixture is the tree fixture's upstream contributed twice, as two unmodified extensions of one
// pack, with a fake build jail that leaves each one's tree and a build child the test controls.
type poolFixture struct {
	*treeFixture
	mu      sync.Mutex
	running int
	peak    int
	child   func(ctx context.Context, b forkBuild) int // the test's stand-in for the child's wait
}

func newPoolFixture(t *testing.T) *poolFixture {
	t.Helper()
	pf := &poolFixture{treeFixture: newTreeFixture(t, "")}
	dir := filepath.Join(pf.packs, "treepool")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"treepool","contributes":[`+
		`{"kind":"files","into":".tool/ext/ext-a","source":"git+file://`+pf.repo+`?ref=main"},`+
		`{"kind":"files","into":".tool/ext/ext-b","source":"git+file://`+pf.repo+`?ref=main"}]}`)
	writeFile(t, filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+dir+`","name":"treepool"}]}`)
	withFakeCaptureJail(t, func(o run.Options) int {
		out := filepath.Join(o.Workspace, captureOutLeaf)
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image node npm\n")
		reserved := packdecl.TreeReservedDir(o.SealedTree)
		body := o.SealedTree + " " + pf.now.String()
		writeFile(t, filepath.Join(capture.TreeDir(out), filepath.FromSlash(reserved), "name"), body)
		m := &capture.Manifest{Schema: capture.ManifestSchema, Home: "/home/agent", Platform: patchedTestPlatform,
			Surfaces: []string{".local"}, Excluded: capture.DefaultExcludes(), RefScan: capture.RefScanFull, Relocatable: true,
			Entries: []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"},
				{Path: packdecl.TreeReservedRoot, Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved, Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved + "/name", Kind: capture.KindFile, Mode: "0644", Size: int64(len(body))},
			}}
		slices.SortFunc(m.Entries, func(a, b capture.ManifestEntry) int { return strings.Compare(a.Path, b.Path) })
		if err := capture.WriteManifest(out, m); err != nil {
			t.Error(err)
			return 1
		}
		return 0
	})
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, staging string, b forkBuild, out, errw io.Writer,
		color bool) (int, bool) {
		pf.mu.Lock()
		pf.running++
		pf.peak = max(pf.peak, pf.running)
		pf.mu.Unlock()
		defer func() {
			pf.mu.Lock()
			pf.running--
			pf.mu.Unlock()
		}()
		if pf.child != nil {
			if rc := pf.child(ctx, b); rc != 0 {
				return rc, false
			}
		}
		return forkBuildRunJail(staging, b, out, errw, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	return pf
}

func (pf *poolFixture) launch(t *testing.T, runtime string, act *run.ActInterrupt) (map[string]run.TreeDelivery, string) {
	t.Helper()
	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(trees) != 2 {
		t.Fatalf("the selection carries %d trees", len(trees))
	}
	var out syncBuffer
	got := deliverTreesForLaunch(run.TreeBuildRequest{Trees: trees, Platform: patchedTestPlatform, Runtime: runtime,
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched"), Interrupt: act}, &out, &out, false)
	return got, out.String()
}

// bothRunning blocks a build until both are in their builds at once, or fails after a bound: the
// proof that the two keys' builds overlap.
func (pf *poolFixture) bothRunning(t *testing.T) func(context.Context, forkBuild) int {
	var arrived sync.WaitGroup
	arrived.Add(2)
	return func(ctx context.Context, _ forkBuild) int {
		arrived.Done()
		done := make(chan struct{})
		go func() { arrived.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("the second build never started while the first ran: the builds ran one after another")
		}
		return 0
	}
}

// THE TWO BUILDS OVERLAP, and the lines print in declaration order: every line of the first key
// before any of the second's, though the second's build ran at the same time. Red if the tree arm
// stops running its keys in the pool.
func TestALaunchsExtensionsBuildAtOnceAndPrintInOrder(t *testing.T) {
	pf := newPoolFixture(t)
	pf.child = pf.bothRunning(t)
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if got[poolKeyA].Dir == "" || got[poolKeyB].Dir == "" {
		t.Fatalf("delivered %+v\n%s", got, out)
	}
	if pf.peak != 2 {
		t.Errorf("at most %d builds ran at once, want both", pf.peak)
	}
	lastA, firstB := strings.LastIndex(out, "extension "+poolKeyA), strings.Index(out, "extension "+poolKeyB+": no build")
	if lastA < 0 || firstB < 0 || lastA > firstB {
		t.Errorf("the second key's lines are not after every line of the first:\n%s", out)
	}
	if !strings.Contains(out, "extension "+poolKeyB+": its build has started — its lines follow once every extension "+
		"listed before it has ended, and its output is in ") {
		t.Errorf("the held key's build did not say it started:\n%s", out)
	}
	if strings.Contains(out, "extension "+poolKeyA+": its build has started") {
		t.Errorf("the first key, whose lines are live, said its start too:\n%s", out)
	}
}

// ON A BACKEND THAT CANNOT START TWO BUILD JAILS SIDE BY SIDE (Apple Container), one at a time.
func TestOnAppleContainerOneBuildRunsAtATime(t *testing.T) {
	pf := newPoolFixture(t)
	got, out := pf.launch(t, "container", &run.ActInterrupt{})
	if got[poolKeyA].Dir == "" || got[poolKeyB].Dir == "" {
		t.Fatalf("delivered %+v\n%s", got, out)
	}
	if pf.peak != 1 {
		t.Errorf("%d builds ran at once on Apple Container, want one", pf.peak)
	}
	if buildSlots("container") != 1 || buildSlots("podman") < 1 {
		t.Error("the build bound does not read the backend")
	}
}

// ONE CTRL-C ENDS EVERY KEY'S WAIT (XB-D10, PF-D57): with both keys building a newer upstream, the
// signal ends both builds — not only the innermost of two scopes — and each key is handed its good
// build. Red if the pool stops running its keys under one interrupt scope.
func TestOneCtrlCEndsEveryExtensionsBuildInThePool(t *testing.T) {
	pf := newPoolFixture(t)
	first, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if first[poolKeyA].Dir == "" || first[poolKeyB].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	var arrived sync.WaitGroup
	arrived.Add(2)
	var once sync.Once
	ended := make(chan string, 2)
	pf.child = func(ctx context.Context, b forkBuild) int {
		arrived.Done()
		arrived.Wait()
		once.Do(func() { _ = syscall.Kill(os.Getpid(), syscall.SIGINT) })
		select {
		case <-ctx.Done():
			ended <- b.Fork.Key()
		case <-time.After(20 * time.Second):
			t.Errorf("the Ctrl-C did not reach %s's build", b.Fork.Key())
		}
		return 130
	}
	act := &run.ActInterrupt{}
	got, out := pf.launch(t, "podman", act)
	close(ended)
	var keys []string
	for k := range ended {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{poolKeyA, poolKeyB}) {
		t.Errorf("the Ctrl-C ended the builds of %v, want both", keys)
	}
	if got[poolKeyA].Entry != first[poolKeyA].Entry || got[poolKeyB].Entry != first[poolKeyB].Entry {
		t.Errorf("after the Ctrl-C the jail is handed %+v, want both good builds\n%s", got, out)
	}
	if !act.Interrupted() || strings.Count(out, ": the advance was interrupted — this jail starts on the good build") != 2 {
		t.Errorf("the act or the lines do not record the interrupt:\n%s", out)
	}
}

// THE ORDERED OUTPUT, alone: a later key's writes are held until every earlier key ends, the head's
// pass straight through, across both streams in the order they came.
func TestOrderedOutputHoldsALaterKeyUntilTheEarlierEnd(t *testing.T) {
	var out, errw bytes.Buffer
	o := newOrderedOutput(&out, &errw, 3)
	a, ae := o.writers(0)
	b, be := o.writers(1)
	c, _ := o.writers(2)
	_, _ = io.WriteString(c, "c1\n")
	_, _ = io.WriteString(b, "b1\n")
	_, _ = io.WriteString(be, "b2\n")
	_, _ = io.WriteString(a, "a1\n")
	o.startLine(2, "c started")
	if out.String() != "a1\n" || errw.String() != "c started\n" {
		t.Fatalf("before any end: out %q err %q", out.String(), errw.String())
	}
	o.end(2) // a later key ending first releases nothing
	if out.String() != "a1\n" {
		t.Errorf("a later key's end flushed: %q", out.String())
	}
	_, _ = io.WriteString(ae, "a2\n")
	o.end(0)
	if out.String() != "a1\nb1\n" || errw.String() != "c started\na2\nb2\n" {
		t.Errorf("after the head ended: out %q err %q", out.String(), errw.String())
	}
	_, _ = io.WriteString(b, "b3\n") // b is the head now: straight through
	o.end(1)                         // and c, already ended, flushes behind it
	if out.String() != "a1\nb1\nb3\nc1\n" {
		t.Errorf("at the end: out %q", out.String())
	}
}

// THE BACKGROUND MODE'S SEAM (XB-D28, XB-D19): a key that updates for the next launch is handed the
// good build it has with no check and no build, and left to the background advance. The timing
// reader is a seam (treeUpdateTiming) until the refresh-timing option is built; this pins the
// launch's half. Red if the tree arm stops reading the reader, or stops handing the background
// advance its keys.
func TestAKeyThatUpdatesForTheNextLaunchIsHandedWhatItHas(t *testing.T) {
	pf := newPoolFixture(t)
	first, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if first[poolKeyA].Dir == "" || first[poolKeyB].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	var builds sync.Map
	pf.child = func(_ context.Context, b forkBuild) int { builds.Store(b.Fork.Key(), true); return 0 }
	prevTiming, prevBackground := treeUpdateTiming, backgroundTreeAdvance
	t.Cleanup(func() { treeUpdateTiming, backgroundTreeAdvance = prevTiming, prevBackground })
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	var queued []string
	backgroundTreeAdvance = func(trees []packload.Fork, _ run.TreeBuildRequest, _ io.Writer, _ bool) {
		for _, f := range trees {
			queued = append(queued, f.Key())
		}
	}
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	n := 0
	builds.Range(func(any, any) bool { n++; return true })
	if n != 0 || strings.Contains(out, "checking extension") {
		t.Errorf("a next-launch key was checked or built in front (%d builds):\n%s", n, out)
	}
	if got[poolKeyA].Entry != first[poolKeyA].Entry || got[poolKeyB].Entry != first[poolKeyB].Entry {
		t.Errorf("the next-launch keys were not handed their good builds: %+v", got)
	}
	slices.Sort(queued)
	if !slices.Equal(queued, []string{poolKeyA, poolKeyB}) {
		t.Errorf("the background advance was handed %v, want both keys", queued)
	}
}
