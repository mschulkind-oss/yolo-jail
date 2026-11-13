package ghbroker

import "github.com/mschulkind-oss/yolo-jail/internal/brokerstore"

// cells.go is the working set's half of the GitHub cells (docs/design/github-write-scopes.md
// §1, §3, GW-D41, GW-D42, GW-D59): which writes a working-set entry covers on its own item.
//
// A cell is one verb on one kind of item (`comment_prs`, `approve_prs`), and a write on an item
// in the working set reads the `working_set` table's cell. This build reads only that table's
// shipped defaults: its four comment and edit cells are present and true, so a working-set
// entry covers a comment or an edit on its item, and `approve_prs` and `merge_prs` ship absent,
// so an approval or a merge on it asks (an absent cell asks unless the user's `always` holds
// it, and no build reads a cell from `always` yet). A project's `[requests.github.working_set]`
// lines, the `all` table and the ceiling's cell tokens are later slices of
// docs/design/github-write-scopes-plan.md.
//
// A timed entry, a human's `yolo approve <id> --for`, is not a working-set member and is not
// read through these cells. A timed answer on an approval records its own kind
// (brokerstore.WorkingApprove), which covers that approval alone, as GW-D10 rules; a timed
// answer on any other PR or issue write still covers every comment and edit on the item, as
// before the cells (the rest of GW-D10's per-cell timed grant is a later slice).

// shippedWorkingSetCells are the working set's shipped defaults (GW-D42): present and true.
var shippedWorkingSetCells = map[string]bool{
	"comment_prs":    true,
	"comment_issues": true,
	"edit_prs":       true,
	"edit_issues":    true,
}

// workingSetCellRuns reports whether the working set's cell runs unasked on a member item.
func workingSetCellRuns(cell string) bool { return shippedWorkingSetCells[cell] }

// entryCovers reports whether live entry e, of one of t's covering kinds, covers t's write in
// cell: a timed approval covers an approval of its PR and nothing else (GW-D10); a timed PR or
// issue entry covers what it covered before the cells, every comment and edit, but no longer
// an approval; a working-set member only a working-set cell that runs.
func entryCovers(e brokerstore.WorkingEntry, t writeTarget, cell string) bool {
	switch {
	case e.Kind == brokerstore.WorkingApprove:
		return t.Verb == verbApprove
	case e.Timed() && (e.Kind == brokerstore.WorkingPR || e.Kind == brokerstore.WorkingIssue):
		return t.Verb != verbApprove
	case e.Timed():
		return true
	}
	return t.Verb != "" && workingSetCellRuns(cell)
}

// workingSetCellPath is the cell's path as the audit and a stop name it (GW-D13).
func workingSetCellPath(cell string) string { return "working_set." + cell }

// verbPhrase is the write a verb names, for a human: "approving PR o/r#32".
func verbPhrase(t writeTarget) string {
	switch t.Verb {
	case verbComment:
		return "commenting on " + t.describe()
	case verbApprove:
		return "approving " + t.describe()
	case verbMerge:
		return "merging " + t.describe()
	}
	return "editing " + t.describe()
}

// cellNotHeldMessage is the stop for a working-set item whose cell does not run (§5): the
// write asks, and the request's own menu names Allow once and a timed answer.
func cellNotHeldMessage(t writeTarget, cell string) string {
	return "github-broker: " + verbPhrase(t) + " asks here: it is in this workspace's working set, but " +
		workingSetCellPath(cell) + " is not granted, so only comments and edits on it run unasked\n"
}
