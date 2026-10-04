package packsrc

// patchcheck.go is a patched fork's CHECK (docs/design/patched-forks.md §4, PF-D5): the act that
// reads the upstream for a newer candidate, keyed by the OWNER KEY (PF-D22) so a patched extension
// runs the same one.
//
// # The throttle (§4.2)
//
// One check per owner key per BranchRefreshInterval, measured from the last ATTEMPT, whatever its
// outcome, gated by a stamp in the check record that is read before any git: inside the interval,
// with nothing it reads changed, a check runs no git and no network. It is the record's own stamp
// and never the mirror's, because the launch's pack refresh runs first and would leave the
// mirror's fresh for a branch pack on the same repository — and reading the mirror's runs a
// `rev-parse` first. An attempt counts, so an offline machine retries hourly rather than paying a
// fetch timeout every launch.
//
// # The check itself (§4.3)
//
// Under the record's lock, re-reading the stamp there: write the attempt; run the per-mirror
// step's fetch (fetchStep) and ref rule for the source's repository, with NO CHECKOUT — a fetch
// when the mirror's own stamp for the branch is over an hour old, or always for an explicit act,
// every tag a fetch moved put back either way (a patched fork never follows a re-pointed tag,
// PF-D4); then apply the follow rule to what the mirror holds and record the walk's list, newest
// first, with this check's sequence number. Nothing is replayed and nothing is built here.
//
// # Locks
//
// The record lock, then the mirror lock inside it; the mirror lock is released before the record
// is written. Two checks of one owner serialize on its record lock, the second re-reading a fresh
// stamp and running no git; checks of different owners wait for each other only on a shared
// mirror's lock, and none takes the fork lock forks.lock.json's writers share.

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// PatchedWant is one owner key a check is asked about, as its declaration and its series say.
type PatchedWant struct {
	// Owner is the owner key: "<pack>/<bin>" for a patched fork.
	Owner string
	// Source is the upstream address as the manifest writes it, Follow the follow rule as written
	// ("" for the default).
	Source, Follow string
	// Base is the series' base commit (Series.Base).
	Base string
}

// Inputs is what a check of w reads (CheckInputs), or why w cannot be checked at all.
func (w PatchedWant) Inputs() (CheckInputs, Addr, FollowRule, error) {
	a, err := Parse(w.Source)
	if err != nil {
		return CheckInputs{}, Addr{}, FollowRule{}, err
	}
	if a.IsLocal() {
		return CheckInputs{}, Addr{}, FollowRule{}, fmt.Errorf("%s is a directory, which has no "+
			"upstream revision to follow", w.Source)
	}
	f, err := ParseFollow(w.Follow)
	if err != nil {
		return CheckInputs{}, Addr{}, FollowRule{}, err
	}
	return CheckInputs{Repo: a.Repo, Subdir: a.Path, Ref: a.Ref, Follow: f.String(), Base: w.Base}, a, f, nil
}

// CheckOptions tunes CheckPatched.
type CheckOptions struct {
	// Force is an EXPLICIT ACT's check (`yolo pack update`, `yolo pack install`, `yolo capture`):
	// it ignores the throttle and the mirror's own stamp and fetches, still putting tags back.
	Force bool
	// Now is the clock; nil means time.Now. Interval overrides BranchRefreshInterval (tests).
	Now      func() time.Time
	Interval time.Duration
	// Begin, when non-nil, is called once, before the first lock wait or git run, and returns the
	// lock-wait notice and the function that ends what Begin started (a progress line). A check
	// the throttle skips never calls it.
	Begin func() (waiting func(string), end func())
}

// CheckResult is what CheckPatched did.
type CheckResult struct {
	// Record is the record after the call: the one a skipped check read, or the one a check
	// wrote. nil only when Err is set and no record could be read.
	Record *CheckRecord
	// Ran is true when a check ran (and so git did).
	Ran bool
	// Due is why it ran, "" when it did not.
	Due string
	// Recovered is the error the record that stood before this call had when it could not be
	// read: the check started over from an empty record (§6.2). nil otherwise.
	Recovered error
	// Err is why no check could run or be recorded: an unparseable source, or a record that
	// could not be locked or written.
	Err error
}

