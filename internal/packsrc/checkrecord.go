package packsrc

// checkrecord.go is a patched fork's CHECK RECORD and its lock (docs/design/patched-forks.md §6.2,
// PF-D9, PF-D22): one record per OWNER KEY in the pack store — the check stamp and what the last
// check read, the last check's commit and the walk's list with its sequence number, the good
// build, and one outcome per entry of the list.
//
// THE OWNER KEY (a term coined in the design, PF-D22) is `<pack>/<bin>` for a patched fork and
// `<pack>/<name>` for a patched extension (patched-extensions.md), so one record and one lock
// serve both routes; nothing here knows which it is keyed for.
//
// MACHINE-LOCAL BY DESIGN: the record never leaves this machine and nothing writes it to the fork
// lock (§6.5). It is written only under its own flock, through a temp file and a rename, and read
// without the lock, so a reader sees one whole record or the previous one.
//
// Layout, beside the store's other per-repository state (store.go):
//
//	<PacksDir>/checks/<owner-slug>.json        the record
//	<PacksDir>/locks/check-<owner-slug>.lock   its flock
//
// LOCK ORDER (§6.6, PF-D17): the build's own lock, then this record lock, then a mirror lock;
// nothing waits for an earlier lock while it holds a later one. This file takes only the record
// lock; CheckPatched takes the mirror lock inside it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CheckRecordSchema is the record's schema version. A record of another schema is unreadable to
// this build: recovered from the capture store, as a lost one is (§6.2), never misread.
const CheckRecordSchema = 1

// CheckRecord is one owner key's check record.
type CheckRecord struct {
	Schema int `json:"schema"`
	// Owner is the owner key the record is for.
	Owner string `json:"owner"`
	// CheckedAt is the CHECK STAMP (the design's term, §4.2): when the last check was ATTEMPTED,
	// in unix seconds, whatever its outcome. 0 when no check has run.
	CheckedAt int64 `json:"checked_at,omitempty"`
	// Read is what the last check read. A launch that finds any of it changed checks at once.
	Read CheckInputs `json:"read"`
	// Seq is the sequence number of the last check that finished, 0 before any.
	Seq int64 `json:"seq,omitempty"`
	// Check is what the last finished check found; nil before any.
	Check *CheckFound `json:"check,omitempty"`
	// Good is the GOOD BUILD (§6.1): this machine's record of the inputs of the last build it
	// admitted. nil when there is none.
	Good *GoodBuild `json:"good,omitempty"`
	// Outcomes holds one outcome per entry of the walk's list, keyed as each kind says.
	Outcomes []EntryOutcome `json:"outcomes,omitempty"`
}

// CheckInputs is what a check reads, the half of a record that decides whether a check is due
// before its interval (§4.2): the repository, subdirectory, ref and follow rule, and the commit the
// series names as its base, since the walk's list holds only versions that contain it.
type CheckInputs struct {
	Repo   string `json:"repo"`
	Subdir string `json:"subdir,omitempty"`
	Ref    string `json:"ref"`
	Follow string `json:"follow"`
	Base   string `json:"base"`
}

// CheckFound is what a finished check found in the upstream's mirror.
type CheckFound struct {
	// Seq is this check's sequence number.
	Seq int64 `json:"seq"`
	// At is when it finished, in unix seconds.
	At int64 `json:"at"`
	// RefKind is what the ref named: "branch", "tag" or "commit". "" when it named nothing usable.
	RefKind string `json:"ref_kind,omitempty"`
	// Tip is the commit the ref resolved to: a branch's head, a tag's or the commit itself.
	Tip string `json:"tip,omitempty"`
	// List is the WALK'S LIST (§6.4), newest first, not yet cut at the good build: under
	// `follow: "head"` the branch's tip, then the version tags merged into the branch that contain
	// the series' base by precedence; for a tag or commit ref that commit alone. AboveGood cuts it.
	List []ListEntry `json:"list,omitempty"`
	// BaseOnBranch is whether the followed branch contains the series' base, which a first
	// advance's fallback needs (§6.4). Always false for a tag or commit ref, which gets no fallback.
	BaseOnBranch bool `json:"base_on_branch,omitempty"`
	// Fetched is whether this check's fetch ran and succeeded; FetchErr is the fetch that failed,
	// one line. A check that needed no fetch (the mirror's own stamp was fresh) has neither.
	Fetched  bool   `json:"fetched,omitempty"`
	FetchErr string `json:"fetch_err,omitempty"`
	// Problem is why the check names no candidate at all, one line with its next step: a ref that
	// is HEAD or an abbreviated commit, one that names nothing, a base the upstream lacks.
	Problem string `json:"problem,omitempty"`
}

