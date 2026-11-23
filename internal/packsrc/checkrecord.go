package packsrc

// checkrecord.go is a patched fork's CHECK RECORD and its lock (docs/reference/patched-forks.md §6.2,
// PF-D9, PF-D22): one record per OWNER KEY in the pack store — the check stamp and what the last
// check read, the last check's commit and the walk's list with its sequence number, the good
// build, and one outcome per entry of the list.
//
// THE OWNER KEY (a term coined in the design, PF-D22) is `<pack>/<bin>` for a patched fork and
// `<pack>/<name>` for a patched extension (pi-extensions.md), so one record and one lock
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

// PatchFailure is a classified failure while applying a named patch-series member. It is kept
// separately from replay's broader operational errors so callers can refuse only genuine patch
// application failures.
type PatchFailure struct {
	Owner  string      `json:"owner,omitempty"`
	Inputs CheckInputs `json:"inputs,omitempty"`
	Series string      `json:"series,omitempty"`
	Target ListEntry   `json:"target"`
	Kind   string      `json:"kind"`
	Member string      `json:"member"`
	Paths  []string    `json:"paths,omitempty"`
	Detail string      `json:"detail,omitempty"`
	Log    string      `json:"log,omitempty"`
	Seq    int64       `json:"seq,omitempty"`
	// Retry is a build failure's retry command (`yolo capture <arg>`), which its error block's Repair
	// names. Never recorded: a build failure is read from the record's outcomes, never kept here.
	Retry string `json:"-"`
	// Cause is, for a PatchFailureBuild, which of YOLO'S OWN BUILD STEPS stopped the newer build
	// (PD-NF1, the maintainer's ruling of 2026-10-16: docs/design/program-delivery.md): one of the
	// BuildCause constants, "" when the build line ran and failed. It names the error block's head and
	// its repair.
	Cause string `json:"cause,omitempty"`
}

// The BuildCause values name YOLO'S OWN BUILD STEP that stopped a newer build (PatchFailure.Cause,
// EntryOutcome.Cause; PD-NF1). Each stops the act as a failed build does: the older build runs only
// under a recovery hatch or an `agent_updates` hold.
const (
	// BuildCauseJail: the build jail did not start, or stopped before its build line ran
	// (docs/reference/patched-forks.md PF-D21, superseded). Recorded, with the back-off.
	BuildCauseJail = "jail"
	// BuildCauseLock: the wait for another build of the same candidate ran out (forkBuildWaitBound).
	BuildCauseLock = "lock"
	// BuildCauseRuntime: the runtime cannot start a build jail now, as Apple Container cannot beside
	// a running jail, or cannot say whether one runs.
	BuildCauseRuntime = "runtime"
	// BuildCauseWorkspace: an earlier build jail of the candidate is not yet known gone, so its
	// staging is kept and no build starts.
	BuildCauseWorkspace = "workspace"
	// BuildCauseReplay: the series could not be replayed onto the candidate — an APPLY ERROR (a
	// failed git command, an object that could not be fetched, a replay that ran out of time, or a
	// host git older than the replay needs), in the walk of the check's list or in the build's own
	// replay (PF-D45, superseded). Read from CheckRecord.ApplyErr while the check that met it is the
	// last.
	BuildCauseReplay = "replay"
)

// PatchFailureBuild is the Kind of a FAILED NEWER BUILD (OQ-PPX3, the maintainer's ruling of
// 2026-10-16): the newest candidate took the series but its build failed while a good build of the
// series as it stands is on this machine. It is never written to CheckRecord.PatchFailure:
// CurrentBuildFailure reads it from the record's outcomes, and it rides the refusal a patch failure
// does, so no act falls back to the older build unless a recovery hatch or an `agent_updates` hold
// says so.
const PatchFailureBuild = "build"

// Summary is the few words a refusal's last line names f by, after the error block said it whole:
// "patch application failure", or "newer build failure" for a PatchFailureBuild.
func (f *PatchFailure) Summary() string {
	if f != nil && f.Kind == PatchFailureBuild {
		return "newer build failure"
	}
	return "patch application failure"
}

