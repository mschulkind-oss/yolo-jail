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

// Inputs is what a check of w reads (CheckInputs), or why w cannot be checked at all. An npm
// source (npm.go) reads its package as the repository and its spec as the ref, with no follow rule
// and no base: it carries no series, and the registry's answer for the spec is the whole list.
func (w PatchedWant) Inputs() (CheckInputs, Addr, FollowRule, error) {
	if IsNpmSource(w.Source) {
		n, err := ParseNpm(w.Source)
		if err != nil {
			return CheckInputs{}, Addr{}, FollowRule{}, err
		}
		return CheckInputs{Repo: n.Repo(), Ref: n.Ref()}, Addr{}, FollowRule{}, nil
	}
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
	// Inputs is what a check of the want reads, for Record.Candidates.
	Inputs CheckInputs
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
			return CheckResult{Record: rec, Inputs: in}
		}
	}
	waiting, end := (func(string))(nil), func() {}
	if opts.Begin != nil {
		waiting, end = opts.Begin()
	}
	defer end()
	res := CheckResult{Inputs: in}
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
		// interval, rather than every launch paying the same timeout. The STAMP ALONE: what the
		// check read is written only with what it found, below, so the record never pairs these
		// inputs with the last check's list. A check killed in its git after an edit to what it
		// reads leaves the old inputs beside the old list, and the next launch is due at once
		// (CheckDue) instead of serving the old rule's list under the new one for an hour.
		r.CheckedAt = now().Unix()
		if err := save(); err != nil {
			return false, err
		}
		res.Ran = true
		var found CheckFound
		if n, err := ParseNpm(w.Source); err == nil {
			found = s.findNpmCandidates(n)
		} else {
			found = s.findCandidates(addr, follow, w.Base, opts.Force, now(), waiting)
		}
		r.Seq++
		found.Seq, found.At = r.Seq, now().Unix()
		r.Read, r.Check = in, &found
		// OUTCOMES FOLLOW THE LIST, and only a list: a check that names no candidate (a problem — a
		// git error, a lock it could not take, a ref edited to HEAD for a minute) lists nothing, and
		// pruning against that would forget every conflict the next good check would otherwise pass
		// over (PF-D9), and in step 2 every build failure's back-off.
		if found.Problem == "" {
			r.Outcomes = keepListedOutcomes(r.Outcomes, found.List)
		}
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
	// AN UNMODIFIED EXTENSION has no series, so no base to make present
	// (docs/design/pi-extension-store-builds.md §4.2): its walk's list is every entry the follow
	// rule names.
	if base != "" {
		if why := s.ensureBase(b, mirror, a, base, found.FetchErr); why != "" {
			found.Problem = why
			return found
		}
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
	if base != "" {
		on, err := s.isAncestor(mirror, base, tip)
		if err != nil {
			found.Problem = "could not ask " + a.Repo + "'s mirror whether " + name + " contains the series' " +
				"base: " + oneLine(err)
			return found
		}
		found.BaseOnBranch = on
	}
	versions, err := s.versionsContaining(mirror, name, base, follow)
	if err != nil {
		found.Problem = "could not list " + a.Repo + "'s version tags: " + oneLine(err)
		return found
	}
	if follow.Kind == FollowRelease && len(versions) == 0 {
		// TWO EMPTY LISTS, ONE OUTCOME, TOLD APART IN WORDS (PF-D60, amending PF-D27): versions that
		// all predate the series' base, and a branch carrying no version the rule reads at all, are
		// each an empty list, so a first advance builds the base (§6.4). The second carries a note,
		// since a release rule there follows nothing until a tag appears, and the launch that meets
		// it and `yolo pack status` say so.
		merged, err := s.versionsContaining(mirror, name, "", follow)
		switch {
		case err != nil:
			found.Problem = "could not list " + a.Repo + "'s version tags: " + oneLine(err)
			return found
		case len(merged) == 0:
			found.NoVersion = noVersionNote(a, follow)
		}
	}
	found.List = walkList(follow, tip, versions)
	return found
}

