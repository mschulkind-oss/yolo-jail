package integration

// keeper_test.go is the end-to-end pin on step 3 of docs/design/jail-lifetime-last-session-wins.md:
// a container jail's KEEPER owns its host services and its life, so the first terminal gets its
// prompt back when its own agent quits, the jail lives while any session does, and a killed keeper
// leaves its sessions running while an arrival is refused (OQ-JL7, JL-D13), the last session then
// reaping the jail itself (JL-D30). The unit tiers pin each piece against a fake runtime
// (internal/cli/run/keeper_test.go); only a real jail proves the shape they make.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// keeperPID is the jail's keeper, read off the owner-PID file it writes, and checked to be the
// keeper by its command line.
func keeperPID(t *testing.T, dir string) int {
	t.Helper()
	path := filepath.Join(paths.GlobalStorage(), "owners", naming.FromWorkspace(dir))
	deadline := time.Now().Add(jailTimeout())
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				cmdline, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
				if strings.Contains(string(cmdline), "internal\x00daemon\x00jail-keeper") {
					return pid
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no keeper named in %s: %q (%v)", path, raw, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// awaitProcessGone waits for pid to be gone (processGone, a zombie counting as gone: a keeper
// outlives the launch that spawned it, and whatever adopted it may never reap it), within a bound.
func awaitProcessGone(pid int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for !processGone(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// TestQuittingTheFirstSessionLeavesTheOthersRunning is §8 items 1, 2 and 4: two sessions in one
// workspace's jail, the first quits, and its launcher returns at once with the one line while the
// second keeps running; a third entry attaches beside it; and the last quit streams the keeper's
// teardown, after which no container and no keeper are left.
func TestQuittingTheFirstSessionLeavesTheOthersRunning(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	const releaseFirst, releaseSecond = "release-first", "release-second"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+releaseFirst+` ] && break; sleep 0.2; done`)
	t.Cleanup(func() { writeRelease(t, dir, releaseFirst) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)
	if !strings.Contains(first.combined(), "keeper: yolo internal daemon jail-keeper will hold") ||
		!regexp.MustCompile(`keeper: started, pid \d+`).MatchString(first.combined()) {
		t.Errorf("the fresh launch did not disclose its keeper:\n%s", first.combined())
	}
	keeper := keeperPID(t, dir)

	second := startYoloBackground(t, "second", dir,
		`echo SECOND-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+releaseSecond+` ] && break; sleep 0.2; done; echo SECOND-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, releaseSecond) })
	awaitOutput(t, second, regexp.MustCompile(`SECOND-IN-42`))
	if !strings.Contains(second.combined(), "Attaching to existing jail") {
		t.Fatalf("the second launch did not attach:\n%s", second.combined())
	}

	// THE FIRST TERMINAL GETS ITS PROMPT BACK, and the jail stays up for the second.
	writeRelease(t, dir, releaseFirst)
	quit := time.Now()
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the first session ended rc %d:\n%s", rc, first.combined())
	}
	if took := time.Since(quit); took > 15*time.Second {
		t.Errorf("the first launcher took %s to give its prompt back", took)
	}
	if !strings.Contains(first.combined(), "Jail "+cname+" stays up for") {
		t.Errorf("the first session's quit did not say the jail stays up:\n%s", first.combined())
	}
	if n := runningContainers(t, cname); n != 1 {
		t.Fatalf("the jail is not running after its first session quit (%d containers)", n)
	}
	select {
	case err := <-second.done:
		t.Fatalf("the second session ended (%v) when the first quit:\n%s", err, second.combined())
	default:
	}
	if syscall.Kill(keeper, 0) != nil {
		t.Fatal("the keeper is gone while a session runs")
	}

	// RE-ENTERING IS AN ORDINARY ATTACH, from any terminal.
	third := runYolo(t, dir, `echo THIRD-$((40+2))`)
	if third.rc != 0 || !strings.Contains(third.stdout, "THIRD-42") {
		t.Fatalf("an entry beside the second failed: rc %d\n%s", third.rc, third.combined())
	}
	if !strings.Contains(third.combined(), "Attaching to existing jail") {
		t.Errorf("the third entry did not attach:\n%s", third.combined())
	}

	// THE LAST QUIT streams the teardown, and nothing is left.
	writeRelease(t, dir, releaseSecond)
	if rc := second.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the second session ended rc %d:\n%s", rc, second.combined())
	}
	if !strings.Contains(second.combined(), "SECOND-OUT-42") {
		t.Errorf("the second session did not finish its command:\n%s", second.combined())
	}
	if !strings.Contains(second.combined(), "That was the last session in "+cname) {
		t.Errorf("the last quit did not stream the keeper's teardown:\n%s", second.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the last session quit", n, cname)
	}
	if !awaitProcessGone(keeper, 10*time.Second) {
		t.Errorf("the keeper (pid %d) outlived its jail's last session", keeper)
	}
}

