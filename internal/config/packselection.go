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
// reader writes nothing to the pack store), whether the first failure stops the selection, and
// which profile table the closure's `via` half reads. The ORDER is not an input:
// PackSelection.Packs is the one precedence order at every notch (OQ-NC4).

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
	// Configured are the packs the entries resolved to, in the order the entries arrived, the
	// conventional local pack included. Packs, not this, is the precedence order.
	Configured []*packload.Pack
	// Added are the packs the selection closure joined (`needs`, then `via`), in the order they
	// joined, each resolved through Join.
	Added []*packload.Pack
	// Causes are the closure's cause lines, one per addition, in the same order.
	Causes []string
	// Unresolved are the entries (and additions) that did not resolve. Empty under FailFast.
	Unresolved []UnresolvedPackEntry
	// ClosureErr is the closure's refusal: a need or via naming a pack yolo does not ship, a
	// via naming a pack that serves none, a needs cycle — or the fork rewrite's (a fork whose base
	// is absent or declares no such program, two forks of one program; packload.ApplyForks).
	// Under FailFast it is also the error
	// SelectPacks returns, so a caller can tell it from a resolution failure.
	ClosureErr error
	// implicit marks the Configured packs an Implicit entry resolved to (the conventional local
	// pack), which Packs places last.
	implicit map[*packload.Pack]bool
}

// Packs is the complete selection IN PRECEDENCE ORDER, the one order "later wins" follows at
// every notch (docs/plans/notch-convergence.md OQ-NC4, ruled A, 2026-09-28): the configured
// packs in the order the user config lists them, then the packs the closure joined (`needs`, then
// `via`) in joining order, then the conventional local pack (an Implicit entry, localPackEntry)
// last, so the user's personal pack outranks everything, a pack the closure pulled in included.
//
// It is THE order, not one caller's. The jail launch stages it and records it in the pack tree
// (packload.WritePackTreeRecord), which the boot and an attach read back, and every host verb,
// `yolo check` and the lazy resolvers read it from here, so one key has one winner wherever it is
// composed. Before it, the launch put embedded entries first, the boot read its tree
// alphabetically, and the host put the local pack ahead of the closure's additions: three
// winners for one key (row B2).
func (s PackSelection) Packs() []*packload.Pack {
	out := make([]*packload.Pack, 0, len(s.Configured)+len(s.Added))
	var last []*packload.Pack
	for _, p := range s.Configured {
		if s.implicit[p] {
			last = append(last, p)
			continue
		}
		out = append(out, p)
	}
	out = append(out, s.Added...)
	return append(out, last...)
}

// Complete reports whether the selection is the whole of what the config asks for: every entry
// resolved and the closure did not refuse.
func (s PackSelection) Complete() bool {
	return len(s.Unresolved) == 0 && s.ClosureErr == nil
}

// SelectPacks resolves entries in the order given and extends them by the selection closure
// (packload.Selection.Close); PackSelection.Packs orders the result. A caller passes the entries
// as config.LoadPacks returns them and never reorders them: the order is Packs', not the caller's. The closure runs over what resolved, so under !FailFast a pack
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
			if e.Implicit {
				if sel.implicit == nil {
					sel.implicit = map[*packload.Pack]bool{}
				}
				sel.implicit[p] = true
			}
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
	// THE FORK REWRITE, once the set is final (docs/design/forked-programs-as-packs.md FP-D5):
	// every base a selected fork names is replaced by a copy whose program carries the fork's
	// delivery, so every reader of the selection sees one program per bin. A refusal is the
	// closure's kind of error — this selection cannot run — and is reported as one.
	if err := sel.applyForks(); err != nil {
		sel.ClosureErr = err
		if spec.FailFast {
			return sel, err
		}
	}
	return sel, nil
}

// applyForks runs packload.ApplyForks over the whole selection and puts each rewritten base back
// where it was — in Configured or Added, and marked implicit if its original was — so the
// precedence order Packs computes is unchanged by the rewrite.
func (s *PackSelection) applyForks() error {
	all := append(append([]*packload.Pack{}, s.Configured...), s.Added...)
	rewritten, err := packload.ApplyForks(all)
	if err != nil {
		return err
	}
	for i := range s.Configured {
		orig, next := s.Configured[i], rewritten[i]
		if orig != next && s.implicit[orig] {
			delete(s.implicit, orig)
			s.implicit[next] = true
		}
		s.Configured[i] = next
	}
	copy(s.Added, rewritten[len(s.Configured):])
	return nil
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
