package cli

// patchedadvance_test.go drives a PATCHED fork's advance (patchedadvance.go;
// docs/design/patched-forks.md §6, §7, §8) against a real local upstream repository, with the build
// jail substituted: the first advance builds the newest fit and hands it; a launch inside the hour
// runs no git; a new tag moves the good build and reaps what no running jail was handed; a
// conflicting tag is said once and held with the previous build serving; a failed build backs off
// while the good build serves, and a build jail that never ran records nothing; the user's own
// failed edit is not held (PF-D23, patchedbase_test.go); a waiter takes the winner's failure
// (patchedrace_test.go); a Ctrl-C ends the advance and the jail starts on the good build (PF-D25);
// a lost record is recovered from the store; and `yolo capture` builds through the same swap.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// patchedTestPlatform is the platform the fake build jail's manifest reports: a container capture
// jail's on this machine, which `yolo capture` asks for.
var patchedTestPlatform = captureJailPlatform()

// syncBuffer is a bytes.Buffer safe to read while an advance writes it from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// patchedAdvanceFixture is newPatchedFixture with the advance's seams: a clock to step launches
// apart by, the build jail substituted, and the interruptible build run in this process.
type patchedAdvanceFixture struct {
	*patchedFixture
	now    time.Time
	builds []string // f.txt as each build saw it in its src/
	rc     int      // what the fake build jail exits with
	ran    bool     // whether the fake build jail writes the toolchain record (its build line ran)
	said   string   // a line the fake build jail's runtime prints on the jail's stderr before it exits, "" for none
	child  int      // how many builds went through the child-process runner
	scoped []bool   // per child build, whether an interrupt scope's context could cancel it
	// sharedLocks is set where other keys of the selection run beside the build in the slot's pool,
	// whose checks and walks hold their own record and mirror locks meanwhile (PPX-D13): the lock
	// assertion is then the single-key tests'.
	sharedLocks bool
	// mu guards builds, child and scoped, which the pool's keys write at once.
	mu sync.Mutex
	// platform is what the fake build jail's manifest reports: a container capture jail's, unless a
	// host floor test makes it the floor's own (capture.Platform), which a materialize on the host
	// requires.
	platform string
	// relocatable records the fake build as the full reference scan found it free of its home, which
	// the host floor's materialize out of the jail's home requires.
	relocatable bool
}

func newPatchedAdvanceFixture(t *testing.T, follow string) *patchedAdvanceFixture {
	t.Helper()
	fx := &patchedAdvanceFixture{patchedFixture: newPatchedFixture(t, follow), now: time.Unix(1_900_000_000, 0), ran: true,
		platform: patchedTestPlatform}
	prevNow := patchedNow
	patchedNow = func() time.Time { return fx.now }
	t.Cleanup(func() { patchedNow = prevNow })
	withFakeCaptureJail(t, fx.buildJail(t))
	prevChild := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		fx.mu.Lock()
		fx.child++
		fx.scoped = append(fx.scoped, ctx.Done() != nil)
		fx.mu.Unlock()
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prevChild })
	return fx
}