// TestAKilledKeeperLeavesItsSessionsAndRefusesArrivals is §8 item 7, as OQ-JL7 ruled: `kill -9` of
// the keeper leaves its session running; the next arrival is refused, naming `yolo stop`; and when
// the session quits, being the last, it reaps the jail itself, so no container is left.
func TestAKilledKeeperLeavesItsSessionsAndRefusesArrivals(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	const release = "release-first"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.2; done; echo FIRST-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)
	keeper := keeperPID(t, dir)

	if err := syscall.Kill(keeper, syscall.SIGKILL); err != nil {
		t.Fatalf("killing the keeper: %v", err)
	}
	if !awaitProcessGone(keeper, 10*time.Second) {
		t.Fatalf("the SIGKILLed keeper (pid %d) is still there", keeper)
	}
	if n := runningContainers(t, cname); n != 1 {
		t.Fatalf("the jail stopped when its keeper was killed (%d containers)", n)
	}

	arrival := runYolo(t, dir, "echo ENTERED-$((40+2))")
	if arrival.rc == 0 || strings.Contains(arrival.stdout, "ENTERED-42") {
		t.Errorf("an arrival entered a jail whose keeper is gone: rc %d\n%s", arrival.rc, arrival.combined())
	}
	if !strings.Contains(arrival.stderr, "Refusing to enter "+cname) || !strings.Contains(arrival.stderr, "'yolo stop'") {
		t.Errorf("the refusal does not name the jail and the remedy:\n%s", arrival.combined())
	}
	select {
	case err := <-first.done:
		t.Fatalf("the session ended (%v) when its keeper was killed:\n%s", err, first.combined())
	default:
	}

	writeRelease(t, dir, release)
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the session ended rc %d:\n%s", rc, first.combined())
	}
	if !strings.Contains(first.combined(), "FIRST-OUT-42") {
		t.Errorf("the session did not finish its command:\n%s", first.combined())
	}
	if !strings.Contains(first.combined(), "This jail's keeper is gone, and this was its last session") {
		t.Errorf("the last session did not say it reaps the unkept jail:\n%s", first.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the unkept jail's last session quit", n, cname)
	}
	// And the workspace launches fresh again.
	again := runYolo(t, dir, "echo AGAIN-$((40+2))")
	if again.rc != 0 || !strings.Contains(again.stdout, "AGAIN-42") {
		t.Errorf("a launch after the reap failed: rc %d\n%s", again.rc, again.combined())
	}
}

// TestAHungUpFirstSessionEndsOnlyItself is JL-D4 for the first session, as OQ-JL8 ruled: a SIGHUP to
// the launcher that started the jail, which is what a closed window or pane sends it, ends that
// session's own processes in the jail and nothing else: the launcher exits 128+SIGHUP, and the
// jail and the other session run on.
func TestAHungUpFirstSessionEndsOnlyItself(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	first := startYoloBackground(t, "first", dir, `echo FIRST-IN-$((40+2)); exec sleep 4321`)
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)
	const release = "release-attach"
	attach := startYoloBackground(t, "attach", dir,
		`echo ATTACH-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.2; done`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, attach, regexp.MustCompile(`ATTACH-IN-42`))

	if err := syscall.Kill(first.pid, syscall.SIGHUP); err != nil {
		t.Fatalf("hanging up the first launcher: %v", err)
	}
	if rc := first.wait(t, jailTimeout()); rc != 128+int(syscall.SIGHUP) {
		t.Errorf("the hung-up first launcher returned %d, want %d:\n%s", rc, 128+int(syscall.SIGHUP), first.combined())
	}
	var after result
	for deadline := time.Now().Add(10 * time.Second); ; {
		after = runYolo(t, dir, inJailSleepCount)
		if strings.Contains(after.stdout, "SLEEPS=0") || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(after.stdout, "SLEEPS=0") {
		t.Errorf("the hung-up first session's command is still running in the jail:\n%s", after.combined())
	}
	if n := runningContainers(t, cname); n != 1 {
		t.Fatalf("the jail stopped when its first session was hung up (%d containers)", n)
	}
	select {
	case err := <-attach.done:
		t.Fatalf("the attached session ended (%v) with the first:\n%s", err, attach.combined())
	default:
	}
	writeRelease(t, dir, release)
	if rc := attach.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the attached session ended rc %d:\n%s", rc, attach.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the last session quit", n, cname)
	}
}
