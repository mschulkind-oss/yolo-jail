package run

// keeperwatch.go is the KEEPER'S RECORD OF A SERVICE THAT GOES DOWN
// (docs/design/jail-lifetime-last-session-wins.md JL-D19, JL-D71): the half of JL-D19 the keeper's
// first build left out, which the design words as "each session's quit prints what the keeper
// recorded while that session was in, and an arrival prints any service the keeper recorded as
// down".
//
// The keeper restarts nothing (JL-D18): a host service that ends while its jail is up stays down,
// and every in-jail client of it fails from then on. Before this, nothing said so: the end of a
// spawned daemon was reaped and dropped, a forward's socat was not reaped at all until the
// teardown, and the sessions that met the failure had no way to tell a dead service from a broken
// agent. So the keeper watches the end of each service it runs, and an end it did not cause is
// recorded twice:
//
//   - in its log, one line, which reaches every terminal its output reaches: before ready the
//     launch's, through the progress pipe; after it launch.log, through the mirror, and each
//     session's quit, which prints what the keeper logged while that session was in
//     (printKeeperRecords);
//   - in its start record (keeperstate.go), whose Down list an arrival reads and prints
//     (noteServicesDown), since an arrival was not in when the service died.
//
// AN END THE KEEPER CAUSED IS NOT A DEATH. Its teardown stops every service it runs, and each of
// those ends closes the same channel a death does. So the keeper marks the moment it begins to end
// its jail (beginStopping), under the lock every record takes, and nothing seen after it is
// recorded.
//
// WHAT IT WATCHES is what it runs: a spawned daemon's process, a front it serves (a fronted daemon's
// and a host-wide daemon's), the in-process cgroup delegate's accept loop, each forward's socat,
// and at macos-user each doorway and launch-owned service it holds, once its supervision gives up.
// Not a host-wide daemon itself: that daemon is the machine's, serves other jails, and is no
// keeper's child, so no keeper can reap or see it end. Nor a spawned daemon's command that exits 0
// with its service still reachable (startExternalService): that is the daemonizing wrapper the
// readiness wait accepts, whose service lives on in a child no keeper can see end.

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// serviceEnd is how a host service is seen to end: done closes once it has ended, by its own fault
// or by its stop, and how then words how ("its process ended (exit status 3)"). A zero serviceEnd
// watches nothing.
type serviceEnd struct {
	done <-chan struct{}
	how  func() string
}

// exitPhrase words a reaped process's end, once its Wait has returned.
func exitPhrase(cmd *exec.Cmd) string {
	if cmd == nil || cmd.ProcessState == nil {
		return "ended"
	}
	return "ended (" + cmd.ProcessState.String() + ")"
}

// frontEnd is a front's end: its serve loop returned (frontRun's done), with the error it returned,
// when there was one, from its failed channel.
func frontEnd(done <-chan struct{}, failed <-chan error) serviceEnd {
	return serviceEnd{done: done, how: func() string {
		select {
		case err := <-failed:
			if err != nil {
				return "its front stopped serving (" + err.Error() + ")"
			}
		default:
		}
		return "its front stopped serving"
	}}
}

// firstEnd is whichever of a and b ends first: a fronted daemon is gone for its clients when either
// its process or its front is.
func firstEnd(a, b serviceEnd) serviceEnd {
	done := make(chan struct{})
	var how func() string
	go func() {
		select {
		case <-a.done:
			how = a.how
		case <-b.done:
			how = b.how
		}
		close(done)
	}()
	return serviceEnd{done: done, how: func() string { return how() }}
}

// keeperServiceDown is one service the keeper saw end while its jail was up.
type keeperServiceDown struct {
	// What names the service, as a sentence's subject: "host service 'x'", or the port forward.
	What string    `json:"what"`
	At   time.Time `json:"at"`
	How  string    `json:"how"`
	Log  string    `json:"log,omitempty"`
}

// beginStopping marks the moment the keeper begins to end its jail: every service end seen after it
// is the keeper's own act, never a record.
func (k *keeper) beginStopping() {
	k.recMu.Lock()
	defer k.recMu.Unlock()
	k.stopping = true
}

