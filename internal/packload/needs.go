package packload

// needs.go resolves the `needs` vocabulary (internal/packdecl/needs.go,
// docs/design/wire-bridge.md §3.1): the transitive closure over the selected
// packs' conditional dependencies, run at selection and BEFORE staging.
//
// BEFORE STAGING is the load-bearing half of the placement, and it is not a
// style preference: the mount is the filter (AGENTS.md, and the staged-tree prune
// in internal/cli/run/packs.go exists because of it). The in-jail entrypoint
// renders every pack it finds under YOLO_PACK_ROOT, so a pack the closure adds
// but nothing stages renders nothing — and a pack that renders nothing is the
// "declares a dependency, gets silence" failure the vocabulary exists to remove.
//
// The closure is PURE. It performs no I/O and reaches nothing outside its
// arguments: the caller hands it the selected packs and a predicate over the pack
// universe a need may draw from (the EMBEDDED official set — WB-D9), and gets
// back the additions. Keeping I/O out is what lets the launch pipeline and
// `yolo check` share one resolution, and what keeps this testable with hand-built
// packs. Printing the additions is the CALLER's duty, not this function's
// (WB-D12 — the banner line and the `yolo check` row are two renderings of the
// same cause strings, and neither may skip them).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ResolveNeeds extends the selected set with every pack a LIVE need names,
// transitively, and returns the packs it added plus one display string per
// addition ("+ <pack> (needed by <needy>: <bin> selected)" — the line both the
// launch banner and `yolo check` print, WB-D12). The selected slice is never
// modified; the caller appends `added` where it needs the complete set.
//
// A need is LIVE when any selected pack — including one an earlier addition put
// in the set — installs one of the need's when_bins (OR over the list, first
// matching bin named in the cause); a need with no when_bins is unconditional
// and always live. Resolution repeats until a full pass adds nothing, so a need
// whose condition only a later addition satisfies is still honored — the design
// says "repeat until stable" (§3.1), not "one pass in selection order".
//
// The rules the design fixes (WB-D9, WB-D10), and how they land here:
//
//   - A live need may draw ONLY from the `embedded` predicate, and a name the
//     predicate cannot resolve is an error naming the needing pack and the
//     target. The check runs ahead of the join on purpose: WB-D9 rules on the
//     DECLARATION ("needs may name only embedded official packs"), so a need
//     naming a pack outside the universe refuses whether or not the user
//     happens to also select that pack today — masked, it would surface the day
//     they dropped it, as a launch failure for a manifest they never changed.
//   - A need whose target is already selected is a NO-OP — join, never
//     override. Explicit user selection is never overwritten, and a pack two
//     needs both name is added once, with the first need's cause line.
//   - A CYCLE in the live-need graph refuses the launch, naming the loop. The
//     join rule above would terminate any fixpoint on its own, so the cycle is
//     checked STRUCTURALLY over the final set's live edges rather than left to
//     termination to imply: manifests that need each other are an authoring bug
//     the user is owed by name, not a property of the walk order.
func ResolveNeeds(selected []*Pack, embedded func(name string) (*Pack, bool)) (
	added []*Pack, causes []string, err error,
) {
	// The working set, keyed by name; `queue` keeps first-seen order (the
	// caller's selection order, then additions in discovery order) so the passes
	// and the cause lines are deterministic.
	set := make(map[string]*Pack, len(selected))
	queue := make([]*Pack, 0, len(selected))
	for _, p := range selected {
		if p == nil {
			continue
		}
		if _, seen := set[p.Name]; !seen {
			queue = append(queue, p)
		}
		set[p.Name] = p
	}

	// The fixpoint: full passes over the (growing) queue until a pass adds
	// nothing. Additions are appended to the queue mid-pass, so the common case —
	// a chain that is live end to end — resolves in one pass.
	for grew := true; grew; {
		grew = false
		for i := 0; i < len(queue); i++ {
			p := queue[i]
			for _, need := range p.Decl.DeclaredNeeds() {
				bin, live := needLive(need, set)
				if !live {
					continue
				}
				target, ok := embedded(need.Pack)
				if !ok {
					return nil, nil, fmt.Errorf(
						"pack %s needs pack %q, which is not an embedded official "+
							"pack — needs may name only packs yolo ships, so a manifest "+
							"cannot pull unreviewed code into a launch (WB-D9); if you "+
							"want %q in the launch, select it explicitly in your "+
							"config's packs list", p.Name, need.Pack, need.Pack)
				}
				if _, have := set[target.Name]; have {
					continue // join: already selected, by the user or by an earlier need
				}
				set[target.Name] = target
				queue = append(queue, target)
				added = append(added, target)
				causes = append(causes, needCause(target.Name, p.Name, bin))
				grew = true
			}
		}
	}

	if loop := needsCycle(set); loop != "" {
		return nil, nil, fmt.Errorf(
			"pack needs form a cycle: %s — packs that need each other would pull "+
				"each other in forever; fix the manifests so every needs chain ends",
			loop)
	}
	return added, causes, nil
}

