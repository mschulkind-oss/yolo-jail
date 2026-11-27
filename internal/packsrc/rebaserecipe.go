package packsrc

// rebaserecipe.go is the REBASE RECIPE (a term coined here): the git commands, with the commits
// filled in, that rebase a patch series onto an upstream commit it no longer fits, for the user or
// their agent to run (docs/reference/patched-forks.md §8.4, PF-D85, which retired `yolo pack
// rebase`). Every line that names the repair of a series prints it through RebaseRecipe.Repair:
// the error block (PatchFailure.Block), `yolo pack update` and `yolo pack status`, `yolo pack
// series check`, cached-good recovery and the base lag's re-anchoring. yolo never runs it and
// never writes the series.
//
// The steps: clone the upstream; on a branch at the series' base apply the series with `git am`
// and tag the applied tip; `git rebase <target>`, which drops a member upstream already holds by
// patch-id as the launch's replay does (PF-D86) — `--onto <target> <base>` would not, comparing
// against nothing between the two; resolve each stop; then the EXPORT, one command line whose
// every step runs only once the one before it succeeded, kept from PF-D49:
//
//   - a test that the rebase branch holds a commit neither the target nor the applied tip reaches,
//     so pasted mid-rebase (the branch is still the applied tip), after an abort, or after a rebase
//     that dropped every member, it changes nothing;
//   - `mkdir <patches>.new`, which refuses an earlier export's leftover;
//   - `git format-patch --src-prefix=a/ --dst-prefix=b/ --no-signature --suffix=.patch
//     --base=<target>` into it, reading the BRANCH, never HEAD, with `.patch` names and a/ b/
//     prefixes whatever the user's config says, since the series reads only `.patch` files and
//     `git am` strips one path component. The prefixes are flags, never `-c format.noprefix=false`:
//     some git versions (measured: 2.44.2 and 2.50.1, Apple Git 2.50.1 among them) read ANY value of format.noprefix as true, so that
//     setting exported a series with no prefixes at all, which `git am` refuses;
//   - the two renames and the `rm`, so a launch never reads an empty or partial series.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const (
	// RecipeBranch is the branch the recipe applies and rebases the series on, and RecipeApplied
	// the tag it puts on the applied tip, which the export's test reads.
	RecipeBranch  = "yolo-rebase"
	RecipeApplied = "yolo-applied"
)

// RecipeLead is the sentence every printed recipe stands under: the error block's Repair line, and
// each verb's conflict line.
const RecipeLead = "rebase the series with git, yourself or with an agent; the next launch builds it:"

// RebaseRecipe is what the recipe needs of one patch series.
type RebaseRecipe struct {
	// Repo is the upstream's repository, as `git clone` takes it; an npm source's is `npm:<name>`.
	Repo string
	// Base is the series' base-commit, "" when the series cannot be read.
	Base string
	// Patches is the series directory, "" when it is not the user's to write: a pack yolo fetched
	// or ships, whose copy on this machine no act writes.
	Patches string
	// Pack and Rel name the series where Patches is "": the pack, and the series directory
	// relative to the pack's root.
	Pack, Rel string
	// Dir is the clone's directory, relative to the current one.
	Dir string
	// PatchBranchRepo and PatchBranchRef are a PATCH BRANCH's repository and branch (patchbranch.go), "" for a series: a
	// patch branch is repaired by merging the target into its branch, in its own repository, and pushing.
	PatchBranchRepo, PatchBranchRef string
	// LocalClone is the directory of the LOCAL CLONE the patch branch was read from (PatchBranchResolved.Checkout
	// when its Copy is "local"), "" when it was read from the repository itself. The branch that failed to merge
	// is that clone's, which may differ from the remote's, so the recipe repairs it there.
	LocalClone string
}

// PatchBranchRecipeLead is RecipeLead for a patch branch, which a merge repairs rather than a rebase.
const PatchBranchRecipeLead = "merge the upstream into the patch branch with git, yourself or with an agent; the next check fetches it:"

// LocalCloneRecipeLead is PatchBranchRecipeLead for a patch branch read from a local clone, which the next launch
// reads again, so nothing is fetched.
const LocalCloneRecipeLead = "merge the upstream into the patch branch in your local clone with git, yourself or with an agent; the next launch reads that clone:"

