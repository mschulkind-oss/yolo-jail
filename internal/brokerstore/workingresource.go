package brokerstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// workingresource.go is the working set's GitHub kinds (docs/design/boundary-broker.md §5.7,
// §5.8, §7, BB-D76, BB-D92, BB-D95, BB-D97): a PR or an issue the workspace may write to
// unasked, and the timed grants a human's `yolo approve <id> --for` records on a GitHub
// request's resource. Every one is a `working/w-<id>.json` record like a git branch, keyed by
// workspace, repository and resource, and written under the store's one transaction lock.
//
// A PR and an issue share one number space per repository, so their resource is "#<n>" and
// the kind only says which it is; a creation's resource is the repository's collection, and
// a repository-level write's is the repository itself.

// The GitHub working-set kinds. WorkingPR and WorkingIssue may be lasting (in the working set
// until the PR is merged or the entry revoked) or timed; the rest are always timed, since a
// merge asks per resource (OQ-BB19), a lasting right to create is a user grant (BB-D90), and
// a repository-level write never joins the working set (BB-D95).
const (
	WorkingPR    = "pr"
	WorkingIssue = "issue"
	WorkingMerge = "merge"
	// WorkingApprove is a timed answer on approving one PR (github-write-scopes.md GW-D10): it
	// covers that cell on that item and no other write, and nothing else covers an approval
	// of a working-set PR the project does not grant one on.
	WorkingApprove     = "approve"
	WorkingCreatePR    = "create-pr"
	WorkingCreateIssue = "create-issue"
	WorkingRepo        = "repo"
)

// The resources of the GitHub kinds that name no number.
const (
	ResourcePulls      = "pulls"      // WorkingCreatePR: the repository's ability to create PRs
	ResourceIssues     = "issues"     // WorkingCreateIssue: its ability to create issues
	ResourceRepository = "repository" // WorkingRepo: its repository-level writes
)

// EndMerged is why a PR's entry ended when the PR was merged (BB-D97): a merge through the
// broker, or one the broker read at the PR's next write.
const EndMerged = "merged"

// lastingKinds are the kinds an entry may hold for good; timedKinds only with an expiry.
var (
	lastingKinds = []string{WorkingBranch, WorkingPR, WorkingIssue}
	timedKinds   = []string{WorkingTag, WorkingMerge, WorkingApprove, WorkingCreatePR, WorkingCreateIssue, WorkingRepo}
)

// checkWorkingKind reports why e's kind cannot be recorded as it is, or nil.
// WorkingCallPrefix starts the kind of a pack call broker's timed entry: "call:<cell>", a
// human's `yolo approve <id> --for` on one classified write, letting that cell write that
// resource unasked until the entry expires (pack-brokers.md BX-D17). Its Resource is the
// write's whole chain, CallResourceKey's form. It is always timed: a pack call never joins
// the working set by an answer (OQ-BX7).
const WorkingCallPrefix = "call:"