// buildJail is the fake build jail: it records the patched f.txt the build saw, checks the build
// sees no .git and holds no record or mirror lock, and leaves a program whose bytes are that f.txt's.
func (fx *patchedAdvanceFixture) buildJail(t *testing.T) func(run.Options) int {
	return func(o run.Options) int {
		if o.OnRuntimeResolved != nil {
			rt := "podman"
			if o.Getenv != nil {
				if selected := o.Getenv("YOLO_RUNTIME"); selected != "" {
					rt = selected
				}
			}
			if err := o.OnRuntimeResolved(rt); err != nil {
				t.Fatalf("record fixture runtime: %v", err)
			}
		}
		src := filepath.Join(o.Workspace, forkSourceLeaf)
		data, err := os.ReadFile(filepath.Join(src, "f.txt"))
		if err != nil {
			t.Errorf("the patched source is not in the build's src/: %v", err)
			return 1
		}
		if _, err := os.Lstat(filepath.Join(src, ".git")); err == nil {
			t.Error("the build's src/ holds a .git, which the build could read and write")
		}
		if !fx.sharedLocks {
			assertNoPackStoreLockHeld(t)
		}
		fx.mu.Lock()
		fx.builds = append(fx.builds, string(data))
		fx.mu.Unlock()
		if fx.ran {
			writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity\n")
		}
		if fx.said != "" {
			// As a launch relays a runtime that refused: its own error on the jail's stderr, between
			// the keeper's lines on the launch's.
			fmt.Fprintln(o.Stderr, "keeper: started, pid 42")
			fmt.Fprintln(jailStderr(o), fx.said)
			fmt.Fprintln(o.Stderr, "keeper: done")
		}
		if fx.rc != 0 {
			return fx.rc
		}
		sum := sha256.Sum256(data)
		body := "#!/bin/sh\n# " + hex.EncodeToString(sum[:]) + "\n"
		out := filepath.Join(o.Workspace, captureOutLeaf)
		writeFile(t, filepath.Join(capture.TreeDir(out), ".local", "bin", "tool"), body)
		if err := os.Chmod(filepath.Join(capture.TreeDir(out), ".local", "bin", "tool"), 0o755); err != nil {
			t.Fatal(err)
		}
		m := &capture.Manifest{Schema: capture.ManifestSchema, Home: "/home/agent", Platform: fx.platform,
			Surfaces: []string{".local"}, Excluded: capture.DefaultExcludes(), Entries: []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin/tool", Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
			}}
		if fx.relocatable {
			m.RefScan, m.Relocatable = capture.RefScanFull, true
		}
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	}
}

// assertNoPackStoreLockHeld fails when any pack-store lock (a check record's, a mirror's) is held
// while the build runs: the record lock is never held across a build, nor the mirror lock (§6.6).
func assertNoPackStoreLockHeld(t *testing.T) {
	t.Helper()
	locks, _ := filepath.Glob(filepath.Join(paths.PacksDir(), "locks", "*.lock"))
	for _, p := range locks {
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Errorf("the pack store lock %s is held across the build", filepath.Base(p))
		} else {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		f.Close()
	}
}

// fork is the fixture's patched fork as the selection reads it.
func (fx *patchedAdvanceFixture) fork(t *testing.T) packload.Fork {
	t.Helper()
	for _, f := range packload.Forks(selectConfiguredHostPacks().packs) {
		if f.Key() == "forkpack/tool" {
			return f
		}
	}
	t.Fatal("the fixture's selection carries no fork forkpack/tool")
	return packload.Fork{}
}

// launch runs one fresh launch's advance and returns it, what it printed, and what it handed.
func (fx *patchedAdvanceFixture) launch(t *testing.T, runtime string) (advanceResult, string, []run.HandedFork) {
	t.Helper()
	var out, errw syncBuffer
	var handed []run.HandedFork
	r := advancePatchedFork(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: runtime,
		workspace: "/ws", out: &out, errw: &errw, launch: true,
		hand: func(_ string, h run.HandedFork) error { handed = append(handed, h); return nil }})
	return r, out.String() + errw.String(), handed
}

func (fx *patchedAdvanceFixture) record(t *testing.T) *packsrc.CheckRecord {
	t.Helper()
	return patchedRecord(t)
}

// later steps the clock past the check's interval.
func (fx *patchedAdvanceFixture) later(d time.Duration) { fx.now = fx.now.Add(d) }

// firstAdvance is the state every later test starts from: v1.1.0 takes the series, v1.2.0 does
// not, and the first advance built v1.1.0.
func firstAdvance(t *testing.T) (fx *patchedAdvanceFixture, v11, v12 string, r advanceResult, out string, handed []run.HandedFork) {
	t.Helper()
	fx = newPatchedAdvanceFixture(t, "")
	v11 = fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 = fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	r, out, handed = fx.launch(t, "podman")
	return
}

func storeEntryExists(key string) bool {
	_, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(key)
	return err == nil
}

