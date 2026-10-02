package run

// keeperarrival_test.go pins what an ARRIVAL hears of its keeper's record of host services that
// went down (keeperwatch.go, docs/design/jail-lifetime-last-session-wins.md JL-D19): a death the
// keeper records at the moment a session arrives reaches that session, through its arrival notice
// (noteServicesDown, from the start record) or its quit (printKeeperRecords, from the keeper's log),
// whatever falls between the two reads.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// arrivalHook is a session's stderr that runs then once, right after the first write that brings
// marker into what it holds: a moment the test lands an event at, in the middle of the code under
// test, without a seam of its own.
type arrivalHook struct {
	bytes.Buffer
	marker string
	then   func()
	fired  bool
}

func (h *arrivalHook) Write(p []byte) (int, error) {
	n, err := h.Buffer.Write(p)
	if !h.fired && strings.Contains(h.String(), h.marker) {
		h.fired = true
		h.then()
	}
	return n, err
}

// TestADeathAtAnArrivalReachesTheArrivingSession is the gap between an arrival's two reads of its
// keeper's record of deaths, at attachExisting's own call sites. The arrival reads the start record
// for the deaths before it came (its notice), and takes the keeper log's length for the deaths
// while it is in (its quit). The keeper writes a death's record before its line (recordServiceDown),
// so a log length taken BEFORE the record is read misses nothing: a death whose line is before it
// is in the record by then. Taken after the read, a death recorded between the two is in neither.
//
// The keeper here is the real writer, recording one death before the session arrives and a second
// the moment the arrival has read the record: its notice of the first is printed from what it read.
// The first is named once, by the notice; the second once, by the quit. Deleting the notice, or
// taking the log length after the notice again, fails it.
func TestADeathAtAnArrivalReachesTheArrivingSession(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	o, cfg, channel, _ := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(), nil)
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	// Another session in the jail, so this one's quit leaves it up and prints what the keeper
	// logged while it was in.
	other, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer other.release()

	logFile, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	k := &keeper{plan: &keeperPlan{Cname: cname, Runtime: "podman"}, ending: make(chan struct{}),
		record: keeperRecord{PID: 4242, Started: time.Now()}, recorded: true,
		sink: &keeperSink{log: logFile}}
	if err := writeKeeperRecord(cname, k.record); err != nil {
		t.Fatal(err)
	}
	dies := func(name string) {
		ended := make(chan struct{})
		close(ended)
		k.awaitServiceEnd("host service '"+name+"'", serviceEnd{done: ended,
			how: func() string { return "its process ended (exit status 3)" }}, "")
	}
	dies("before")

	stderr := &arrivalHook{marker: "Host service 'before' of this jail has been down since",
		then: func() { dies("during") }}
	o.Stderr = stderr
	exitWith(t, "0")
	if rc, _ := o.attachExisting(cname, "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: zaiSelected(t)}, channel, false, nil); rc != 0 {
		t.Fatalf("rc %d:\n%s", rc, stderr.String())
	}
	out := stderr.String()
	if !stderr.fired {
		t.Fatalf("the arrival never named the death recorded before it came:\n%s", out)
	}
	if !strings.Contains(out, "stays up for") {
		t.Fatalf("the session's quit did not leave the jail up for its other session, so it printed "+
			"nothing the keeper logged:\n%s", out)
	}
	if n := strings.Count(out, "service 'before'"); n != 1 {
		t.Errorf("the death before the arrival was named %d times, want once (its notice):\n%s", n, out)
	}
	if n := strings.Count(out, "service 'during'"); n != 1 {
		t.Errorf("the death the keeper recorded as the session arrived was named %d times, want once: "+
			"the arrival read the record before it, and took the log's length after its line:\n%s", n, out)
	}
}
