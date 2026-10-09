package cli

// treebackground_test.go pins THE NEXT-LAUNCH MODE at a fresh launch (treedelivery.go,
// backgroundadvance.go; docs/design/pi-extension-store-builds.md §7.4, XB-D17 to XB-D21): the timing
// read from `agent_updates`, the one background advance a launch starts and only when something is
// due, the keys a running one already has, Apple Container's demotion, and the next launch's report
// of what the background advance did.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// agentUpdates rewrites the pool fixture's user config with `agent_updates` set to value, JSON.
func (pf *poolFixture) agentUpdates(t *testing.T, value string) {
	t.Helper()
	dir := filepath.Join(pf.packs, "treepool")
	writeFile(t, filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+dir+`","name":"treepool"}],"agent_updates":`+value+`}`)
}

// countBuilds makes every build of the fixture count, and returns the count.
func (pf *poolFixture) countBuilds() *atomic.Int32 {
	var n atomic.Int32
	pf.child = func(context.Context, forkBuild) int { n.Add(1); return 0 }
	return &n
}

// spawns stands in for the detached spawn of the background advance, recording each argv.
type spawns struct {
	mu    sync.Mutex
	argvs [][]string
	logs  []string
}

func stubSpawn(t *testing.T) *spawns {
	t.Helper()
	s := &spawns{}
	prev := startBackgroundAdvance
	startBackgroundAdvance = func(argv []string, log string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.argvs, s.logs = append(s.argvs, argv), append(s.logs, log)
		return nil
	}
	t.Cleanup(func() { startBackgroundAdvance = prev })
	return s
}

// keys are the --key values of spawn i, sorted.
func (s *spawns) keys(i int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, a := range s.argvs[i] {
		if k, ok := strings.CutPrefix(a, "--key="); ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func (s *spawns) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.argvs)
}

