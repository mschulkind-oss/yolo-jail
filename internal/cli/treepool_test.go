package cli

// treepool_test.go pins THE PARALLEL ADVANCE (treepool.go; docs/design/pi-extension-store-builds.md
// §5.2, §5.4, XB-D10): a launch's extensions build at once, each under its own locks; at most one
// build jail at a time on a backend that cannot start two side by side; the lines of each key print
// in declaration order whichever ends first; a held key's build says at once that it started; and
// one Ctrl-C ends every key's wait, each handed the good build it has.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
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
	standInCPUs(t, 8)
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
	forkBuildChild = func(ctx context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
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
		return forkBuildRunJail(staging, b, s, color), false
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

// standInCPUs stands a machine of n CPUs in for the pools' build bound (poolCPUs) for the test's life.
// A test that needs builds at once must, since the bound is the runner's: one build on a machine of
// fewer than four CPUs, which GitHub's 3-CPU macOS runners are.
func standInCPUs(t *testing.T, n int) {
	t.Helper()
	prev := poolCPUs
	poolCPUs = func() int { return n }
	t.Cleanup(func() { poolCPUs = prev })
}

// arrivedWithin waits for every party of wg, or for d, and reports whether they all arrived: a
// build waiting for another that the pool never starts fails with a message rather than hanging.
func arrivedWithin(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// bothRunning blocks a build until both are in their builds at once, or fails after a bound: the
// proof that the two keys' builds overlap.
func (pf *poolFixture) bothRunning(t *testing.T) func(context.Context, forkBuild) int {
	var arrived sync.WaitGroup
	arrived.Add(2)
	return func(ctx context.Context, _ forkBuild) int {
		arrived.Done()
		if !arrivedWithin(&arrived, 20*time.Second) {
			t.Error("the second build never started while the first ran: the builds ran one after another")
		}
		return 0
	}
}

// THE TWO BUILDS OVERLAP, and the lines print in declaration order: each build's start line at
// once, and every result line of the first key before the second's, though the second's build ran
// at the same time (the launch's build pool, buildpool.go, XB-D56). Red if the tree arm stops running
// its keys in the pool.
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
	resultA, resultB := strings.Index(out, "built extension "+poolKeyA), strings.Index(out, "built extension "+poolKeyB)
	startB := strings.Index(out, "build extension "+poolKeyB+": ")
	if resultA < 0 || resultB < 0 || resultA > resultB || startB < 0 || startB > resultA {
		t.Errorf("the start lines are not said at once, or the second key's result is not after the first's:\n%s", out)
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
		if !arrivedWithin(&arrived, 20*time.Second) {
			t.Errorf("%s's build waited for the other key's, which never started: the pool ran one build at a time",
				b.Fork.Key())
		}
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

// A HELD KEY'S BUILD LOG, the file its start line names, receives the key's lines as its build
// runs; it is opened only once the key's build starts and appended to, so an act that builds
// nothing of the key never touches it, and another act's build of it is never truncated. A host
// act's pool (a jail launch's builds each write the launch's own .yolo/build-<slug>.log, XB-D56). Red
// if the pool stops teeing a lane into its log, or opens it truncating, or before the build.
func TestAHeldKeysBuildLogHoldsItsLinesAndNoOtherLaunchTruncatesIt(t *testing.T) {
	pf := newPoolFixture(t)
	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	var logB string
	for _, f := range trees {
		if f.Key() == poolKeyB {
			logB = treeBuildLog(f)
		}
	}
	if logB == "" {
		t.Fatal("the selection carries no " + poolKeyB)
	}
	// What another launch's build of the key has written so far, while it still runs.
	const other = "ANOTHER LAUNCH'S BUILD OF THIS KEY, STILL RUNNING\n"
	writeFile(t, logB, other)
	pf.child = pf.bothRunning(t)
	var host syncBuffer
	advanceHostTrees(&host, false, "", &run.ActInterrupt{})
	out := host.String()
	if !strings.Contains(out, "its output is in "+logB) {
		t.Fatalf("the held key's start line does not name %s:\n%s", logB, out)
	}
	data, err := os.ReadFile(logB)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), other) {
		t.Errorf("the build truncated another launch's log of the key:\n%s", data)
	}
	if !strings.Contains(string(data), "built extension "+poolKeyB) || strings.Contains(string(data), poolKeyA+":") {
		t.Errorf("the held key's log does not hold its own lines alone:\n%s", data)
	}
	// An act inside the hour checks and builds nothing, and leaves the log as it is.
	pf.child = nil
	writeFile(t, logB, other)
	var again syncBuffer
	advanceHostTrees(&again, false, "", &run.ActInterrupt{})
	if strings.Contains(again.String(), "built extension "+poolKeyB) {
		t.Fatalf("the second act built again:\n%s", again.String())
	}
	if data, _ := os.ReadFile(logB); string(data) != other {
		t.Errorf("a launch that built nothing of the key rewrote its log:\n%s", data)
	}
}

// THE HOST'S ADVANCE IS THE SAME POOL (XB-D39): `yolo host apply --assert` and `yolo host -- <bin>`
// build their extensions side by side too. Red if advanceHostTrees stops running its keys through
// runTreesInParallel.
func TestTheHostsExtensionsBuildAtOnce(t *testing.T) {
	pf := newPoolFixture(t)
	pf.child = pf.bothRunning(t)
	var errw syncBuffer
	advanceHostTrees(&errw, false, "", &run.ActInterrupt{})
	if pf.peak != 2 {
		t.Errorf("at most %d host builds ran at once, want both:\n%s", pf.peak, errw.String())
	}
	for _, k := range []string{poolKeyA, poolKeyB} {
		if !strings.Contains(errw.String(), "built extension "+k) {
			t.Errorf("the host did not build %s:\n%s", k, errw.String())
		}
	}
}

// AT MOST treeCheckSlots CHECKS AT ONCE (XB-D10): ten npm extensions, a registry that holds every
// request until eight are in flight and then a little longer, and never more than eight at once.
// Red if the advance stops running its check under the pool's check slot.
func TestAPoolRunsAtMostEightChecksAtOnce(t *testing.T) {
	pf := newPoolFixture(t)
	const keys = 10
	var mu sync.Mutex
	inflight, peak := 0, 0
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inflight++
		peak = max(peak, inflight)
		if inflight == treeCheckSlots {
			once.Do(func() { close(release) })
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(10 * time.Second):
		}
		time.Sleep(200 * time.Millisecond) // long enough for any unbounded check to arrive meanwhile
		mu.Lock()
		inflight--
		mu.Unlock()
		name := strings.TrimPrefix(r.URL.Path, "/")
		_, _ = w.Write([]byte(`{"name":"` + name + `","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{}}}`))
	}))
	t.Cleanup(srv.Close)
	prev := packsrc.NpmRegistry
	packsrc.NpmRegistry = srv.URL
	t.Cleanup(func() { packsrc.NpmRegistry = prev })
	var contribs []string
	for i := range keys {
		contribs = append(contribs, fmt.Sprintf(`{"kind":"files","into":".tool/ext/ext-%d","source":"npm:ext-%d"}`, i, i))
	}
	writeFile(t, filepath.Join(pf.packs, "treepool", "pack.json"), `{"name":"treepool","contributes":[`+
		strings.Join(contribs, ",")+`]}`)
	pf.child = func(context.Context, forkBuild) int { return 1 } // no build: the checks are the measure
	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(trees) != keys {
		t.Fatalf("the selection carries %d trees, want %d", len(trees), keys)
	}
	var out syncBuffer
	deliverTreesForLaunch(run.TreeBuildRequest{Trees: trees, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched"), Interrupt: &run.ActInterrupt{}},
		&out, &out, false)
	mu.Lock()
	defer mu.Unlock()
	if peak != treeCheckSlots {
		t.Errorf("%d checks ran at once, want exactly %d (the bound, reached):\n%s", peak, treeCheckSlots, out.String())
	}
}