// BuildHead is what a PatchFailureBuild's head says stopped it, before "upstream <target>": "the
// build failed at" for a build line that ran and failed, "the patch series could not be replayed
// onto" for an apply error, and "yolo could not build" for any other of yolo's own steps (Cause).
func (f *PatchFailure) BuildHead() string {
	switch f.Cause {
	case "":
		return "the build failed at"
	case BuildCauseReplay:
		return "the patch series could not be replayed onto"
	}
	return "yolo could not build"
}

func (f *PatchFailure) Error() string {
	if f == nil {
		return ""
	}
	// The target's label already carries its commit (ListEntry.Label), so it is said once.
	if f.Kind == PatchFailureBuild {
		head := f.BuildHead() + " upstream " + f.Target.Label()
		if f.Detail == "" {
			return head
		}
		return fmt.Sprintf("%s: %s", head, f.Detail)
	}
	if f.Member == "" {
		return "patch application failed at " + f.Target.Label()
	}
	return fmt.Sprintf("patch application failed at %s: %s", f.Target.Label(), f.Member)
}

// CheckRecord is one owner key's check record.
type CheckRecord struct {
	Fresh  *CheckStart `json:"fresh_start,omitempty"`
	Schema int         `json:"schema"`
	// Owner is the owner key the record is for.
	Owner string `json:"owner"`
	// CheckedAt is the CHECK STAMP (the design's term, §4.2): when the last check was ATTEMPTED,
	// in unix seconds, whatever its outcome. 0 when no check has run.
	CheckedAt int64 `json:"checked_at,omitempty"`
	// Read is what the last FINISHED check read, written with Check and never with the attempt's
	// stamp alone, so the two always describe one check. A launch that finds any of it changed
	// checks at once.
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
	// ApplyErr is the last walk of a check's list that ended in an APPLY ERROR (§5.2): not an
	// outcome (PF-D9), since it settles no entry, but the check it was of, so a launch that reads
	// that same check while a good build serves replays nothing again and the next check retries
	// it (§6.2's apply-error row, PF-D45). nil once a walk of the list settles what it reached.
	ApplyErr *ApplyError `json:"apply_err,omitempty"`
	// PatchFailure records one currently relevant application failure independently from the
	// last-build outcome and from whether a warning was already shown.
	PatchFailure *PatchFailure `json:"patch_failure,omitempty"`
	// PatchBranch is the last patch branch a check resolved (patchbranch.go): what a patch branch's series is read from with
	// no git (PatchBranchSeries), kept apart from Check so a later check's problem does not lose it.
	PatchBranch *PatchBranchResolved `json:"patch_branch,omitempty"`
	// WalkBack is the WALK-BACK HOLD (docs/reference/patched-forks.md §8.6): set when a run under
	// YOLO_ALLOW_OLDER_UPSTREAM admitted an older release because the newest did not take the series or
	// did not build. While its newest entry is still the newest of the list, the good build it admitted
	// runs, held, with no refusal (WalkBackHolds). A build any other run admits clears it.
	WalkBack *WalkBackHold `json:"walk_back,omitempty"`
}

// WalkBackHold is the record's walk-back hold: the newest list entry the series did not take, and
// the good build the walk-back admitted in its place.
type WalkBackHold struct {
	// Newest is the list entry that did not take the series or did not build.
	Newest ListEntry `json:"newest"`
	// Kind is why: OutcomeConflict, or OutcomeBuildFailed.
	Kind string `json:"kind"`
	// Good is the commit of the build the walk-back admitted, Series that build's series digest.
	Good   string `json:"good"`
	Series string `json:"series"`
}

// WalkBackHolds is the record's walk-back hold when it still holds for a check of in and series:
// its newest entry is still the newest on the last check's list, and the good build is the one it
// admitted, of this series. nil otherwise.
func (r *CheckRecord) WalkBackHolds(in CheckInputs, series string) *WalkBackHold {
	if r == nil || r.WalkBack == nil || !r.Answers(in) || len(r.Check.List) == 0 || r.Good == nil {
		return nil
	}
	h := r.WalkBack
	if r.Check.List[0].Commit != h.Newest.Commit || r.Good.Commit != h.Good || r.Good.Series != series ||
		h.Series != series {
		return nil
	}
	return h
}

// Line is the hold's few words: "upstream v1.4.0 does not take the series".
func (h *WalkBackHold) Line() string {
	what := "does not take the series"
	if h.Kind == OutcomeBuildFailed {
		what = "does not build with the series"
	}
	return "upstream " + h.Newest.ShortLabel() + " " + what
}