// CheckPatched runs the check for w, unless the throttle says it is not due and opts.Force is
// unset, and returns the record.
func (s *Store) CheckPatched(w PatchedWant, opts CheckOptions) CheckResult {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	in, addr, follow, err := w.Inputs()
	if err != nil {
		return CheckResult{Err: err}
	}
	// THE THROTTLE, A FILE READ: no lock and no git when the stamp is fresh.
	rec, readErr := s.LoadCheckRecord(w.Owner)
	if readErr == nil && !opts.Force {
		if due, _ := CheckDue(rec, in, now(), opts.Interval); !due {
			return CheckResult{Record: rec}
		}
	}
	waiting, end := (func(string))(nil), func() {}
	if opts.Begin != nil {
		waiting, end = opts.Begin()
	}
	defer end()
	var res CheckResult
	err = s.WithCheckRecord(w.Owner, waiting, func(r *CheckRecord, recErr error, save func() error) (bool, error) {
		res.Recovered = recErr
		// RE-READ UNDER THE LOCK: another launch may have checked while this one waited.
		if !opts.Force && recErr == nil {
			if due, _ := CheckDue(r, in, now(), opts.Interval); !due {
				res.Record = r
				return false, nil
			}
		}
		_, res.Due = CheckDue(r, in, now(), opts.Interval)
		if opts.Force {
			res.Due = "an explicit act checks now"
		}
		// THE ATTEMPT COUNTS, written before the fetch: a check that dies in it still waits out the
		// interval, rather than every launch paying the same timeout.
		r.CheckedAt, r.Read = now().Unix(), in
		if err := save(); err != nil {
			return false, err
		}
		res.Ran = true
		found := s.findCandidates(addr, follow, w.Base, opts.Force, now(), waiting)
		r.Seq++
		found.Seq, found.At = r.Seq, now().Unix()
		r.Check = &found
		r.Outcomes = keepListedOutcomes(r.Outcomes, found.List)
		res.Record = r
		return true, nil
	})
	if err != nil {
		res.Err = err
	}
	return res
}

// keepListedOutcomes drops the outcomes of commits no longer on the list: the record keeps one
// outcome per entry of the walk's list, so it stays the size of that list.
func keepListedOutcomes(outcomes []EntryOutcome, list []ListEntry) []EntryOutcome {
	on := map[string]bool{}
	for _, e := range list {
		on[e.Commit] = true
	}
	var kept []EntryOutcome
	for _, o := range outcomes {
		if on[o.Commit] {
			kept = append(kept, o)
		}
	}
	return kept
}

// findCandidates is the check's fetch and ref rule, under the mirror's lock, with no checkout: the
// fetch step, then the ref read afresh, the series' base made present, and the walk's list.
func (s *Store) findCandidates(a Addr, follow FollowRule, base string, force bool, now time.Time,
	waiting func(string)) CheckFound {
	var found CheckFound
	unlock, err := s.lockMirror(a.Repo, waiting)
	if err != nil {
		found.Problem = oneLine(err)
		return found
	}
	defer unlock()
	b, cancel := s.newBudget()
	defer cancel()
	mirror := s.mirrorPath(a.Repo)

	step := s.fetchStep(b, a.Repo, []Addr{a}, fetchMode{always: force, keepTags: true}, now,
		BranchRefreshInterval)
	found.Fetched = step.fetched
	if step.fetchErr != nil {
		found.FetchErr = oneLine(step.fetchErr)
	}
	if !mirrorExists(mirror) {
		found.Problem = "this machine has never fetched " + a.Repo + ", and fetching it failed (" +
			found.FetchErr + ") — the next check, in an hour, tries again, or `yolo pack update` checks now"
		return found
	}
	kind, name, tip, err := s.classifyRef(mirror, a.Ref)
	switch {
	case err != nil:
		found.Problem = oneLine(err)
		return found
	case kind == refUnresolved:
		if found.FetchErr != "" {
			found.Problem = "?ref=" + a.Ref + " is not in this machine's copy of " + a.Repo + ", and " +
				"fetching it failed (" + found.FetchErr + ") — the next check, in an hour, tries again, " +
				"or `yolo pack update` checks now"
		} else {
			found.Problem = "?ref=" + a.Ref + " names no branch, tag or commit of " + a.Repo +
				" — check the fork's ?ref="
		}
		return found
	case kind == refOther:
		found.Problem = "?ref=" + a.Ref + " names neither a branch, a tag nor a full commit of " + a.Repo +
			" (HEAD moves with its branch and an abbreviated commit names nothing that holds) — name a " +
			"branch to follow, or a tag or a full commit to hold at"
		return found
	}
	found.Tip = tip
	if why := s.ensureBase(b, mirror, a, base, found.FetchErr); why != "" {
		found.Problem = why
		return found
	}
	switch kind {
	case refTag:
		found.RefKind = "tag"
		short := strings.TrimPrefix(name, "refs/tags/")
		e := ListEntry{Commit: tip, Tag: short}
		if v, ok := follow.TagVersion(short); ok && v.Pre == "" {
			e.Version = v.String()
		}
		found.List = []ListEntry{e}
		return found
	case refCommit:
		found.RefKind = "commit"
		found.List = []ListEntry{{Commit: tip}}
		return found
	}
	found.RefKind = "branch"
	on, err := s.isAncestor(mirror, base, tip)
	if err != nil {
		found.Problem = "could not ask " + a.Repo + "'s mirror whether " + name + " contains the series' " +
			"base: " + oneLine(err)
		return found
	}
	found.BaseOnBranch = on
	versions, err := s.versionsContaining(mirror, name, base, follow)
	if err != nil {
		found.Problem = "could not list " + a.Repo + "'s version tags: " + oneLine(err)
		return found
	}
	found.List = walkList(follow, tip, versions)
	return found
}