// THE FIRST ADVANCE builds the newest fit — v1.1.0, past the v1.2.0 the series does not take —
// in this process (a first advance has nothing to start on, so a Ctrl-C ends the launch), writes
// the good build after the admit, records the build's fork, series and tree, pins nothing, and
// hands the jail the build under the record lock.
func TestAFirstAdvanceBuildsTheNewestFitAndHandsIt(t *testing.T) {
	fx, v11, v12, r, out, handed := firstAdvance(t)
	if len(fx.builds) != 1 || fx.builds[0] != lines30(map[int]string{10: "ten", 12: "twelve", 14: "fourteen"}) {
		t.Fatalf("builds = %q, want one of v1.1.0 with the series applied\n%s", fx.builds, out)
	}
	if fx.child != 0 {
		t.Error("a first advance's build ran as a child: a Ctrl-C there must end the launch, as a cold install's does")
	}
	rec := fx.record(t)
	g := rec.Good
	if g == nil || g.Commit != v11 || g.Tag != "v1.1.0" || g.Entry != r.delivery.Key || g.Tree == "" || g.Patches != 2 ||
		g.Read == nil {
		t.Fatalf("the good build = %+v, want v1.1.0's admitted build handed as %q", g, r.delivery.Key)
	}
	if len(handed) != 1 || handed[0].Key != g.Entry || handed[0].Fork != "forkpack/tool" || handed[0].Tag != "v1.1.0" ||
		handed[0].Patches != 2 || handed[0].Commit != v11 {
		t.Errorf("handed %+v, want the good build with its label", handed)
	}
	for _, w := range []string{"upstream v1.2.0 (" + shortSHA(v12) + ") does not take the patch series",
		"0001-ten.patch conflicts in f.txt", "building the newest fit, v1.1.0",
		"built fork forkpack/tool: v1.1.0 (" + shortSHA(v11) + ") + 2 patches; this jail runs it"} {
		if !strings.Contains(out, w) {
			t.Errorf("the first advance lacks %q:\n%s", w, out)
		}
	}
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(g.Entry)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root))
	series, _ := fx.fork(t).ReadSeries()
	if len(recs) != 1 || recs[0].Fork != "forkpack/tool" || recs[0].Series != series.Digest || recs[0].Tree != g.Tree ||
		recs[0].Tag != "v1.1.0" || recs[0].Version != "1.1.0" || recs[0].Source != "git+file://"+fx.repo ||
		recs[0].Revision != v11 {
		t.Errorf("the build receipt = %+v", recs)
	}
	if got := pinnedCommit(t); got != "" {
		t.Errorf("the advance pinned the patched fork at %s", got)
	}
}

