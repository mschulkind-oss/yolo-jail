package run

// keepervanish.go is a jail whose CONTAINER THE RUNTIME LOST under its keeper, and why.
//
// THE INCIDENT (2026-11-09). A second integration suite ran in the same jail as a release gate's:
// podman's state directory, /run/libpod, was recreated, the next podman command found its "alive"
// file missing and ran its refresh, the refresh forgot the gate's --rm containers, and a jail
// vanished under its keeper. The keeper read only a `podman wait` that ANSWERED an exit status,
// and a container podman's database no longer has gives a wait no status to answer (read from
// podman's wait, which reads the container's state from that database; the refresh itself was not
// reproduced, since it resets every jail of the store), so it went on holding a jail that was
// gone, and the session whose exec then failed with 255 was told "nothing recorded why".
//
// WHAT THE KEEPER DOES NOW. A `podman wait` that returns without an exit status is the event, and
// the tri-state existence probe confirms it: a runtime that answers that no container of the name
// exists, running or stopped, has lost the jail, and the keeper ends as it does when the container
// exits, recording why first. A wait that fails while the container is still listed, or a probe that
// cannot say, ends nothing (JL-P3: "could not ask" is never "gone"); the client's exit is then the
// keeper's one remaining observation, as it always was on Apple Container.
//
// WHY THE WAIT AND NOT `podman events`. A refresh recreates the directory podman's file event log
// lives in (/run/libpod/events for root), so a `podman events` stream started before it tails a
// file that is gone and never sees the removal; `podman wait` reads the database, which the refresh
// rewrote. No timer: the wait is the event, and the one probe after it is the confirmation.
//
// WHY IT ENDED, WHEN NOTHING SAID. A container end the keeper did not cause and no one recorded
// (no stop record written since the jail started) gets one now: the refresh, when podman's event log
// has one since the jail started, else the plain fact that the runtime no longer lists the
// container. A session whose jail ended with no record asks the same event log (endSession), so a
// keeper that died first, or a record that came too late, still leaves the session told.
//
// PARITY. Apple Container has no wait verb and no refresh: its API server holds container state
// and keeps no such reset, so its keeper learns of an end from the client's exit and the running
// probe (watchContainer), and a removal reaches it that way, with no reason claimed, since a client's
// exit cannot tell a removal from an end. macos-user runs no container at all (holdKey): its keeper
// ends on its sessions and signals, and there is nothing for a runtime to lose.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// containerVanishedReason completes "This session ended because its jail stopped: …" for a jail
// whose container the runtime lost with no refresh in its event log to say how.
const containerVanishedReason = "the container runtime no longer lists this jail's container, " +
	"and nothing recorded what removed it. Start it again: run `yolo` from this workspace"

// podmanRefreshReason is why a jail ended when podman reset its state at at: removed when the keeper
// saw the runtime lose the container, and otherwise the reset as the one recorded event its end
// followed.
func podmanRefreshReason(at time.Time, removed bool) string {
	what := "and that removed this jail's container"
	if !removed {
		what = "which removes a running jail's container, and nothing else recorded a stop"
	}
	return fmt.Sprintf("podman reset its state at %s (another podman command recreated its runtime "+
		"state directory, /run/libpod for root), %s. Start it again: run `yolo` from this workspace",
		at.Local().Format("15:04:05"), what)
}

// podmanRefreshEventsTimeout bounds the event-log read: it reads a file and exits.
const podmanRefreshEventsTimeout = 5 * time.Second

