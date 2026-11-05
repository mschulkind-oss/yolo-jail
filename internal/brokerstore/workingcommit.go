package brokerstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// requestRecord keeps an answer's multi-resource commit in the request file. A
// reader sees either the old pending request or its complete committed answer;
// per-entry files written before or after that rename remain independently editable.
type requestRecord struct {
	Request
	WorkingEntries []WorkingEntry `json:"working_entries,omitempty"`
}

func readOptionalRecords[T any](dir, kind string, out *[]T) error {
	path := filepath.Join(dir, kind)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return readRecords(path, out)
}

func readStoreRecords(dir string, r *records) error {
	var requests []requestRecord
	if err := readOptionalRecords(dir, "requests", &requests); err != nil {
		return err
	}
	for _, record := range requests {
		q := record.Request
		q.workingEntries = record.WorkingEntries
		r.requests = append(r.requests, q)
	}
	if err := readOptionalRecords(dir, "grants", &r.grants); err != nil {
		return err
	}
	if err := readOptionalRecords(dir, "working", &r.working); err != nil {
		return err
	}
	r.standaloneWorking = make(map[string]int, len(r.working))
	for _, e := range r.working {
		r.standaloneWorking[e.ID] = e.Revision
	}
	return overlayCommittedWorking(r)
}

func overlayCommittedWorking(r *records) error {
	byID := make(map[string]int, len(r.working))
	for i, e := range r.working {
		byID[e.ID] = i
	}
	for _, q := range r.requests {
		if q.State != "allowed" || q.Decided.IsZero() || (q.Answer != AnswerAdd && q.Answer != AnswerFor) {
			continue
		}
		seen := make(map[string]bool, len(q.workingEntries))
		for _, e := range q.workingEntries {
			if seen[e.ID] || !validID(e.ID, "w-") || !SameWorkspace(e.Workspace, q.Workspace) || e.Repo == "" || e.Resource == "" || e.By == "" || e.Revision < 1 || checkWorkingKind(e) != nil ||
				(q.Answer == AnswerAdd && (e.Timed() || !q.Until.IsZero())) || (q.Answer == AnswerFor && (e.Expires.IsZero() || !e.Expires.Equal(q.Until))) {
				return fmt.Errorf("invalid committed working entry in request %s; repair the host broker store record before retrying", q.ID)
			}
			seen[e.ID] = true
			if i, ok := byID[e.ID]; ok {
				if r.working[i].Revision < e.Revision {
					r.working[i] = e
				}
			} else {
				byID[e.ID] = len(r.working)
				r.working = append(r.working, e)
			}
		}
	}
	return nil
}

func validWorkingDecisionEntry(q Request, e WorkingEntry, add bool) error {
	if e.Workspace == "" || !SameWorkspace(e.Workspace, q.Workspace) || e.Repo == "" || e.Resource == "" || e.By == "" {
		return errors.New("a per-resource answer needs the request's host workspace, a repository, a resource and what added it")
	}
	if e.Kind == "" {
		e.Kind = WorkingBranch
	}
	if e.Kind == WorkingTag && add {
		return errors.New("a tag never joins the working set for good: its entry needs an expiry")
	}
	return checkWorkingKind(e)
}

// planWorkingDecision computes the complete resulting coverage without writing any
// working record. Unchanged entries stay in their original standalone/committed form.
func planWorkingDecision(r *records, now time.Time, q Request, entries []WorkingEntry, expires time.Time, add bool) ([]WorkingEntry, []WorkingEntry, error) {
	working := append([]WorkingEntry(nil), r.working...)
	var out, changed []WorkingEntry
	for _, e := range entries {
		e.Expires = expires
		if e.Kind == "" {
			e.Kind = WorkingBranch
		}
		if err := validWorkingDecisionEntry(q, e, add); err != nil {
			return nil, nil, err
		}
		found := false
		for i := range working {
			w := &working[i]
			if !w.matches(now, e.Workspace, e.Repo, e.Resource) || kindOf(*w) != kindOf(e) {
				continue
			}
			found = true
			switch {
			case w.Timed() && !e.Timed():
				w.Expires, w.By = time.Time{}, e.By
			case w.Timed() && e.Expires.After(w.Expires):
				w.Expires = e.Expires
			default:
				out = append(out, *w)
				continue
			}
			w.Revision++
			if w.Revision < 1 {
				w.Revision = 1
			}
			changed = append(changed, *w)
			out = append(out, *w)
			break
		}
		if found {
			continue
		}
		id, err := newID("w-")
		if err != nil {
			return nil, nil, err
		}
		e.ID, e.Added, e.Ended, e.EndReason, e.Revision = id, now, time.Time{}, "", 1
		working = append(working, e)
		changed = append(changed, e)
		out = append(out, e)
	}
	return out, changed, nil
}

func pruneWorkingFile(dir, id string) error {
	err := os.Remove(filepath.Join(dir, "working", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