// INSIDE THE HOUR, NOTHING PENDING: the next launch runs no git process at all (P4) and hands the
// same build, building nothing.
func TestALaunchInsideTheHourRunsNoGit(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	writeFile(t, filepath.Join(dir, "git"), "#!/bin/sh\necho \"$@\" >> "+shquote.Quote(logPath)+"\nexec "+
		shquote.Quote(realGit)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(dir, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	// The store's git named by its path, so no answer this process already has (`git version`'s,
	// asked once per binary) can stand in for a run.
	prev := patchedAdvanceStore
	patchedAdvanceStore = func(launch bool) *packsrc.Store {
		s := prev(launch)
		s.Git = filepath.Join(dir, "git")
		return s
	}
	t.Cleanup(func() { patchedAdvanceStore = prev })
	fx.later(10 * time.Minute)
	again, out, _ := fx.launch(t, "podman")
	if again.delivery.Key != r.delivery.Key || len(fx.builds) != 1 {
		t.Errorf("the second launch handed %+v after %d builds, want the first's build and no new one\n%s",
			again.delivery, len(fx.builds), out)
	}
	if logged, err := os.ReadFile(logPath); err == nil && len(logged) > 0 {
		t.Errorf("a launch inside the hour ran git:\n%s", logged)
	}
}

// A NEW TAG MOVES THE GOOD BUILD, once that build is admitted, says so, and reaps the build it
// replaced, which no running jail was handed; one a running jail was handed is kept.
func TestANewTagMovesTheGoodBuildAndReapsTheOld(t *testing.T) {
	for _, handedOld := range []bool{false, true} {
		t.Run(map[bool]string{false: "reaped", true: "kept while a jail runs it"}[handedOld], func(t *testing.T) {
			fx, v11, _, r, _, _ := firstAdvance(t)
			old := r.delivery.Key
			if handedOld {
				root := paths.PackTreeRoot("yolo-other")
				writeFile(t, filepath.Join(root, "20261004T000000Z-1.forks.json"),
					`{"schema":1,"forks":{"tool":{"key":"`+old+`","fork":"forkpack/tool"}}}`)
			}
			v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
			fx.later(2 * time.Hour)
			moved, out, handed := fx.launch(t, "podman")
			if moved.delivery.Key == "" || moved.delivery.Key == old || len(fx.builds) != 2 {
				t.Fatalf("the launch after v1.3.0 handed %+v after %d builds\n%s", moved.delivery, len(fx.builds), out)
			}
			if fx.child != 1 {
				t.Errorf("the build of a newer upstream, with a good build serving, ran %d times as a child, want 1", fx.child)
			}
			for _, w := range []string{"upstream moved — v1.3.0 (" + shortSHA(v13) + ") is newer than the good build v1.1.0 (" + shortSHA(v11) + ")",
				"a Ctrl-C starts this jail on the good build v1.1.0",
				"updated fork forkpack/tool: v1.1.0 (" + shortSHA(v11) + ") → v1.3.0 (" + shortSHA(v13) + "), 2 patches; this jail runs the new build"} {
				if !strings.Contains(out, w) {
					t.Errorf("the moving launch lacks %q:\n%s", w, out)
				}
			}
			if g := fx.record(t).Good; g.Commit != v13 || g.Entry != moved.delivery.Key || len(handed) != 1 || handed[0].Key != g.Entry {
				t.Errorf("good build %+v, handed %+v", g, handed)
			}
			if got := storeEntryExists(old); got != handedOld {
				t.Errorf("the replaced build exists = %v, want %v (handed to a running jail: %v)", got, handedOld, handedOld)
			}
		})
	}
}

// A CONFLICTING TAG IS HELD: said once with its member, its paths and the next step, the previous
// build still handed; a later launch replays it no more, says nothing, and the fork's line carries
// the held suffix.
func TestAConflictingTagIsHeldWithThePreviousBuildServing(t *testing.T) {
	fx, v11, _, r, _, _ := firstAdvance(t)
	v14 := fx.commit(t, "v1.4.0", map[int]string{14: "fourteen", 11: "eleven-again"})
	fx.later(2 * time.Hour)
	held, out, _ := fx.launch(t, "podman")
	if held.delivery.Key != r.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("the conflicting tag handed %+v after %d builds\n%s", held.delivery, len(fx.builds), out)
	}
	for _, w := range []string{"fork forkpack/tool: upstream v1.4.0 (" + shortSHA(v14) + ") does not take the patch series —",
		"0001-ten.patch conflicts in f.txt", "still running v1.1.0 (" + shortSHA(v11) + ") + 2 patches",
		"rebase the series: yolo pack rebase forkpack/tool"} {
		if !strings.Contains(out, w) {
			t.Errorf("the held launch lacks %q:\n%s", w, out)
		}
	}
	fx.later(2 * time.Hour)
	again, out, _ := fx.launch(t, "podman")
	if again.delivery.Key != r.delivery.Key || strings.Contains(out, "does not take the patch series") || len(fx.builds) != 1 {
		t.Errorf("a later launch replayed the conflict again or moved:\n%s", out)
	}
	f := fx.fork(t)
	series, _ := f.ReadSeries()
	in, _, _, _ := f.CheckWant(series).Inputs()
	suffix := run.HeldSuffix(f, fx.record(t), in, series.Digest, forkBuild{Fork: f, Series: series}.recipe())
	if !strings.Contains(suffix, "held at v1.1.0 ("+shortSHA(v11)+"): upstream v1.4.0 ("+shortSHA(v14)+") does not take 0001-ten.patch"+
		" — `yolo pack rebase forkpack/tool`") {
		t.Errorf("the held suffix is %q", suffix)
	}
}

// A FAILED BUILD of a newer upstream is recorded and backed off while the good build serves (said
// once, with where its output is and the next steps); inside the back-off nothing is built, past it
// the build is tried again.
func TestAFailedBuildBacksOffWhileTheGoodBuildServes(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.rc = 2
	fx.later(2 * time.Hour)
	failed, out, _ := fx.launch(t, "podman")
	if failed.delivery.Key != r.delivery.Key || len(fx.builds) != 2 {
		t.Fatalf("the failed build handed %+v after %d builds\n%s", failed.delivery, len(fx.builds), out)
	}
	for _, w := range []string{"the build of v1.3.0 (" + shortSHA(v13) + ") + 2 patches failed", "still running v1.1.0",
		filepath.Join("/ws", ".yolo", "launch.log"), "`yolo capture tool` retries it now", "`agent_updates` off for pack forkpack"} {
		if !strings.Contains(out, w) {
			t.Errorf("the failed build's lines lack %q:\n%s", w, out)
		}
	}
	fx.later(2 * time.Hour)
	if _, out, _ = fx.launch(t, "podman"); len(fx.builds) != 2 {
		t.Errorf("a launch inside the back-off built again:\n%s", out)
	}
	fx.rc = 0
	fx.later(48 * time.Hour)
	moved, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 3 || moved.delivery.Key == r.delivery.Key {
		t.Errorf("past the back-off the build was not tried again (%d builds), or did not move:\n%s", len(fx.builds), out)
	}
}

// A BUILD JAIL THAT NEVER RAN THE BUILD LINE is not a failed build (PF-D21): nothing is recorded,
// the good build serves, the line relays what the jail said last, on a line of its own (PPX-D42),
// and names the step (Apple Container's own besides), and the next launch tries again with no
// back-off. The jail runs as the
// child a serving advance runs (forkBuildChild), so this is red if that closure stops handing the
// child the act's teed writers, as well as if the act stops relaying (PPX-D39).
func TestABuildJailThatNeverRanRecordsNothing(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.rc, fx.ran, fx.said = 125, false, "Error: the fixture's runtime refused the container"
	fx.later(2 * time.Hour)
	children := fx.child
	got, out, _ := fx.launch(t, "container")
	if got.delivery.Key != r.delivery.Key {
		t.Fatalf("handed %+v\n%s", got.delivery, out)
	}
	if fx.child == children {
		t.Fatalf("the build did not run as a child, which this test is about:\n%s", out)
	}
	for _, w := range []string{"fork forkpack/tool: its build jail exited 125 before its build line ran — still " +
		"running v1.1.0",
		"\n    Error: the fixture's runtime refused the container\n",
		"  Fix what it names, then `yolo capture tool` builds it; the next fresh launch tries too",
		"On Apple Container a capture jail cannot start beside a running jail: if that is what stopped it, " +
			"`yolo capture tool` builds it once the other jails stop"} {
		if !strings.Contains(out, w) {
			t.Errorf("the lines lack %q:\n%s", w, out)
		}
	}
	for _, w := range []string{"((", "))", "once the runtime starts jails again"} {
		if strings.Contains(out, w) {
			t.Errorf("the lines still say %q:\n%s", w, out)
		}
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("a build jail that never ran was recorded as a failed build: %+v", o)
		}
	}
	fx.later(time.Minute)
	if _, out, _ = fx.launch(t, "container"); len(fx.builds) != 3 {
		t.Errorf("the next launch did not try the build again (%d builds):\n%s", len(fx.builds), out)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A CTRL-C DURING THE BUILD ends the advance, not the launch (PF-D25): the jail starts on the good
// build, nothing is recorded, and the next launch builds it.
func TestACtrlCDuringTheBuildStartsTheJailOnTheGoodBuild(t *testing.T) {
	fx, v11, _, r, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ captureStreams, _ bool) (int, bool) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			t.Error("the Ctrl-C did not reach the advance")
		}
		return 130, false
	}
	got, out, handed := fx.launch(t, "podman")
	forkBuildChild = prev
	if got.delivery.Key != r.delivery.Key || len(handed) != 1 || handed[0].Key != r.delivery.Key {
		t.Fatalf("after the Ctrl-C the jail is handed %+v (recorded %+v), want the good build\n%s", got.delivery, handed, out)
	}
	if !strings.Contains(out, "the advance was interrupted — this jail starts on the good build v1.1.0 ("+shortSHA(v11)+") + 2 patches") {
		t.Errorf("the interrupted advance does not say what the jail starts on:\n%s", out)
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Commit == v13 && o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("an interrupted build was recorded as failed: %+v", o)
		}
	}
	fx.later(time.Minute)
	if moved, out, _ := fx.launch(t, "podman"); moved.delivery.Key == r.delivery.Key {
		t.Errorf("the launch after the Ctrl-C did not build the candidate:\n%s", out)
	}
}