// ApplyError is a walk's apply error as the record keeps it: the check whose list was walked, and
// the error, one line.
type ApplyError struct {
	Seq    int64                `json:"seq"`
	Error  string               `json:"error"`
	Legacy *LegacyReplayAttempt `json:"legacy_replay,omitempty"`
}

// LegacyReplayAttempt records one completed, unavailable attempt to recover concrete evidence
// from an opaque schema-1 ApplyError. Only unavailable results are retained for retry identity.
type LegacyReplayAttempt struct {
	Seq    int64       `json:"seq"`
	Inputs CheckInputs `json:"inputs"`
	Series string      `json:"series"`
	Target ListEntry   `json:"target"`
	Yolo   string      `json:"yolo"`
	Git    string      `json:"git,omitempty"`
	State  string      `json:"state"`
	Detail string      `json:"detail,omitempty"`
}

// ApplyErrAtLastCheck is the apply error the last walk of the last check's list ended in, or nil:
// one recorded for an earlier check is the next check's to retry, and has been.
func (r *CheckRecord) ApplyErrAtLastCheck() *ApplyError {
	if r == nil || r.ApplyErr == nil || r.ApplyErr.Seq != r.Seq {
		return nil
	}
	return r.ApplyErr
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
	// PatchBranch is a PATCH BRANCH's address as written (patchbranch.go), "" for a patch series: an edit to it makes
	// a check due at once, as an edit to the ref does. A patch branch's Base is "": the check resolves it.
	PatchBranch string `json:"patch_branch,omitempty"`
	// PatchBranchRepo is the filesystem identity of the resolved repository for a relative patch branch. It
	// distinguishes clones while staying the same through a bind mount's host and jail path spellings.
	// It is empty for explicit locators, whose address already names their repository.
	PatchBranchRepo string `json:"patch_branch_repo,omitempty"`
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
	// NoVersion is set when a release rule finds no version tag on the followed branch at all
	// (PF-D60): the list is empty, as it is when every version predates the series' base, so a first
	// advance builds the base, and this says why, one line naming what the rule reads. NoVersionLine
	// completes it with where the fork stays. "" otherwise.
	NoVersion string `json:"no_version,omitempty"`
	// PatchBranch is what this check resolved a patch branch to (patchbranch.go), nil for a patch series or when the
	// patch branch could not be resolved; the record keeps the last one in CheckRecord.Patch branch.
	PatchBranch *PatchBranchResolved `json:"patch_branch,omitempty"`
	// lockHeld is a NoWait store's check that found the mirror's lock held (an error matching
	// ErrLockHeld): it fetched and listed nothing, and CheckPatched records none of it. Never written.
	lockHeld error
	// unchecked is why the launch deadline ended the check before it asked the upstream anything.
	unchecked string
}