// podmanRefreshSince is the latest podman refresh at or after since, from podman's event log.
// ok is false when there was none, the runtime is not podman (no other has a refresh), or the log
// could not be read: nothing is claimed then.
func (o *Options) podmanRefreshSince(rt string, since time.Time) (time.Time, bool) {
	if rt != "podman" { // parity: HonoredBy — Apple Container keeps its state in its API server, which has no refresh; macos-user has no container runtime
		return time.Time{}, false
	}
	res := o.Exec([]string{"podman", "events", "--stream=false", "--since", strconv.FormatInt(since.Unix(), 10),
		"--filter", "type=system", "--filter", "event=refresh", "--format", "json"}, "", nil, podmanRefreshEventsTimeout)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return time.Time{}, false
	}
	var latest time.Time
	for _, line := range strings.Split(res.Stdout, "\n") {
		var ev struct {
			Status   string `json:"Status"`
			Time     int64  `json:"time"`
			TimeNano int64  `json:"timeNano"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &ev) != nil || ev.Status != "refresh" {
			continue
		}
		at := time.Unix(ev.Time, 0)
		if ev.TimeNano != 0 {
			at = time.Unix(0, ev.TimeNano)
		}
		if at.Before(since.Truncate(time.Second)) || !at.After(latest) {
			continue
		}
		latest = at
	}
	return latest, !latest.IsZero()
}

// awaitPodmanWait is the keeper's `podman wait`: done once it answers an exit status, or once it
// fails and the runtime then answers that no container of the name exists, the jail lost under its
// keeper (markVanished first, so the ended arm reads it). A wait that fails with the container
// still listed, or a runtime that cannot say, ends nothing, and the keeper says it now has only its
// client's exit to go on.
func (k *keeper) awaitPodmanWait(done func()) {
	o, p := k.o, k.plan
	res := o.Exec([]string{p.Runtime, "wait", p.Cname}, "", nil, 0)
	if res.Ran && !res.Timeout && res.RC == 0 && strings.TrimSpace(res.Stdout) != "" {
		done()
		return
	}
	if !res.Ran {
		return
	}
	select {
	case <-k.ending:
		return
	default:
	}
	if id, known := o.probeExistingContainer(p.Cname, p.Runtime, trackingProbeTimeout); known && id == "" {
		k.vanished.Store(true)
		done()
		return
	}
	k.sink.logf("keeper: `%s wait %s` ended without the container's exit status (%s) while the runtime still lists it, or could not say; the keeper now learns of its end only from its runtime client's exit",
		p.Runtime, p.Cname, waitFailure(res))
}

// waitFailure words a wait that gave no exit status, for the keeper's log.
func waitFailure(res ExecResult) string {
	if msg := strings.TrimSpace(res.Stderr); msg != "" {
		return msg
	}
	return "exit status " + strconv.Itoa(res.RC)
}

// explainUnrecordedEnd is the keeper's record of why its container ended, when it ended on its own
// and nothing recorded a stop since the jail started: a podman refresh in the event log, or the
// runtime having lost the container. Written before the teardown, so a session whose exec the end
// returned, which waits a moment for a record (whyTheJailEnded), finds it. Nothing is written when
// neither is known: the session then says nothing recorded why, which is true.
func (k *keeper) explainUnrecordedEnd() {
	o, p := k.o, k.plan
	k.recMu.Lock()
	since := k.record.Started
	k.recMu.Unlock()
	if since.IsZero() {
		since = k.startedAt
	}
	if rec, ok := readJailStop(p.Cname); ok && !rec.At.Before(since) {
		return
	}
	vanished := k.vanished.Load()
	reason := ""
	if at, ok := o.podmanRefreshSince(p.Runtime, since); ok {
		reason = podmanRefreshReason(at, vanished)
	} else if vanished {
		reason = containerVanishedReason
	}
	if reason == "" {
		return
	}
	o.recordJailStop(p.Cname, reason)
	k.sink.logf("keeper: %s ended with no recorded stop: %s", p.Cname, reason)
}

// unrecordedEndReason is a session's reason for a jail that ended with no record: a podman refresh
// since the session began, or "" when there was none to name.
func (o *Options) unrecordedEndReason(rt string, since time.Time) string {
	at, ok := o.podmanRefreshSince(rt, since)
	if !ok {
		return ""
	}
	return podmanRefreshReason(at, false)
}