// IsCallKind reports whether kind is a pack call broker's timed kind with a valid cell name.
func IsCallKind(kind string) bool {
	cell, ok := strings.CutPrefix(kind, WorkingCallPrefix)
	if !ok || cell == "" || len(cell) > 64 || cell[0] < 'a' || cell[0] > 'z' {
		return false
	}
	for _, r := range cell {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// CallResourceKey is a classified write's chain, leaf first, as one Resource string:
// `type:"id"` per resource joined by " < ". Each id is Go-quoted, so any id the classifier may
// return (no NUL, CR or LF) keeps the key unambiguous.
func CallResourceKey(chain []CallResource) string {
	parts := make([]string, len(chain))
	for i, r := range chain {
		parts[i] = r.Type + ":" + strconv.Quote(r.ID)
	}
	return strings.Join(parts, " < ")
}

func checkWorkingKind(e WorkingEntry) error {
	switch {
	case slices.Contains(lastingKinds, e.Kind):
		return nil
	case slices.Contains(timedKinds, e.Kind) || IsCallKind(e.Kind):
		if !e.Timed() {
			return fmt.Errorf("a %s entry never joins the working set for good: it needs an expiry", e.Kind)
		}
		return nil
	}
	return fmt.Errorf("working-set entry kind %q is not one the store records", e.Kind)
}

// kindOf is an entry's kind, a record written before kinds existed being a branch.
func kindOf(e WorkingEntry) string {
	if e.Kind == "" {
		return WorkingBranch
	}
	return e.Kind
}

// liveKindIn is the index in r of the first entry covering workspace, repo and resource at now
// whose kind is one of kinds, or -1.
func liveKindIn(r *records, now time.Time, workspace, repo, resource string, kinds []string) int {
	for i, w := range r.working {
		if w.matches(now, workspace, repo, resource) && slices.Contains(kinds, kindOf(w)) {
			return i
		}
	}
	return -1
}

// LiveWorkingKind is the first live entry for workspace, repository and resource whose kind
// is one of kinds, ok false for none.
func (s *Store) LiveWorkingKind(workspace, repo, resource string, kinds ...string) (WorkingEntry, bool, error) {
	var out WorkingEntry
	found := false
	err := s.transaction(func(r *records, now time.Time) error {
		if i := liveKindIn(r, now, workspace, repo, resource, kinds); i >= 0 {
			out, found = r.working[i], true
		}
		return nil
	})
	return out, found, err
}

// DispatchWorkingContext authorizes one write by a live entry and runs it, in ONE transaction:
// it finds the first live entry for workspace, repository and resource whose kind is one of
// kinds, calls gate with it (the caller's re-read of its live host authority, which must not
// take the store's lock), and runs dispatch under the same lock. A revoke landing first leaves
// nothing to cover the write; one landing later waits for dispatch to return. It returns the
// entry and true when one covered the write, whatever dispatch then did.
func (s *Store) DispatchWorkingContext(ctx context.Context, workspace, repo, resource string, kinds []string,
	gate func(WorkingEntry) error, dispatch func(WorkingEntry) error) (WorkingEntry, bool, error) {
	return s.DispatchWorkingMatchContext(ctx, workspace, repo, resource, kinds, nil, gate, dispatch)
}

// DispatchWorkingMatchContext is DispatchWorkingContext over only the live entries accept
// takes (nil takes every one): the first such entry is the one gated and dispatched. A caller
// whose write some entries of a kind cover and others do not (a working-set member's cell
// that does not run, beside a timed grant on the same item) passes the rule here, so the
// choice is made under the same lock as the dispatch.
func (s *Store) DispatchWorkingMatchContext(ctx context.Context, workspace, repo, resource string, kinds []string,
	accept func(WorkingEntry) bool, gate func(WorkingEntry) error, dispatch func(WorkingEntry) error) (WorkingEntry, bool, error) {
	if workspace == "" || repo == "" || resource == "" || len(kinds) == 0 || gate == nil {
		return WorkingEntry{}, false, errors.New("a working-set write needs a workspace, a repository, a resource, its kinds and a gate")
	}
	var out WorkingEntry
	found := false
	err := s.transactionContext(ctx, func(r *records, now time.Time) error {
		i := -1
		for j, w := range r.working {
			if w.matches(now, workspace, repo, resource) && slices.Contains(kinds, kindOf(w)) && (accept == nil || accept(w)) {
				i = j
				break
			}
		}
		if i < 0 {
			return nil
		}
		if err := gate(r.working[i]); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		out, found = r.working[i], true
		if dispatch != nil {
			return dispatch(out)
		}
		return nil
	})
	return out, found, err
}

// EndWorkingResource ends every live entry for workspace, repository and resource whose kind
// is one of kinds, for reason, and returns how many it ended: a merged PR leaves the working
// set (BB-D97), and so do the timed grants on it.
func (s *Store) EndWorkingResource(workspace, repo, resource, reason string, kinds ...string) (int, error) {
	switch reason {
	case EndDeleted, EndGone, EndRevoked, EndMerged:
	default:
		return 0, fmt.Errorf("a working-set entry cannot end as %q", reason)
	}
	ended := 0
	err := s.transaction(func(r *records, now time.Time) error {
		for i := range r.working {
			w := &r.working[i]
			if !w.Live() || w.Workspace != workspace || w.Repo != repo || w.Resource != resource || !slices.Contains(kinds, kindOf(*w)) {
				continue
			}
			w.Ended, w.EndReason = now, reason
			if err := s.write("working", w.ID, w); err != nil {
				return err
			}
			ended++
		}
		return nil
	})
	return ended, err
}
