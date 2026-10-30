package brokerstore

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// working.go is a source's working set (docs/design/git-broker.md §5.1,
// docs/design/git-broker-plan.md ID-11, boundary-broker.md BB-D85): the resources a workspace
// may write to unasked. For the git source each entry is one branch of one repository, recorded
// when the broker forwarded its creation under a `create-branch` grant and the upstream
// reported it accepted. An entry is keyed by workspace, so two jails of one workspace share it,
// and it lasts across launches until the branch is deleted, found gone, or revoked.
//
// Entries are `working/w-<id>.json` records, written under the store's one transaction lock
// like requests and grants.

// WorkingBranch is the kind of a git working-set entry: a branch, named by its full ref.
const WorkingBranch = "branch"

// WorkingTag is the kind of a timed entry on a tag: a human's Allow push to <tag> for a time.
// A tag never joins the working set for good (git-broker.md GB-D18), so a WorkingTag entry
// always has an Expires.
const WorkingTag = "tag"

// The reasons a working-set entry ends, recorded in WorkingEntry.EndReason; a human's revoke
// is EndRevoked, as for a grant.
const (
	EndDeleted = "deleted" // a delete pushed through the broker, reported accepted
	EndGone    = "gone"    // a creation on its ref: the branch was deleted upstream outside the broker
)

// WorkingEntry is one resource in a workspace's working set.
type WorkingEntry struct {
	ID        string
	Workspace string // the host workspace path
	Repo      string // the repository identity (gitproto.Identity), such as github.com/o/r
	Kind      string // WorkingBranch
	Resource  string // the ref, such as refs/heads/agent/x
	Added     time.Time
	// By is what added it: "create-branch" (a creation under that user grant), a grant id,
	// or "human".
	By        string
	Ended     time.Time
	EndReason string `json:",omitempty"`
	// Expires is when a timed entry (a human's Allow push to <ref> for a time) stops covering
	// its resource; zero for an entry that stays until it is deleted or revoked.
	Expires time.Time
}

// Live reports whether the entry has not been ended. A timed entry past its Expires is still
// Live here; LiveAt is the question a coverage check asks.
func (e WorkingEntry) Live() bool { return e.Ended.IsZero() }

// LiveAt reports whether the entry covers its resource at now: not ended, and not past its
// Expires.
func (e WorkingEntry) LiveAt(now time.Time) bool {
	return e.Live() && (e.Expires.IsZero() || now.Before(e.Expires))
}

// Timed reports whether the entry expires on its own.
func (e WorkingEntry) Timed() bool { return !e.Expires.IsZero() }

func (e WorkingEntry) matches(now time.Time, workspace, repo, resource string) bool {
	return e.LiveAt(now) && e.Workspace == workspace && e.Repo == repo && e.Resource == resource
}

// upsertWorkingIn records e in the caller's transaction and returns the entry that now holds
// its resource, with added true when a new record was written. A live entry for the same
// workspace, repository and resource is kept, except that a timed one is made lasting when e
// is lasting, and made longer when e is timed and runs later.
func (s *Store) upsertWorkingIn(r *records, now time.Time, e WorkingEntry) (WorkingEntry, bool, error) {
	for i := range r.working {
		w := &r.working[i]
		// A kind is part of what an entry covers: a timed merge grant on a PR never becomes
		// the PR's own working-set entry, nor the other way round.
		if !w.matches(now, e.Workspace, e.Repo, e.Resource) || kindOf(*w) != kindOf(e) {
			continue
		}
		switch {
		case w.Timed() && !e.Timed():
			w.Expires, w.By = time.Time{}, e.By
		case w.Timed() && e.Expires.After(w.Expires):
			w.Expires = e.Expires
		default:
			return *w, false, nil
		}
		return *w, false, s.write("working", w.ID, w)
	}
	id, err := newID("w-")
	if err != nil {
		return WorkingEntry{}, false, err
	}
	e.ID, e.Added, e.Ended, e.EndReason = id, now, time.Time{}, ""
	if err := s.write("working", id, e); err != nil {
		return WorkingEntry{}, false, err
	}
	return e, true, nil
}

