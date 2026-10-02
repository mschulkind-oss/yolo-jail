package packload

// forkpin.go is a fork's PIN as every reader sees it (packsrc.ForkLock; forked-programs-as-packs.md
// FP-D7, FP-D18): what the fork lock says each fork is pinned to, or why it has no pin, and the
// LAUNCH'S PIN (PinForks), which pins a fork the lock does not pin for its declared source and
// leaves a standing pin where it is. Readers that must not fetch — `yolo pack status`, a dry run,
// the floor's status — use LoadForkPins; a launch, `yolo host -- <bin>`, `yolo host apply --assert`
// and `yolo capture <forked bin>` use PinForks.

import "github.com/mschulkind-oss/yolo-jail/internal/packsrc"

// ForkPin is what the fork lock says about one fork: the commit its source is pinned to, or why it
// has no usable pin.
type ForkPin struct {
	Fork Fork
	// Commit is the full pinned commit, "" when there is no usable pin.
	Commit string
	// Ref is the ref the pin was resolved from.
	Ref string
	// Reason is why there is no usable pin, naming what makes one. "" when Commit is set.
	Reason string
	// LockedSource is the source a stale pin was made for, when the fork now declares another.
	LockedSource string
	// Pinnable is true when the fork lock was read and holds no pin for the fork's declared source:
	// the pin a launch makes (PinForks). False for a pinned fork, and for one whose lock cannot be
	// read, which nothing pins over.
	Pinnable bool
	// Pinned is true when THIS call made the pin (PinForks): the commit is new to the fork lock, and
	// the caller discloses it (PinnedLine).
	Pinned bool
	// Warning is a fetch that failed while the pin was made from the commit this machine already
	// held, said as one line; "" otherwise.
	Warning string
}

// ForkPins reads each fork's pin from lock (nil reads as an empty lock).
//
// A PIN MADE FOR ANOTHER SOURCE IS NO PIN: an edited `source` is a new address, and the old
// commit answering for it would build a revision of a repository the manifest no longer names.
// Both that and an absent entry are pinnable, and the next launch pins them (FP-D18).
func ForkPins(forks []Fork, lock *packsrc.ForkLock) []ForkPin {
	out := make([]ForkPin, 0, len(forks))
	for _, f := range forks {
		p := ForkPin{Fork: f}
		var e packsrc.ForkLockEntry
		ok := false
		if lock != nil {
			e, ok = lock.Get(f.Key())
		}
		switch {
		case !ok || e.Commit == "":
			p.Pinnable = true
			p.Reason = "it has no pin yet — the next launch pins it, or `yolo pack install` pins it now"
		case e.Source != f.Source:
			p.Pinnable = true
			p.LockedSource = e.Source
			p.Reason = "its source changed since it was pinned (pinned for " + e.Source +
				") — the next launch pins " + f.Source + ", or `yolo pack install` pins it now"
		default:
			p.Commit, p.Ref = e.Commit, e.Ref
		}
		out = append(out, p)
	}
	return out
}

// LoadForkPins is ForkPins over the fork lock at lockPath, for every reader that asks the file
// rather than a lock it already holds and must not fetch: `yolo pack status`, a dry run, a launch
// inside a jail, and the host floor's status. A lock that cannot be read pins NOTHING, and every
// fork carries the read error as its reason — a broken lock is a missing tool, never a refused
// launch (forked-programs-as-packs.md §9). One spelling, so the host and a jail can never disagree
// about which forks a broken lock pins.
func LoadForkPins(forks []Fork, lockPath string) []ForkPin {
	if len(forks) == 0 {
		return nil
	}
	lock, err := packsrc.LoadForkLock(lockPath)
	if err != nil {
		pins := ForkPins(forks, nil)
		for i := range pins {
			pins[i].Pinnable = false
			pins[i].Reason = "the fork lock cannot be read (" + err.Error() + ")"
		}
		return pins
	}
	return ForkPins(forks, lock)
}

// PinForks is THE LAUNCH'S PIN (forked-programs-as-packs.md FP-D18, applying the maintainer's
// OQ-PF1): every fork the fork lock at lockPath does not pin for its declared source is pinned now,
// through store (packsrc.Store.PinForks: its ref resolved once, by the launch's ref rule, and the
// commit recorded under the fork lock's flock), and every fork's pin is returned. A standing pin is
// never moved and costs no git run. A fork that could not be pinned has no Commit, and its Reason
// names the failure and the next step. begin, when non-nil, brackets the git work (a launch's
// progress line), and is not called when every fork is already pinned.
func PinForks(forks []Fork, lockPath string, store *packsrc.Store, begin func() (func(string), func())) []ForkPin {
	if len(forks) == 0 {
		return nil
	}
	want := make([]packsrc.ForkWant, len(forks))
	for i, f := range forks {
		want[i] = packsrc.ForkWant{Key: f.Key(), Source: f.Source}
	}
	outcomes := store.PinForks(lockPath, want, packsrc.ForkPinOptions{Begin: begin})
	pins := make([]ForkPin, len(forks))
	for i, o := range outcomes {
		p := ForkPin{Fork: forks[i]}
		if o.Entry.Commit != "" {
			p.Commit, p.Ref, p.Pinned = o.Entry.Commit, o.Entry.Ref, o.Pinned
			if o.FetchErr != nil {
				p.Warning = "fork " + p.Fork.Key() + ": could not fetch " + p.Fork.Source + " (" +
					o.FetchErr.Error() + "), so it is pinned at " + shortForkCommit(p.Commit) +
					", the commit this machine already had — `yolo pack update` re-resolves it once the fetch works"
			}
		} else {
			p.Reason = "it has no pin, and pinning it failed (" + o.Err.Error() + ") — fix what that names " +
				"and launch again, or pin it with `yolo pack install`, which can ask for an ssh host key or " +
				"passphrase at your terminal"
		}
		pins[i] = p
	}
	return pins
}

// PinnedLine is the one line a caller discloses for a pin it made (Pinned): which fork, at which
// commit of which source, and what moves it. A launch has no quiet mode (OQ-RO3), and the commit a
// fork is built at is the fact nothing else keeps (OQ-FP6).
func (p ForkPin) PinnedLine() string {
	return "pinned fork " + p.Fork.Key() + " at " + shortForkCommit(p.Commit) + " (" + p.Fork.Source +
		"); `yolo pack update` moves it"
}

// shortForkCommit is a commit as a pin line names it, the length a `Fetched pack` line uses.
func shortForkCommit(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	return c
}

// Line is the pin's one-line disclosure, the line OQ-FP6 rules on: a source-built program names the
// REVISION it is built at, never only the ref, because the commit that produced the binary on the
// PATH is the one fact nothing else keeps.
func (p ForkPin) Line() string {
	head := "fork " + p.Fork.Pack + ": " + p.Fork.Bin + " (in place of pack " + p.Fork.Base + "'s)"
	if p.Commit == "" {
		return head + " — " + p.Reason
	}
	return head + " is built from " + p.Fork.Source + " at commit " + p.Commit
}
