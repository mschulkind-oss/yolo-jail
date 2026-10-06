package cli

// buildpool_test.go pins a jail launch's build pool (buildpool.go; docs/design/pi-extension-store-builds.md
// XB-D10, XB-D56): the slot's keys build at once, up to the pool's bound and never past it, one at a
// time on Apple Container; each key's lines print in declaration order whichever ends first; one
// Ctrl-C ends every build and every wait for a slot, the launch going on without what nothing serves;
// and `yolo run` wires the slot as one act.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// treesFixture is the patched-fork fixture's upstream and series contributed by one pack as n
// patched extensions, ext-1 to ext-n, with a fake build jail that leaves each its tree.
type treesFixture struct {
	*patchedFixture
	trees []packload.Fork
}

func newTreesFixture(t *testing.T, n int) *treesFixture {
	t.Helper()
	fx := &treesFixture{patchedFixture: newPatchedFixture(t, "")}
	dir := filepath.Join(fx.packs, "treespack")
	series, _ := filepath.Glob(filepath.Join(fx.forkDir, "patches", "*.patch"))
	for _, p := range series {
		writeFile(t, filepath.Join(dir, "patches", filepath.Base(p)), mustRead(t, p))
	}
	var contribs []string
	for i := 1; i <= n; i++ {
		contribs = append(contribs, fmt.Sprintf(`{"kind":"files","into":".tool/ext/ext-%d","source":"git+file://%s?ref=main",`+
			`"patches":"patches","build":"true","produces":["f.txt"]}`, i, fx.repo))
	}
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"treespack","contributes":[`+strings.Join(contribs, ",")+`]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+dir+`","name":"treespack"}]}`)
	asLinuxTreeHost(t)
	var mu sync.Mutex
	withFakeCaptureJail(t, func(o run.Options) int {
		mu.Lock()
		defer mu.Unlock()
		data, err := os.ReadFile(filepath.Join(o.Workspace, forkSourceLeaf, "f.txt"))
		if err != nil {
			t.Errorf("the patched source is not in the build's src/: %v", err)
			return 1
		}
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity\n")
		out := filepath.Join(o.Workspace, captureOutLeaf)
		reserved := packdecl.TreeReservedDir(o.SealedTree)
		writeFile(t, filepath.Join(capture.TreeDir(out), filepath.FromSlash(reserved), "f.txt"), string(data))
		m := &capture.Manifest{Schema: capture.ManifestSchema, Home: "/home/agent", Platform: patchedTestPlatform,
			Surfaces: []string{".local"}, Excluded: capture.DefaultExcludes(), RefScan: capture.RefScanFull, Relocatable: true,
			Entries: []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"},
				{Path: packdecl.TreeReservedRoot, Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved, Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved + "/f.txt", Kind: capture.KindFile, Mode: "0644", Size: int64(len(data))},
			}}
		slices.SortFunc(m.Entries, func(a, b capture.ManifestEntry) int { return strings.Compare(a.Path, b.Path) })
		if err := capture.WriteManifest(out, m); err != nil {
			t.Error(err)
		}
		return 0
	})
	fx.trees = packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(fx.trees) != n {
		t.Fatalf("the selection carries %d trees, want %d", len(fx.trees), n)
	}
	return fx
}

// slot runs one launch's slot for every tree, on a stream, under act, with at most maxBuilds builds
// at once (0: the runtime's bound).
func (fx *treesFixture) slot(t *testing.T, rt string, maxBuilds int, act *run.ActInterrupt) (map[string]run.TreeDelivery,
	*launchStream) {
	t.Helper()
	stream := &launchStream{}
	req := run.TreeBuildRequest{Trees: fx.trees, Platform: patchedTestPlatform, Runtime: rt, Workspace: t.TempDir(),
		Build: true, CopyRoot: filepath.Join(t.TempDir(), "trees.patched"), Stderr: stream, Progress: progress.Config{},
		Interrupt: act}
	_, got := runBuildSlot(run.BuildSlotRequest{Trees: &req, MaxBuilds: maxBuilds}, stream, false)
	return got, stream
}

