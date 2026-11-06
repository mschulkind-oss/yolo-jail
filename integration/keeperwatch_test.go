package integration

// keeperwatch_test.go is the end-to-end pin on the keeper's record of a host service that goes down
// while its jail is up (docs/design/jail-lifetime-last-session-wins.md JL-D19, JL-D71): a host
// daemon's supervisor restarts it, bounded (docs/design/host-daemon-ownership.md HD-D25), and a death
// that stands is said to the session that was in when the service went, at its quit, and to the
// next arrival, as it enters. The unit tier drives a keeper in-process against a fake runtime
// (internal/cli/run/keeperwatch_test.go, hostdaemonsupervise_test.go); this one kills a real
// keeper's real daemon.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// childrenMatching is every child of parent whose command line holds each of parts. A child of
// this test's own keeper, never any process that matches: another run of this test in the same
// jail has a daemon of the same name. The children and their command lines are the harness's
// process probes' (procinfo_test.go).
func childrenMatching(parent int, parts ...string) []int {
	var pids []int
	for _, pid := range processChildren(parent) {
		args, err := processArgs(pid)
		if err != nil {
			continue
		}
		cmdline := strings.Join(args, " ")
		all := true
		for _, p := range parts {
			all = all && strings.Contains(cmdline, p)
		}
		if all {
			pids = append(pids, pid)
		}
	}
	return pids
}

