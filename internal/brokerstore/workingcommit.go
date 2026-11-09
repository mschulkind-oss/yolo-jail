package brokerstore

import (
	"context"
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
	// A lasting entry holds a branch, a PR or an issue; every other kind is timed. No answer
	// records one now that `--add` is removed (GB-D28), so this guards the store's own seam.
	if add && e.Kind != WorkingBranch && e.Kind != WorkingPR && e.Kind != WorkingIssue {
		return fmt.Errorf("a %s entry never joins the working set for good: its entry needs an expiry", e.Kind)
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

// priorRecord is a record's bytes before an answer wrote it; nil bytes mean the record did not
// exist, so undoing the write removes it.
type priorRecord struct {
	kind, id string
	bytes    []byte
}

// readPriorRecord reads the record an answer is about to write, so the write can be undone.
func (s *Store) readPriorRecord(kind, id string) (priorRecord, error) {
	if !validID(id, recordPrefix(kind)) {
		return priorRecord{}, errors.New("invalid record identity")
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, kind, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return priorRecord{kind: kind, id: id}, nil
	}
	if err != nil {
		return priorRecord{}, err
	}
	if b == nil {
		b = []byte{}
	}
	return priorRecord{kind: kind, id: id, bytes: b}, nil
}

// restore puts the record back as it was: its prior bytes, or no record.
func (s *Store) restore(p priorRecord) error {
	path := filepath.Join(s.Dir, p.kind, p.id+".json")
	if p.bytes == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return s.syncDirectory(filepath.Join(s.Dir, p.kind))
	}
	f, err := os.CreateTemp(filepath.Join(s.Dir, p.kind), ".write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(p.bytes); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return s.syncDirectory(filepath.Join(s.Dir, p.kind))
}

// undoWorkingAnswer takes back an answer whose committed request was written but one of whose
// entry records failed (cause): it restores every entry record the answer wrote, newest first,
// then writes the request back as it was before the answer. The caller holds the store's lock,
// so no reader saw the partial answer. The decision already in the audit log is followed by a
// decision-withdrawn event. When the undo itself fails, the error says the store needs a look.
func (s *Store) undoWorkingAnswer(answered, pending Request, written []priorRecord, cause error) error {
	var undoErr error
	for i := len(written) - 1; i >= 0; i-- {
		if err := s.restore(written[i]); err != nil && undoErr == nil {
			undoErr = fmt.Errorf("restore working record %s: %w", written[i].id, err)
		}
	}
	if undoErr == nil {
		if err := s.write("requests", pending.ID, &pending); err != nil {
			undoErr = fmt.Errorf("put request %s back to pending: %w", pending.ID, err)
		}
	}
	if undoErr != nil {
		return fmt.Errorf("request %s: an entry record could not be written (%w), and undoing the answer failed (%v); "+
			"the answer may be stored for only some of its resources. Next: run `yolo approve` on the host to inspect "+
			"the request and its working-set entries, and revoke any entry it lists with `yolo approve --revoke w-<id>`",
			answered.ID, cause, undoErr)
	}
	_ = s.audit(context.Background(), "decision-withdrawn", pending, Grant{})
	return fmt.Errorf("request %s was not answered: an entry record could not be written (%w); no resource was stored "+
		"and the request is still pending. Next: fix the host broker store (see the error), then answer it again", answered.ID, cause)
}

func pruneWorkingFile(dir, id string) error {
	err := os.Remove(filepath.Join(dir, "working", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
