package basehome

import (
	"fmt"
	"sort"
	"strings"
)

// Bytes is the candidate byte total.
func (r Report) Bytes() int64 {
	var n int64
	for _, e := range r.Candidates {
		n += e.Bytes
	}
	return n
}

// RootSummary is one walk root's candidate total.
type RootSummary struct {
	Root       string
	Bytes      int64
	Candidates int
	Unreadable int
}

// ByRoot aggregates the candidates per walk root, largest first, ties by name.
//
// THE AGGREGATION IS THE REDACTION, not a convenience. §5.8 forbids the launch line from
// naming workspaces or absolute source paths, and an entry's own path is BOTH: the base
// home's largest runtime tree is `.claude/projects/<mangled absolute workspace path>`, so
// printing entry paths would print the user's directory names to every terminal the launch
// touches. A root is a state dir name and nothing else, which is exactly what the design
// permits.
func (r Report) ByRoot() []RootSummary {
	idx := map[string]*RootSummary{}
	var order []string
	for _, e := range r.Candidates {
		s, ok := idx[e.Root]
		if !ok {
			s = &RootSummary{Root: e.Root}
			idx[e.Root] = s
			order = append(order, e.Root)
		}
		s.Bytes += e.Bytes
		s.Candidates++
		if e.Class == Unclassified {
			s.Unreadable++
		}
	}
	out := make([]RootSummary, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Root < out[j].Root
	})
	return out
}

// Summary is §5.8's one-line disclosure, or "" when there is nothing to say.
//
// Silence is the zero-candidate case, and the precedent is noteHostCASAlias's rule: print
// when there is something to act on. A base home with no legacy runtime state is the
// expected steady state on a fresh host, and restating it on every launch of every jail is
// noise rather than disclosure.
//
// What the line does NOT carry, and why:
//
//   - no entry paths, no workspace names, no absolute paths (ByRoot's doc comment);
//   - no transcript content — nothing here ever opened a file;
//   - NO "command to quarantine", which §5.8 asks for, because in this step there is no
//     such command. Naming one that does not exist is worse than omitting it, so the line
//     says what it is instead: detection only. The command belongs to the step that can
//     honor it (§11 step 2), and this sentence is where it goes.
func (r Report) Summary() string {
	roots := r.ByRoot()
	if len(roots) == 0 {
		return ""
	}
	parts := make([]string, 0, len(roots))
	total := 0
	unreadable := 0
	for _, s := range roots {
		parts = append(parts, fmt.Sprintf("%s %s (%s)", s.Root, humanBytes(s.Bytes), plural(s.Candidates, "entry", "entries")))
		total += s.Candidates
		unreadable += s.Unreadable
	}
	// A ZERO-BYTE FINDING IS NOT REPORTED AT ALL. Ruled 2026-09-21, and it reverses the
	// always-on disclosure this shipped with.
	//
	// The bytes are the entire point (R1: move, never delete, because transcripts have no
	// regeneration path). An EMPTY directory has none, so there is nothing to move, nothing
	// to lose and nothing for a reader to do — and the candidates are empty on every host
	// that never ran the old shared-writable home, which is every host but one or two.
	// A permanent line about a legacy condition that CANNOT RECUR — the base is bound `:ro`
	// into every jail and the host CLI only ever MkdirAlls into it — is noise that teaches
	// people to stop reading launch output.
	if r.Bytes() == 0 && unreadable == 0 {
		return ""
	}

	// A ZERO-BYTE FINDING IS SAID DIFFERENTLY, and it is the common case rather than a
	// corner: MEASURED on this repo's own base home 2026-09-21, all six candidates were
	// empty directories — test and example residue (`.foo`, `.filespack`, `yolo-it-newdir`)
	// that the base never removes, since EnsureGlobalStorage only ever MkdirAlls.
	//
	// The line is NOT suppressed — §5.8 makes a disclosure non-suppressible, and these
	// really are candidates. But R1's whole rationale is that the bytes are the point, and
	// an empty directory has none: move-never-delete buys nothing, and a permanent
	// "0 B" alarm on every launch is how a line stops being read. So the wording carries
	// the distinction the number alone does not.
	var line string
	if r.Bytes() == 0 {
		line = fmt.Sprintf("Base home holds %s of legacy per-workspace state, all EMPTY "+
			"directories (no data to preserve): %s.",
			plural(total, "entry", "entries"), strings.Join(parts, ", "))
	} else {
		line = fmt.Sprintf("Legacy per-workspace state in the base home: %s — %s in %s total.",
			strings.Join(parts, ", "), humanBytes(r.Bytes()), plural(total, "entry", "entries"))
	}
	if unreadable > 0 {
		line += fmt.Sprintf(" %s could not be read and is counted as-is.", plural(unreadable, "entry", "entries"))
	}
	// No trailing "nothing has been moved" any more: the launch now REFUSES on a
	// non-zero finding and prints the mv, so the sentence contradicted the instruction
	// directly beneath it.
	return line
}