// ListEntry is one entry of the walk's list.
type ListEntry struct {
	Commit string `json:"commit"`
	// Tag is the version tag's short name, "" for a branch tip no version tag names.
	Tag string `json:"tag,omitempty"`
	// Version is the tag's version under the follow rule, "" when Tag is not one.
	Version string `json:"version,omitempty"`
	// Tip marks the branch's tip under `follow: "head"`.
	Tip bool `json:"tip,omitempty"`
}

// Label is the entry as a line names it: its tag and short commit, or the short commit alone.
func (e ListEntry) Label() string {
	if e.Tag != "" {
		return e.Tag + " (" + shortCommit(e.Commit) + ")"
	}
	return shortCommit(e.Commit)
}

// GoodBuild is the good build's inputs (§6.1).
type GoodBuild struct {
	Commit string `json:"commit"`
	// Tag and Version are the version it runs: its tag under a release rule, or the newest
	// version its commit contains. "" when there is none.
	Tag     string `json:"tag,omitempty"`
	Version string `json:"version,omitempty"`
	// Series is the series digest, Recipe the recipe hash, Tree the patched tree.
	Series string `json:"series"`
	Recipe string `json:"recipe"`
	Tree   string `json:"tree,omitempty"`
	// Patches is how many patches the series held.
	Patches int `json:"patches,omitempty"`
	// Seq is the sequence number of the check its candidate came from, which the move's
	// compare-and-swap reads (§6.1).
	Seq int64 `json:"seq"`
	// Entry is its capture store entry's key.
	Entry string `json:"entry,omitempty"`
	// At is when it was admitted, in unix seconds.
	At int64 `json:"at,omitempty"`
}

// The kinds an EntryOutcome records.
const (
	// OutcomeConflict is a replay that stopped on a member with conflicting paths (did not apply),
	// keyed by (commit, series, yolo version, git version): no launch replays that key again.
	OutcomeConflict = "conflict"
	// OutcomeApplies is a replay that was clean. Informational: it holds back nothing, since the
	// advance replays again to build (an explicit act records it so `yolo pack status` can say so).
	OutcomeApplies = "applies"
	// OutcomeBuildFailed is a build of the entry that failed, keyed by (commit, series, recipe,
	// yolo version), backed off while a good build serves.
	OutcomeBuildFailed = "build-failed"
)

// EntryOutcome is one entry's outcome.
type EntryOutcome struct {
	Commit string `json:"commit"`
	Kind   string `json:"kind"`
	// The key's other parts: the series digest, the yolo version, the git version and, for a
	// build, the recipe.
	Series string `json:"series"`
	Yolo   string `json:"yolo"`
	Git    string `json:"git,omitempty"`
	Recipe string `json:"recipe,omitempty"`
	// Member and Paths are a conflict's: the member that stopped and its conflicting paths.
	Member string   `json:"member,omitempty"`
	Paths  []string `json:"paths,omitempty"`
	// Upstream lists the members a clean replay found already upstream (their pick changed
	// nothing), to be dropped from the series.
	Upstream []string `json:"upstream,omitempty"`
	// Error, Count and At are a build failure's: the error, the failures so far, and the last.
	Error string `json:"error,omitempty"`
	Count int    `json:"count,omitempty"`
	At    int64  `json:"at,omitempty"`
}

