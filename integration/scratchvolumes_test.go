package integration

// scratchvolumes_test.go pins, end to end, that quitting a podman jail does not wait for
// its scratch volumes to be deleted, and that they ARE deleted shortly after.
//
// THE BUG. /tmp, /var/tmp and the nested container dirs were ANONYMOUS volumes under
// `podman run --rm`, which makes the attached client delete them itself, file by file,
// before it exits — while the launcher waits on it and the terminal waits on the
// launcher. 32 s on the maintainer's host after a 59 h session (docs/reference/perf-logging.md,
// "The linger was the scratch volumes"). Reproduced in a nested jail 2026-09-28 with bare
// podman: 200,000 empty files in an anonymous /tmp held the client 8.8 s past its
// container's exit; the same files in a named volume, 0.2 s.
//
// These are the CALL-SITE pins: runContainer minting a real per-launch id (the mount
// source must parse as a scratch volume the reaper recognises), the teardown starting
// the remover without waiting, the remover finishing, and the housekeeping slot reaping
// what no remover reached. The unit tier (internal/cli/run/scratchremoval_test.go,
// internal/prune/scratchvolumes_test.go) pins each callee.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// scratchVolumesOf lists the scratch volumes the runtime holds for cname.
func scratchVolumesOf(t *testing.T, cname string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, detectRuntime(), "volume", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		t.Fatalf("volume ls: %v", err)
	}
	var mine []string
	for _, line := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(line); strings.HasPrefix(n, cname+".scratch.") {
			mine = append(mine, n)
		}
	}
	return mine
}

func requirePodman(t *testing.T) {
	t.Helper()
	if detectRuntime() != "podman" {
		t.Skip("scratch volumes exist on podman only; Apple Container's scratch is tmpfs")
	}
}