// A LOST CHECK RECORD COSTS A LOOKUP, NOT A REBUILD (§6.2): the store's newest build of the fork
// under the manifest's recipe is the good build again, said once.
func TestALostCheckRecordIsRecoveredFromTheStore(t *testing.T) {
	fx, v11, _, r, _, _ := firstAdvance(t)
	store := &packsrc.Store{Dir: paths.PacksDir()}
	if err := os.Remove(store.CheckRecordPath("forkpack/tool")); err != nil {
		t.Fatal(err)
	}
	got, out, _ := fx.launch(t, "podman")
	if got.delivery.Key != r.delivery.Key || len(fx.builds) != 1 {
		t.Errorf("after the record was lost the launch handed %+v after %d builds\n%s", got.delivery, len(fx.builds), out)
	}
	if !strings.Contains(out, "recovered its good build v1.1.0 ("+shortSHA(v11)+") from the capture store") {
		t.Errorf("the recovery is not said:\n%s", out)
	}
}

// `yolo capture <bin>` OF A PATCHED FORK builds the candidate through the swap, pins nothing, and
// with nothing pending rebuilds the good build's own inputs.
func TestCaptureOfAPatchedForkBuildsThroughTheSwap(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	if g := fx.record(t).Good; g == nil || g.Commit != v11 || len(fx.builds) != 1 {
		t.Fatalf("capture's good build = %+v after %d builds", g, len(fx.builds))
	}
	if got := pinnedCommit(t); got != "" {
		t.Errorf("capture pinned the patched fork at %s", got)
	}
	out.Reset()
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc != 0 || len(fx.builds) != 2 {
		t.Fatalf("a second capture rc=%d after %d builds, want a rebuild of the good build\n%s", rc, len(fx.builds), errw.String())
	}
	if !strings.Contains(out.String(), "rebuilt fork forkpack/tool: v1.1.0") {
		t.Errorf("the rebuild is not said:\n%s", out.String())
	}
}