// needLive answers the condition half: is this need live against `set`, and
// which bin made it so ("" for an unconditional need)? The bin is the FIRST of
// when_bins, in declaration order, that any selected pack installs — the cause
// line names one bin, and the first match is the deterministic one.
func needLive(need packdecl.PackNeed, set map[string]*Pack) (bin string, live bool) {
	if len(need.WhenBins) == 0 {
		return "", true
	}
	for _, want := range need.WhenBins {
		if want == "" {
			continue // validation refuses it; never let it match an empty bin
		}
		for _, p := range set {
			for _, installed := range p.InstallBins() {
				if installed == want {
					return want, true
				}
			}
		}
	}
	return "", false
}

// needCause renders the one line the launch banner and `yolo check` both print
// for an addition (WB-D12). The design's example is the conditional form:
// "+ wire-bridge (needed by cerebras: claude selected)". An unconditional need
// names no bin because none fired.
func needCause(pack, needy, bin string) string {
	if bin == "" {
		return "+ " + pack + " (needed by " + needy + ")"
	}
	return "+ " + pack + " (needed by " + needy + ": " + bin + " selected)"
}

// needsCycle looks for a cycle in the live-need graph over the final set and
// returns it rendered "a → b → a", or "" when the graph is acyclic.
//
// Edges are computed against the FINAL set — the same set every liveness answer
// at termination saw a superset of — so the verdict cannot depend on the order
// additions happened in. Only live needs are edges: a condition-false need
// contributes nothing to the closure, and a cycle through it is no cycle.
func needsCycle(set map[string]*Pack) string {
	edges := make(map[string][]string, len(set))
	for name, p := range set {
		for _, need := range p.Decl.DeclaredNeeds() {
			if _, live := needLive(need, set); live {
				edges[name] = append(edges[name], need.Pack)
			}
		}
	}

	const unseen, open, done = 0, 1, 2
	state := make(map[string]int, len(set))
	var stack []string
	// visit returns the rendered loop when it closes one, "" otherwise. The
	// `open` hit is the cycle: `next` is an ancestor of the current walk, and
	// stack[idx:] is the path from it to here.
	var visit func(name string) string
	visit = func(name string) string {
		state[name] = open
		stack = append(stack, name)
		for _, next := range edges[name] {
			if _, known := set[next]; !known {
				continue // unreachable post-closure; defensive against a racing set
			}
			switch state[next] {
			case open:
				idx := 0
				for i, s := range stack {
					if s == next {
						idx = i
						break
					}
				}
				loop := append(append([]string{}, stack[idx:]...), next)
				return strings.Join(loop, " → ")
			case unseen:
				if got := visit(next); got != "" {
					return got
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		return ""
	}

	// Sorted start order, so a graph with several cycles reports the same one
	// every run: the refusal names a loop, and which loop it names must not be a
	// property of map iteration.
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if state[name] == unseen {
			if loop := visit(name); loop != "" {
				return loop
			}
		}
	}
	return ""
}
