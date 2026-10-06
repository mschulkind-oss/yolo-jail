package run

import "github.com/mschulkind-oss/yolo-jail/internal/miseuse"

// markMiseUseRecording starts the mise use record's clock (internal/miseuse.SinceName; a term
// coined for docs/design/minimal-disk-footprint.md OQ-DF4): the first HOST launch to bind the
// shared tool store marks, in it, the moment from which the host's launches run jails that record
// which tool versions they use. The host's sweep of unused versions judges nothing until that
// mark is 30 days old, since a version used before it was never recorded (DF-D5).
//
// ONLY A HOST LAUNCH, AND ONLY FOR THE STORE EVERY JAIL SHARES. An in-jail launch binds the host's
// store too (/mise), but it says nothing about what the host's own launches record: a development
// jail running a newer tree than the host's yolo would otherwise start the clock while the host's
// jails still record nothing. A sealed build binds a store of its own; a Mac's jails bind a volume
// inside the container VM, which the host never judges.
//
// Best-effort: a mark that cannot be written only leaves the sweep waiting, the safe direction,
// and the next launch tries again.
func (o *Options) markMiseUseRecording(store string) {
	if o.inJail() || o.Sealed || o.IsMacOS || store == "" {
		return
	}
	_ = miseuse.MarkSince(store, o.Now())
}