// TestAHostServiceThatGoesDownIsToldToItsSessionsAndTheNextArrival: a jail's keeper runs a host
// daemon the user config declares; the daemon is killed while a session is in; the keeper's log
// records it, the next arrival is told as it enters, and the session that was in is told as it quits.
func TestAHostServiceThatGoesDownIsToldToItsSessionsAndTheNextArrival(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" {
		t.Skip("has run only on Linux: the keeper's record of a host service that goes down is " +
			"unmeasured on either Mac backend (docs/design/jail-lifetime-last-session-wins.md JL-D71)")
	}
	socat, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("the stand-in host daemon is socat, which is not on this host's PATH")
	}
	const service = "kw-down-probe"
	// Through `sh -c`, with the socket as its own argument: the placement rule reads an argument
	// with a slash and no shell character as a path relative to the workspace, and refuses
	// socat's "UNIX-LISTEN:<socket>,fork" there. ONCE: every restart the supervisor makes exits at
	// its start, so after the kill the supervision gives up (HD-D25, about 31 s at the production
	// backoff) and the death stands, which is what this test records.
	packHome(t, oncePerJailSocat(t, service, socat))
	dir := writeProject(t, `{}`)
	cname := jailName(dir)

	const release = "release-first"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.2; done; echo FIRST-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)
	if !strings.Contains(first.combined(), "the "+service+" service") {
		t.Fatalf("the keeper line does not name the stand-in daemon, so the keeper never started it:\n%s", first.combined())
	}
	keeper := keeperPID(t, dir)

	// THE DAEMON GOES DOWN, by a kill from outside, as an OOM kill or a crash would end it.
	logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
	daemons := childrenMatching(keeper, "UNIX-LISTEN:", service)
	if len(daemons) == 0 {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("no %s daemon is running under the keeper; the launch:\n%s\nthe keeper's log:\n%s",
			service, first.combined(), raw)
	}
	for _, pid := range daemons {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	want := "host service '" + service + "' went down"
	deadline := time.Now().Add(jailTimeout())
	for {
		raw, _ := os.ReadFile(logPath)
		if strings.Contains(string(raw), want) {
			if !strings.Contains(string(raw), "signal: killed") {
				t.Errorf("the keeper's record does not say how the daemon ended:\n%s", raw)
			}
			if !strings.Contains(string(raw), "restart 5 of 5") || !strings.Contains(string(raw), "giving up") {
				t.Errorf("the keeper's log does not show the supervisor's restarts and its give-up:\n%s", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keeper never recorded the daemon's death in %s:\n%s", logPath, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// THE NEXT ARRIVAL is told as it enters, and enters.
	arrival := runYolo(t, dir, "echo ENTERED-$((40+2))")
	if arrival.rc != 0 || !strings.Contains(arrival.stdout, "ENTERED-42") {
		t.Fatalf("an arrival beside the session failed: rc %d\n%s", arrival.rc, arrival.combined())
	}
	if !strings.Contains(arrival.stderr, "Host service '"+service+"' of this jail has been down since") ||
		!strings.Contains(arrival.stderr, "Nothing restarts it now") {
		t.Errorf("the arrival was not told the service is down:\n%s", arrival.combined())
	}

	// THE SESSION THAT WAS IN is told as it quits: it was the last, so its quit streams the
	// keeper's log from the moment it began, the death included.
	writeRelease(t, dir, release)
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the session ended rc %d:\n%s", rc, first.combined())
	}
	if !strings.Contains(first.combined(), want) {
		t.Errorf("the session that was in when the daemon died was not told at its quit:\n%s", first.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the last session quit", n, cname)
	}
}

// oncePerJailSocat is a loophole config whose daemon is socat on the socket, started ONCE: a later
// start (a supervisor's restart) finds the marker its first start left in a directory of the test's
// own and exits 3 at once, so a kill from outside becomes a death that stands after HD-D25's five
// restarts. The marker's path is inside the script, which the placement rule leaves alone, never an
// argument of its own, which it would read as a workspace path.
func oncePerJailSocat(t *testing.T, service, socat string) string {
	marker := filepath.Join(t.TempDir(), "started-once")
	return `{"loopholes": {"` + service + `": {"command": ["sh", "-c", "if [ -e '` + marker +
		`' ]; then exit 3; fi; : > '` + marker + `'; exec ` +
		socat + ` UNIX-LISTEN:\"$0\",fork EXEC:cat", "{socket}"]}}}`
}

// TestAKilledHostDaemonIsRestartedByItsKeeper: a jail's keeper runs a host daemon the user config
// declares; the daemon is killed while a session is in; its supervisor (the keeper's own process,
// docs/design/host-daemon-ownership.md OQ-HD5, HD-D25) restarts it with the same argv, says so in its
// log, and records nothing down, so the next arrival is told nothing.
func TestAKilledHostDaemonIsRestartedByItsKeeper(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" {
		t.Skip("has run only on Linux: the host daemon supervisor's restart is unmeasured on either " +
			"Mac backend")
	}
	socat, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("the stand-in host daemon is socat, which is not on this host's PATH")
	}
	const service = "kw-restart-probe"
	packHome(t, `{"loopholes": {"`+service+`": {"command": ["sh", "-c", "exec `+socat+
		` UNIX-LISTEN:\"$0\",fork EXEC:cat", "{socket}"]}}}`)
	dir := writeProject(t, `{}`)
	cname := jailName(dir)

	const release = "release-first"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.2; done; echo FIRST-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)
	keeper := keeperPID(t, dir)
	logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
	before := childrenMatching(keeper, "UNIX-LISTEN:", service)
	if len(before) == 0 {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("no %s daemon is running under the keeper; the launch:\n%s\nthe keeper's log:\n%s",
			service, first.combined(), raw)
	}
	for _, pid := range before {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	restarted := "host service '" + service + "' restarted (pid"
	deadline := time.Now().Add(jailTimeout())
	for {
		raw, _ := os.ReadFile(logPath)
		if strings.Contains(string(raw), restarted) {
			if !strings.Contains(string(raw), "host service '"+service+"' died mid-session (its process ended (signal: killed))") {
				t.Errorf("the keeper's log does not report the death before the restart:\n%s", raw)
			}
			if strings.Contains(string(raw), "went down") {
				t.Errorf("a restarted daemon was recorded down:\n%s", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keeper never restarted the killed daemon; its log %s:\n%s", logPath, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	after := childrenMatching(keeper, "UNIX-LISTEN:", service)
	if len(after) == 0 {
		t.Errorf("no %s daemon runs under the keeper after its restart", service)
	}
	for _, pid := range after {
		for _, old := range before {
			if pid == old {
				t.Errorf("the daemon after the restart is the killed pid %d", pid)
			}
		}
	}
	hostLog, _ := os.ReadFile(filepath.Join(paths.GlobalStorage(), "logs", "host-service-"+service+".log"))
	if !strings.Contains(string(hostLog), restarted) {
		t.Errorf("the daemon's own host log does not record the restart:\n%s", hostLog)
	}

	arrival := runYolo(t, dir, "echo ENTERED-$((40+2))")
	if arrival.rc != 0 || !strings.Contains(arrival.stdout, "ENTERED-42") {
		t.Fatalf("an arrival beside the session failed: rc %d\n%s", arrival.rc, arrival.combined())
	}
	if strings.Contains(arrival.stderr, "has been down since") {
		t.Errorf("an arrival was told a restarted daemon is down:\n%s", arrival.combined())
	}
	writeRelease(t, dir, release)
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the session ended rc %d:\n%s", rc, first.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the last session quit", n, cname)
	}
}