// ensureBase makes the series' base present in the mirror, fetching it by its id when no branch or
// tag brought it, and says why not, or "". A base the mirror cannot get is no candidate list at
// all: the walk lists only versions that contain it, and the replay starts there.
func (s *Store) ensureBase(b budget, mirror string, a Addr, base, fetchErr string) string {
	if base == "" {
		return "the series names no base commit — re-export it with `git format-patch --base=<upstream commit>`"
	}
	if sha, err := s.revParse(mirror, base); err != nil {
		return "could not read the series' base " + shortCommit(base) + " in " + a.Repo + "'s mirror: " + oneLine(err)
	} else if sha != "" {
		return ""
	}
	if fetchErr != "" {
		return "the series' base " + shortCommit(base) + " is not in this machine's copy of " + a.Repo +
			", and fetching failed (" + fetchErr + ") — the next check, in an hour, tries again, or " +
			"`yolo pack update` checks now"
	}
	_, err := s.runIn(b, mirror, withFsck("-c", "gc.auto=0", "fetch", "origin", "--no-tags",
		"--no-write-fetch-head", "--recurse-submodules=no", base)...)
	if sha, rerr := s.revParse(mirror, base); rerr == nil && sha != "" {
		return ""
	}
	why := "the series' base " + shortCommit(base) + " is not a commit of " + a.Repo
	if err != nil {
		why += " (fetching it by its id failed: " + oneLine(err) + ")"
	}
	return why + " — a series is exported from a branch of its upstream: re-export it with " +
		"`git format-patch --base=<an upstream commit>`"
}

