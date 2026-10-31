package packsrc

// patchcheck.go is a patched fork's CHECK (docs/reference/patched-forks.md §4, PF-D5): the act that
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
// fetch timeout every launch. ONE CALLER TAKES IT BACK (CheckOptions.RetryFailedFetch): an advance
// with nothing built to run, for which a failed fetch is no state of the upstream but a fault of
// the moment — the network not up yet at boot — and an hour's wait a refused launch for nothing.
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
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
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
	// Base is the series' base commit (Series.Base); "" for a patch branch, whose base the check resolves.
	Base string
	// PatchBranch is a PATCH BRANCH's address as written (patchbranch.go), "" for a patch series: the check fetches
	// it with the upstream and resolves it, its commit and its merge base with the upstream.
	PatchBranch string
	// PatchBranchDir is the directory of the pack declaring the patch branch, as this process sees it: what a
	// relative locator (relative.go) is resolved against. "" when the declaring pack has none.
	PatchBranchDir string
	// PatchBranchRepoIdentity is the host-established repository identity carried through the pack-tree
	// mapping. Empty on the host, where Inputs derives it from PatchBranchDir.
	PatchBranchRepoIdentity string
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
	if w.PatchBranch != "" {
		c, err := ParseFrom(w.PatchBranch, w.PatchBranchDir)
		if err != nil {
			return CheckInputs{}, Addr{}, FollowRule{}, fmt.Errorf("the patch branch: %w", err)
		}
		if c.Repo == "" && (!IsRelativeLocator(w.PatchBranch) || w.PatchBranchRepoIdentity == "") {
			// A RELATIVE PATCH BRANCH WITH NO PACK DIRECTORY and no host-established identity is a caller
			// that lost the directory, never the user's manifest.
			return CheckInputs{}, Addr{}, FollowRule{}, fmt.Errorf("the patch branch %s is relative to its pack's "+
				"directory, and this check was handed none — a yolo bug; report it with this line, and meanwhile "+
				"write the repository's absolute address (git+file:///abs/path?ref=%s)", w.PatchBranch, c.Ref)
		}
		in := CheckInputs{Repo: a.Repo, Subdir: a.Path, Ref: a.Ref, Follow: f.String(), PatchBranch: w.PatchBranch}
		if IsRelativeLocator(w.PatchBranch) {
			in.PatchBranchRepo = w.PatchBranchRepoIdentity
			if in.PatchBranchRepo == "" {
				in.PatchBranchRepo, err = RelativeRepositoryIdentity(w.PatchBranch, w.PatchBranchDir)
				if err != nil {
					return CheckInputs{}, Addr{}, FollowRule{}, fmt.Errorf("the patch branch: %w", err)
				}
			}
		}
		return in, a, f, nil
	}
	return CheckInputs{Repo: a.Repo, Subdir: a.Path, Ref: a.Ref, Follow: f.String(), Base: w.Base}, a, f, nil
}