// A POOLED BUILD THAT FAILS AND LEAVES NOTHING TO DELIVER, beside one that builds: the pool prints
// its one result line and its last lines, says its cause nowhere, and hands the launch the reason,
// marked unsaid, which the launch's refusal of a missing build says once (run's missingbuilds.go,
// PPX-D40). The other key is delivered. Red if the pooled act says the failure's cause itself, or
// stops leaving it to the launch.
func TestAPooledBuildThatFailsLeavesItsCauseToTheLaunch(t *testing.T) {
	pf := newPoolFixture(t)
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, d time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		if b.Fork.Key() == poolKeyB {
			s.jailReady()
			writeFile(t, filepath.Join(staging, forkToolchainLeaf), "image-identity\n")
			fmt.Fprintln(s.errw, "npm ERR! the build line of ext-b failed")
			return 2, false
		}
		return prev(ctx, d, staging, b, s, color)
	}
	t.Cleanup(func() { forkBuildChild = prev })
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if got[poolKeyA].Dir == "" {
		t.Fatalf("the key that builds was not delivered: %+v\n%s", got[poolKeyA], out)
	}
	d := got[poolKeyB]
	if d.Dir != "" || !d.Unsaid || !strings.Contains(d.Reason, "failed on the host") {
		t.Errorf("the failed key is handed %+v, want no build and its reason left to the launch", d)
	}
	if n := strings.Count(out, "Building extension "+poolKeyB+": failed ("); n != 1 {
		t.Errorf("the failed build has %d result lines, want one:\n%s", n, out)
	}
	if !strings.Contains(out, "npm ERR! the build line of ext-b failed") {
		t.Errorf("the failed build's last lines are not under its result:\n%s", out)
	}
	if strings.Contains(out, ": the build of ") || strings.Contains(out, "failed on the host") {
		t.Errorf("the pooled act said the failure's cause, which the launch says once:\n%s", out)
	}
}