// noVersionNote is the note for a branch on which a release rule finds no version tag at all: what
// the rule reads (CheckFound.NoVersionLine says where the fork stays, and what follows the branch).
func noVersionNote(a Addr, follow FollowRule) string {
	reads := "a tag named a semantic version, optionally `v`-led"
	if follow.Prefix != "" {
		reads = "a tag named `" + follow.Prefix + "` and a semantic version"
	}
	return "?ref=" + a.Ref + " of " + a.Repo + " carries no version tag that `follow: \"" + follow.String() +
		"\"` reads (" + reads + ", never a pre-release)"
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

// versionsContaining lists the version tags merged into branch (a full ref name) that contain base
// (every one merged, when base is ""), under follow's grammar, releases only, newest first by
// precedence. A tag naming no commit is left out by git itself; ties in precedence keep the tag
// whose name sorts last, so the order is the same on every machine.
func (s *Store) versionsContaining(mirror, branch, base string, follow FollowRule) ([]taggedVersion, error) {
	args := []string{"for-each-ref", "--merged=" + branch}
	if base != "" {
		args = append(args, "--contains="+base)
	}
	out, err := s.run(mirror, append(args, "--format=%(refname:strip=2) %(objectname) %(*objectname)",
		"refs/tags")...)
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

// BeforeGood is the walk's list cut at the good build's COMMIT, in list order (PF-D34): every entry
// above that commit, and the whole list when the commit is not on it. It is the cut for a list
// whose order is not the good build's: a tag or commit hold, which is that commit always (§3.3),
// and a list read under another rule than the good build's, whose versions may be older than the
// version it runs and still be candidates "like any other" (§3.3) — while a newer fit above the
// good build's own entry is still never passed over for an older one below it.
func BeforeGood(list []ListEntry, good *GoodBuild) []ListEntry {
	if good == nil {
		return list
	}
	for i, e := range list {
		if e.Commit == good.Commit {
			return list[:i:i]
		}
	}
	return list
}

// Candidates is the walk's list as an advance considers it now, for a check of in: the last
// check's list cut at the good build. nil before any finished check, and nil when the last one
// read something other than in — an edited ref, follow rule or base, whose own check is due at once
// (CheckDue): that list answers another question, and serving it under the new one is the stale
// answer §4.2 rules out.
//
// THE CUT (PF-D34): by version precedence (AboveGood) for a branch list read under the rule the
// good build's check read; at the good build's commit (BeforeGood) for a tag or commit hold, and
// for a list read under another repository, subdirectory, ref or follow rule than the good build's.
// A good build that records no inputs is cut by precedence, so a missing field never moves one back.
func (r *CheckRecord) Candidates(in CheckInputs) []ListEntry {
	if !r.Answers(in) {
		return nil
	}
	list, good := r.Check.List, r.Good
	switch {
	case good == nil:
		return list
	case r.Check.RefKind == "tag" || r.Check.RefKind == "commit" || IsNpmRefKind(r.Check.RefKind):
		// An npm list is the registry's one answer for the spec, which npm itself would install, so
		// it is a candidate whenever it is not the good build: cut at the good build's version.
		return BeforeGood(list, good)
	case good.Read != nil && !sameFollowed(*good.Read, r.Read):
		return BeforeGood(list, good)
	}
	return AboveGood(list, good)
}

// sameFollowed reports whether two checks followed the same thing: one repository, subdirectory,
// ref and follow rule. The series' base is not part of it: a re-exported series is the series' row
// of §6.1, a candidate at the commit the follow rule names, not a new rule.
func sameFollowed(a, b CheckInputs) bool {
	return a.Repo == b.Repo && a.Subdir == b.Subdir && a.Ref == b.Ref && a.Follow == b.Follow
}

// Answers reports whether the record's last finished check is a check of in.
func (r *CheckRecord) Answers(in CheckInputs) bool {
	return r != nil && r.Check != nil && r.Read == in
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

// The kinds a Held names besides an outcome's: the last check's problem, and an apply error the
// last walk of its list ended in.
const (
	HeldByProblem    = "problem"
	HeldByApplyError = "apply-error"
)

// Held is why a record's newest candidate is not what runs, read offline with no git: the first
// entry of the walk's list (as Candidates cuts it) that a recorded conflict or failed build of the
// series as it stands stops, or the last check's problem (§8: the HELD SUFFIX a fork's line carries
// until the candidate changes). Its Kind is OutcomeConflict, OutcomeBuildFailed, HeldByProblem or
// HeldByApplyError.
type Held struct {
	Kind  string
	Entry ListEntry
	// Member and Paths are a conflict's; Error a failed build's or the problem.
	Member string
	Paths  []string
	Error  string
}

// HeldAt is what holds the record's newest candidate back for a check of in and the series digest
// and recipe asked for now, or nil when nothing does: the first entry the walk would reach, and
// nothing below it, since an entry that may still fit holds nothing. Outcomes are matched on the
// series (and a build's on the recipe) and not on the yolo or git that recorded them: this is what
// the record says, which the next advance revisits under a new yolo or git. The first entry with
// no outcome is held by the apply error the last walk of this check's list ended in, when it did:
// no launch replays it again until the next check.
func (r *CheckRecord) HeldAt(in CheckInputs, series, recipe string) *Held {
	if r == nil || r.Check == nil || !r.Answers(in) {
		return nil
	}
	if r.Check.Problem != "" {
		return &Held{Kind: HeldByProblem, Error: r.Check.Problem}
	}
	for _, e := range r.Candidates(in) {
		for _, o := range r.Outcomes {
			if o.Commit != e.Commit || o.Series != series {
				continue
			}
			switch {
			case o.Kind == OutcomeConflict:
				return &Held{Kind: o.Kind, Entry: e, Member: o.Member, Paths: o.Paths}
			case o.Kind == OutcomeBuildFailed && o.Recipe == recipe:
				return &Held{Kind: o.Kind, Entry: e, Error: o.Error}
			}
		}
		if ae := r.ApplyErrAtLastCheck(); ae != nil {
			return &Held{Kind: HeldByApplyError, Entry: e, Error: ae.Error}
		}
		return nil
	}
	return nil
}
