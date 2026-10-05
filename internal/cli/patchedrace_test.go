package cli

// patchedrace_test.go drives two PATCHED-fork advances at once (patchedadvance.go;
// docs/design/patched-forks.md §6.1, §6.6, PF-D17, PF-D20, PF-D46): a waiter takes the winner's
// failure even when the winner's record write is held up, since the winner records it before it lets
// the build lock go; a move never reaps a build another advance has admitted and not yet settled; a
// build that loses the swap to a newer check's is reaped and the jail runs the winner's; and the good
// build never moves to an entry that has left the store.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// concurrentLaunch runs one fresh launch's advance into out, handing what hand says.
func (fx *patchedAdvanceFixture) concurrentLaunch(t *testing.T, ws string, out *syncBuffer,
	hand func(string, run.HandedFork) error) advanceResult {
	if hand == nil {
		hand = func(string, run.HandedFork) error { return nil }
	}
	return advancePatchedFork(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: "podman",
		workspace: ws, out: out, errw: out, launch: true, hand: hand})
}

// waitFor polls cond for up to 30 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A WAITER TAKES THE WINNER'S FAILURE (§6.6), two real advances: the winner records its failed
// build while it still holds the build lock, so a waiter that takes the lock next reads it and
// builds nothing — even when the winner's record write waits behind another holder of the record
// lock (any concurrent check holds it across its fetch).
func TestAWaiterTakesTheWinnersFailure(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	var calls int32
	aBuilding, proceed := make(chan struct{}), make(chan struct{})
	withFakeCaptureJail(t, func(o run.Options) int {
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity\n")
		if atomic.AddInt32(&calls, 1) == 1 {
			close(aBuilding)
			<-proceed
		}
		return 2 // the build line ran and failed
	})
	var wg sync.WaitGroup
	var aOut, bOut syncBuffer
	var bRes advanceResult
	wg.Add(2)
	go func() { defer wg.Done(); fx.concurrentLaunch(t, "/ws", &aOut, nil) }()
	select {
	case <-aBuilding:
	case <-time.After(30 * time.Second):
		t.Fatalf("A never built:\n%s", aOut.String())
	}
	go func() { defer wg.Done(); bRes = fx.concurrentLaunch(t, "/ws2", &bOut, nil) }()
	waitFor(t, "B to wait on the build lock", func() bool { return strings.Contains(bOut.String(), "waiting for pid") })
	// ANOTHER HOLDER OF THE RECORD LOCK as A's build ends: A's failure cannot be written until it goes.
	locks, _ := filepath.Glob(filepath.Join(paths.PacksDir(), "locks", "check-*.lock"))
	if len(locks) != 1 {
		t.Fatalf("record locks = %v", locks)
	}
	f, err := os.OpenFile(locks[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	time.Sleep(1500 * time.Millisecond) // B's poll of the build lock runs every 50 ms
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
	wg.Wait()
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("the candidate was built %d times; the waiter did not take the winner's failure\n--- A:\n%s\n--- B:\n%s",
			n, aOut.String(), bOut.String())
	}
	if bRes.delivery.Key != r.delivery.Key {
		t.Errorf("the waiter handed %+v, want the good build", bRes.delivery)
	}
	if !strings.Contains(bOut.String(), "another launch's build of v1.3.0 ("+shortSHA(v13)+") failed while this one waited") {
		t.Errorf("the waiter does not say whose failure it took:\n%s", bOut.String())
	}
}

// A MOVE NEVER REAPS A BUILD ANOTHER ADVANCE HAS ADMITTED AND NOT SETTLED (PF-D46): while A's move
// reaps, C — whose candidate came from a newer check — has admitted its build and waits to swap it
// in, holding its build lock; C's swap then wins with an entry that is still there, and its jail is
// handed it.
func TestAMoveNeverReapsABuildAnotherAdvanceHasNotSettled(t *testing.T) {
	fx, _, _, _, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	inner := fx.buildJail(t)
	var calls int32
	goA, goC := make(chan struct{}), make(chan struct{})
	aInJail, cInJail := make(chan struct{}), make(chan struct{})
	withFakeCaptureJail(t, func(o run.Options) int {
		rc := inner(o)
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			close(aInJail)
			<-goA
		case 2:
			close(cInJail)
			<-goC
		}
		return rc
	})
	aInHand, cAdmitted := make(chan struct{}), make(chan struct{})
	var aOut, cOut syncBuffer
	var cRes advanceResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // A: the older check's candidate, v1.3.0
		defer wg.Done()
		var once sync.Once
		fx.concurrentLaunch(t, "/ws", &aOut, func(string, run.HandedFork) error {
			once.Do(func() {
				close(aInHand)
				select {
				case <-cAdmitted:
				case <-time.After(20 * time.Second):
					t.Error("C never admitted its build")
				}
			})
			return nil
		})
	}()
	select {
	case <-aInJail:
	case <-time.After(30 * time.Second):
		t.Fatalf("A never built:\n%s", aOut.String())
	}
	// A NEWER CHECK while A builds: v1.4.0 is published and `yolo pack update` reads it now.
	fx.commit(t, "v1.4.0", map[int]string{14: "fourteen", 20: "twenty", 25: "twentyfive"})
	if rc, out, errw := packVerb(t, "update"); !strings.Contains(out+errw, "v1.4.0") {
		t.Fatalf("pack update rc=%d did not see v1.4.0:\n%s\n%s", rc, out, errw)
	}
	wg.Add(1)
	go func() { defer wg.Done(); cRes = fx.concurrentLaunch(t, "/ws2", &cOut, nil) }()
	select {
	case <-cInJail:
	case <-time.After(30 * time.Second):
		t.Fatalf("C never built:\n%s", cOut.String())
	}
	close(goA) // A admits v1.3.0's build and enters its move, holding the record lock
	select {
	case <-aInHand:
	case <-time.After(30 * time.Second):
		t.Fatalf("A never reached its hand:\n%s", aOut.String())
	}
	close(goC) // C admits v1.4.0's build, then waits on the record lock for its swap
	waitFor(t, "C to admit its build", func() bool { return strings.Contains(cOut.String(), "built forkpack/tool") })
	close(cAdmitted)
	wg.Wait()
	good := fx.record(t).Good
	if good.Tag != "v1.4.0" || !storeEntryExists(good.Entry) || cRes.delivery.Key != good.Entry {
		t.Errorf("the good build is %s at %s (in the store: %v), and C's jail was handed %+v\n--- A:\n%s\n--- C:\n%s",
			good.Tag, good.Entry, storeEntryExists(good.Entry), cRes.delivery, aOut.String(), cOut.String())
	}
}