// TestQuitDoesNotWaitForTheScratchDelete measures what 200,000 files left in /tmp add to the
// quit, against the same launch with an empty /tmp, and fails when they add half of what deleting
// them costs on the same machine.
//
// THE QUIT IT TIMES. The jail's only session is its last, so its quit streams the keeper's
// teardown and returns once the keeper has exited (endSession, streamKeeperTeardown). The keeper
// stops the container, confirms it gone, and runs teardownAfterExit, whose first act starts the
// remover detached and never waits for it (startScratchRemoval). So the quit includes the whole
// teardown and none of the delete, and the delete is back on it if anything on that chain comes
// to wait for the remover. The volumes going anonymous again is caught by launchAndQuit's mount
// check directly, whatever the timing shows.
//
// WHY A BASELINE AND NOT A FIXED BUDGET. This test asserted a fixed 5 s from the jail's command
// finishing to yolo returning. The macOS nightly's podman machine measured 2.4 s to 10.4 s across
// six runs and failed at 5.33 s (run 36521751485) and 10.37 s (run 36711874486), the remover then
// taking another 12 s to 45 s to finish, so the budget could not tell a slow VM's ordinary quit
// from the delete being on the critical path. And it was blind in the other direction on Linux,
// where the delete is cheap: in a nested jail on 2026-09-30 the jail's own `rm -rf` of these
// files took 1.5 s to 1.7 s, and a remover spawn made to wait for its child held the quit 1.8 s,
// inside the 5 s. So the test launches twice in one workspace:
//
//   - THE BASELINE makes the same files the same way and deletes them itself, timed, before its
//     command finishes. Its quit is this machine's quit after the same workload, with an empty
//     /tmp, and its timed `rm -rf` is what the delete costs here: the same unlinks, on the same
//     filesystem, that a quit waiting on the delete would wait for.
//   - THE MEASURED RUN leaves the files for the teardown.
//
// A quit that waits on the delete pays about the whole delete cost more than the baseline; one
// that does not pays about nothing more, give or take the two quits' noise and the remover's
// contention with the rest of the teardown. The bound is the midpoint, half the measured delete
// cost, which leaves the same margin on both sides and scales with the machine. That same nested
// jail measured the files adding -2 ms to 1 ms against bounds of 0.75 s to 0.83 s, and the
// waiting spawn adding 1.6 s.
//
// Each lag runs from the jail's clock to the host's. On Linux those are one kernel's clock; on a
// podman machine the jail's is the VM's, and the subtraction cancels any offset between the two,
// leaving only their drift across the minutes between the runs.
//
// macOS podman is the same case, not a different one: the argv mounts the same named scratch
// volumes there (assembleRunCmd puts ScratchMountArgs after podmanBaseMounts with no host-OS
// branch), and the teardown hands them to the same remover. What differs is how the remover
// deletes: a podman remote client has no `podman unshare`, so the out-of-podman empty fails and
// `podman volume rm` does the whole delete inside the VM (prune.emptyScratchVolume). That moves
// the remover's time, not the quit's, which is what this measures.
func TestQuitDoesNotWaitForTheScratchDelete(t *testing.T) {
	requireJail(t)
	requirePodman(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	base := launchAndQuit(t, dir, cname, true)
	awaitScratchRemoval(t, dir, cname, 1)
	full := launchAndQuit(t, dir, cname, false)
	log := awaitScratchRemoval(t, dir, cname, 2)

	// A launch id minted per launch, never per workspace: a relaunch handed the last session's
	// volumes would get whatever its remover had not yet deleted (newScratchLaunchID).
	if base.volume == full.volume {
		t.Errorf("two launches of one workspace mounted the same /tmp volume %s", full.volume)
	}

	added := full.lag - base.lag
	bound := base.deleteCost / 2
	removerTook := "unknown"
	if m := regexp.MustCompile(`scratch: removed 4 volume\(s\) in ([0-9.]+s)`).FindAllStringSubmatch(log, -1); len(m) > 0 {
		removerTook = m[len(m)-1][1]
	}
	t.Logf("jail command done -> yolo returned: %s with an empty /tmp, %s with %d files in it; the "+
		"files added %s (bound %s, half the %s the jail's own rm -rf of them took); the detached "+
		"remover took %s after the quit",
		base.lag.Round(time.Millisecond), full.lag.Round(time.Millisecond), scratchFillFiles,
		added.Round(time.Millisecond), bound.Round(time.Millisecond),
		base.deleteCost.Round(time.Millisecond), removerTook)
	if added >= bound {
		t.Errorf("%d files in /tmp added %s to the quit (%s against %s with an empty /tmp), at least "+
			"half the %s deleting them costs on this machine: the scratch delete is on the critical "+
			"path again", scratchFillFiles, added.Round(time.Millisecond), full.lag.Round(time.Millisecond),
			base.lag.Round(time.Millisecond), base.deleteCost.Round(time.Millisecond))
	}
}

// scratchFillFiles is how many files the fixture makes in /tmp: 200 directories of 1,000. On the
// nested podman this was first measured on, their anonymous-volume delete held the client 8.8 s
// past its container's exit.
const scratchFillFiles = 200 * 1000

// quitSample is one launch of the scratch fixture.
type quitSample struct {
	// lag is from the jail's command finishing, by the jail's clock, to yolo returning.
	lag time.Duration
	// volume is the scratch volume the launch mounted at /tmp.
	volume string
	// deleteCost is how long the jail's own `rm -rf` of the files took, by its clock: the
	// baseline's only.
	deleteCost time.Duration
}

// launchAndQuit launches the fixture in dir and times its quit. With clean, the jail deletes the
// files itself, timed, before its command finishes.
func launchAndQuit(t *testing.T, dir, cname string, clean bool) quitSample {
	t.Helper()
	script := `set -e
mkdir -p /tmp/fill && cd /tmp/fill
for d in $(seq 1 200); do mkdir $d; (cd $d && seq 1 1000 | xargs touch); done
cd /
`
	if clean {
		script += `rm_start=$(date +%s.%N); rm -rf /tmp/fill; rm_end=$(date +%s.%N)
echo "RM_TOOK=$(awk -v a="$rm_start" -v b="$rm_end" 'BEGIN{printf "%.6f", b-a}')"
`
	}
	// The last line is the moment the jail's command finished, by the jail's clock.
	script += `awk '$5=="/tmp"{print "TMP_SOURCE=" $4}' /proc/self/mountinfo
echo "EXIT_AT=$(date +%s.%N)"`
	res := runYolo(t, dir, script, withTimeout(10*time.Minute))
	returned := time.Now()
	if res.rc != 0 {
		t.Fatalf("rc=%d\n%s", res.rc, res.combined())
	}

	// The mount source names a scratch volume with a real launch id — what the reaper
	// and the remover recognise. An empty id would be a name every launch reused.
	m := regexp.MustCompile(`TMP_SOURCE=\S*/volumes/(` + regexp.QuoteMeta(cname) + `\.scratch\.[0-9a-f]{16}\.tmp)/_data`).
		FindStringSubmatch(res.stdout)
	if m == nil {
		t.Fatalf("/tmp is not a per-launch named scratch volume of %s:\n%s", cname, res.stdout)
	}
	s := quitSample{volume: m[1]}

	em := regexp.MustCompile(`EXIT_AT=([0-9.]+)`).FindStringSubmatch(res.stdout)
	if em == nil {
		t.Fatalf("no EXIT_AT line:\n%s", res.stdout)
	}
	exitAt, err := strconv.ParseFloat(em[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	s.lag = returned.Sub(time.Unix(0, int64(exitAt*1e9)))

	if clean {
		rm := regexp.MustCompile(`RM_TOOK=([0-9.]+)`).FindStringSubmatch(res.stdout)
		if rm == nil {
			t.Fatalf("no RM_TOOK line:\n%s", res.stdout)
		}
		took, err := strconv.ParseFloat(rm[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		s.deleteCost = time.Duration(took * float64(time.Second))
	}
	return s
}

// awaitScratchRemoval waits for the detached remover of dir's last launch to finish, and returns
// the housekeeping log once it has: the volumes going, then the remover itself, since its log line
// follows the last `volume rm`. runs is how many launches of dir have quit so far, each of which
// must have left the remover's record of its run.
func awaitScratchRemoval(t *testing.T, dir, cname string, runs int) string {
	t.Helper()
	logPath := filepath.Join(dir, ".yolo", "housekeeping.log")
	deadline := time.Now().Add(2 * time.Minute)
	for {
		left := scratchVolumesOf(t, cname)
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(logPath)
			t.Fatalf("scratch volumes still present 2 min after the quit: %v\nhousekeeping.log:\n%s", left, log)
		}
		time.Sleep(time.Second)
	}
	// The volumes going is not the remover being done: its log line follows the last
	// `volume rm`. Wait for the remover itself (its in-flight lock) before reading it.
	if err := run.WaitForScratchRemovers(dir, detachedWriterWait); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(logPath)
	if got := strings.Count(string(log), "scratch: removed 4 volume(s)"); got < runs {
		t.Fatalf("after %d quits the remover left %d records of its runs:\n%s", runs, got, log)
	}
	return string(log)
}

// What no remover reached — a launcher SIGKILLed before its teardown, a host that went
// down mid-delete — is dangling, and the next launch's housekeeping slot removes it once
// it is past the age floor. A young dangling volume (a jail being created right now) and
// a volume that is not a scratch volume are left alone.
//
// The one launch here turns the automatic reapers back on (withAutoReapers), which the suite
// keeps off: this reaper is its subject.
func TestTheSlotReapsLeftoverScratchVolumes(t *testing.T) {
	requireJail(t)
	requirePodman(t)
	rt := detectRuntime()
	mkvol := func(name string) {
		t.Helper()
		if out, err := exec.Command(rt, "volume", "create", name).CombinedOutput(); err != nil {
			t.Fatalf("volume create %s: %v\n%s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command(rt, "volume", "rm", name).Run() })
	}
	exists := func(name string) bool {
		return exec.Command(rt, "volume", "exists", name).Run() == nil
	}
	leftover := "yolo-scratchreap-1a2b3c4d.scratch.00000000deadbeef.var-lib-containers"
	decoy := "yolo-scratchreap-1a2b3c4d.scratch.notalaunchid.tmp"
	mkvol(leftover)
	mkvol(decoy)
	// Past prune.ScratchVolumeGrace (1 min).
	time.Sleep(65 * time.Second)
	young := "yolo-scratchreap-1a2b3c4d.scratch.00000000cafef00d.tmp"
	youngMade := time.Now()
	mkvol(young)

	dir := writeProject(t, `{}`)
	// The slot dies at the launch's exit and the scratch class is its last, so the jail must
	// outlive every class ahead of it. A fixed sleep did not: on a CI runner the image class
	// had a stale image to reclaim and the jail quit before the slot reached the scratch class
	// (run 37819753351). So the jail waits for the slot's own note that it started the removal,
	// in the workspace's housekeeping log, bounded so a slot that never notes still ends.
	res := runYolo(t, dir, `for i in $(seq 1 90); do
  grep -q 'scratch: removing' /workspace/.yolo/housekeeping.log 2>/dev/null && exit 0
  sleep 1
done`, withAutoReapers())
	if res.rc != 0 {
		t.Fatalf("rc=%d\n%s", res.rc, res.combined())
	}
	deadline := time.Now().Add(time.Minute)
	for exists(leftover) {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, ".yolo", "housekeeping.log"))
			t.Fatalf("the leftover %s was never reaped\nhousekeeping.log:\n%s", leftover, log)
		}
		time.Sleep(time.Second)
	}
	if !exists(decoy) {
		t.Errorf("a volume that is not a scratch volume was reaped: %s", decoy)
	}
	if !exists(young) {
		checkYoungReapWasPastTheFloor(t, filepath.Join(dir, ".yolo", "housekeeping.log"), young, youngMade)
	}
}

// checkYoungReapWasPastTheFloor decides whether the young volume's removal was a misjudged
// age or a slot that simply reached the scratch class after the volume HAD cleared the floor.
//
// The young volume is made just before the launch, and the slot reaches the scratch class
// only after the container is up and every class ahead of it has run. On Linux that is
// seconds. On the 2026-09-28 macOS nightly (run 36425623325) this launch took at least 110 s,
// and the test failed with "a dangling scratch volume younger than the floor was
// reaped" without saying when. The slot's note names what it removed, stamped by the host's
// clock to the second, so the removal is judged by that: a note less than a floor after the
// volume was made is a reap of a volume that was provably young, and anything later proves
// nothing about the floor.
func checkYoungReapWasPastTheFloor(t *testing.T, logPath, young string, made time.Time) {
	t.Helper()
	log, _ := os.ReadFile(logPath)
	var noted time.Time
	for _, line := range strings.Split(string(log), "\n") {
		if !strings.Contains(line, "scratch: removing") || !strings.Contains(line, young) {
			continue
		}
		stamp, _, _ := strings.Cut(line, "  ")
		if ts, err := time.Parse(time.RFC3339, stamp); err == nil {
			noted = ts
			break
		}
	}
	if noted.IsZero() {
		t.Errorf("the young volume %s is gone and no slot note names it: something other than the "+
			"reaper removed it\nhousekeeping.log:\n%s", young, log)
		return
	}
	// The stamp is truncated to the second, so the removal was decided before noted+1s.
	if upper := noted.Add(time.Second).Sub(made); upper < prune.ScratchVolumeGrace {
		t.Errorf("a dangling scratch volume younger than the floor was reaped: %s, removed at most "+
			"%s after it was made (floor %s)\nhousekeeping.log:\n%s", young, upper, prune.ScratchVolumeGrace, log)
		return
	}
	t.Logf("the slot reached the scratch class %s after the young volume was made, past the %s "+
		"floor, so removing it was right and this launch could not test the floor; "+
		"internal/prune's unit tests pin it", noted.Sub(made).Round(time.Second), prune.ScratchVolumeGrace)
}