// Warnings is every degradation this pass found, one line each — a refused root, or a
// declaration set that cannot classify what it is supposed to protect. Each is something
// the user or a maintainer can act on, and §5.8's rule is that a degradation is never
// silent.
//
// A refused root is the one with real teeth: a `.claude` SYMLINK in the base home means
// every jail on this machine has been writing into whatever it points at, which is a fact
// worth a line whether or not anything is ever quarantined.
func (r Report) Warnings() []string {
	var out []string
	// A REFUSED ROOT IS ALWAYS WARNED, whatever was measured. It is an anomaly rather than
	// a quantity: a `.claude` SYMLINK in the base home means every jail on this machine has
	// been writing into whatever it points at, and that is worth a line even if nothing is
	// ever quarantined.
	for _, p := range r.Refused {
		out = append(out, fmt.Sprintf("Not examining the base home's %s: it %s.", p.Root, p.Reason))
	}
	// THE CLASSIFICATION LIMITS ARE GATED, because they only matter if something might be
	// moved. With nothing to move and nothing unknown, warning that an undeclared root
	// would have been classified without its own declarations is a permanent line about a
	// decision nobody is going to take — and on most hosts these are empty fossils like
	// `.yolo-shims`. Silence here is what stops a legacy condition that CANNOT RECUR (the
	// base is `:ro` to every jail and the host CLI only MkdirAlls into it) from costing
	// every launch a warning forever.
	if r.Bytes() > 0 || r.unreadableCount() > 0 {
		for _, p := range r.Problems {
			out = append(out, "Base-home detection is incomplete: "+p+".")
		}
	}
	return out
}

// ActionableBytes is Bytes() minus everything under a root whose declarations we never
// read, and it is what a caller may REFUSE on.
//
// THE DISTINCTION IS NOT COSMETIC. Decls comes from the SHIPPED packs, so a top-level dir
// declared by a LOCAL or fetched pack is walked with none of its own declarations: its
// `files` destinations, its config surfaces and any credential it holds all read as
// RUNTIME. MEASURED on a real host 2026-09-21 — `.claude/bin` is a `files` destination of
// a local pack, and Bytes() counted it.
//
// So Bytes() is what was FOUND and ActionableBytes is what is KNOWN, and a refusal must use
// the second. Reporting still uses the first, because an undeclared root with bytes is
// worth a line; it is just not worth failing someone's launch over.
func (r Report) ActionableBytes() int64 {
	unknown := map[string]bool{}
	for _, u := range r.UnknownRoots {
		unknown[u] = true
	}
	var n int64
	for _, e := range r.Candidates {
		if !unknown[e.Root] {
			n += e.Bytes
		}
	}
	return n
}

// unreadableCount is how many candidates could not be read. A zero BYTE total next to a
// non-zero count means "unknown", not "nothing" — which is why it gates the silence.
func (r Report) unreadableCount() int {
	n := 0
	for _, s := range r.ByRoot() {
		n += s.Unreadable
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// humanBytes renders a byte count the way check's min-free line and capture's report do
// (internal/cli/check/section_autogc.go, internal/cli/capturehost.go). A third unexported
// copy is the lesser evil versus this package importing a CLI package for one formatter.
func humanBytes(n int64) string {
	const gib = 1 << 30
	const mib = 1 << 20
	const kib = 1 << 10
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%d MiB", n/mib)
	case n >= kib:
		return fmt.Sprintf("%d KiB", n/kib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