// THE TIMING READER (XB-D18, the precedence decision): the owning agent pack's own entry, then the
// contributing pack's, then "*"; a hold from either pack is at the launch. Red if treeUpdateTiming
// stops reading `agent_updates`.
func TestTreeUpdateTimingReadsOwnerThenContributingPack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	tree := packload.Fork{Pack: "matt", Owner: "pi", Bin: "ext", Into: ".pi/ext/ext"}
	ownerless := packload.Fork{Pack: "matt", Bin: "ext", Into: ".tool/ext/ext"}
	for _, c := range []struct {
		value string
		f     packload.Fork
		want  updateTiming
	}{
		{`{"pi":"next-launch"}`, tree, timingNextLaunch},
		{`{"matt":"next-launch"}`, tree, timingNextLaunch},
		{`{"pi":"launch","matt":"next-launch"}`, tree, timingAtLaunch},
		{`{"*":"next-launch","matt":true}`, tree, timingAtLaunch},
		{`{"matt":false,"pi":"next-launch"}`, tree, timingAtLaunch},
		{`"next-launch"`, tree, timingNextLaunch},
		{`{"matt":"next-launch"}`, ownerless, timingNextLaunch},
		{`{"pi":"next-launch"}`, ownerless, timingAtLaunch},
	} {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"agent_updates":`+c.value+`}`)
		if got := treeUpdateTiming(c.f); got != c.want {
			t.Errorf("agent_updates %s, %s (owner %q): timing %d, want %d", c.value, c.f.Key(), c.f.Owner, got, c.want)
		}
	}
}

// A NEXT-LAUNCH KEY WHOSE CHECK IS DUE is handed its good build, and the launch starts ONE background
// advance for every such key, naming its log; nothing is checked or built in front. Red if the launch
// stops starting it (runBuildSlot's call, or backgroundTreeAdvance's spawn).
func TestANextLaunchKeyDueForACheckStartsOneBackgroundAdvance(t *testing.T) {
	pf := newPoolFixture(t)
	first, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if first[poolKeyA].Dir == "" || first[poolKeyB].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	builds := pf.countBuilds()
	sp := stubSpawn(t)
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if builds.Load() != 0 || strings.Contains(out, "checking extension") {
		t.Errorf("a next-launch key was checked or built in front (%d builds):\n%s", builds.Load(), out)
	}
	if got[poolKeyA].Entry != first[poolKeyA].Entry || got[poolKeyB].Entry != first[poolKeyB].Entry {
		t.Errorf("the keys were not handed their good builds: %+v", got)
	}
	if sp.count() != 1 {
		t.Fatalf("%d background advances started, want one:\n%s", sp.count(), out)
	}
	argv := sp.argvs[0]
	if len(argv) < 2 || argv[0] != "internal" || argv[1] != backgroundAdvanceVerb || !slices.Contains(argv, "--runtime=podman") ||
		!slices.Contains(argv, "--platform="+patchedTestPlatform) {
		t.Errorf("the background advance's argv = %q", argv)
	}
	if !slices.Equal(sp.keys(0), []string{poolKeyA, poolKeyB}) {
		t.Errorf("the background advance was handed %v, want both keys", sp.keys(0))
	}
	if !strings.Contains(out, "a background advance is checking and building them now; its log is "+backgroundAdvanceLog()) {
		t.Errorf("the spawn is not said with its log:\n%s", out)
	}
}

// A NEXT-LAUNCH KEY WITH NOTHING DUE — checked within the hour, nothing pending — starts none. Red if
// the launch starts a background advance for every key it handed a good build.
func TestANextLaunchKeyWithNothingDueStartsNone(t *testing.T) {
	pf := newPoolFixture(t)
	if first, out := pf.launch(t, "podman", &run.ActInterrupt{}); first[poolKeyA].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.now = pf.now.Add(time.Minute)
	sp := stubSpawn(t)
	if _, out := pf.launch(t, "podman", &run.ActInterrupt{}); sp.count() != 0 {
		t.Errorf("a launch with nothing due started %d background advances:\n%s", sp.count(), out)
	}
}

// A KEY THAT TOOK ITS FALLBACK for want of a build starts the background advance, which builds it;
// a key with neither builds in front. Red if the fallback's key is not handed to the spawn.
func TestAFallbackKeyStartsTheBackgroundAdvance(t *testing.T) {
	pf := newPoolFixture(t)
	writeFile(t, filepath.Join(pf.packs, "treepool", "pack.json"), `{"name":"treepool","contributes":[`+
		`{"kind":"files","into":".tool/ext/ext-a","source":"git+file://`+pf.repo+`?ref=main","fallback":"git:example.com/ext-a"},`+
		`{"kind":"files","into":".tool/ext/ext-b","source":"git+file://`+pf.repo+`?ref=main"}]}`)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	sp := stubSpawn(t)
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if got[poolKeyA].Dir != "" || !strings.Contains(got[poolKeyA].Reason, "a background advance builds it") {
		t.Errorf("the fallback key was handed %+v", got[poolKeyA])
	}
	if got[poolKeyB].Dir == "" {
		t.Errorf("the key with neither a build nor a fallback was not built in front: %+v\n%s", got[poolKeyB], out)
	}
	if sp.count() != 1 || !slices.Equal(sp.keys(0), []string{poolKeyA}) {
		t.Errorf("the background advance was started %d times for %v, want once for %s", sp.count(), sp.argvs, poolKeyA)
	}
}

// A KEY ANOTHER BACKGROUND ADVANCE RUNS NOW is said to be running and not handed a second one. Red if
// the launch ignores the key's background lock.
func TestAKeyWhoseBackgroundAdvanceRunsIsNotStartedAgain(t *testing.T) {
	pf := newPoolFixture(t)
	if first, out := pf.launch(t, "podman", &run.ActInterrupt{}); first[poolKeyA].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	lk, err := pidlock.Acquire(backgroundKeyLock(poolKeyA), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	if err := writeBackgroundOutcome(backgroundOutcome{Key: poolKeyA, Label: "extension " + poolKeyA, State: bgRunning,
		Pid: os.Getpid(), At: pf.now.Unix(), Log: backgroundAdvanceLog()}); err != nil {
		t.Fatal(err)
	}
	sp := stubSpawn(t)
	_, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if sp.count() != 1 || !slices.Equal(sp.keys(0), []string{poolKeyB}) {
		t.Errorf("the background advance was started for %v, want %s alone", sp.argvs, poolKeyB)
	}
	if !strings.Contains(out, "extension "+poolKeyA+": the background advance started at ") ||
		!strings.Contains(out, "is still checking and building it") {
		t.Errorf("the running advance is not said:\n%s", out)
	}
	if _, err := os.Stat(backgroundOutcomePath(poolKeyA)); err != nil {
		t.Errorf("a running advance's record was claimed: %v", err)
	}
}

// ON APPLE CONTAINER the next-launch mode is the at-launch wait (XB-D21), said: the key is checked and
// built in front and no background advance starts. Red if the runtime gate is dropped.
func TestOnAppleContainerANextLaunchKeyUpdatesAtTheLaunch(t *testing.T) {
	pf := newPoolFixture(t)
	first, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if first[poolKeyA].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	sp := stubSpawn(t)
	got, out := pf.launch(t, "container", &run.ActInterrupt{})
	if sp.count() != 0 {
		t.Errorf("Apple Container started a background advance:\n%s", out)
	}
	if got[poolKeyA].Entry == first[poolKeyA].Entry {
		t.Errorf("the key was not updated at the launch:\n%s", out)
	}
	if !strings.Contains(out, "on Apple Container this yolo runs no background advance — it updates at this launch") {
		t.Errorf("the demotion is not said:\n%s", out)
	}
}

// THE BACKGROUND ADVANCE'S MOVE IS SAID AT THE NEXT LAUNCH, ONCE, and that launch runs the new build:
// a real background advance builds both keys for the next launch, and the launch after it says so;
// the one after that says nothing. Red if the launch stops reading the outcome record, or stops
// claiming it.
func TestTheNextLaunchReportsTheBackgroundMoveOnce(t *testing.T) {
	pf := newPoolFixture(t)
	first, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if first[poolKeyA].Dir == "" || first[poolKeyB].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	var bg bytes.Buffer
	// Advance each key separately: simultaneous keys of this fixture share one repository, and a
	// NoWait background attempt intentionally skips a sibling holding its mirror lock.
	for _, key := range []string{poolKeyA, poolKeyB} {
		runBackgroundAdvanceUnder(context.Background(), backgroundArgs{runtime: "podman", platform: patchedTestPlatform,
			keys: []string{key}}, &bg)
	}
	sp := stubSpawn(t)
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	for _, k := range []string{poolKeyA, poolKeyB} {
		if got[k].Entry == first[k].Entry {
			t.Errorf("%s: the next launch does not run the background advance's build:\n%s\n%s", k, bg.String(), out)
		}
		if !strings.Contains(out, "extension "+k+": the background advance at ") || !strings.Contains(out, "; available for this launch") {
			t.Errorf("%s: the move is not said:\n%s", k, out)
		}
	}
	if !strings.Contains(out, "(was ") {
		t.Errorf("the move line does not say what it moved from:\n%s", out)
	}
	if sp.count() != 0 {
		t.Errorf("a launch after a finished background advance started another:\n%s", out)
	}
	_, again := pf.launch(t, "podman", &run.ActInterrupt{})
	if strings.Contains(again, "the background advance at ") {
		t.Errorf("the move was said twice:\n%s", again)
	}
}

// A BACKGROUND BUILD THAT FAILED is said at the next launch with its log and its retry time. Red if
// the failure's record or its line loses either.
func TestTheNextLaunchReportsAFailureWithItsLogAndRetry(t *testing.T) {
	pf := newPoolFixture(t)
	if first, out := pf.launch(t, "podman", &run.ActInterrupt{}); first[poolKeyB].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "", map[int]string{5: "five"})
	pf.now = pf.now.Add(2 * time.Hour)
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, d time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		// THE BUILD LINE RUNS AND FAILS: the jail's boot is done, then its build exits 2.
		s.jailReady()
		writeFile(t, filepath.Join(staging, forkToolchainLeaf), "image-identity\n")
		fmt.Fprintln(s.errw, "npm ERR! the build line of ext-b failed")
		return 2, false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	var bg bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), backgroundArgs{runtime: "podman", platform: patchedTestPlatform,
		keys: []string{poolKeyB}}, &bg)
	stubSpawn(t)
	_, out := pf.launch(t, "podman", &run.ActInterrupt{})
	want := "⚠ extension " + poolKeyB + ": the background advance at "
	if !strings.Contains(out, want) || !strings.Contains(out, " could not build ") ||
		!strings.Contains(out, "its output is in "+backgroundAdvanceLog()) || !strings.Contains(out, "it is retried after ") ||
		!strings.Contains(out, "`yolo capture "+poolKeyB+"` now") {
		t.Errorf("the failure is not said with its log and retry:\n%s\nthe background advance said:\n%s", out, bg.String())
	}
}

// AN UNFINISHED BACKGROUND ADVANCE — its record running, its lock free, as a SIGKILL leaves it — is
// said, and this launch starts another though nothing else is due. Red if an unfinished record does
// not start one.
func TestAnUnfinishedBackgroundAdvanceIsSaidAndStartedAgain(t *testing.T) {
	pf := newPoolFixture(t)
	if first, out := pf.launch(t, "podman", &run.ActInterrupt{}); first[poolKeyA].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", first, out)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.now = pf.now.Add(time.Minute)
	if err := writeBackgroundOutcome(backgroundOutcome{Key: poolKeyA, Label: "extension " + poolKeyA, State: bgRunning,
		Pid: 1, At: pf.now.Unix(), Log: backgroundAdvanceLog()}); err != nil {
		t.Fatal(err)
	}
	sp := stubSpawn(t)
	_, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if !strings.Contains(out, "did not finish — its output is in "+backgroundAdvanceLog()+"; a launch with next-launch updates retries it") {
		t.Errorf("the unfinished advance is not said:\n%s", out)
	}
	if sp.count() != 1 || !slices.Equal(sp.keys(0), []string{poolKeyA}) {
		t.Errorf("the background advance was started for %v, want %s alone", sp.argvs, poolKeyA)
	}
}

// A background move reports what it built, not what a launch runs: this launch may have no copy
// destination (or fail to copy), and a held/edited recipe can select a different build.
func TestABackgroundMoveDoesNotClaimAnUndeliveredTreeRuns(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	var bg bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), bgArgs(poolKeyA), &bg)
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	var out syncBuffer
	got := deliverTreesForLaunch(run.TreeBuildRequest{Trees: []packload.Fork{f}, Runtime: "podman",
		Platform: patchedTestPlatform, Build: true, Interrupt: &run.ActInterrupt{}}, &out, &out, false)
	if got[poolKeyA].Dir != "" || !strings.Contains(out.String(), "the background advance at ") {
		t.Fatalf("fixture did not report an undelivered background move: %+v\n%s", got, out.String())
	}
	if strings.Contains(out.String(), "this jail runs it") {
		t.Errorf("a launch that handed no tree claimed it runs the background build:\n%s", out.String())
	}
	_ = pf
}

// Exactly one fresh launch claims a completed outcome; the other launches must not repeat it.
func TestConcurrentLaunchesReportABackgroundOutcomeOnce(t *testing.T) {
	newPoolFixture(t)
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	if err := writeBackgroundOutcome(backgroundOutcome{Key: f.Key(), State: bgMoved, At: time.Now().Unix(), To: "new-build"}); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			noteBackgroundOutcome(f, richtext.Printer{W: &out})
		}()
	}
	wg.Wait()
	if n := strings.Count(out.String(), "the background advance at "); n != 1 {
		t.Errorf("%d launches reported the same outcome, want one:\n%s", n, out.String())
	}
}
