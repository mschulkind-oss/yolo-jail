package packload

// forkpin.go reads a fork's PIN out of the fork lock (packsrc.ForkLock; forked-programs-as-packs.md
// FP-D7) for every reader that needs one: a launch, `yolo pack status`, and the build act. The lock
// is written only by `yolo pack install` and `yolo pack update`; everything here only reads it.

import "github.com/mschulkind-oss/yolo-jail/internal/packsrc"

// ForkPin is what the fork lock says about one fork: the commit its source is pinned to, or why it
// has no usable pin.
type ForkPin struct {
	Fork Fork
	// Commit is the full pinned commit, "" when there is no usable pin.
	Commit string
	// Ref is the ref the pin was resolved from.
	Ref string
	// Reason is why there is no usable pin, naming the command that makes one. "" when Commit is set.
	Reason string
	// LockedSource is the source a stale pin was made for, when the fork now declares another.
	LockedSource string
}

// ForkPins reads each fork's pin from lock (nil reads as an empty lock).
//
// A PIN MADE FOR ANOTHER SOURCE IS NO PIN: an edited `source` is a new address, and the old
// commit answering for it would build a revision of a repository the manifest no longer names.
// Both that and an absent entry send the reader to `yolo pack install`, which pins them.
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
			p.Reason = "it has no pin yet — run `yolo pack install` to pin " + f.Source
		case e.Source != f.Source:
			p.LockedSource = e.Source
			p.Reason = "its source changed since it was pinned (pinned for " + e.Source +
				") — run `yolo pack install` to pin " + f.Source
		default:
			p.Commit, p.Ref = e.Commit, e.Ref
		}
		out = append(out, p)
	}
	return out
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
