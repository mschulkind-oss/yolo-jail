package run

// launchguard.go is a container launch's LAUNCH GUARD, a term coined here: the signal arm that
// covers the launch from its pack staging until an arm of its own takes over, the keeper's after
// its spawn or an attach's before its exec (docs/design/jail-lifetime-last-session-wins.md JL-D75).
//
// Before it nothing caught a signal ahead of the keeper's spawn. A Ctrl-C while the image built, a
// prompt waited or the launch lock was awaited took the default action: the process ended there,
// no deferred cleanup ran, and the launch's pack tree stayed under AGENTS_DIR/<cname>/pack-trees
// (a nested jail showed three), with the terminal still wearing the jail's colors.
//
// WHAT IT REMOVES is what the launch made that no container can be using: the pack tree it staged,
// the home skeleton it built and, once it has written them, the live-tree record and the tracking
// file. A tree goes only once its container is known gone (packtree.go); here that is known by
// construction, because only the keeper starts this launch's container and none was spawned. The
// tracking file still goes only on the runtime's own answer (forgetGone), as at every other end.
//
// THE HAND-OFF. From its spawn on, what the launch made is the keeper's, which unwinds it once the
// lifeline closes (keeper.go), so the guard never discards after a spawn: a signal that lands
// during the spawn waits for it, then ends the keeper's jail as the keeper's own arm would
// (keeperPreReadyTeardown), and a spawn the guard's teardown began ahead of is never made
// (startKeeper's errLaunchEnded). The arm installed after the spawn, or the attach's, then takes
// over and the guard is retired, with no gap between them: only the innermost arm acts on a signal
// (armstack.go), so the two are never both acting and never both absent.

import (
	"errors"
	"sync"
)

// errLaunchEnded is a keeper spawn refused because a signal's teardown has begun ending the launch.
var errLaunchEnded = errors.New("this launch was ended by a signal before its keeper started")

// launchGuard is one launch's guard: its arm, and what the launch has made that it would discard.
type launchGuard struct {
	arm       *launchSignalArm
	cname, rt string

	mu sync.Mutex
	unspawned
	// kp is the keeper, once spawned: from then on everything unspawned names is its own.
	kp *keeperProcess
	// ended says a signal's teardown has begun: no keeper is spawned after it.
	ended bool
}

// unspawned is what a launch has made before its keeper's spawn and no container holds.
type unspawned struct {
	tree     string // the pack tree it staged (Options.packTree)
	skeleton string // the home skeleton it built, once built
	recorded bool   // the tracking file and the live-tree record name this launch's jail
}

// armLaunchGuard installs this launch's guard over the pack tree it has just staged. A container
// launch only: macos-user's sandbox runs the host daemons it starts from that tree, and has no
// keeper to hand it to. Its exit writes the launch's machine-wide line first (launchrecord.go),
// since a signal it takes ends the process before Run's deferred record could.
func (o *Options) armLaunchGuard(cname, rt string) {
	g := &launchGuard{cname: cname, rt: rt, unspawned: unspawned{tree: o.packTree}}
	g.arm = armLaunchSignalsOuter(o.launchGuardTeardown(g), func() { o.abandonLaunch(g) }, o.interruptedArmExit())
	o.launchGuard = g
}

// launchGuardTeardown is the guard's teardown. Before the keeper's spawn it discards what the launch
// made, prints the timing report, releases the herdr pane and gives the terminal back, which no
// statement after the arm's exit could. Once the keeper is spawned it is the keeper's own pre-ready
// teardown, whose lifeline close has the keeper unwind.
func (o *Options) launchGuardTeardown(g *launchGuard) func() {
	return func() {
		left, kp := g.end()
		if kp != nil {
			o.keeperPreReadyTeardown(kp, g.cname, g.rt)()
			return
		}
		o.Perf.Mark("terminate.signal")
		o.discardUnspawned(g.cname, g.rt, left)
		o.emitTimingReport(0, g.cname, g.rt)
		o.releaseHerdrAgent()
		o.restoreTerminal()
	}
}

// abandonLaunch is the guard's outer hook: a launch running inside this one, in this process (a
// capture jail's), ends the process on a signal, and this launch with it. What it made goes, as at
// its own signal, its machine-wide line is written, and the terminal goes back; a keeper it spawned
// unwinds on its own once the process's exit closes the lifeline.
func (o *Options) abandonLaunch(g *launchGuard) {
	if left, kp := g.end(); kp == nil {
		o.discardUnspawned(g.cname, g.rt, left)
	}
	o.recordLaunchEndedBySignal()
	o.restoreTerminal()
}

// end marks the launch ended by a signal and returns what it made, or the keeper that holds it. It
// waits for a spawn under way, so it never discards what a keeper is just being handed.
func (g *launchGuard) end() (unspawned, *keeperProcess) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ended = true
	return g.unspawned, g.kp
}

// discardUnspawned removes what a launch that spawned no keeper made: its skeleton and pack tree,
// which no container ever held, and, once it wrote them, the live-tree record naming that tree and,
// on the runtime's answer that no container of the name exists, the tracking file. The launch lock
// goes first, since forgetGone takes it non-blocking and backs off a holder, this process included.
func (o *Options) discardUnspawned(cname, rt string, left unspawned) {
	discardUnheldSkeleton(cname, left.skeleton)
	if left.recorded {
		forgetLivePackTree(cname, left.tree)
	}
	discardPackTree(cname, left.tree)
	if left.recorded {
		o.releaseLaunchLock()
		o.forgetGone(cname, rt, "", "")
	}
}

// noteSkeleton records the home skeleton this launch built, for the guard to discard. False once a
// signal's teardown has begun, which took only what it already knew: the caller discards the
// skeleton itself and leaves the exit to the teardown.
func (g *launchGuard) noteSkeleton(dir string) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ended {
		return false
	}
	g.skeleton = dir
	return true
}

// record runs write, the launch's writing of the tracking file and the live-tree record, under the
// guard's hold, so a signal's teardown takes back both or finds neither: one that began first
// refuses it (false, and nothing is written), and one that lands during it waits for it.
func (g *launchGuard) record(write func()) bool {
	if g == nil {
		write()
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ended {
		return false
	}
	g.recorded = true
	write()
	return true
}

// lockSpawn holds the guard through a keeper's spawn, so a signal's teardown waits for it; false,
// and nothing held, once a teardown has begun. unlockSpawn ends the hold, handing the guard the
// keeper the spawn started, or nil for none. Both are no-ops for a launch with no guard.
func (g *launchGuard) lockSpawn() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	if g.ended {
		g.mu.Unlock()
		return false
	}
	return true
}

func (g *launchGuard) unlockSpawn(kp *keeperProcess) {
	if g == nil {
		return
	}
	if kp != nil {
		g.kp = kp
	}
	g.mu.Unlock()
}

// retireLaunchGuard removes the guard once an arm of this launch's own has taken over. False when
// the guard has begun a teardown, which then owns the exit: the caller must never race it.
func (o *Options) retireLaunchGuard() bool {
	if o.launchGuard == nil {
		return true
	}
	return o.launchGuard.arm.disarm()
}

// endLaunchGuard is Run's deferred retirement, at every return: a guard that began a teardown owns
// the exit, which is waited for rather than raced.
func (o *Options) endLaunchGuard() {
	if !o.retireLaunchGuard() {
		o.launchGuard.arm.awaitExit()
	}
}