// isAncestor reports whether ancestor is an ancestor of (or equal to) commit in the mirror, with no
// network.
func (s *Store) isAncestor(mirror, ancestor, commit string) (bool, error) {
	_, err := s.run(mirror, "merge-base", "--is-ancestor", ancestor, commit)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// taggedVersion is one version tag the list may hold.
type taggedVersion struct {
	tag    string
	commit string
	v      Version
}

// versionsContaining lists the version tags merged into branch (a full ref name) that contain base,
// under follow's grammar, releases only, newest first by precedence. A tag naming no commit is
// left out by git itself; ties in precedence keep the tag whose name sorts last, so the order is
// the same on every machine.
func (s *Store) versionsContaining(mirror, branch, base string, follow FollowRule) ([]taggedVersion, error) {
	out, err := s.run(mirror, "for-each-ref", "--merged="+branch, "--contains="+base,
		"--format=%(refname:strip=2) %(objectname) %(*objectname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	var vs []taggedVersion
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, ok := follow.TagVersion(f[0])
		if !ok || v.Pre != "" {
			continue
		}
		commit := f[1]
		if len(f) >= 3 && f[2] != "" {
			commit = f[2] // an annotated tag: the commit it peels to
		}
		vs = append(vs, taggedVersion{tag: f[0], commit: commit, v: v})
	}
	sort.SliceStable(vs, func(i, j int) bool {
		if c := vs[i].v.Compare(vs[j].v); c != 0 {
			return c > 0
		}
		return vs[i].tag > vs[j].tag
	})
	return vs, nil
}

// walkList is the walk's list (§6.4, PF-D10 as amended 2026-10-04), newest first: under
// `follow: "head"` the branch's tip, then the version tags, one entry per commit — a commit two
// versions name keeps the newer, and a tip a version names is that version's entry, marked as the
// tip.
func walkList(follow FollowRule, tip string, versions []taggedVersion) []ListEntry {
	var list []ListEntry
	seen := map[string]bool{}
	if follow.Kind == FollowHead {
		e := ListEntry{Commit: tip, Tip: true}
		for _, v := range versions {
			if v.commit == tip {
				e.Tag, e.Version = v.tag, v.v.String()
				break
			}
		}
		list = append(list, e)
		seen[tip] = true
	}
	for _, v := range versions {
		if seen[v.commit] {
			continue
		}
		seen[v.commit] = true
		list = append(list, ListEntry{Commit: v.commit, Tag: v.tag, Version: v.v.String()})
	}
	return list
}

// AboveGood is the walk's list cut at the good build (§6.4): with no good build, the whole list;
// with one, the tip when it differs from the good build's commit, and the versions of higher
// precedence than the one the good build runs (every version, when it runs none).
func AboveGood(list []ListEntry, good *GoodBuild) []ListEntry {
	if good == nil {
		return list
	}
	gv, hasV := ParseVersion(good.Version)
	var out []ListEntry
	for _, e := range list {
		switch {
		case e.Commit == good.Commit:
		case e.Tip:
			out = append(out, e)
		case e.Version == "" || !hasV:
			out = append(out, e)
		default:
			if v, ok := ParseVersion(e.Version); ok && v.Compare(gv) > 0 {
				out = append(out, e)
			}
		}
	}
	return out
}

// Conflict is the conflict outcome recorded for commit under (series, yolo, git), or nil: a key
// whose replay stopped on a member, which no launch replays again.
func (r *CheckRecord) Conflict(commit, series, yolo, git string) *EntryOutcome {
	return r.outcome(OutcomeConflict, commit, series, yolo, git)
}

// Applies is the clean-replay outcome recorded for commit under (series, yolo, git), or nil.
func (r *CheckRecord) Applies(commit, series, yolo, git string) *EntryOutcome {
	return r.outcome(OutcomeApplies, commit, series, yolo, git)
}

func (r *CheckRecord) outcome(kind, commit, series, yolo, git string) *EntryOutcome {
	if r == nil {
		return nil
	}
	for i := range r.Outcomes {
		o := &r.Outcomes[i]
		if o.Kind == kind && o.Commit == commit && o.Series == series && o.Yolo == yolo && o.Git == git {
			return o
		}
	}
	return nil
}

// SetOutcome records o, replacing any outcome of its kind for its commit: an entry keeps one
// outcome of a kind, under its newest key, and a clean replay and a conflict exclude each other.
func (r *CheckRecord) SetOutcome(o EntryOutcome) {
	var kept []EntryOutcome
	for _, prev := range r.Outcomes {
		replay := func(k string) bool { return k == OutcomeConflict || k == OutcomeApplies }
		if prev.Commit == o.Commit && (prev.Kind == o.Kind || replay(prev.Kind) && replay(o.Kind)) {
			continue
		}
		kept = append(kept, prev)
	}
	r.Outcomes = append(kept, o)
}

// Candidates is the walk's list as an advance considers it now: the last check's list cut at the
// good build (AboveGood). nil before any check.
func (r *CheckRecord) Candidates() []ListEntry {
	if r == nil || r.Check == nil {
		return nil
	}
	return AboveGood(r.Check.List, r.Good)
}

// RecordWalk writes a walk's outcomes into owner's record under its lock, keyed by the series
// digest, the yolo version and the walk's git: a conflict for each entry that stopped on a member,
// and a clean replay for each that took the series. An apply error is no outcome (PF-D9): its entry
// stays pending, and the next check retries it. Called after WalkSeries has released the mirror's
// lock, never inside it (the lock order, checkrecord.go).
func (s *Store) RecordWalk(owner, series, yolo string, w WalkResult, now time.Time) error {
	if len(w.Results) == 0 {
		return nil
	}
	return s.WithCheckRecord(owner, nil, func(r *CheckRecord, _ error, _ func() error) (bool, error) {
		changed := false
		for _, res := range w.Results {
			o := EntryOutcome{Commit: res.Entry.Commit, Series: series, Yolo: yolo, Git: w.Git, At: now.Unix()}
			switch {
			case res.Conflict != nil:
				o.Kind, o.Member, o.Paths = OutcomeConflict, res.Conflict.Member, res.Conflict.Paths
			case res.Clean:
				o.Kind, o.Upstream = OutcomeApplies, res.Upstream
			default:
				continue
			}
			r.SetOutcome(o)
			changed = true
		}
		return changed, nil
	})
}
