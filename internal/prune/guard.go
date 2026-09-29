package prune

// Guard brackets ONE deletion of a pass that shares its stores with other work
// (docs/design/podman-reboot-readiness.md OQ-PR2). It takes the caller's lock, runs recheck,
// runs del only if recheck still says the item may go, releases the lock, and reports whether
// del ran. A pass takes the lock per deletion and releases it between deletions, so whatever
// else waits for the lock — a launch's image re-inspect-and-record — waits at most one
// deletion, never a whole pass.
//
// THE RECHECK IS THE PRICE OF THAT. A pass that held the lock throughout decided on evidence
// nothing could change under it; one that lets go between deletions must ask again, under the
// lock and right before each deletion, whether the item is still unused. Each class says what
// its recheck reads; each reads only what can change while a pass runs.
//
// A nil Guard runs del directly and never rechecks: the manual `yolo prune`, which takes no
// such lock and whose behavior this does not change.
type Guard func(recheck func() bool, del func()) bool

// Do is g, or the direct deletion when g is nil.
func (g Guard) Do(recheck func() bool, del func()) bool {
	if g == nil {
		del()
		return true
	}
	return g(recheck, del)
}
