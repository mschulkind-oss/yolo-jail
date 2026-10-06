package run

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

const (
	testScratchID = "0123456789abcdef"
	testCname     = "yolo-ws-test0000"
)

// THE CALL SITE: the normal-exit teardown hands this launch's scratch volumes to the
// detached remover, FIRST, and returns without waiting on it. Delete the call from
// teardownAfterExit and this fails; make the spawn block and the chain's own spans would
// follow a wait. The proxy has already returned by the time the chain runs, so
// `child.termios_restored` precedes it by construction (runContainer).
func TestTeardownStartsTheScratchRemovalFirst(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.Timing = true
	o.initPerf(testCname)
	o.scratchVolumes = ScratchVolumeNames("volume", testCname, testScratchID)
	o.scratchRemovalOnce = &sync.Once{}
	var got [][]string
	o.StartDetached = func(argv []string, inherit *os.File) error {
		got = append(got, argv)
		assertScratchLockHeld(t, ws, inherit)
		return nil
	}

	o.teardownAfterExit(nil, "", nil, t.TempDir(), testCname, "podman", "", 0)

	if len(got) != 1 {
		t.Fatalf("remover spawned %d times, want once", len(got))
	}
	want := append([]string{"internal", ScratchRemoverVerb, "--runtime", "podman", "--workspace", ws,
		"--wait", scratchRemovalWait.String(), "--lock-fd", "3", "--"}, o.scratchVolumes...)
	if !slices.Equal(got[0][1:], want) {
		t.Errorf("remover argv = %v\nwant <yolo> %v", got[0], want)
	}
	file := scratchPerfLog(t, ws)
	started := strings.Index(file, "mark   shutdown.scratch_volumes.rm_started")
	firstSpan := strings.Index(file, "start  shutdown.cleanup_port_forwarding")
	if started < 0 || firstSpan < 0 || started > firstSpan {
		t.Errorf("the removal must start before the rest of the chain; log:\n%s", file)
	}
}

// assertScratchLockHeld fails unless inherit is <ws>/.yolo/scratch-rm.lock with a lock on it
// at the moment of the spawn — what the child inherits as fd 3, and what a waiter
// (WaitForScratchRemovers) sees until the child exits.
func assertScratchLockHeld(t *testing.T, ws string, inherit *os.File) {
	t.Helper()
	if inherit == nil {
		t.Fatal("the remover was spawned without the in-flight lock")
	}
	lockPath := filepath.Join(ws, ".yolo", ScratchRemoverLockName)
	a, errA := os.Stat(lockPath)
	b, errB := inherit.Stat()
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Fatalf("the inherited file is not %s (%v, %v)", lockPath, errA, errB)
	}
	other, err := os.Open(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Error("the in-flight lock is not held at the spawn: a waiter would not wait")
	}
}

// Both arms reach startScratchRemoval on the signal path; the remover starts once.
func TestScratchRemovalStartsOncePerLaunch(t *testing.T) {
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.scratchVolumes = ScratchVolumeNames("volume", testCname, testScratchID)
	o.scratchRemovalOnce = &sync.Once{}
	n := 0
	o.StartDetached = func([]string, *os.File) error { n++; return nil }
	o.startScratchRemoval("podman")
	o.startScratchRemoval("podman")
	if n != 1 {
		t.Fatalf("spawned %d removers, want 1", n)
	}
}

// An attach and a tmpfs launch mount no scratch volumes, and start no remover.
func TestNoScratchVolumesNoRemover(t *testing.T) {
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.StartDetached = func([]string, *os.File) error {
		t.Fatal("a launch with no scratch volumes started a remover")
		return nil
	}
	o.startScratchRemoval("podman")
	o.scratchVolumes = ScratchVolumeNames("tmpfs", testCname, testScratchID)
	o.scratchRemovalOnce = &sync.Once{}
	o.startScratchRemoval("podman")
}

