package run

// forkbuild.go is the launch's half of the fork route (docs/design/forked-programs-as-packs.md):
// which forks this launch's packs carry, and what the fork lock says each is pinned to.
//
// THE LAUNCH ONLY READS THE LOCK (FP-D7). Pinning is `yolo pack install`'s and moving a pin is
// `yolo pack update`'s; a launch that resolved a fork's ref itself would be the rebuild on a timer
// §9 forbids, and a fork's source is not one of the packs the launch's refresh fetches.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// forkPins reads the pin of every fork packs carry from the fork lock beside the user config. A
// lock that cannot be read pins nothing, and every fork then carries the read error as its reason:
// a broken lock is a missing tool, never a refused launch (§9).
func forkPins(packs []*packload.Pack) []packload.ForkPin {
	forks := packload.Forks(packs)
	if len(forks) == 0 {
		return nil
	}
	lock, err := packsrc.LoadForkLock(packsrc.ForkLockPath(paths.UserConfigPath()))
	if err != nil {
		pins := packload.ForkPins(forks, nil)
		for i := range pins {
			pins[i].Reason = "the fork lock cannot be read (" + err.Error() + ")"
		}
		return pins
	}
	return packload.ForkPins(forks, lock)
}

// noteForkPins prints, on every launch that carries a fork, the line OQ-FP6 rules on: the REVISION
// each source-built program is pinned to — never only its ref — or why it has none and the command
// that pins it. A disclosure, so it has no quiet switch (OQ-RO3). It returns the pins it read, for
// the build trigger that acts on them.
func (o *Options) noteForkPins(packs []*packload.Pack) []packload.ForkPin {
	pins := forkPins(packs)
	if len(pins) == 0 {
		return nil
	}
	out := o.pr(o.Stderr)
	out.print("[dim]Forks this launch:[/dim]")
	for _, p := range pins {
		if p.Commit == "" {
			out.print("[yellow]  " + p.Line() + "[/yellow]")
			continue
		}
		out.print("[dim]  " + p.Line() + "[/dim]")
	}
	return pins
}
