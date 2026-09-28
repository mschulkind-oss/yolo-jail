package config

// packselection.go is THE ONE SELECTION FUNCTION (docs/plans/notch-convergence.md item 6, rows
// B1 and B4): "which packs does this carry", answered once for every notch and every verb.
//
// Before it, the jail launch, `yolo check`, config validation, the lazy loophole resolvers and
// `config promote` each looped over the `packs` entries and ran the selection closure themselves,
// and no host verb ran the closure at all. So `"packs": ["claude"]` was four packs in a jail
// (claude, aws-auth, openai-auth and wire-bridge, through claude's `needs`) and one at the host.
// The host kept off the closure on purpose (credential-sources-separation.md ES-D24) because it
// would have added aws-auth's pointer at an address nothing on the host serves; served-address
// composition (item 2, NC-D16) withholds and names such a pointer, which is what makes the host
// safe to close (NC-D4).
//
// What differs between callers is an INPUT here, never a second loop: how an entry resolves (a
// launch stages it into its own tree, a host verb resolves it for its process, a read-only
// reader writes nothing to the pack store), which order the entries arrive in, whether the
// first failure stops the selection, and which profile table the closure's `via` half reads.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// PackSelectSpec is one caller's inputs to SelectPacks.
type PackSelectSpec struct {
	// Resolve loads one configured entry the way this caller reads packs. A nil pack with a nil
	// error means the entry contributes nothing and is not a failure: `yolo check` reports a pack
	// that stages zero files that way, having said so itself.
	Resolve func(PackEntry) (*packload.Pack, error)
	// FailFast stops at the first resolution failure, a join failure or a closure refusal, and
	// returns it as SelectPacks' error: the launch's staging is fail-closed and describes no
	// selection past its first fault. Otherwise every failure is recorded on the result and the
	// rest of the selection still resolves, so a reporting verb can name all of them.
	FailFast bool
	// Selection is the closure's profile input (UseProfiles, UserProfiles). Its Embedded field is
	// ignored and filled from packload.Embedded(): a need or a via may add only a pack yolo ships
	// (WB-D9, WG-I7), and there is one embedded materialization per process (row B7).
	Selection packload.Selection
	// Announce, when set, receives each cause line of the closure before the pack it names is
	// joined (WB-D12: a pack no config line named never joins a launch silently).
	Announce func(cause string)
	// Join resolves a pack the closure added. nil keeps the embedded declaration as it is, which
	// is what a reader of declarations needs; a caller that reads files resolves the addition
	// through the same resolver as an entry, as the bare-name entry it stands for
	// (EmbeddedPackEntry).
	Join func(*packload.Pack) (*packload.Pack, error)
}

// UnresolvedPackEntry is one entry SelectPacks could not resolve, with the resolver's own error.
type UnresolvedPackEntry struct {
	Entry PackEntry
	Err   error
}

// PackSelection is SelectPacks' answer.
type PackSelection struct {
	// Configured are the packs the entries resolved to, in the order the entries arrived.
	Configured []*packload.Pack
	// Added are the packs the selection closure joined (`needs`, then `via`), in the order they
	// joined, each resolved through Join.
	Added []*packload.Pack
	// Causes are the closure's cause lines, one per addition, in the same order.
	Causes []string
	// Unresolved are the entries (and additions) that did not resolve. Empty under FailFast.
	Unresolved []UnresolvedPackEntry
	// ClosureErr is the closure's refusal: a need or via naming a pack yolo does not ship, a
	// via naming a pack that serves none, a needs cycle. Under FailFast it is also the error
	// SelectPacks returns, so a caller can tell it from a resolution failure.
	ClosureErr error
}

// Packs is the complete selection: the configured packs, then the closure's additions.
func (s PackSelection) Packs() []*packload.Pack {
	return append(append([]*packload.Pack(nil), s.Configured...), s.Added...)
}

// Complete reports whether the selection is the whole of what the config asks for: every entry
// resolved and the closure did not refuse.
func (s PackSelection) Complete() bool {
	return len(s.Unresolved) == 0 && s.ClosureErr == nil
}

// SelectPacks resolves entries in the order given and extends them by the selection closure
// (packload.Selection.Close). The closure runs over what resolved, so under !FailFast a pack
// that did not resolve cannot contribute a need; the selection is then incomplete, which the
// result says.
func SelectPacks(entries []PackEntry, spec PackSelectSpec) (PackSelection, error) {
	var sel PackSelection
	for _, e := range entries {
		p, err := spec.Resolve(e)
		if err != nil {
			if spec.FailFast {
				return sel, err
			}
			sel.Unresolved = append(sel.Unresolved, UnresolvedPackEntry{Entry: e, Err: err})
			continue
		}
		if p != nil {
			sel.Configured = append(sel.Configured, p)
		}
	}
	closure := spec.Selection
	closure.Embedded = embeddedPackLookup()
	added, causes, err := closure.Close(sel.Configured)
	if err != nil {
		sel.ClosureErr = err
		if spec.FailFast {
			return sel, err
		}
		return sel, nil
	}
	for i, p := range added {
		if spec.Announce != nil {
			spec.Announce(causes[i])
		}
		joined := p
		if spec.Join != nil {
			if joined, err = spec.Join(p); err != nil {
				if spec.FailFast {
					return sel, err
				}
				sel.Unresolved = append(sel.Unresolved,
					UnresolvedPackEntry{Entry: EmbeddedPackEntry(p.Name), Err: err})
				continue
			}
		}
		sel.Added = append(sel.Added, joined)
		sel.Causes = append(sel.Causes, causes[i])
	}
	return sel, nil
}

// embeddedPackLookup is the closure's universe: the embedded official packs by name, from the
// process's one materialization. On a broken materialization it is empty, so a need names a pack
// "yolo does not ship"; every caller that resolves an embedded ENTRY has already been told the
// real reason (EmbeddedProblems), which is the one it reports.
func embeddedPackLookup() func(string) (*packload.Pack, bool) {
	byName := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		byName[p.Name] = p
	}
	return func(name string) (*packload.Pack, bool) {
		p, ok := byName[name]
		return p, ok
	}
}
