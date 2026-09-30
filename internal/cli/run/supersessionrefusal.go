package run

import (
	"errors"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// unmatchedSupersessionVerdict opens the refusal at every notch. The sentences under it are
// the discovery warning's, unchanged (docs/design/reference-mismatch-diagnostics.md §4.4: "the
// same sentence, as a launch refusal"); only the disposition around them is the launch's.
const unmatchedSupersessionVerdict = "a selected pack's `supersedes` names a capability no " +
	"loophole of this launch serves."

// unmatchedSupersessionHeader is the verdict as a jail launch prints it. The host notch prints
// the same verdict after its own "yolo host: refusing to launch: " prefix.
const unmatchedSupersessionHeader = "Refusing to launch: " + unmatchedSupersessionVerdict

// refuseUnmatchedSupersessions is the launch's supersession gate
// (docs/design/reference-mismatch-diagnostics.md §7 steps 4 and 5): a selected pack's
// `supersedes` claim that matches no capability any loophole of this pack set serves REFUSES
// the launch, with the claim's own sentence, the fix, and — only when version.SourceSkew can
// prove it — the skew that would explain it (RM-D2).
//
// # Why refuse
//
// A typo supersedes nothing, silently, and the author believes it worked: the loophole keeps
// running and nothing the jail shows says why. OQ-RM2 is answered by OQ-TP6
// (docs/design/trust-paths.md): a refused claim refuses the launch, never the pack alone, so
// nothing runs with part of a pack disabled by a refusal. It used to be a stderr warning, the
// one reference check that reported where the others refuse.
//
// # Where
//
// Here, among the pack pre-flights of stagePacksInto, because this is where the pack set
// becomes complete — the closure's additions included — and it is the only place the whole
// set a loophole can serve from is in hand: the in-jail entrypoint cannot resolve it, and
// `yolo pack lint` sees one pack. It covers every backend and the attach path, as the other
// pre-flights do: a claim that matches nothing is a config error either way. The host notch
// runs the same gate (UnmatchedSupersessionRefusal) over its own selection. `yolo check`
// grades the same finding [FAIL] through the same construction (RM-D1), and `yolo loopholes
// list` and `status` keep only reporting it.
//
// It reads the packs just staged, not the process-wide records, which stagePacksInto sets
// only after its pre-flights pass. So the gate runs BEFORE anything on the launch path
// discovers loopholes through NewHostSet: that discovery warns an unmatched claim to stderr,
// and a launch that printed the sentence as a warning and then refused over it would say one
// thing twice, once as a report the refusal then contradicts.
//
// NO HATCH, which is OQ-RM4's leaning while that question stays open: a mistyped capability
// is always the config, and the fix is shorter than a workaround.
func (o *Options) refuseUnmatchedSupersessions(packs []*packload.Pack) error {
	lines := UnmatchedSupersessionRefusal(packs, o.skewRepoRoot)
	if lines == nil {
		return nil
	}
	return errors.New("Refusing to launch: " + strings.Join(lines, "\n"))
}

// UnmatchedSupersessionRefusal is the supersession gate over one launch's complete pack set,
// at every notch that launches: nil when every `supersedes` claim in packs matches a capability
// a loophole of packs serves, else the refusal's lines — the verdict, each claim's sentence
// indented under it, then the fix (loopholes.UnmatchedSupersessionFix). A jail launch prints
// them after "Refusing to launch: " (refuseUnmatchedSupersessions); `yolo host --` and
// `yolo host env` after their own prefix (internal/cli's host composition). One gate, the notch
// as an input (docs/plans/notch-convergence.md NC-D1): at the host a claim retires a loophole's
// doorway, so a claim there that matches nothing is the same undetectable no-op.
//
// repoRoot is asked only once a claim has failed to match, so the happy path runs no git.
// It warns no claim: SupersessionProblemsFor never does, and a notch about to refuse must not
// first print the same sentence as a bare warning.
func UnmatchedSupersessionRefusal(packs []*packload.Pack, repoRoot func() string) []string {
	problems := loopholes.SupersessionProblemsFor(packLoopholeModules(packs), packSupersessions(packs))
	if len(problems) == 0 {
		return nil
	}
	lines := []string{unmatchedSupersessionVerdict}
	for _, p := range problems {
		lines = append(lines, "  "+p)
	}
	return append(lines, loopholes.UnmatchedSupersessionFix(repoRoot())...)
}

// skewRepoRoot is the repo root the skew clause compares against: the one this launch
// resolved, or "" when it resolved none, which SourceSkew answers with no skew. Asked only on
// the refusal path, and tolerant of an Options built without the seam, as the staging-only
// callers build it.
func (o *Options) skewRepoRoot() string {
	if o.RepoRoot == nil {
		return ""
	}
	res, ok := o.RepoRoot()
	if !ok {
		return ""
	}
	return res.Root
}