// concurrentChildren stands a build child in that counts the builds running at once, holds each
// until want of them run together (or hold passes), and then lets them end, the earliest
// declared last: what the pool must still print first.
func concurrentChildren(t *testing.T, want int, hold time.Duration) (most *int) {
	t.Helper()
	var mu sync.Mutex
	running, peak := 0, 0
	all := make(chan struct{})
	var closeOnce sync.Once
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams, color bool) (int, bool) {
		mu.Lock()
		running++
		peak = max(peak, running)
		if running == want {
			closeOnce.Do(func() { close(all) })
		}
		mu.Unlock()
		select {
		case <-all:
		case <-time.After(hold):
		}
		// The earliest declared ends last.
		var n int
		_, _ = fmt.Sscanf(b.Fork.Bin, "ext-%d", &n)
		time.Sleep(time.Duration(10-n) * 20 * time.Millisecond)
		rc := forkBuildRunJail(staging, b, s, color)
		mu.Lock()
		running--
		mu.Unlock()
		return rc, false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	return &peak
}

// THE SLOT'S KEYS BUILD AT ONCE, up to the pool's bound and never past it, and their lines print in
// declaration order however they end: five extensions' first builds, at most four at a time, the
// earliest declared ending last among them. Red with the pool's keys run one after another, or its
// flush in the order keys end.
func TestTheSlotBuildsItsKeysAtOnceAndPrintsThemInOrder(t *testing.T) {
	fx := newTreesFixture(t, 5)
	peak := concurrentChildren(t, 4, 5*time.Second)
	got, stream := fx.slot(t, "podman", 4, &run.ActInterrupt{})
	term := stream.terminal()
	if *peak != 4 {
		t.Errorf("the pool ran at most %d builds at once, want its bound of 4\n%s", *peak, term)
	}
	last := -1
	for _, f := range fx.trees {
		if got[f.Key()].Dir == "" {
			t.Errorf("%s was not delivered: %+v", f.Key(), got[f.Key()])
		}
		at := strings.Index(term, "built extension "+f.Key()+": ")
		if at < 0 || at < last {
			t.Errorf("%s's result line is missing or out of declaration order:\n%s", f.Key(), term)
		}
		last = at
	}
	// The four that ran at once each printed its start line at once, before any key's result.
	if first := strings.Index(term, "built extension "); strings.Count(term[:max(first, 0)], "build extension ") < 4 {
		t.Errorf("a running build's start line waited for another key's result:\n%s", term)
	}
}

// APPLE CONTAINER BUILDS ONE KEY AT A TIME (XB-D10): a capture jail cannot start beside a running
// jail there, so the pool's bound for it is one. Red with the pool's own bound not read from
// run.SlotBuildJails.
func TestAppleContainerBuildsOneKeyAtATime(t *testing.T) {
	fx := newTreesFixture(t, 3)
	peak := concurrentChildren(t, 2, 200*time.Millisecond)
	got, stream := fx.slot(t, "container", 0, &run.ActInterrupt{})
	if *peak != 1 {
		t.Errorf("on Apple Container the pool ran %d builds at once, want 1\n%s", *peak, stream.terminal())
	}
	for _, f := range fx.trees {
		if got[f.Key()].Dir == "" {
			t.Errorf("%s was not delivered: %+v", f.Key(), got[f.Key()])
		}
	}
}

// ONE CTRL-C ENDS EVERY BUILD AND EVERY WAIT OF THE POOL (PF-D57, PF-D80): three extensions' first
// builds, two at a time; the Ctrl-C lands while two build and the third waits for a slot. Both
// builds end, the third begins none, nothing is delivered, each says so and names the step that
// builds it, and the slot returns, so the launch goes on. Red if the pool stops running its keys
// under the act's one scope, or a key waiting for a slot ignores it.
func TestOneCtrlCEndsEveryBuildAndWaitOfThePool(t *testing.T) {
	fx := newTreesFixture(t, 3)
	calls := ctrlCOnceAllBuild(t, 2)
	act := &run.ActInterrupt{}
	done := make(chan struct{})
	var got map[string]run.TreeDelivery
	var stream *launchStream
	go func() {
		defer close(done)
		got, stream = fx.slot(t, "podman", 2, act)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the pool did not return after the Ctrl-C")
	}
	term := stream.terminal()
	if *calls != 2 || !act.Interrupted() {
		t.Errorf("%d build jails began (want the two running) and the act reads interrupted %v\n%s", *calls,
			act.Interrupted(), term)
	}
	for _, f := range fx.trees {
		if d := got[f.Key()]; d.Dir != "" || d.Reason == "" {
			t.Errorf("%s was handed %+v after the Ctrl-C, want no build and a reason", f.Key(), d)
		}
		if want := "extension " + f.Key() + ": not built — a Ctrl-C ended this launch's wait for its patched builds, " +
			"and this jail has no extension " + f.Key() + "; the next fresh launch builds it, or `yolo capture " +
			f.Key() + "` now"; !strings.Contains(term, want) {
			t.Errorf("the launch lacks %q:\n%s", want, term)
		}
	}
}