// checkRecordPath is where owner's record lives in the store at dir.
func checkRecordPath(dir, owner string) string {
	return filepath.Join(dir, "checks", mirrorSlug(owner)+".json")
}

// CheckRecordPath is where owner's check record lives in this store.
func (s *Store) CheckRecordPath(owner string) string { return checkRecordPath(s.Dir, owner) }

// ErrNoCheckRecord marks a record that does not exist: no check has run on this machine.
var ErrNoCheckRecord = errors.New("no check record")

// LoadCheckRecord reads owner's record without its lock. A missing record is ErrNoCheckRecord;
// one that cannot be read, or is another schema or another owner's, is an error naming the file.
func (s *Store) LoadCheckRecord(owner string) (*CheckRecord, error) {
	p := s.CheckRecordPath(owner)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoCheckRecord
	}
	if err != nil {
		return nil, fmt.Errorf("reading the check record %s: %w", p, err)
	}
	var r CheckRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("reading the check record %s: %w", p, err)
	}
	if r.Schema != CheckRecordSchema {
		return nil, fmt.Errorf("the check record %s is schema %d, which this yolo does not read (it "+
			"reads %d)", p, r.Schema, CheckRecordSchema)
	}
	if r.Owner != owner {
		return nil, fmt.Errorf("the check record %s is %q's, not %q's", p, r.Owner, owner)
	}
	return &r, nil
}

// save writes r through a temp file and a rename. The caller holds the record's lock.
func (s *Store) saveCheckRecord(r *CheckRecord) error {
	p := s.CheckRecordPath(r.Owner)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	r.Schema = CheckRecordSchema
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// lockCheckRecord takes owner's record lock and returns its release.
func (s *Store) lockCheckRecord(owner string, waiting func(string)) (func(), error) {
	return flockPath(filepath.Join(s.Dir, "locks", "check-"+mirrorSlug(owner)+".lock"),
		"the check record of "+owner, waiting)
}

// WithCheckRecord is a read-modify-write of owner's record under its lock. fn gets the record as
// it stands under the lock — a fresh one, Owner set, when none exists or the one there cannot be
// read (readErr says which) — and reports whether it changed it; nothing is written when it did
// not. fn may call save to write an intermediate state while it still holds the lock (the check
// writes its attempt before its fetch).
func (s *Store) WithCheckRecord(owner string, waiting func(string),
	fn func(r *CheckRecord, readErr error, save func() error) (bool, error)) error {
	unlock, err := s.lockCheckRecord(owner, waiting)
	if err != nil {
		return err
	}
	defer unlock()
	r, readErr := s.LoadCheckRecord(owner)
	if r == nil {
		r = &CheckRecord{Schema: CheckRecordSchema, Owner: owner}
	}
	if errors.Is(readErr, ErrNoCheckRecord) {
		readErr = nil
	}
	changed, err := fn(r, readErr, func() error { return s.saveCheckRecord(r) })
	if err != nil || !changed {
		return err
	}
	return s.saveCheckRecord(r)
}

// CheckDue reports whether a check is due for a record that last read r.Read, and why, as a short
// phrase: never checked, what it reads changed, or the interval passed since the last attempt. A
// stamp in the future (a clock set back) is due, the answer that checks. r may be nil.
func CheckDue(r *CheckRecord, in CheckInputs, now time.Time, interval time.Duration) (bool, string) {
	if interval <= 0 {
		interval = BranchRefreshInterval
	}
	switch {
	case r == nil || r.CheckedAt == 0:
		return true, "never checked on this machine"
	case r.Read != in:
		return true, "what it follows changed since the last check"
	}
	age := now.Sub(time.Unix(r.CheckedAt, 0))
	if age < 0 || age >= interval {
		return true, "the last check was over " + interval.String() + " ago"
	}
	return false, ""
}

// NextCheck is when the next check falls due under the throttle, for a status line.
func (r *CheckRecord) NextCheck(interval time.Duration) time.Time {
	if interval <= 0 {
		interval = BranchRefreshInterval
	}
	return time.Unix(r.CheckedAt, 0).Add(interval)
}
