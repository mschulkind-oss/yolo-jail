package packload

// briefingsource.go resolves a `briefing` contribution's PROSE — what one contribution delivers —
// for a reader that holds a contribution rather than a pack. It is skillssource.go's sibling, and
// it exists for the same reason: a `from` that one notch honored and the other ignored
// (roadmap.md §6a-4 — the host render built `[from, "AGENTS.md", "CLAUDE.md"]` while the jail
// scanned the conventional pair unconditionally).
//
// It is now a thin reader over the governance predicate (governance.go), and that is the whole
// change the briefing/ convention (docs/reference/pack-system.md#briefing) made here:
//
//   - AGENTS.md IS NEVER READ (P1). The convention is every *.md directly inside briefing/.
//   - THE FALLBACK CHAIN IS GONE (P4, #briefing-p4). A declared `from` that is absent, not a file, blank or
//     escaping delivers NOTHING and is reported. It used to deliver the pack's AGENTS.md instead,
//     with a "used instead" warning — a named source quietly replaced by a different file.
//   - A DESTINATION SOURCES NOTHING (P5). An agent pack's `{agent, into}` names where content
//     lands; it used to also read the pack's own root prose into its own agent.
//   - WHAT A CONTRIBUTION CARRIES IS DECIDED PER FILE (pack-system.md#briefing-governance). An omitted `from` carries the files
//     no sibling names — so this cannot be answered from the contribution alone, and asks the pack.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// BriefingProseFor resolves ONE briefing contribution to the prose it carries — its governed
// files joined as one section (JoinBriefingSources), in filename order — plus a problem string when
// its declared source could not be honored ("" otherwise).
//
// The contribution is matched to its governor by SourceKey, so a synthesized ResolveDestinations
// copy (`{into, from}`) answers with exactly what the declaration it was borrowed for governs, and
// a synthesized implicit borrower (`{into}`, no `from`) with the implicit broadcast's files.
//
// It has NO PRODUCTION CALLER: ComposeHostBriefings and carriesFor call GovernedBriefingFor
// directly. The guards it exercises (P5, the reserved `from`) are pinned at that call site by
// entrypoint's ComposeHostBriefings tests, so deleting this wrapper and its tests unpins nothing.
func (p *Pack) BriefingProseFor(c packdecl.Contribution) (string, string) {
	sources, problems := p.GovernedBriefingFor(c)
	return JoinBriefingSources(sources), strings.Join(problems, "; ")
}