// A BUILD THAT LOSES THE SWAP IS REAPED AT ONCE (§6.1, PF-D20): A's candidate came from an older
// check than the one C moved the good build to meanwhile, so A's jail is handed C's build, A says it
// lost, and A's own build is gone from the store.
func TestABuildThatLosesTheSwapIsReapedAndHandsTheWinner(t *testing.T) {
	fx, _, _, _, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	inner := fx.buildJail(t)
	var calls int32
	goA, aInJail := make(chan struct{}), make(chan struct{})
	withFakeCaptureJail(t, func(o run.Options) int {
		rc := inner(o)
		if atomic.AddInt32(&calls, 1) == 1 {
			close(aInJail)
			<-goA
		}
		return rc
	})
	var aOut syncBuffer
	var aRes advanceResult
	done := make(chan struct{})
	go func() { defer close(done); aRes = fx.concurrentLaunch(t, "/ws", &aOut, nil) }()
	select {
	case <-aInJail:
	case <-time.After(30 * time.Second):
		t.Fatalf("A never built:\n%s", aOut.String())
	}
	fx.commit(t, "v1.4.0", map[int]string{14: "fourteen", 20: "twenty", 25: "twentyfive"})
	if rc, out, errw := packVerb(t, "update"); !strings.Contains(out+errw, "v1.4.0") {
		t.Fatalf("pack update rc=%d did not see v1.4.0:\n%s\n%s", rc, out, errw)
	}
	var cOut syncBuffer
	cRes := fx.concurrentLaunch(t, "/ws2", &cOut, nil) // C moves the good build to v1.4.0, from the newer check
	if cRes.delivery.Key == "" || fx.record(t).Good.Tag != "v1.4.0" {
		t.Fatalf("C handed %+v\n%s", cRes.delivery, cOut.String())
	}
	close(goA)
	<-done
	if aRes.delivery.Key != cRes.delivery.Key || !aRes.lost {
		t.Errorf("A handed %+v (lost %v), want C's build\n%s", aRes.delivery, aRes.lost, aOut.String())
	}
	if !strings.Contains(aOut.String(), "another launch moved the good build meanwhile, from a newer check") {
		t.Errorf("A does not say it lost the swap:\n%s", aOut.String())
	}
	scan, err := capture.Scan(&capture.Store{Dir: paths.CapturesDir()}, captureRecords)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range scan {
		for _, rec := range e.Records {
			if rec.Revision == v13 {
				t.Errorf("the build that lost the swap is still in the store: %s", e.Key)
			}
		}
	}
}

// THE GOOD BUILD NEVER MOVES TO AN ENTRY THAT LEFT THE STORE (§6.7): an admitted build gone before
// its move (another reaper, a wiped store) is not moved to, the jail is handed the good build as the
// record names it, and the line says so with the next step.
func TestTheGoodBuildNeverMovesToAnEntryThatLeftTheStore(t *testing.T) {
	fx, v11, _, r, _, _ := firstAdvance(t)
	store := &capture.Store{Dir: paths.CapturesDir()}
	staged, err := store.Stage("gone")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(capture.TreeDir(staged), ".local", "bin", "tool"), "gone")
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReapEntry(entry.Key); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	a, early := newAdvance(fx.fork(t), advanceOptions{platform: patchedTestPlatform, out: &out, errw: &out, launch: true})
	if early != nil {
		t.Fatal(early)
	}
	a.seq = a.rec.Seq + 1 // a newer check's: the swap would prefer it
	b := forkBuild{Fork: a.f, Commit: v11, Platform: patchedTestPlatform, Series: a.series,
		Entry: packsrc.ListEntry{Commit: v11, Tag: "v1.1.0", Version: "1.1.0"}}
	got := a.moved(b, entry, baseNone, false)
	if g := fx.record(t).Good; g.Entry != r.delivery.Key || got.delivery.Key != r.delivery.Key || got.built {
		t.Errorf("the move to a gone entry left the good build at %s and handed %+v (built %v)", g.Entry, got.delivery, got.built)
	}
	if !strings.Contains(out.String(), "left the capture store before this launch could hand it") ||
		!strings.Contains(out.String(), "this jail runs v1.1.0") {
		t.Errorf("the line does not say what happened:\n%s", out.String())
	}
}