// `yolo run` WIRES THE SLOT AS ONE ACT (TestALaunchWiresTheForkBuildTrigger's shape), by invoking
// the closure it wired. Red with `opts.BuildSlot = …` deleted.
func TestALaunchWiresTheBuildSlot(t *testing.T) {
	fx := newTreesFixture(t, 2)
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	if rc := runRun([]string{"run", "--", "true"}); rc != 0 {
		t.Fatalf("runRun = %d with the pipeline stubbed", rc)
	}
	if seen.BuildSlot == nil {
		t.Fatal("`yolo run` did not wire Options.BuildSlot: the slot's keys run one after another")
	}
	stream := &launchStream{}
	var trees map[string]run.TreeDelivery
	quiet(t, func() {
		_, trees = seen.BuildSlot(run.BuildSlotRequest{Trees: &run.TreeBuildRequest{Trees: fx.trees,
			Platform: patchedTestPlatform, Runtime: "podman", Build: true, CopyRoot: filepath.Join(t.TempDir(), "p"),
			Stderr: stream, Interrupt: &run.ActInterrupt{}}})
	})
	for _, f := range fx.trees {
		if trees[f.Key()].Dir == "" {
			t.Errorf("the wired slot delivered %+v for %s\n%s", trees[f.Key()], f.Key(), stream.terminal())
		}
	}
}

// A KEY WAITING FOR ANOTHER LAUNCH'S BUILD says so on the pool's line, which can stand for that wait
// for up to forkBuildWaitBound; the act's other phases go to the build's log alone. Red with
// buildRun.phase's note deleted.
func TestThePoolsLineSaysWhatABuildWaitsFor(t *testing.T) {
	pool := newBuildPool(io.Discard, progress.Config{}, false, "", "podman", 0, 0, &run.ActInterrupt{})
	var replaying, waiting string
	pool.add("extension a/b", func(it *poolItem) {
		release, _ := it.build()
		defer release()
		r := pool.report.begin(buildStart{fork: packload.Fork{Pack: "a", Bin: "b", Into: ".x/b"}, what: "v1"}, it)
		detail := func() string {
			it.pool.mu.Lock()
			defer it.pool.mu.Unlock()
			return it.pool.renderLocked()
		}
		r.phase("replaying the series")
		replaying = detail()
		r.phase("waiting for pid 7's build of it, at most 20m0s")
		waiting = detail()
		r.fail("done")
	})
	pool.run()
	if replaying != "building extension a/b; 0 of 1 done" {
		t.Errorf("a replay's line reads %q, want the key alone", replaying)
	}
	if want := "building extension a/b (waiting for pid 7's build of it, at most 20m0s); 0 of 1 done"; waiting != want {
		t.Errorf("a lock wait's line reads %q, want %q", waiting, want)
	}
}

// A FIFO SEMAPHORE gives a freed slot to the waiter declared first, so with one slot the keys run in
// turn, and a waiter whose context ends leaves the queue.
func TestTheFIFOSemaphoreServesTheEarliestDeclaredFirst(t *testing.T) {
	s := newFIFOSem(1)
	ctx := context.Background()
	if !s.acquire(ctx, 9) {
		t.Fatal("a free slot was not taken")
	}
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for _, i := range []int{3, 1, 2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.acquire(ctx, i) {
				mu.Lock()
				order = append(order, i)
				mu.Unlock()
				s.release()
			}
		}()
		time.Sleep(20 * time.Millisecond) // queued in this order: 3, then 1, then 2
	}
	cancelled, cancel := context.WithCancel(ctx)
	gone := make(chan bool)
	go func() { gone <- s.acquire(cancelled, 0) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if <-gone {
		t.Error("a waiter whose context ended was handed the slot")
	}
	s.release()
	wg.Wait()
	if !slices.Equal(order, []int{1, 2, 3}) {
		t.Errorf("the slot went to %v, want the earliest declared first: [1 2 3]", order)
	}
}