// Repair is the recipe for rebasing the series onto target as lines: lead, then the steps
// indented under it, each line prefixed with indent. A patch branch's takes PatchBranchRecipeLead in RecipeLead's
// place.
func (r RebaseRecipe) Repair(target ListEntry, lead, indent string) []string {
	if r.PatchBranchRepo != "" {
		patchLead := PatchBranchRecipeLead
		if r.LocalClone != "" {
			patchLead = LocalCloneRecipeLead
		}
		lead = strings.Replace(lead, RecipeLead, patchLead, 1)
	}
	out := []string{indent + lead}
	for _, l := range r.Lines(target) {
		out = append(out, indent+"  "+l)
	}
	return out
}

// Lines is the recipe's steps: the commands, and the one sentence between them saying when the
// export runs. A command line is indented two spaces under a sentence.
func (r RebaseRecipe) Lines(target ListEntry) []string {
	q := shquote.QuoteDisplay
	if strings.HasPrefix(r.Repo, NpmSourcePrefix) {
		return []string{"the upstream is an npm package, which has no git history to rebase onto: rework the series " +
			"against version " + target.Label() + "'s published files and export it with `git format-patch --base`"}
	}
	if r.PatchBranchRepo != "" && r.LocalClone != "" {
		// A PATCH BRANCH READ FROM A LOCAL CLONE: the branch that failed is the clone's own, so it is repaired
		// there; cloning the repository would repair the remote's, which a launch does not read.
		c := q(r.LocalClone)
		return []string{
			"  git -C " + c + " checkout " + q(r.PatchBranchRef),
			"  git -C " + c + " fetch " + q(r.Repo) + " " + target.Commit + " && git -C " + c + " merge " + target.Commit,
			"resolve each conflict (edit, git add, git commit, all in " + r.LocalClone + "); nothing more is needed, " +
				"and pushing the branch to its remote is optional",
		}
	}
	if r.PatchBranchRepo != "" {
		// A PATCH BRANCH: the target merged into the branch, in the patch branch's own repository, and pushed —
		// the next check fetches the branch and merges it again. yolo never writes the patch branch.
		return []string{
			"  git clone " + q(r.PatchBranchRepo) + " " + q(r.Dir) + " && cd " + q(r.Dir) + " && git checkout " + q(r.PatchBranchRef),
			"  git fetch " + q(r.Repo) + " " + target.Commit + " && git merge " + target.Commit,
			"resolve each conflict (edit, git add, git commit), then push the branch:",
			"  git push origin " + q(r.PatchBranchRef),
		}
	}
	base := r.Base
	if base == "" {
		base = "<the series' base-commit>"
	}
	t := target.Commit
	// The series directory and its two siblings, each quoted whole; a placeholder where it is not
	// the user's to write.
	dir := func(suffix string) string {
		if r.Patches == "" {
			return "<patches>" + suffix
		}
		return q(r.Patches + suffix)
	}
	var lines []string
	if r.Patches == "" {
		lines = append(lines, "pack "+r.Pack+" is not yours to write here, so <patches> below is its series "+
			"directory ("+r.Rel+") in your own clone of the pack's repository; commit and push it after")
	}
	return append(lines,
		"  git clone "+q(r.Repo)+" "+q(r.Dir)+" && cd "+q(r.Dir),
		"  git checkout -q -b "+RecipeBranch+" "+base+" && git am "+dir("")+"/*.patch && git tag "+RecipeApplied,
		"  git rebase "+t,
		"resolve each stop (edit, git add, git rebase --continue), then replace the series — this line changes "+
			"nothing until the rebase is done:",
		"  test -n \"$(git rev-list -n 1 "+RecipeBranch+" --not "+t+" "+RecipeApplied+" --)\" && mkdir "+
			dir(".new")+" && git format-patch --src-prefix=a/ --dst-prefix=b/ --no-signature --suffix=.patch --base="+t+
			" -o "+dir(".new")+" "+t+".."+RecipeBranch+" && mv "+dir("")+" "+dir(".old")+" && mv "+dir(".new")+
			" "+dir("")+" && rm -r "+dir(".old"))
}