// AddWorking records e as live and returns it with its id and time, added true. When a live
// entry already holds the same workspace, repository and resource, it returns that one, added
// false, and writes nothing.
func (s *Store) AddWorking(e WorkingEntry) (WorkingEntry, bool, error) {
	if e.Kind == "" {
		e.Kind = WorkingBranch
	}
	if e.Workspace == "" || e.Repo == "" || e.Resource == "" || e.By == "" {
		return WorkingEntry{}, false, errors.New("a working-set entry needs a workspace, a repository, a resource and what added it")
	}
	if e.Kind == WorkingTag && !e.Timed() {
		return WorkingEntry{}, false, errors.New("a tag never joins the working set for good: its entry needs an expiry")
	}
	if err := checkWorkingKind(e); err != nil {
		return WorkingEntry{}, false, err
	}
	var out WorkingEntry
	added := false
	err := s.transaction(func(r *records, now time.Time) error {
		var err error
		out, added, err = s.upsertWorkingIn(r, now, e)
		return err
	})
	return out, added, err
}

// LiveWorking is the live entry for workspace, repository and resource, ok false for none.
func (s *Store) LiveWorking(workspace, repo, resource string) (WorkingEntry, bool, error) {
	var out WorkingEntry
	found := false
	err := s.transaction(func(r *records, now time.Time) error {
		for _, w := range r.working {
			if w.matches(now, workspace, repo, resource) {
				out, found = w, true
				return nil
			}
		}
		return nil
	})
	return out, found, err
}

// EndWorking ends the live entry id for reason (EndDeleted, EndGone or EndRevoked). An entry
// already ended is left as it ended.
func (s *Store) EndWorking(id, reason string) error {
	switch reason {
	case EndDeleted, EndGone, EndRevoked, EndMerged:
	default:
		return fmt.Errorf("a working-set entry cannot end as %q", reason)
	}
	return s.transaction(func(r *records, now time.Time) error {
		for i := range r.working {
			w := &r.working[i]
			if w.ID != id {
				continue
			}
			if !w.Live() {
				return nil
			}
			w.Ended, w.EndReason = now, reason
			return s.write("working", w.ID, w)
		}
		return refusal(ErrUnknownID, "working-set entry %q not found", id)
	})
}

// Working is every entry the store holds, live and recently ended, in the order added.
func (s *Store) Working() ([]WorkingEntry, error) {
	var out []WorkingEntry
	err := s.transaction(func(r *records, _ time.Time) error {
		out = append(out, r.working...)
		return nil
	})
	sortWorking(out)
	return out, err
}

func sortWorking(ws []WorkingEntry) {
	sort.SliceStable(ws, func(i, j int) bool {
		if !ws[i].Added.Equal(ws[j].Added) {
			return ws[i].Added.Before(ws[j].Added)
		}
		return ws[i].ID < ws[j].ID
	})
}

// RevokeIn is Revoke within one workspace. id "all" with a workspace ends the live grants that
// workspace holds (Grant.Workspace) and its working set; with no workspace it ends
// every live grant and leaves every working set (BB-D86: `--revoke all` is grants, and
// `--revoke all --workspace <path>` is that workspace's grants and working set). A `g-` or
// `w-` id ends that one record, and takes no workspace. It returns how many live records it
// ended, so a caller can say when a workspace matched nothing.
func (s *Store) RevokeIn(id, workspace string) (int, error) {
	if workspace != "" && id != "all" {
		return 0, errors.New("a workspace narrows only `all`; revoke one grant or entry by its id alone")
	}
	ended := 0
	if workspace != "" {
		workspace = filepath.Clean(workspace)
	}
	err := s.transaction(func(r *records, now time.Time) error {
		found := id == "all"
		for i := range r.grants {
			g := &r.grants[i]
			if id != "all" && g.ID != id {
				continue
			}
			if workspace != "" && !SameWorkspace(g.Workspace, workspace) {
				continue
			}
			found = true
			if g.Ended.IsZero() {
				ended++
				g.Ended = now
				g.EndReason = EndRevoked
				if err := s.write("grants", g.ID, g); err != nil {
					return err
				}
			}
		}
		for i := range r.working {
			w := &r.working[i]
			switch {
			case id == "all" && (workspace == "" || filepath.Clean(w.Workspace) != workspace):
				continue
			case id != "all" && w.ID != id:
				continue
			}
			found = true
			if w.Live() {
				ended++
				w.Ended, w.EndReason = now, EndRevoked
				if err := s.write("working", w.ID, w); err != nil {
					return err
				}
			}
		}
		if !found {
			return refusal(ErrUnknownID, "grant or working-set entry %q not found", id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return ended, nil
}