// watchServices starts one watch per service this keeper runs (awaitServiceEnd). Called once they
// have all started, before the container, so a service that dies during the boot is recorded too:
// its line then crosses the progress pipe to the launch's terminal.
func (k *keeper) watchServices() {
	for _, h := range k.handles {
		if h.end.done == nil {
			continue
		}
		go k.awaitServiceEnd("host service '"+h.name+"'", h.end, h.log)
	}
	for _, fp := range k.socat {
		what := fmt.Sprintf("the port forward from jail port %d to host port %d", fp.forward.LocalPort, fp.forward.HostPort)
		go k.awaitServiceEnd(what, fp.end(), fp.log)
	}
	// A DOORWAY OR LAUNCH-OWNED SERVICE a keeper at macos-user holds is gone for its clients once its
	// supervision gives up on it, which is when its Running is done (launchservice.Running.Done): its
	// restart policy left it down, or a restart never came back. Each death before that is a line its
	// supervision already logged, and a restart that came back is no record. A start a test stands in
	// for that has no Done is not watched.
	for _, l := range k.launched {
		d, ok := l.r.(interface{ Done() <-chan struct{} })
		if !ok {
			continue
		}
		go k.awaitServiceEnd(l.what, serviceEnd{done: d.Done(), how: func() string {
			return "its supervision gave up on it (its log says why)"
		}}, l.log)
	}
}

// awaitServiceEnd waits for one service's end, and records it unless the keeper caused it or has
// already ended.
func (k *keeper) awaitServiceEnd(what string, end serviceEnd, log string) {
	select {
	case <-end.done:
	case <-k.ending:
		return
	}
	k.recordServiceDown(keeperServiceDown{What: what, At: time.Now(), How: end.how(), Log: log})
}

// recordServiceDown is one end the keeper did not cause: its start record rewritten with it, then
// its log line, both under recMu, so beginStopping never falls between a death and its record.
//
// THE RECORD COMES FIRST, so a death's line in the log means its record is already written: a
// reader that saw the line and then reads the record finds the death there. Written the other way
// round, a reader between the two writes saw the line but a record without the death.
func (k *keeper) recordServiceDown(d keeperServiceDown) {
	k.recMu.Lock()
	defer k.recMu.Unlock()
	if k.stopping {
		return
	}
	var recErr error
	if k.recorded {
		k.record.Down = append(k.record.Down, d)
		recErr = writeKeeperRecord(k.stateKey(), k.record)
	}
	where := ""
	if d.Log != "" {
		where = "; its log: " + d.Log
	}
	users, again := "what in the jail uses it", "until the jail is launched again"
	if k.plan.Notch != "" {
		users, again = "what in this workspace's sandboxes uses it", "until its macos-user sessions are launched again"
	}
	k.sink.logf("keeper: %s went down at %s: %s. Nothing restarts it: %s fails %s (%s, then a launch)%s",
		d.What, d.At.Format("15:04:05"), d.How, users, again, stopRemedy(k.plan.Runtime, k.plan.Cname), where)
	if recErr != nil {
		k.sink.logf("keeper: could not add that to its start record (%v), so an arrival will not be told", recErr)
	}
}

// noteServicesDown is an arrival's account of what its jail's keeper recorded down (JL-D19): one
// line per service, since when, how, what starts it again and where its log is. An arrival was not
// in when the service died, so the keeper's log line never reached it. key is the jail's name, or
// a macos-user key (keeperKey), whose record says so.
func (o *Options) noteServicesDown(key, rt string) {
	rec, ok := readKeeperRecord(key)
	if !ok {
		return
	}
	of, users := "this jail", "what in the jail uses it"
	if rec.Notch != "" {
		of, users = "this workspace's macos-user host services", "what in the sandboxes uses it"
	}
	now := o.Now()
	for _, d := range rec.Down {
		when := d.At.Format("15:04:05")
		if y, m, day := d.At.Date(); y != now.Year() || m != now.Month() || day != now.Day() {
			when = d.At.Format("Jan 2 15:04:05")
		}
		where := ""
		if d.Log != "" {
			where = " Its log: " + richtext.Escape(d.Log) + "."
		}
		o.pr(o.Stderr).printf("[yellow]%s of %s has been down since %s: %s. Its keeper restarts "+
			"nothing, so %s fails; %s, then a launch, starts it again.%s[/yellow]",
			richtext.Escape(capitalize(d.What)), of, when, richtext.Escape(d.How), users, stopRemedy(rt, key), where)
	}
}

// capitalize upper-cases a sentence subject's first ASCII letter.
func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
