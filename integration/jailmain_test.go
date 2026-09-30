package integration

// jailmain_test.go is the end-to-end pin on step 2 of
// docs/design/jail-lifetime-last-session-wins.md: a container jail's main process is a HOLD,
// every session enters by exec (the first included), provisioning runs once on the first
// session and records its outcome, and the host-side session lock keeps an orphan sweep from
// stopping a jail with a session in it. The unit tiers pin each piece's call site by reading
// the source (internal/entrypoint's TestMainWiresTheHoldAndTheGateInOrder, internal/cli/run's
// TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec); only a real container proves
// the shape the pieces make.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// jailShapeProbe prints what a session can see of the jail's shape. The hold's flag is built
// at run time, so this script's own command line (which carries the probe's text) never
// matches it.
const jailShapeProbe = `pat="--yolo-""hold-main"; ` +
	`for p in /proc/[0-9]*; do c=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); ` +
	`case "$c" in *"$pat"*) echo "HOLD-PPID=$(awk '{print $4}' "$p/stat")";; esac; done; ` +
	`echo "SESSION-PPID=$PPID"; ` +
	`echo "MAIN=$YOLO_JAIL_MAIN"; ` +
	`echo "BOOT=$(cat /run/yolo/main/boot)"; ` +
	`echo "OUTCOME=$(cat /run/yolo/main/provision.outcome)"`

// TestTheMainProcessIsAHoldAndTheFirstSessionAnExec: in a fresh launch, the container's pid 1
// (the runtime's init) has the entrypoint's hold as its child, the session itself was exec'd
// into the container (its parent is outside the pid namespace, so bash reads $PPID as 0), pid
// 1's boot is done and provisioning recorded "done" before the command ran. The jail still ends
// when its only session does: nothing of it is left once the launch returns, since that launch
// was the last session and waited for its keeper's teardown, and the command's status is the
// launch's.
func TestTheMainProcessIsAHoldAndTheFirstSessionAnExec(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	r := runYolo(t, dir, jailShapeProbe)
	if r.rc != 0 {
		t.Fatalf("rc %d\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"HOLD-PPID=1\n", "SESSION-PPID=0\n", "MAIN=hold\n", "BOOT=ready\n", "OUTCOME=done\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the session did not see %q:\n%s", strings.TrimSpace(want), r.combined())
		}
	}
	if strings.Contains(r.combined(), "yolo-entrypoint: boot done") {
		t.Errorf("the main process's ready line reached the terminal; the launcher must swallow it:\n%s",
			r.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s are still running after the first session returned", n, cname)
	}

	status := runYoloDirect(t, dir, "sh", "-c", "exit 7")
	if status.rc != 7 {
		t.Errorf("the launch returned %d for a command that exited 7:\n%s", status.rc, status.combined())
	}
}

// TestAnOrphanSweepSparesAJailWithASessionInIt is §2.3 item 4, with the keeper: the first launcher
// is SIGKILLed while a second terminal is attached. That is one session ending, however it ended:
// the jail's keeper owns the jail, so the next launch in ANOTHER workspace, sweeping orphans, finds
// its owner alive and leaves it; an entry attaches as to any running jail; and once the attached
// session leaves, the last one, the keeper ends the jail, with no sweep needed.
func TestAnOrphanSweepSparesAJailWithASessionInIt(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)
	other := writeProject(t, `{}`)

	const releaseFirst, releaseAttach = "release-first", "release-attach"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+releaseFirst+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { writeRelease(t, dir, releaseFirst) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)
	keeper := keeperPID(t, dir)

	attach := startYoloBackground(t, "attach", dir,
		`echo "ATTACH-SAW=$(cat /run/yolo/main/provision.outcome)"; echo ATTACH-IN-$((40+2)); `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseAttach+` ] && break; sleep 0.5; done; echo ATTACH-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, releaseAttach) })
	awaitOutput(t, attach, regexp.MustCompile(`ATTACH-IN-42`))
	if !strings.Contains(attach.combined(), "Attaching to existing jail") {
		t.Fatalf("the second launch did not attach:\n%s", attach.combined())
	}
	if !strings.Contains(attach.combined(), "ATTACH-SAW=done") {
		t.Errorf("the attach entered before provisioning's outcome was recorded:\n%s", attach.combined())
	}

	// The first launcher dies with no teardown at all.
	if err := syscall.Kill(first.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("killing the first launcher: %v", err)
	}
	if !awaitProcessGone(first.pid, 30*time.Second) {
		t.Fatalf("the SIGKILLed first launcher (pid %d) was never reaped", first.pid)
	}

	sweep := runYolo(t, other, "true")
	if sweep.rc != 0 {
		t.Fatalf("the sweeping launch failed: rc %d\n%s", sweep.rc, sweep.combined())
	}
	if strings.Contains(sweep.combined(), "Reaping orphaned jail "+cname) {
		t.Errorf("the sweep reaped a jail whose keeper is alive:\n%s", sweep.combined())
	}
	if n := runningContainers(t, cname); n != 1 {
		t.Fatalf("the jail with a session in it is not running after the sweep (%d containers)", n)
	}

	// An entry into the kept jail is an ordinary attach: its host services are its keeper's.
	entry := runYolo(t, dir, "echo ENTERED-$((40+2))")
	if entry.rc != 0 || !strings.Contains(entry.stdout, "ENTERED-42") {
		t.Fatalf("an entry into the kept jail failed: rc %d\n%s", entry.rc, entry.combined())
	}
	if strings.Contains(entry.stderr, "is gone") {
		t.Errorf("the entry was told a live keeper's jail lost its owner:\n%s", entry.combined())
	}

	writeRelease(t, dir, releaseAttach)
	if rc := attach.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the attached session ended rc %d:\n%s", rc, attach.combined())
	}
	if !strings.Contains(attach.combined(), "ATTACH-OUT-42") {
		t.Errorf("the attached session did not finish its command:\n%s", attach.combined())
	}
	// The last session left: its keeper ended the jail, and itself.
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the last session left", n, cname)
	}
	if !awaitProcessGone(keeper, 10*time.Second) {
		t.Errorf("the keeper (pid %d) outlived its jail", keeper)
	}
}

// writeRelease writes a release file a background session's script is polling for.
func writeRelease(t *testing.T, dir, name string) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, name), []byte("go\n"), 0o644)
}

// awaitOutput waits for re in a background run's output, failing if the run exits first.
func awaitOutput(t *testing.T, r *bgRun, re *regexp.Regexp) {
	t.Helper()
	deadline := time.Now().Add(jailTimeout())
	for !re.MatchString(r.combined()) {
		select {
		case err := <-r.done:
			t.Fatalf("%s exited (%v) before printing %s:\n%s", r.name, err, re, r.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not print %s within %s:\n%s", r.name, re, jailTimeout(), r.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