// CheckOptions tunes CheckPatched.
type CheckOptions struct {
	StartGuard               func(func() error) error
	FreshAfter               *CheckStart
	InputIdentity, RequestID string
	// Force is an EXPLICIT ACT's check (`yolo pack update`, `yolo pack install`, `yolo capture`):
	// it ignores the throttle and the mirror's own stamp and fetches, still putting tags back.
	Force bool
	// RetryFailedFetch is a caller with NOTHING BUILT TO RUN, which a failed fetch on the record does
	// not hold inside the interval: the record's last check could not reach the upstream (its
	// FetchErr — DNS, a refused or timed-out connection, TLS, a server error), which says nothing
	// about the upstream, and the throttle's purpose — not paying a fetch timeout on every launch
	// while a good build runs (§4.2) — has no good build to protect. The check runs, its fetch under
	// the store's usual timeout. A check that DID reach the upstream and found a problem (a base the
	// upstream lacks, a ref naming nothing) still holds for the interval: that is the upstream's
	// answer.
	RetryFailedFetch bool
	// Now is the clock; nil means time.Now. Interval overrides BranchRefreshInterval (tests).
	Now      func() time.Time
	Interval time.Duration
	// Begin, when non-nil, is called once, before the first lock wait or git run, and returns the
	// lock-wait notice and the function that ends what Begin started (a progress line). A check
	// the throttle skips never calls it.
	Begin func() (waiting func(string), end func())
	// RefreshPatchBranch is a FRESH LAUNCH's check (patchbranchcopy.go): a patch branch given as a local
	// checkout is fetched and resolved again even when the throttle holds the check, and a branch that moved
	// runs the check at once. A patch branch at a URL, and a patch series, are unaffected.
	RefreshPatchBranch bool
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
	// PatchBranchRefreshed is true when a fresh launch's patch branch refresh ran (RefreshPatchBranch) whether
	// or not a check followed: the record's patch branch resolution is this call's.
	PatchBranchRefreshed bool
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
	if w.PatchBranch != "" && addr.Repo == "" {
		return CheckResult{Err: fmt.Errorf("the patch branch repository is not mapped into this workspace — run `yolo pack series check` on the host or start a host launch")}
	}
	// THE LOCAL CLONE MAP, asked once per check (localclones.go): which registered clone answers for a patch
	// branch URL, or why the one that matched does not. A broken `local_clones` refuses the check.
	var clone cloneChoice
	if w.PatchBranch != "" {
		if c, err := ParseFrom(w.PatchBranch, w.PatchBranchDir); err == nil {
			if clone, err = s.chooseClone(c); err != nil {
				return CheckResult{Err: err, Inputs: in}
			}
		}
	}
	refresh := opts.RefreshPatchBranch && w.PatchBranch != "" && isCheckoutLocator(w, clone)
	once := fetchOnce{}
	rec, readErr := s.LoadCheckRecord(w.Owner)
	if readErr == nil && !opts.Force && opts.FreshAfter == nil && !retryFetch(opts, rec, in) && !refresh {
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
		// RE-READ UNDER THE LOCK: a qualifying winner serves all requests registered
		// before its start. A check in progress at registration never qualifies.
		if opts.FreshAfter != nil && recErr == nil && r.Read == in && r.Fresh.Qualifies(*opts.FreshAfter, opts.InputIdentity) {
			err := s.WithFreshAuthority(w.Owner, *opts.FreshAfter, r.Fresh, opts.InputIdentity, func() error { return nil })
			if err != nil {
				return false, err
			}
			res.Record = r
			return false, nil
		}
		// RE-READ UNDER THE LOCK: another launch may have checked while this one waited.
		retry := recErr == nil && retryFetch(opts, r, in)
		moved := false
		if !opts.Force && opts.FreshAfter == nil && recErr == nil && !retry {
			if due, _ := CheckDue(r, in, now(), opts.Interval); !due {
				if !refresh {
					res.Record = r
					return false, nil
				}
				// A FRESH LAUNCH REFRESHES A CHECKOUT'S PATCH BRANCH inside the throttle (patchbranchcopy.go): the
				// same resolution, against the tip the last check recorded. Unmoved, the record takes this
				// resolution's copy, relation and freshness, and no check runs; moved, the check runs now.
				res.PatchBranchRefreshed = true
				if moved = s.refreshPatchBranch(w, clone, addr, r, now(), waiting, once); !moved {
					res.Record = r
					return true, nil
				}
			}
		}
		_, res.Due = CheckDue(r, in, now(), opts.Interval)
		if moved {
			res.Due = "the patch branch moved"
		}
		if opts.Force {
			res.Due = "an explicit act checks now"
		} else if retry {
			res.Due = "the last check could not reach the upstream, and nothing is built to run"
		}
		// THE ATTEMPT COUNTS, written before the fetch: a check that dies in it still waits out the
		// interval, rather than every launch paying the same timeout. The STAMP ALONE: what the
		// check read is written only with what it found, below, so the record never pairs these
		// inputs with the last check's list. A check killed in its git after an edit to what it
		// reads leaves the old inputs beside the old list, and the next launch is due at once
		// (CheckDue) instead of serving the old rule's list under the new one for an hour.
		begun, err := s.beginCheck(w.Owner, opts.InputIdentity, opts.RequestID, opts.FreshAfter, now(), opts.StartGuard)
		if err != nil {
			return false, err
		}
		stamp := r.CheckedAt
		r.CheckedAt = now().Unix()
		if err := save(); err != nil {
			return false, err
		}
		res.Ran = true
		var found CheckFound
		if n, err := ParseNpm(w.Source); err == nil {
			found = s.findNpmCandidates(n)
		} else {
			var patchBranch *Addr
			if w.PatchBranch != "" {
				c, _ := ParseFrom(w.PatchBranch, w.PatchBranchDir) // Inputs parsed it
				patchBranch = &c
			}
			found = s.findCandidates(addr, follow, w.Base, patchBranch, clone, opts.Force || opts.FreshAfter != nil, now(), waiting, once)
		}
		if found.lockHeld != nil {
			// A NoWait STORE MET A HELD MIRROR LOCK: nothing was fetched or listed, so the attempt's
			// stamp goes back to what it was and the record says nothing of this call; the caller
			// skips the key (§6.2 rule 5). The start entry minted above is intentionally left
			// behind: the mint must predate the transport, so a request registered while this
			// check runs takes a boundary past it and the in-flight check can never satisfy
			// that later request (TestFreshCheckBoundaryExcludesInFlightAndSharesFollowup).
			// The orphan burns one sequence number and can never land in any record's Fresh,
			// so WithFreshAuthority can never accept it — authority churn, fail-closed.
			r.CheckedAt, res.Ran, res.Err, res.Record = stamp, false, found.lockHeld, r
			return true, nil
		}
		r.Fresh = &begun
		r.Seq++
		found.Seq, found.At = r.Seq, now().Unix()
		r.Read, r.Check = in, &found
		if found.PatchBranch != nil {
			resolved := *found.PatchBranch
			resolved.At = found.At
			resolved.Repository = in.PatchBranchRepo
			r.PatchBranch = &resolved
		}
		if pf := r.PatchFailure; pf != nil {
			switch {
			case pf.Inputs != in:
				r.PatchFailure = nil
			case found.FetchErr == "" && found.Problem == "" && pf.Kind != "base" &&
				!listHasCommit(found.List, pf.Target.Commit):
				r.PatchFailure = nil
			}
		}
		// OUTCOMES FOLLOW THE LIST, and only a list: a check that names no candidate (a problem — a
		// git error, a lock it could not take, a ref edited to HEAD for a minute) lists nothing, and
		// pruning against that would forget every conflict the next good check would otherwise pass
		// over (PF-D9), and in step 2 every build failure's back-off.
		if found.Problem == "" && found.FetchErr == "" {
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

// isCheckoutLocator reports a patch branch given as a local checkout: a relative locator, a git+file:// one, or a
// URL a registered local clone's remote names (localclones.go).
func isCheckoutLocator(w PatchedWant, clone cloneChoice) bool {
	c, err := ParseFrom(w.PatchBranch, w.PatchBranchDir)
	return err == nil && (strings.HasPrefix(c.Repo, "file://") || clone.match != nil)
}

// refreshPatchBranch is a fresh launch's patch branch refresh inside the throttle: the checkout's patch branch
// resolved again (resolvePatchBranch: both its fetches, at most once per command) against the upstream tip r's
// last check found. It reports whether the check must run now — the branch moved, or the refresh could not say
// (no recorded tip, a problem, the checkout's own fetch failed) — and otherwise puts this resolution in r. A
// mirror lock another process holds keeps r as it stands.
func (s *Store) refreshPatchBranch(w PatchedWant, clone cloneChoice, upstream Addr, r *CheckRecord, now time.Time, waiting func(string), once fetchOnce) bool {
	if r.Check == nil || r.Check.Tip == "" || r.PatchBranch == nil {
		return true
	}
	c, err := ParseFrom(w.PatchBranch, w.PatchBranchDir)
	if err != nil {
		return true
	}
	c, plan := s.patchBranchPlan(c, clone)
	if plan == nil {
		return true
	}
	unlock, err := s.lockMirrors(mirrorsOf(upstream.Repo, &c, plan), waiting)
	if err != nil {
		return false
	}
	defer unlock()
	b, cancel := s.newBudget()
	defer cancel()
	resolved, why, fetchErr := s.resolvePatchBranch(b, c, plan, upstream.Repo, r.Check.Tip, now, once)
	if why != "" || fetchErr != "" || resolved == nil ||
		resolved.Commit != r.PatchBranch.Commit || resolved.Base != r.PatchBranch.Base {
		return true
	}
	resolved.At, resolved.Repository = now.Unix(), r.PatchBranch.Repository
	r.PatchBranch = resolved
	if r.Check.PatchBranch != nil {
		found := *resolved
		r.Check.PatchBranch = &found
	}
	return false
}

// retryFetch reports whether opts asks a failed fetch to be retried (RetryFailedFetch) and r's last
// check of in is one: its fetch failed, whatever it then made of the mirror it had.
func retryFetch(opts CheckOptions, r *CheckRecord, in CheckInputs) bool {
	return opts.RetryFailedFetch && r != nil && r.Check != nil && r.Read == in && r.Check.FetchErr != ""
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
func (s *Store) findCandidates(a Addr, follow FollowRule, base string, patchBranch *Addr, clone cloneChoice, force bool, now time.Time,
	waiting func(string), once fetchOnce) CheckFound {
	var found CheckFound
	var plan *checkoutPlan
	if patchBranch != nil {
		pb, p := s.patchBranchPlan(*patchBranch, clone)
		patchBranch, plan = &pb, p
	}
	unlock, err := s.lockMirrors(mirrorsOf(a.Repo, patchBranch, plan), waiting)
	if err != nil {
		if errors.Is(err, ErrLockHeld) {
			found.lockHeld = err
		}
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
		found.Problem = "this machine has never fetched " + a.Repo + ", and " + fetchFailed(found.FetchErr)
		return found
	}
	// THE SERIES' BASE IS LOOKED UP WITH THE REF, in its git start, when ensureBase is what asks
	// for it below: nothing between the two changes the mirror, which this check holds locked.
	var also []string
	if patchBranch == nil && base != "" {
		also = []string{base}
	}
	kind, name, tip, baseLookup, err := s.classifyRefAnd(mirror, a.Ref, also...)
	switch {
	case err != nil:
		found.Problem = oneLine(err)
		return found
	case kind == refUnresolved:
		if found.FetchErr != "" {
			found.Problem = "?ref=" + a.Ref + " is not in this machine's copy of " + a.Repo + ", and " +
				fetchFailed(found.FetchErr)
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
	// A PATCH BRANCH (patchbranch.go): fetched from its own repository into this mirror, under this lock and
	// budget, whenever a check runs, and resolved to a commit and its merge base with the tip, which
	// is its base from here on. A fetch that fails is this check's failed fetch, and the copy the
	// mirror holds answers meanwhile, as the upstream's does.
	if patchBranch != nil {
		resolved, why, fetchErr := s.resolvePatchBranch(b, *patchBranch, plan, a.Repo, tip, now, once)
		if fetchErr != "" {
			if found.FetchErr != "" {
				found.FetchErr += "; " + fetchErr
			} else {
				found.FetchErr = fetchErr
			}
		}
		if why != "" {
			if clone.skip != "" {
				why += " (" + clone.skip + ")"
			}
			found.Problem = why
			return found
		}
		if clone.match == nil && resolved.Checkout == "" {
			resolved.CloneSkipped = clone.skip
		}
		found.PatchBranch, base = resolved, resolved.Base
	} else if base != "" {
		// AN UNMODIFIED EXTENSION has no series, so no base to make present
		// (docs/design/pi-extension-store-builds.md §4.2): its walk's list is every entry the follow
		// rule names.
		why, fetchErr := s.ensureBase(b, mirror, a, base, found.FetchErr, baseLookup)
		if fetchErr != "" {
			// THE BASE'S OWN FETCH COULD NOT REACH THE UPSTREAM: a failed fetch, as the step's would be,
			// never the verdict that the upstream lacks the commit.
			found.FetchErr = fetchErr
		}
		if why != "" {
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
	var versions []taggedVersion
	switch {
	case base != "" && patchBranch == nil:
		// ONE for-each-ref ASKS BOTH whether the branch contains the base and which versions do: the
		// branch is listed beside the tags exactly when its commit contains the base. Its commit is
		// tip still: under this lock nothing but ensureBase's fetch by id has run since classifyRef
		// read it, and that fetch names no ref to update.
		var on bool
		versions, on, err = s.versionsContainingOnBranch(mirror, name, base, follow)
		if err != nil {
			found.Problem = "could not ask " + a.Repo + "'s mirror whether " + name + " contains the series' " +
				"base: " + oneLine(err)
			return found
		}
		found.BaseOnBranch = on
	default:
		if base != "" {
			// A PATCH BRANCH's fetch ran since classifyRef, and may have moved the branch when the patch
			// branch lives in the upstream's own repository: the base is asked of tip, as read then.
			on, err := s.isAncestor(mirror, base, tip)
			if err != nil {
				found.Problem = "could not ask " + a.Repo + "'s mirror whether " + name + " contains the series' " +
					"base: " + oneLine(err)
				return found
			}
			found.BaseOnBranch = on
		}
		versions, err = s.versionsContaining(mirror, name, base, follow)
		if err != nil {
			found.Problem = "could not list " + a.Repo + "'s version tags: " + oneLine(err)
			return found
		}
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
// all: the walk lists only versions that contain it, and the replay starts there. Its second result
// is a by-id fetch that could not REACH the upstream (TransportFailure), which the caller records as
// the check's failed fetch: the upstream was not asked, so "not a commit of" would be no answer.
//
// looked is the base's revParse answer when the caller already has it from this state of the
// mirror (a one-element slice, classifyRefAnd), nil to ask.
func (s *Store) ensureBase(b budget, mirror string, a Addr, base, fetchErr string, looked []string) (string, string) {
	if base == "" {
		return "the series names no base commit — re-export it with `git format-patch --base=<upstream commit>`", ""
	}
	if len(looked) == 1 {
		if looked[0] != "" {
			return "", ""
		}
	} else if sha, err := s.revParse(mirror, base); err != nil {
		return "could not read the series' base " + shortCommit(base) + " in " + a.Repo + "'s mirror: " + oneLine(err), ""
	} else if sha != "" {
		return "", ""
	}
	if fetchErr != "" {
		return "the series' base " + shortCommit(base) + " is not in this machine's copy of " + a.Repo +
			", and " + fetchFailed(fetchErr), ""
	}
	_, err := s.runIn(b, mirror, withFsck("-c", "gc.auto=0", "fetch", "origin", "--no-tags",
		"--no-write-fetch-head", "--recurse-submodules=no", base)...)
	if sha, rerr := s.revParse(mirror, base); rerr == nil && sha != "" {
		return "", ""
	}
	if err != nil && TransportFailure(err.Error()) {
		failed := "fetching " + shortCommit(base) + " from " + a.Repo + ": " + oneLine(err)
		return "the series' base " + shortCommit(base) + " is not in this machine's copy of " + a.Repo +
			", and " + fetchFailed(failed), failed
	}
	why := "the series' base " + shortCommit(base) + " is not a commit of " + a.Repo
	if err != nil {
		why += " (fetching it by its id failed: " + oneLine(err) + ")"
	}
	return why + " — a series is exported from a branch of its upstream: re-export it with " +
		"`git format-patch --base=<an upstream commit>`", ""
}

// fetchFailed is a check's problem clause for a fetch that failed with err, with its next step: the
// network named as the cause when err is a failure to reach the upstream (TransportFailure). It
// promises no "in an hour": an advance with nothing built to run fetches again at its next run
// (CheckOptions.RetryFailedFetch), and one with a good build says its own next check.
func fetchFailed(err string) string {
	if TransportFailure(err) {
		return "fetching failed because the upstream could not be reached (" + err + ")" + unreachedNext(err)
	}
	return "fetching failed (" + err + ")" + unreachedNext(err)
}

// unreachedNext is the next step after a check that could not ask its upstream, err its failure:
// the network, when err says the upstream could not be reached, and `yolo pack update` either way.
func unreachedNext(err string) string {
	if TransportFailure(err) {
		return " — the network looks down or not up yet; once it is up, `yolo pack update` checks now"
	}
	return " — `yolo pack update` checks now"
}

// transportFailures are git's and curl's words for failing to REACH a remote — name resolution, a
// connection, TLS, a timeout, a server error — as against an answer from it (a ref or an object it
// does not have, a refused credential).
var transportFailures = []string{
	"could not resolve host", "could not resolve proxy", "temporary failure in name resolution",
	"name or service not known", "nodename nor servname", "failed to connect", "connection refused",
	"connection timed out", "connection reset", "operation timed out", "timed out after", "timeout was reached",
	"network is unreachable", "no route to host", "ssl", "tls", "gnutls", "certificate",
	"the requested url returned error: 5", "rpc failed", "early eof", "unexpected disconnect",
	// Go's resolver and dialer, as an npm check's registry request reports them.
	"no such host", "server misbehaving", "i/o timeout", "did not answer within",
}

// TransportFailure reports whether a fetch's error says the remote could not be REACHED — a fault of
// this machine's network or the remote's host, which the next attempt may not meet — rather than
// an answer about what the remote holds.
func TransportFailure(err string) bool {
	low := strings.ToLower(err)
	for _, w := range transportFailures {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
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
	vs, _, err := s.listVersions(mirror, branch, base, follow, false)
	return vs, err
}

// versionsContainingOnBranch is versionsContaining with base set, and whether branch's commit
// contains base too, which isAncestor would answer, from the same git start: the branch, merged
// into itself, is listed beside the tags exactly when its commit contains base.
func (s *Store) versionsContainingOnBranch(mirror, branch, base string, follow FollowRule) ([]taggedVersion, bool, error) {
	return s.listVersions(mirror, branch, base, follow, true)
}

// listVersions is versionsContaining, with branch's own ref asked for beside the tags when
// withBranch is set, and reported as listed or not.
func (s *Store) listVersions(mirror, branch, base string, follow FollowRule, withBranch bool) ([]taggedVersion, bool, error) {
	args := []string{"for-each-ref", "--merged=" + branch}
	if base != "" {
		args = append(args, "--contains="+base)
	}
	args = append(args, "--format=%(refname) %(objectname) %(*objectname)", "refs/tags")
	if withBranch {
		args = append(args, branch)
	}
	out, err := s.run(mirror, args...)
	if err != nil {
		return nil, false, err
	}
	var vs []taggedVersion
	listed := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if withBranch && f[0] == branch {
			listed = true
			continue
		}
		tag, ok := strings.CutPrefix(f[0], "refs/tags/")
		if !ok {
			continue
		}
		f[0] = tag
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
	return vs, listed, nil
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

// CurrentPatchFailure returns a deep copy of current typed authority, or synthesizes concrete
// authority from a selected legacy OutcomeConflict. Failed checks cannot prove that a target left
// the selected list; only a successful finished check can do that.
func (r *CheckRecord) CurrentPatchFailure(in CheckInputs, series string) *PatchFailure {
	if r == nil || r.Owner == "" {
		return nil
	}
	if r.WalkBackHolds(in, series) != nil {
		// THE WALK-BACK HOLDS (docs/reference/patched-forks.md §8.6): the newest's failure is the
		// hold's, and the walked-back good build runs until upstream lists something newer.
		return nil
	}
	if pf := r.PatchFailure; pf != nil && pf.Owner == r.Owner && pf.Inputs == in && pf.Series == series {
		if pf.Kind == "base" {
			if pf.Target.Commit == in.Base {
				return clonePatchFailure(pf)
			}
		} else {
			if r.Read != in || !successfulCheck(r) ||
				listHasCommit(r.Check.List, pf.Target.Commit) && !r.passedBy(pf, in, series) {
				return clonePatchFailure(pf)
			}
		}
	}
	if legacy := r.legacyConflict(in, series); legacy != nil && !r.passedBy(legacy, in, series) {
		return legacy
	}
	return nil
}

// passedBy reports whether a successful check of in lists something the walk reaches before pf's
// target (docs/reference/patched-forks.md §6.2): A RECORDED FAILURE FOLLOWS UPSTREAM'S NEWEST,
// so it governs only while its target is the newest candidate not already passed over by a conflict
// of this series. A newer entry that is untried, replays cleanly or failed to build passes it by, and
// so does a good build of this series that moved past it. A `base` failure, a failure at the good
// build's own commit, and anything read under a failed check are never passed by.
func (r *CheckRecord) passedBy(pf *PatchFailure, in CheckInputs, series string) bool {
	if pf == nil || pf.Kind == "base" || r.Read != in || !successfulCheck(r) {
		return false
	}
	if r.Good != nil && pf.Target.Commit == r.Good.Commit {
		return false
	}
	for _, e := range r.Candidates(in) {
		if e.Commit == pf.Target.Commit {
			return false
		}
		if !r.conflictedUnder(e.Commit, series) {
			return true
		}
	}
	// The target is below the cut: a good build of this very series moved past it.
	return r.Good != nil && r.Good.Series == series
}

// conflictedUnder reports whether a conflict is recorded for commit under series, whatever yolo or
// git recorded it: what the record says, as HeldAt reads it.
func (r *CheckRecord) conflictedUnder(commit, series string) bool {
	for _, o := range r.Outcomes {
		if o.Kind == OutcomeConflict && o.Commit == commit && o.Series == series {
			return true
		}
	}
	return false
}

// CurrentBuildFailure is the FAILED NEWER BUILD the record holds for a check of in, the series
// digest and recipe asked for now and this yolo (OQ-PPX3), as a PatchFailure of Kind
// PatchFailureBuild, with the outcome it was read from; nil when there is none. It is current only
// while a good build of this series and recipe is recorded — with none, nothing serves, and the
// missing build is the launch's refusal already — and the walk-back hold does not hold, and only
// for the first candidate the walk would reach (a recorded conflict of this series passes over an
// entry, as passedBy reads it): a newer release passes the failure by, as it does a patch failure.
// The yolo matters because a build failure is keyed by it: another yolo builds the candidate again.
// Whether a back-off still holds is the caller's to decide.
func (r *CheckRecord) CurrentBuildFailure(in CheckInputs, series, recipe, yolo string) (*PatchFailure, *EntryOutcome) {
	if r == nil || r.Owner == "" || !r.Answers(in) || r.Good == nil || r.Good.Series != series ||
		r.Good.Recipe != recipe || r.WalkBackHolds(in, series) != nil {
		return nil, nil
	}
	for _, e := range r.Candidates(in) {
		if r.conflictedUnder(e.Commit, series) {
			continue
		}
		if e.Commit == r.Good.Commit {
			return nil, nil
		}
		for i := range r.Outcomes {
			o := r.Outcomes[i]
			if o.Kind == OutcomeBuildFailed && o.Commit == e.Commit && o.Series == series && o.Recipe == recipe &&
				o.Yolo == yolo {
				return &PatchFailure{Owner: r.Owner, Inputs: in, Series: series, Target: e, Kind: PatchFailureBuild,
					Detail: o.Error, Seq: r.Seq, Cause: o.Cause}, &o
			}
		}
		return nil, nil
	}
	return nil, nil
}

// CurrentReplayFailure is the APPLY ERROR the record holds for a check of in, read as what stops a
// launch (PD-NF1, the maintainer's ruling of 2026-10-16, superseding PF-D45's serving): the last
// walk of the last check's list, or the build's own replay, could not replay the series as it stands
// (CheckRecord.ApplyErr), while a good build of this series and recipe is here, as a PatchFailure of
// Kind PatchFailureBuild and Cause BuildCauseReplay, aimed at the newest candidate the walk would
// reach. nil when there is none, when nothing is pending, or while a walk-back hold holds. It is
// bound to that check (ApplyErrAtLastCheck): the next check replays the list again.
func (r *CheckRecord) CurrentReplayFailure(in CheckInputs, series, recipe string) *PatchFailure {
	if r == nil || r.Owner == "" || !r.Answers(in) || r.ApplyErrAtLastCheck() == nil || r.Good == nil ||
		r.Good.Series != series || r.Good.Recipe != recipe || r.WalkBackHolds(in, series) != nil {
		return nil
	}
	for _, e := range r.Candidates(in) {
		if r.conflictedUnder(e.Commit, series) {
			continue
		}
		if e.Commit == r.Good.Commit {
			return nil
		}
		return &PatchFailure{Owner: r.Owner, Inputs: in, Series: series, Target: e, Kind: PatchFailureBuild,
			Cause: BuildCauseReplay, Detail: r.ApplyErr.Error, Seq: r.Seq}
	}
	return nil
}

func successfulCheck(r *CheckRecord) bool {
	return r != nil && r.Check != nil && r.Check.Seq == r.Seq &&
		r.Check.FetchErr == "" && r.Check.Problem == ""
}

func (r *CheckRecord) legacyConflict(in CheckInputs, series string) *PatchFailure {
	if r == nil || r.Owner == "" || r.Read != in || r.Check == nil || r.Check.Seq != r.Seq {
		return nil
	}
	for _, selected := range r.Check.List {
		for _, outcome := range r.Outcomes {
			if outcome.Commit != selected.Commit || outcome.Kind != OutcomeConflict || outcome.Series != series {
				continue
			}
			return &PatchFailure{
				Owner: r.Owner, Inputs: in, Series: series, Target: selected, Kind: "conflict",
				Member: outcome.Member, Paths: append([]string(nil), outcome.Paths...), Seq: r.Seq,
			}
		}
	}
	return nil
}

func listHasCommit(list []ListEntry, commit string) bool {
	for _, e := range list {
		if e.Commit == commit {
			return true
		}
	}
	return false
}

// RecordWalk writes an outcome only when its walk carries the check authority captured before replay.
func (s *Store) RecordWalk(owner, series, yolo string, w WalkResult, now time.Time) error {
	if w.snapshot == nil {
		return ErrUnboundReplay
	}
	bound := *cloneReplaySnapshot(w.snapshot)
	if owner != bound.Owner || series != bound.Series {
		return fmt.Errorf("recording arguments do not match the walk's original snapshot: %w", ErrStaleReplay)
	}
	return s.RecordReplay(bound, yolo, w, now).Err
}

// RecordWalkForCheck records only the exact check snapshot supplied by its original caller.
func (s *Store) RecordWalkForCheck(owner, series, yolo string, seq int64, inputs CheckInputs, w WalkResult, now time.Time) error {
	bound, err := snapshotForRecord(owner, series, seq, inputs, w)
	if err != nil {
		return err
	}
	return s.RecordReplay(bound, yolo, w, now).Err
}

func failureIndex(list []ListEntry, f *PatchFailure) int {
	if f == nil {
		return len(list) + 1
	}
	if f.Kind == "base" {
		return -1
	}
	for i, e := range list {
		if e.Commit == f.Target.Commit {
			return i
		}
	}
	return len(list) + 1
}

func walkHasResult(w WalkResult) bool { return len(w.Results) != 0 || w.Base != nil }

func recordWalkOutcomes(r *CheckRecord, snapshot ReplaySnapshot, yolo string, w WalkResult, now time.Time) bool {
	changed := false
	current := r.CurrentPatchFailure(snapshot.Inputs, snapshot.Series)
	if current != nil {
		for _, result := range w.Results {
			if result.Clean && result.Entry.Commit == current.Target.Commit {
				if failure := r.PatchFailure; failure != nil && failure.Owner == current.Owner &&
					failure.Inputs == current.Inputs && failure.Series == current.Series &&
					failure.Target.Commit == current.Target.Commit {
					r.PatchFailure = nil
					changed = true
				}
				current = nil
				break
			}
		}
	}
	if current != nil && r.PatchFailure == nil {
		r.PatchFailure = clonePatchFailure(current)
		changed = true
	}
	if failure := w.PatchFailure(); failure != nil {
		failure.Owner, failure.Inputs, failure.Series, failure.Seq = snapshot.Owner, snapshot.Inputs, snapshot.Series, snapshot.Seq
		if current == nil || failure.Target.Commit == current.Target.Commit ||
			failureIndex(r.Check.List, failure) < failureIndex(r.Check.List, current) {
			r.PatchFailure = clonePatchFailure(failure)
			changed = true
		}
	}
	for _, result := range w.Results {
		outcome := EntryOutcome{Commit: result.Entry.Commit, Series: snapshot.Series, Yolo: yolo, Git: w.Git, At: now.Unix()}
		switch {
		case result.Conflict != nil:
			outcome.Kind, outcome.Member = OutcomeConflict, result.Conflict.Member
			outcome.Paths = append([]string(nil), result.Conflict.Paths...)
		case result.Clean:
			outcome.Kind, outcome.Upstream = OutcomeApplies, append([]string(nil), result.Upstream...)
		default:
			continue
		}
		r.SetOutcome(outcome)
		changed = true
	}
	return changed
}

// RecordReplay preserves classified operation evidence even when its guarded persistence fails.
func (s *Store) RecordReplay(snapshot ReplaySnapshot, yolo string, walk WalkResult, now time.Time) RecordReplayResult {
	result := RecordReplayResult{Failure: clonePatchFailure(walk.PatchFailure())}
	if err := invalidSnapshot(snapshot); err != nil {
		result.Err = err
		return result
	}
	if result.Failure != nil {
		result.Failure.Owner, result.Failure.Inputs, result.Failure.Series, result.Failure.Seq =
			snapshot.Owner, snapshot.Inputs, snapshot.Series, snapshot.Seq
	}
	if walk.snapshot == nil {
		result.Err = ErrUnboundReplay
		return result
	}
	if !replaySnapshotsEqual(snapshot, *walk.snapshot) {
		result.Err = fmt.Errorf("walk binding does not match the requested original snapshot: %w", ErrStaleReplay)
		return result
	}
	if !walkHasResult(walk) {
		return result
	}
	valid := false
	changed := false
	err := s.WithCheckRecord(snapshot.Owner, nil, func(r *CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil || r.Owner != snapshot.Owner || r.Seq != snapshot.Seq || r.Read != snapshot.Inputs ||
			r.Check == nil || r.Check.Seq != snapshot.Seq || !reflect.DeepEqual(r.PatchFailure, snapshot.Failure) ||
			!reflect.DeepEqual(r.ApplyErr, snapshot.ApplyErr) {
			return false, ErrStaleReplay
		}
		valid = true
		changed = recordWalkOutcomes(r, snapshot, yolo, walk, now)
		return changed, nil
	})
	if err != nil {
		result.Err = err
		if errors.Is(err, ErrStaleReplay) {
			result.Err = ErrStaleReplay
		}
		return result
	}
	result.Recorded = valid && changed
	return result
}

// (The successful-check clearing in CheckPatched is deliberately separate from replay writes.)

// ReclassifyLegacyApplyError locally retries one opaque schema-1 replay diagnosis, never using
// its text as a classifier or allowing fetch/prefetch during the probe.
func (s *Store) ReclassifyLegacyApplyError(snapshot ReplaySnapshot, series *Series, target ListEntry,
	yolo string, opts LegacyReplayOptions) LegacyReplayResult {
	result := LegacyReplayResult{State: "not-needed"}
	if err := invalidSnapshot(snapshot); err != nil {
		result.Err = err
		return result
	}
	if series == nil {
		result.Err = errors.New("cannot reclassify an opaque apply error without a series")
		return result
	}
	if series.Digest != snapshot.Series {
		result.State, result.Err = "stale", ErrStaleReplay
		return result
	}
	if !series.Replays() || strings.HasPrefix(snapshot.Inputs.Repo, NpmSourcePrefix) {
		return result
	}
	record, err := s.LoadCheckRecord(snapshot.Owner)
	if err != nil {
		result.Err = err
		return result
	}
	if !snapshotMatchesRecord(record, snapshot) {
		result.State = "stale"
		result.Err = ErrStaleReplay
		return result
	}
	if failure := record.CurrentPatchFailure(snapshot.Inputs, snapshot.Series); failure != nil {
		result.State, result.Failure = "failure", failure
		return result
	}
	applyErr := record.ApplyErrAtLastCheck()
	if applyErr == nil || applyErr.Seq != snapshot.Seq || snapshot.ApplyErr == nil ||
		!reflect.DeepEqual(applyErr, snapshot.ApplyErr) {
		return result
	}
	if !legacyTargetSelected(record, snapshot, series, target) {
		result.State, result.Diagnostic = "unavailable", "the supplied target is not a selected pending entry or the current branch base"
		return result
	}
	limit := opts.Timeout
	if limit <= 0 || limit > ReplayTimeout {
		limit = ReplayTimeout
	}
	parent := s.parentCtx()
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	probeStore := *s
	probeStore.Ctx, probeStore.NoWait = ctx, true
	gitVersion, gitErr := probeStore.GitVersionContext(ctx)
	if gitErr != nil {
		result.State, result.Diagnostic = "unavailable", gitErr.Error()
		result.Attempted = true
		retryable := errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.Recorded, result.Err = persistLegacyAttempt(&probeStore, snapshot, target, yolo, "", result.Diagnostic, retryable)
		return result
	}
	if attempt := applyErr.Legacy; attempt != nil && sameLegacyAttempt(*attempt, snapshot, target, yolo, gitVersion) {
		result.State, result.Diagnostic = "unavailable", attempt.Detail
		return result
	}
	if !gitAtLeast(gitVersion, replayMinGit) {
		old := &GitTooOldError{Have: gitVersion}
		result.State, result.Diagnostic, result.Attempted = "unavailable", old.Error(), true
		result.Recorded, result.Err = persistLegacyAttempt(&probeStore, snapshot, target, yolo, gitVersion, result.Diagnostic, false)
		return result
	}
	walk := probeStore.WalkSeries(snapshot.Inputs.Repo, snapshot.Inputs.Subdir, series,
		[]ListEntry{target}, WalkOptions{Timeout: limit, Snapshot: &snapshot, LocalOnly: true, StopOnPatchFailure: true})
	result.Attempted = true
	if failure := walk.PatchFailure(); failure != nil {
		failure.Owner, failure.Inputs, failure.Series, failure.Seq = snapshot.Owner, snapshot.Inputs, snapshot.Series, snapshot.Seq
		result.Failure, result.State = failure, "failure"
	} else if diagnostic := walkOperationalDiagnostic(walk); diagnostic != "" {
		result.State, result.Diagnostic = "unavailable", diagnostic
	} else if len(walk.Results) == 1 && walk.Results[0].Clean && walk.Fit == 0 {
		result.State = "clean"
	} else {
		result.State, result.Diagnostic = "unavailable", "local-only replay did not establish a clean or concrete application-failure result"
	}
	if result.State == "unavailable" {
		retryable := errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			errors.Is(walk.Err, ErrLockHeld)
		result.Recorded, result.Err = persistLegacyAttempt(&probeStore, snapshot, target, yolo, gitVersion, result.Diagnostic, retryable)
		return result
	}
	persisted := probeStore.RecordReplay(snapshot, yolo, walk, legacyReplayNow(opts.Now))
	if persisted.Err != nil {
		if errors.Is(persisted.Err, ErrStaleReplay) {
			result.State, result.Err = "stale", ErrStaleReplay
		} else {
			result.Err = persisted.Err
		}
		return result
	}
	result.Recorded = persisted.Recorded
	if err := clearLegacyApplyError(&probeStore, snapshot); err != nil {
		result.Err = err
		result.Recorded = false
	}
	return result
}

func walkOperationalDiagnostic(walk WalkResult) string {
	if walk.Err != nil {
		return walk.Err.Error()
	}
	if walk.Base != nil {
		return walk.Base.Error()
	}
	for i := len(walk.Results) - 1; i >= 0; i-- {
		if walk.Results[i].Err != nil {
			return walk.Results[i].Err.Error()
		}
	}
	return ""
}

func legacyReplayNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now()
	}
	return now
}

func snapshotMatchesRecord(record *CheckRecord, snapshot ReplaySnapshot) bool {
	return record != nil && record.Owner == snapshot.Owner && record.Seq == snapshot.Seq &&
		record.Read == snapshot.Inputs && record.Check != nil && record.Check.Seq == snapshot.Seq &&
		reflect.DeepEqual(record.PatchFailure, snapshot.Failure) && reflect.DeepEqual(record.ApplyErr, snapshot.ApplyErr)
}

func legacyTargetSelected(record *CheckRecord, snapshot ReplaySnapshot, series *Series, target ListEntry) bool {
	if record == nil || record.Check == nil || target.Commit == "" {
		return false
	}
	if target.Commit == series.Base && snapshot.Inputs.Base == series.Base {
		return record.Check.BaseOnBranch
	}
	for _, candidate := range record.Candidates(snapshot.Inputs) {
		if candidate == target {
			return true
		}
	}
	return false
}

func sameLegacyAttempt(attempt LegacyReplayAttempt, snapshot ReplaySnapshot, target ListEntry, yolo, git string) bool {
	return attempt.State == "unavailable" && attempt.Seq == snapshot.Seq && attempt.Inputs == snapshot.Inputs &&
		attempt.Series == snapshot.Series && attempt.Target == target && attempt.Yolo == yolo && attempt.Git == git
}

func persistLegacyAttempt(s *Store, snapshot ReplaySnapshot, target ListEntry, yolo, git, detail string,
	retryable bool) (bool, error) {
	if retryable {
		return false, nil
	}
	recorded := false
	err := s.WithCheckRecord(snapshot.Owner, nil, func(r *CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil || !snapshotMatchesRecord(r, snapshot) || r.ApplyErr == nil || r.ApplyErr.Seq != snapshot.Seq {
			return false, ErrStaleReplay
		}
		r.ApplyErr.Legacy = &LegacyReplayAttempt{Seq: snapshot.Seq, Inputs: snapshot.Inputs, Series: snapshot.Series,
			Target: target, Yolo: yolo, Git: git, State: "unavailable", Detail: detail}
		recorded = true
		return true, nil
	})
	return recorded && err == nil, err
}

func clearLegacyApplyError(s *Store, snapshot ReplaySnapshot) error {
	expected, err := s.LoadCheckRecord(snapshot.Owner)
	if err != nil || expected.Seq != snapshot.Seq || expected.Read != snapshot.Inputs || expected.Check == nil ||
		expected.Check.Seq != snapshot.Seq || !reflect.DeepEqual(expected.ApplyErr, snapshot.ApplyErr) {
		return ErrStaleReplay
	}
	expectedFailure := clonePatchFailure(expected.PatchFailure)
	return s.WithCheckRecord(snapshot.Owner, nil, func(r *CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil || r.Owner != snapshot.Owner || r.Seq != snapshot.Seq || r.Read != snapshot.Inputs ||
			r.Check == nil || r.Check.Seq != snapshot.Seq || !reflect.DeepEqual(r.ApplyErr, snapshot.ApplyErr) ||
			!reflect.DeepEqual(r.PatchFailure, expectedFailure) {
			return false, ErrStaleReplay
		}
		r.ApplyErr = nil
		return true, nil
	})
}

// The kinds a Held names besides an outcome's: the last check's problem, and an apply error the
// last walk of its list ended in.
const (
	HeldByProblem    = "problem"
	HeldByApplyError = "apply-error"
	// HeldByWalkBack is a walk-back hold (CheckRecord.WalkBack): its Error is the hold's line.
	HeldByWalkBack = "walk-back"
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
	if h := r.WalkBackHolds(in, series); h != nil {
		return &Held{Kind: HeldByWalkBack, Entry: h.Newest, Error: h.Line()}
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