// NoVersionLine is the line that says, once at a launch and in `yolo pack status`, why a release
// rule follows nothing here (PF-D60): the note, that what runs stays at what at names until a tag
// appears or `follow` changes, and the spelling that follows the branch's commits instead.
func (f *CheckFound) NoVersionLine(at string) string {
	return f.NoVersion + ", so what runs stays at " + at + " until a tag appears or `follow` changes — " +
		"`follow: \"head\"` follows the branch's commits"
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

// Label is the entry as a line names it: its tag and short commit, or the short commit alone; and
// an npm version, which is its own tag (NpmListEntry), once.
func (e ListEntry) Label() string {
	if e.Tag != "" && e.Tag == e.Commit {
		return e.Tag
	}
	if e.Tag != "" {
		return e.Tag + " (" + shortCommit(e.Commit) + ")"
	}
	return shortCommit(e.Commit)
}

// ShortLabel is the entry as a short line names it: its tag, or its short commit.
func (e ListEntry) ShortLabel() string {
	if e.Tag != "" {
		return e.Tag
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
	// PatchBranch is the patch branch's label ("<ref>@<short commit>", SeriesPatchBranch.Label) when the build merged a
	// patch branch, "" for a patch series.
	PatchBranch string `json:"patch_branch,omitempty"`
	// Seq is the sequence number of the check its candidate came from, which the move's
	// compare-and-swap reads (§6.1).
	Seq int64 `json:"seq"`
	// Read is what that check read (the record's Read when the advance took its candidate), so a
	// later list read under another ref or follow rule is cut at this build's commit rather than by
	// its version (Candidates, PF-D34). The advance writes it; nil, as an older record has it,
	// reads as the same rule.
	Read *CheckInputs `json:"read,omitempty"`
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
	// Upstream lists the members a clean replay found already upstream (their commit changed
	// something of its own and the pick changed nothing), to be dropped from the series.
	Upstream []string `json:"upstream,omitempty"`
	// Error, Count and At are a build failure's: the error, the failures so far, and the last.
	Error string `json:"error,omitempty"`
	Count int    `json:"count,omitempty"`
	At    int64  `json:"at,omitempty"`
	// Cause is a build failure's, when one of yolo's own build steps stopped it rather than its build
	// line (a BuildCause constant, PD-NF1); "" for a build line that ran and failed.
	Cause string `json:"cause,omitempty"`
}

// RekeySeries moves the good build, typed failure, unavailable legacy attempt and matching outcomes
// to a new series digest (PF-D62). The good build also requires the same recipe; outcome recipes
// move only when they match oldRecipe. The two digests name the same files, so nothing is wrong.
// It reports whether anything moved.
func (r *CheckRecord) RekeySeries(oldSeries, oldRecipe, newSeries, newRecipe string) bool {
	changed := false
	if g := r.Good; g != nil && g.Series == oldSeries && g.Recipe == oldRecipe {
		g.Series, g.Recipe = newSeries, newRecipe
		changed = true
	}
	for i := range r.Outcomes {
		o := &r.Outcomes[i]
		if o.Series != oldSeries {
			continue
		}
		o.Series = newSeries
		if o.Recipe == oldRecipe {
			o.Recipe = newRecipe
		}
		changed = true
	}
	if f := r.PatchFailure; f != nil && f.Series == oldSeries {
		f.Series = newSeries
		changed = true
	}
	if h := r.WalkBack; h != nil && h.Series == oldSeries {
		h.Series = newSeries
		changed = true
	}
	if apply := r.ApplyErr; apply != nil && apply.Legacy != nil && apply.Legacy.Series == oldSeries {
		apply.Legacy.Series = newSeries
		changed = true
	}
	return changed
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
	return s.flock(filepath.Join(s.Dir, "locks", "check-"+mirrorSlug(owner)+".lock"),
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
	case r.Check == nil:
		// An attempt with no answer: the check died in its git (a kill, a closed terminal) before
		// it found anything, so there is nothing for the interval to keep serving.
		return true, "no check has finished on this machine"
	case r.Read != in:
		return true, "what it follows changed since the last check"
	case r.Settled(in):
		return false, ""
	}
	age := now.Sub(time.Unix(r.CheckedAt, 0))
	if age < 0 || age >= interval {
		return true, "the last check was over " + interval.String() + " ago"
	}
	return false, ""
}

// Settled reports whether r's last check of in resolved a revision that never moves, so no launch
// checks it again (CheckDue): AN UNMODIFIED EXTENSION HELD AT A TAG, A COMMIT OR AN EXACT npm
// VERSION is checked only until it first resolves (docs/design/pi-extension-store-builds.md
// XB-D2), since a later check could only fetch (a patched fork never follows a re-pointed tag
// either, PF-D4). A patched series always names a base, so this is never true of a patched fork's
// or a patched extension's hourly check.
func (r *CheckRecord) Settled(in CheckInputs) bool {
	return r != nil && r.CheckedAt != 0 && r.Check != nil && r.Read == in && in.Base == "" &&
		r.Check.Problem == "" && fixedRefKind(r.Check.RefKind)
}

// fixedRefKind reports whether a check's ref kind names one revision for good: a git tag or commit,
// or an exact npm version.
func fixedRefKind(kind string) bool {
	return kind == "tag" || kind == "commit" || kind == RefKindNpmVersion
}

// NextCheck is when the next check falls due under the throttle, for a status line.
func (r *CheckRecord) NextCheck(interval time.Duration) time.Time {
	if interval <= 0 {
		interval = BranchRefreshInterval
	}
	return time.Unix(r.CheckedAt, 0).Add(interval)
}
