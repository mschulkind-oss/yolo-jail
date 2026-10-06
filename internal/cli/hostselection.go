package cli

// hostselection.go is the one selection function at every host verb
// (docs/plans/notch-convergence.md item 6, rows B1 and B4): config.SelectPacks, with the host's
// resolver and the host's dispositions.
//
// Every host verb used to resolve each `packs` entry alone and run no selection closure, so
// `"packs": ["claude"]` gave claude, aws-auth, openai-auth and wire-bridge in a jail and claude
// alone at `yolo host --`, `yolo host env`, `yolo host apply`, the footer, the `config` verbs,
// capture, revert and check-deps. The host kept off the closure on purpose while it would have
// exported aws-auth's pointer at an address nothing on the host serves (ES-D24); served-address
// composition withholds and names such a pointer (item 2, NC-D16), so the host now closes the
// selection as the jail does (NC-D4, HS-D1).
//
// And a `packs` entry that is malformed, or a pack that does not resolve, used to be dropped with
// a warning at `yolo host --` and in silence everywhere else. The dispositions are now NC-D5's:
// a launch-shaped verb (`yolo host --`, `yolo host env`) refuses and names the pack, as a jail
// launch does; `yolo host apply --assert` refuses an incomplete set, as it did; the read-only
// verbs report.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostPackSet is the one selection function's answer at a host verb.
type hostPackSet struct {
	// packs is the complete selection in the one precedence order (config.PackSelection.Packs):
	// configured packs in config order, then the closure's additions, then the local pack.
	packs []*packload.Pack
	// configured are the packs the `packs` entries resolved to; added are the closure's
	// additions, and causes their cause lines (WB-D12), in joining order.
	configured, added []*packload.Pack
	causes            []string
	// unresolved are the entries that did not resolve, with the resolver's reason.
	unresolved []unresolvedPack
	// entryProblems are the `packs` entries that did not lower (config.LoadPackEntries), each
	// naming its entry.
	entryProblems []string
	// closureErr is the closure's refusal (a need naming a pack yolo does not ship, a cycle).
	closureErr error
	// loadErr is an unreadable user config: no selection at all.
	loadErr error
}

// selectHostPacks runs the one selection function over the user's `packs` with a host verb's
// resolver. The closure reads table's profile selection; an addition resolves through the same
// resolver as an entry, as the bare-name entry it stands for, so every file a host verb reads
// from a joined pack is one the one resolver delivered.
func selectHostPacks(resolve func(config.PackEntry) (*packload.Pack, error), table packload.Selection) hostPackSet {
	entries, problems, err := config.LoadPackEntries()
	if err != nil {
		return hostPackSet{loadErr: err}
	}
	return selectHostPacksFrom(entries, problems, resolve, table)
}

// selectHostPacksFrom is selectHostPacks over entries (and their lowering problems) the caller
// already read with config.LoadPackEntries.
func selectHostPacksFrom(entries []config.PackEntry, problems []string,
	resolve func(config.PackEntry) (*packload.Pack, error), table packload.Selection) hostPackSet {
	sel, _ := config.SelectPacks(entries, config.PackSelectSpec{
		Resolve:   resolve,
		Selection: table,
		Join: func(p *packload.Pack) (*packload.Pack, error) {
			return resolve(config.EmbeddedPackEntry(p.Name))
		},
	})
	out := hostPackSet{
		packs:         sel.Packs(),
		configured:    sel.Configured,
		added:         sel.Added,
		causes:        sel.Causes,
		entryProblems: problems,
		closureErr:    sel.ClosureErr,
	}
	for _, u := range sel.Unresolved {
		out.unresolved = append(out.unresolved, newUnresolvedPack(u.Entry, u.Err))
	}
	return out
}

// selectConfiguredHostPacks is selectHostPacks with the resolver every host verb but the
// footer reads packs through (resolveConfiguredPack) and the user-scope profile table: the
// selection of a host verb that launches nothing.
func selectConfiguredHostPacks() hostPackSet {
	return selectHostPacks(resolveConfiguredPack, config.UserScopeSelection())
}