// A spawn that fails says so in the housekeeping log and the perf log, never on the
// terminal the user has just been handed back.
func TestScratchRemovalSpawnFailureIsRecorded(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(ws, home)
	o.Timing = true
	o.initPerf(testCname)
	o.scratchVolumes = ScratchVolumeNames("volume", testCname, testScratchID)
	o.scratchRemovalOnce = &sync.Once{}
	o.StartDetached = func([]string, *os.File) error { return errors.New("no fork for you") }
	o.startScratchRemoval("podman")
	if !strings.Contains(scratchPerfLog(t, ws), "mark   shutdown.scratch_volumes.rm_not_started") {
		t.Error("a failed spawn left no mark")
	}
	b, _ := os.ReadFile(filepath.Join(ws, ".yolo", "housekeeping.log"))
	if !strings.Contains(string(b), "could not start the removal of 4 volume(s)") {
		t.Errorf("housekeeping.log = %q", b)
	}
}

// scratchRun is a RunFunc whose `volume ls` answers come from a script, one step per
// listing, recording every call.
type scratchRun struct {
	steps [][2]string // {json listing, dangling names}; "" json => the listing fails
	i     int
	calls []string
}

func (s *scratchRun) run(argv []string, _ time.Duration) prune.ProbeResult {
	k := strings.Join(argv, " ")
	s.calls = append(s.calls, k)
	switch {
	case strings.HasSuffix(k, "volume ls --format json"):
		step := s.steps[min(s.i, len(s.steps)-1)]
		s.i++
		if step[0] == "" {
			return prune.ProbeResult{Ran: true, RC: 125}
		}
		return prune.ProbeResult{Ran: true, Stdout: step[0]}
	case strings.Contains(k, "dangling=true"):
		step := s.steps[min(s.i-1, len(s.steps)-1)]
		return prune.ProbeResult{Ran: true, Stdout: step[1]}
	case strings.Contains(k, " volume rm "), strings.Contains(k, "rm -rf"):
		return prune.ProbeResult{Ran: true}
	}
	return prune.ProbeResult{Ran: false}
}