// THE LAUNCH'S WIRED TRIGGER RUNS THE ADVANCE for a patched fork (TestALaunchWiresTheForkBuildTrigger's
// shape): red if runRun stops wiring Options.BuildForks, if buildForksForLaunch stops sending a
// patched fork to its advance, or if the wiring stops writing the advance to the launch's own
// stream (the request's Stderr, teed into its launch.log), or writes any of it on the jail
// command's stdout (PF-D79).
func TestTheWiredTriggerRunsAPatchedForksAdvance(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	if rc := runRun([]string{"run", "--", "true"}); rc != 0 {
		t.Fatalf("runRun = %d with the pipeline stubbed", rc)
	}
	if seen.BuildForks == nil {
		t.Fatal("`yolo run` did not wire Options.BuildForks")
	}
	var got map[string]entrypoint.ForkDelivery
	var launchOut, launchErr syncBuffer
	quiet(t, func() {
		got = seen.BuildForks(run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: fx.fork(t),
			Reason: packload.PatchedForkPinReason}}, Platform: patchedTestPlatform, Stdout: &launchOut, Stderr: &launchErr})
	})
	if got["tool"].Key == "" || len(fx.builds) != 1 {
		t.Errorf("the wired trigger answered %+v after %d builds, want the advance's build", got, len(fx.builds))
	}
	if !strings.Contains(launchErr.String(), "built fork forkpack/tool") || launchOut.String() != "" {
		t.Errorf("the advance's lines did not reach the launch's stream alone:\nstdout: %s\nstderr: %s", launchOut.String(),
			launchErr.String())
	}
}