// joined reports whether the named pack joined through the closure rather than a `packs` entry,
// and the cause line that says why, so a message about the pack never says it is "in `packs`"
// when no config line names it (HS-D1).
func (s hostPackSet) joined(name string) (string, bool) {
	for i, p := range s.added {
		if p.Name == name {
			return s.causes[i], true
		}
	}
	return "", false
}

// skewNotes is every selected pack's version-skew notes (packload.Pack.SkewNotes, each naming its
// pack): what the use read skipped, kept without a field, or ignored because this yolo cannot read
// it (docs/design/patched-forks.md PF-D68). In the selection's precedence order.
func (s hostPackSet) skewNotes() []string {
	var out []string
	for _, p := range s.packs {
		out = append(out, p.SkewNotes...)
	}
	return out
}

// skewed is each selected pack holding a contribution this yolo cannot read, as the record a host
// report of an incomplete set prints (unresolvedPack, its Skipped set): `yolo host apply --assert`
// refuses such a set, since a skipped contribution's earlier render would be retired from the
// real home (PF-D70), while a launch goes on without it and says so.
func (s hostPackSet) skewed() []unresolvedPack {
	var out []unresolvedPack
	for _, p := range s.packs {
		if len(p.SkewNotes) == 0 {
			continue
		}
		u := unresolvedPack{Name: p.Name, Shipped: p.Official}
		for _, note := range p.SkewNotes {
			u.Skipped = append(u.Skipped, strings.TrimPrefix(note, "pack "+p.Name+": "))
		}
		u.Reason = strings.Join(u.Skipped, "; ")
		out = append(out, u)
	}
	return out
}

// problems is every reason the selection is not what the config asks for, one record each,
// in the shape every host report of an unresolvable pack already prints (unresolvedPack), so a
// verb that names unresolvable packs names a malformed entry and a refused closure beside them
// rather than dropping either in silence. A malformed entry is named by its place in the list
// (`config.packs[1]`), since it has no pack name; a refused closure by "pack selection".
func (s hostPackSet) problems() []unresolvedPack {
	var out []unresolvedPack
	if s.loadErr != nil {
		out = append(out, unresolvedPack{Name: "packs", Reason: s.loadErr.Error()})
	}
	// Where each entry was written (config's sources.go): the name is only its place in the
	// composed list, and the user scope is many files. Read once, and only with a problem.
	var src *config.Sources
	if len(s.entryProblems) > 0 {
		src = config.UserScopeSources()
	}
	for _, p := range s.entryProblems {
		name, reason, found := strings.Cut(p, ": ")
		if !found {
			name, reason = "packs", p
		}
		if where := src.Locations(name); len(where) > 0 {
			reason += " (written at " + strings.Join(where, " and at ") + ")"
		}
		out = append(out, unresolvedPack{Name: name, Reason: reason})
	}
	out = append(out, s.unresolved...)
	if s.closureErr != nil {
		out = append(out, unresolvedPack{Name: "pack selection", Reason: s.closureErr.Error()})
	}
	return out
}

// launchRefusal is NC-D5's refusal for a launch-shaped host verb (`yolo host --`, `yolo host
// env`): every problem, each naming its pack or entry, then the remedies. nil when the selection
// is complete. A jail launch refuses each of these too (config validation, stagePacks), so the
// two notches stop at the same configs.
func (s hostPackSet) launchRefusal() error {
	problems := s.problems()
	if len(problems) == 0 {
		return nil
	}
	var lines []string
	for _, u := range problems {
		lines = append(lines, fmt.Sprintf("  ✗ %s: %s", u.Name, u.Reason))
	}
	for _, g := range unresolvedPackGroups(problems) {
		lines = append(lines, "  → "+g.Remedy)
	}
	return fmt.Errorf("packs: %s — this launch cannot carry the pack set your config asks for, "+
		"and a launch never runs on part of it (a jail launch refuses the same config):\n%s",
		describeCount(len(problems)), strings.Join(lines, "\n"))
}

// describeCount is the refusal's head: how many problems it lists.
func describeCount(n int) string {
	return plural(n, "1 problem", fmt.Sprintf("%d problems", n))
}