func listing(names ...string) string {
	var parts []string
	for _, n := range names {
		parts = append(parts, `{"Name":"`+n+`","Mountpoint":"/s/volumes/`+n+`/_data","CreatedAt":"2026-09-28T00:00:00Z"}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// The remover waits while podman cannot answer or still has a container on the volume,
// removes each one the moment it is dangling, and never touches a name that is not a
// scratch volume, whatever its argv says.
func TestRemoverWaitsForDanglingThenRemoves(t *testing.T) {
	names := ScratchVolumeNames("volume", testCname, testScratchID)
	tmp, lib := names[0], names[2]
	s := &scratchRun{steps: [][2]string{
		{"", ""},                   // could not ask
		{listing(tmp, lib), ""},    // both still referenced
		{listing(tmp, lib), tmp},   // /tmp let go
		{listing(lib), lib + "\n"}, // /var/lib/containers too
	}}
	clock := time.Unix(0, 0)
	now := func() time.Time { return clock }
	sleep := func(d time.Duration) { clock = clock.Add(d) }
	r := removeScratchVolumes("podman", []string{tmp, lib, "userdata"}, time.Minute, s.run, now, sleep)
	if !slices.Equal(r.removed, sortedCopy(tmp, lib)) || len(r.pending) != 0 {
		t.Fatalf("result = %+v", r)
	}
	if !slices.Equal(r.failed, []string{"userdata"}) {
		t.Errorf("a non-scratch name must be refused, not removed: %+v", r)
	}
	for _, c := range s.calls {
		if strings.Contains(c, "userdata") {
			t.Errorf("the remover touched a volume that is not a scratch volume: %q", c)
		}
	}
	// Removal order follows the evidence: /tmp's rm precedes the listing that freed lib.
	if i, j := slices.Index(s.calls, "podman volume rm "+tmp), slices.Index(s.calls, "podman volume rm "+lib); i < 0 || j < 0 || i > j {
		t.Errorf("calls = %v", s.calls)
	}
}

// Past the wait, a volume still in use (or a runtime still unreachable) is LEFT, for the
// reaper, and never forced.
func TestRemoverLeavesWhatItCannotProve(t *testing.T) {
	names := ScratchVolumeNames("volume", testCname, testScratchID)
	for _, steps := range [][][2]string{
		{{listing(names[0]), ""}}, // in use throughout
		{{"", ""}},                // never answerable
	} {
		s := &scratchRun{steps: steps}
		clock := time.Unix(0, 0)
		r := removeScratchVolumes("podman", names[:1], 3*time.Second, s.run,
			func() time.Time { return clock }, func(d time.Duration) { clock = clock.Add(d) })
		if !slices.Equal(r.pending, names[:1]) || len(r.removed) != 0 {
			t.Errorf("steps %v: result = %+v, want the volume left pending", steps, r)
		}
		for _, c := range s.calls {
			if strings.Contains(c, " rm") {
				t.Errorf("removed without evidence: %q", c)
			}
		}
	}
	// A volume podman no longer lists is gone — removed already, or never created.
	s := &scratchRun{steps: [][2]string{{listing(), ""}}}
	r := removeScratchVolumes("podman", names, 0, s.run, time.Now, func(time.Duration) {})
	if len(r.gone) != 4 || len(r.pending) != 0 {
		t.Errorf("result = %+v", r)
	}
}

// The slot's reaper: tri-state, the age floor, only dangling volumes, the opt-out.
func TestSlotReapsOnlyProvenLeftovers(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	gone := prune.ScratchVolumeName("yolo-gone-11111111", "1111111111111111", "tmp")
	young := prune.ScratchVolumeName("yolo-new-22222222", "2222222222222222", "tmp")
	live := prune.ScratchVolumeName("yolo-live-33333333", "3333333333333333", "tmp")
	vol := func(n string, created time.Time) string {
		return `{"Name":"` + n + `","Mountpoint":"/s/volumes/` + n + `/_data","CreatedAt":"` + created.Format(time.RFC3339Nano) + `"}`
	}
	all := "[" + vol(gone, now.Add(-3*time.Hour)) + "," + vol(young, now.Add(-5*time.Second)) + "," + vol(live, now.Add(-3*time.Hour)) + "]"

	mk := func(listOK bool, env string) (*Options, *[][]string) {
		o := goldenOptions(t.TempDir(), t.TempDir())
		o.Now = func() time.Time { return now }
		o.Getenv = func(k string) string {
			if k == autoReapOptOutEnv {
				return env
			}
			return ""
		}
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			k := strings.Join(argv, " ")
			if !listOK {
				return ExecResult{Ran: true, RC: 125}
			}
			if strings.Contains(k, "dangling=true") {
				return ExecResult{Ran: true, Stdout: gone + "\n" + young + "\n"}
			}
			if strings.HasSuffix(k, "--format json") {
				return ExecResult{Ran: true, Stdout: all}
			}
			return ExecResult{}
		}
		var spawned [][]string
		o.StartDetached = func(argv []string, inherit *os.File) error {
			spawned = append(spawned, argv)
			assertScratchLockHeld(t, o.Workspace, inherit)
			return nil
		}
		return o, &spawned
	}

	o, spawned := mk(true, "")
	o.reapScratchVolumes("podman")
	if len(*spawned) != 1 {
		t.Fatalf("spawned %v", *spawned)
	}
	argv := (*spawned)[0]
	if tail := argv[len(argv)-2:]; !slices.Equal(tail, []string{"--", gone}) {
		t.Errorf("reaper handed %v; only the old dangling volume is proven gone", argv)
	}
	if !slices.Contains(argv, "--lock-fd") {
		t.Errorf("the slot's remover was spawned without the in-flight lock: %v", argv)
	}

	for _, tc := range []struct {
		name   string
		listOK bool
		env    string
		rt     string
	}{
		{"could not ask", false, "", "podman"},
		{"opted out", true, "1", "podman"},
		{"apple container", true, "", "container"},
	} {
		o, spawned := mk(tc.listOK, tc.env)
		o.reapScratchVolumes(tc.rt)
		if len(*spawned) != 0 {
			t.Errorf("%s: spawned %v", tc.name, *spawned)
		}
	}
}

// The housekeeping slot runs the class: delete the call and this fails.
func TestHousekeepingSlotRunsTheScratchReaper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "") // the host frame
	o := goldenOptions(t.TempDir(), home)
	asked := false
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if strings.Contains(strings.Join(argv, " "), "volume ls --format json") {
			asked = true
		}
		return ExecResult{Ran: false}
	}
	o.runHousekeeping("podman", reclaimConsent{}, testCname)
	if !asked {
		t.Error("the housekeeping slot never listed scratch volumes")
	}
}

func sortedCopy(s ...string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// scratchPerfLog reads the workspace's host perf log ("" when absent).
func scratchPerfLog(t *testing.T, ws string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	return string(b)
}

// THE REMOVER NEVER RESURRECTS A WORKSPACE. It logs up to a minute after the jail exited,
// and a user may delete the workspace — or just its `.yolo` — the moment they quit; its
// note must then go nowhere rather than recreate the directory (CI run 36383731749 caught
// it recreating `<ws>/.yolo/housekeeping.log` under a t.TempDir mid-cleanup). The runtime
// is a path that does not exist, so no listing answers and the run is all "pending": the
// note is still written when there is somewhere to write it (the positive control).
func TestTheRemoverNeverRecreatesAMissingWorkspace(t *testing.T) {
	noRuntime := filepath.Join(t.TempDir(), "no-such-podman")
	vol := testCname + ".scratch." + testScratchID + ".tmp"
	remove := func(ws string) int {
		return ScratchRemoverMain([]string{"--runtime", noRuntime, "--workspace", ws, "--wait", "0s", "--", vol})
	}

	t.Run("the workspace was deleted", func(t *testing.T) {
		ws := filepath.Join(t.TempDir(), "deleted-workspace")
		if rc := remove(ws); rc != 1 {
			t.Errorf("rc = %d, want 1 (the volume was left pending)", rc)
		}
		if _, err := os.Lstat(ws); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the remover recreated the deleted workspace %s (%v)", ws, err)
		}
	})
	t.Run("the workspace's .yolo was deleted", func(t *testing.T) {
		ws := t.TempDir()
		remove(ws)
		if _, err := os.Lstat(filepath.Join(ws, ".yolo")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the remover recreated .yolo (%v)", err)
		}
	})
	t.Run("a live workspace gets the note", func(t *testing.T) {
		ws := t.TempDir()
		if err := os.Mkdir(filepath.Join(ws, ".yolo"), 0o755); err != nil {
			t.Fatal(err)
		}
		remove(ws)
		b, _ := os.ReadFile(filepath.Join(ws, ".yolo", "housekeeping.log"))
		if !strings.Contains(string(b), "scratch: removed 0 volume(s)") || !strings.Contains(string(b), vol) {
			t.Errorf("housekeeping.log = %q", b)
		}
	})
}

// A --lock-fd the remover cannot use is a usage error, not a silently unlocked run.
func TestTheRemoverRefusesABadLockFD(t *testing.T) {
	for _, v := range []string{"x", "2", "-1"} {
		if rc := ScratchRemoverMain([]string{"--runtime", "podman", "--lock-fd", v, "--", "v"}); rc != 2 {
			t.Errorf("--lock-fd %s: rc = %d, want 2", v, rc)
		}
	}
}

// With no remover ever started for a workspace there is nothing to wait for, and the wait
// creates nothing — the suite calls it on every workspace a launch ran in, including ones
// whose launch never reached a jail.
func TestWaitForScratchRemoversWithNoneStarted(t *testing.T) {
	ws := t.TempDir()
	if err := WaitForScratchRemovers(ws, 0); err != nil {
		t.Errorf("no .yolo: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the wait created .yolo (%v)", err)
	}
	if err := os.Mkdir(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WaitForScratchRemovers(ws, 0); err != nil {
		t.Errorf("no lock file: %v", err)
	}
	if err := WaitForScratchRemovers(filepath.Join(ws, "gone"), 0); err != nil {
		t.Errorf("no workspace: %v", err)
	}
}