// THE CHILD BUILD JAIL'S PLUMBING: the argv names the workspace, the bin, the fork and its base and
// the build line, and `yolo internal fork-build-jail` reads it back into the sealed, narrowed build
// jail forkBuildRunJail would run in this process.
func TestTheChildBuildJailRunsTheSealedBuildJail(t *testing.T) {
	forkBuildHome(t)
	b := forkBuild{Fork: packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "probetool", Build: "make install && echo 'x y'"}}
	argv := forkBuildChildArgv("/staging/ws", b, false)
	if argv[0] != "internal" || argv[1] != forkBuildJailVerb || argv[len(argv)-1] != b.Fork.Build {
		t.Fatalf("argv = %q", argv)
	}
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 7 })
	if rc := runInternal(argv[1:]); rc != 7 {
		t.Errorf("the child exited %d, want the build jail's 7", rc)
	}
	if seen.Workspace != "/staging/ws" || !seen.Sealed || !slices.Equal(seen.OnlyPacks, []string{"forkpack", "basepack"}) ||
		!slices.Equal(seen.Args, forkBuildJailArgv(b.Fork.Build)) || seen.CapturesDir() != "" || !seen.NeverAttach {
		t.Errorf("the child ran the build jail with %+v", seen)
	}
}

// THE CHILD IS STOPPED by the advance's Ctrl-C and by the bound, a SIGINT first, and the bound is
// told apart, since only a build past it is a failed build.
func TestTheChildBuildJailIsStoppedByTheScopeAndTheBound(t *testing.T) {
	prev := forkBuildChildCommand
	started := filepath.Join(t.TempDir(), "started")
	forkBuildChildCommand = func([]string) (*exec.Cmd, error) {
		return exec.Command("sh", "-c", testsupport.UntilInterrupted(":", started)), nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prev })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	rc, bound := runForkBuildChild(ctx, time.Hour, "/s", forkBuild{}, discardStreams, false)
	if rc != 130 || bound {
		t.Errorf("a cancelled child = %d, bound %v; want the SIGINT's 130 and no bound", rc, bound)
	}
	rc, bound = runForkBuildChild(context.Background(), 300*time.Millisecond, "/s", forkBuild{}, discardStreams, false)
	if rc != 130 || !bound {
		t.Errorf("a child past its bound = %d, bound %v; want 130 and the bound", rc, bound)
	}
}

// `yolo pack status` AFTER A LAUNCH'S ADVANCE names the good build and that the store holds it, and
// when a fresh launch next checks (§8.3); with the store's entry gone, that the next launch builds
// it again.
func TestPackStatusNamesTheGoodBuildAndTheNextCheck(t *testing.T) {
	fx, v11, _, r, _, _ := firstAdvance(t)
	_, out, _ := packVerb(t, "status")
	for _, w := range []string{"good build: v1.1.0 (" + shortSHA(v11) + ") + 2 patches", "built (" + r.delivery.Key,
		"next check: in 1 hour, at a fresh launch then"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "does not build it yet") {
		t.Errorf("status still says this yolo builds nothing:\n%s", out)
	}
	if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(r.delivery.Key); err != nil {
		t.Fatal(err)
	}
	fx.later(2 * time.Hour)
	_, out, _ = packVerb(t, "status")
	for _, w := range []string{"not in the capture store", "the next fresh launch builds it again", "next check: due"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
}

// A HAND THAT CANNOT BE RECORDED still hands the build, and says what that costs — another launch's
// move may remove it before the jail's first run — and the step that recovers it.
func TestAHandThatCannotBeRecordedSaysWhatItCosts(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	var out syncBuffer
	r := advancePatchedFork(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: "podman",
		workspace: "/ws", out: &out, errw: &out, launch: true,
		hand: func(string, run.HandedFork) error { return errors.New("disk full") }})
	if r.delivery.Key == "" {
		t.Fatalf("a hand that could not be recorded handed nothing:\n%s", out.String())
	}
	for _, w := range []string{"could not record what this launch hands its jail (disk full)",
		"another launch's move of this fork may remove the build before this jail first runs tool",
		"if tool then cannot start, a fresh launch delivers the good build"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("the warning lacks %q:\n%s", w, out.String())
		}
	}
}
