package integration

// keeperwatch_test.go is the end-to-end pin on the keeper's record of a host service that goes down
// while its jail is up (docs/design/jail-lifetime-last-session-wins.md JL-D19, JL-D71): the keeper
// restarts nothing, so it says so, to the session that was in when the service went, at its quit, and
// to the next arrival, as it enters. The unit tier drives a keeper in-process against a fake runtime
// (internal/cli/run/keeperwatch_test.go); this one kills a real keeper's real daemon.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// childrenMatching is every child of parent whose command line holds each of parts. A child of
// this test's own keeper, never any process that matches: another run of this test in the same
// jail has a daemon of the same name.
func childrenMatching(parent int, parts ...string) []int {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		// The parent is the field after the state, which follows the last ')': comm may hold
		// spaces and parentheses of its own.
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		after := string(stat)
		if i := strings.LastIndexByte(after, ')'); i >= 0 {
			after = after[i+1:]
		}
		if f := strings.Fields(after); len(f) < 2 || f[1] != strconv.Itoa(parent) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmdline := strings.ReplaceAll(string(raw), "\x00", " ")
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
		t.Skip("finds the keeper and its daemon through /proc, which only Linux has")
	}
	socat, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("the stand-in host daemon is socat, which is not on this host's PATH")
	}
	const service = "kw-down-probe"
	// Through `sh -c`, with the socket as its own argument: the placement rule reads an argument
	// with a slash and no shell character as a path relative to the workspace, and refuses
	// socat's "UNIX-LISTEN:<socket>,fork" there.
	packHome(t, `{"loopholes": {"`+service+`": {"command": ["sh", "-c", "exec `+socat+
		` UNIX-LISTEN:\"$0\",fork EXEC:cat", "{socket}"]}}}`)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

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
		!strings.Contains(arrival.stderr, "restarts nothing") {
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
