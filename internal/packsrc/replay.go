package packsrc

// replay.go is a patched fork's REPLAY and the NEWEST-FIT WALK over it
// (docs/reference/patched-forks.md §5, §6.4; PF-D6, PF-D10, PF-D18): the series made into commits at
// its base, then those commits picked onto each entry of the walk's list, newest first, until one
// takes the whole series cleanly.
//
//  1. AT THE BASE: `git am` applies each member, in series order, onto the series' base commit. A
//     series exported with `--base` applies there by construction; one that does not is a
//     malformed series (SeriesBaseError), the fork's reason, never a conflict with the upstream.
//  2. ONTO AN ENTRY: each commit step 1 made is picked three-way onto the entry, one merge per
//     commit whose base is that commit's parent — `git merge-tree --write-tree --merge-base`, the
//     merge `git cherry-pick` and `git rebase` make, with no work tree. A pick whose result is the
//     tree it was picked onto changed nothing, which is clean, for one of two reasons: the member
//     is ALREADY UPSTREAM, or its commit was EMPTY WHEN WRITTEN (its tree is its parent's) and so
//     could never change anything. A pick that stops with conflicting paths is a CONFLICT; anything
//     else that stops the replay (a blob that is not there, a git that fails or runs out of time)
//     is an APPLY ERROR.
//  3. A CONFLICTING MEMBER THAT IS UPSTREAM ALL THE SAME: before a conflict is said, the member's
//     patch-id (`git patch-id --stable`) is compared with that of every commit between the
//     series' base and the entry that changes the same paths, the rule by which `git rebase` drops
//     a commit as already applied. An equal one makes the member ALREADY UPSTREAM, as a pick that
//     changes nothing does: upstream merged it and then edited it further (PF-D86). Their blobs
//     are fetched into the mirror first, a blobless mirror having only the entries' own.
//
// WHERE: a scratch repository in a private temporary directory, never in a staging workspace,
// borrowing the mirror's objects read-only (objects/info/alternates) and deleted when the walk
// ends. A repository that borrows objects has NO PROMISOR, so it cannot fetch what it lacks
// (measured in the design's §5.1): every blob the replay reads is first fetched INTO THE MIRROR,
// through the store's own checked prefetch and under the mirror's lock, which is held from the
// first prefetch to the end of the last replay so no fetch or gc runs in the mirror under
// borrowed objects. The whole of the base's commit and of each entry's is prefetched: a pick's
// rename detection reads blobs anywhere in the two trees, not only the series' paths.
//
// TWO CONFIG REGIMES. The prefetch is the store's (gitCmd: the user's own git config honored, for
// a private upstream's credential helpers). The replay sees NO user or system config at all — no
// hooks, no signing, no whitespace judgment, no attributes file, no template, a fixed committer —
// because a user setting changes its answer or runs a user program: with commit.gpgsign=true
// `git am` runs the configured signer, and with apply.whitespace=error a member adding a trailing
// space fails (both MEASURED in the design). replayEnv and newScratch's `--template=` are that
// regime.
//
// THE GIT IT NEEDS: `merge-tree --merge-base` arrived in git 2.40, so an older git is an apply
// error naming the version, never a conflict.
//
// Nothing here records anything: the caller writes the outcomes into the check record once the
// mirror's lock is released (lock order, checkrecord.go).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReplayTimeout bounds one advance's replays before a build, their prefetches included: a budget
// of its own, apart from the check's fetch (§6.4), so git holds a launch at most twice
// LaunchFetchTimeout before a build (WalkOptions.Spent).
const ReplayTimeout = 60 * time.Second

// replayMinGit is the oldest git whose merge-tree takes --merge-base.
var replayMinGit = [2]int{2, 40}

// GitTooOldError is the replay's refusal of a git older than replayMinGit, before it runs anything:
// the walk returns it. It is its own type so a caller names updating git as
// the next step, where a retry would meet the same git (docs/reference/patched-forks.md PF-D58).
type GitTooOldError struct {
	// Have is the git this host runs, as `git version` names it.
	Have string
	// PatchBranch is set when what was refused is a patch branch's merge (OQ-PFK6), whose form is
	// `merge-tree --write-tree`: one floor, one error, its words the patch branch's.
	PatchBranch bool
}

// Error names what the replay needs, what this host has, and the fix.
func (e *GitTooOldError) Error() string {
	return e.Need() + ", and this host's git is " + e.Have + " — update git"
}

// Need is what the replay needs: "replaying a patch series needs git 2.40 or newer (`git merge-tree
// --merge-base`)".
func (e *GitTooOldError) Need() string {
	if e.PatchBranch {
		return fmt.Sprintf("merging a patch branch needs git %d.%d or newer (`git merge-tree --write-tree`)",
			replayMinGit[0], replayMinGit[1])
	}
	return fmt.Sprintf("replaying a patch series needs git %d.%d or newer (`git merge-tree --merge-base`)",
		replayMinGit[0], replayMinGit[1])
}

// ReplayResult is one entry's replay.
type ReplayResult struct {
	Entry ListEntry
	// Clean is true when the whole series took: the entry is a FIT.
	Clean bool
	// Upstream lists the members whose pick changed nothing because the upstream already holds
	// their change.
	Upstream []string
	// Empty lists the members whose pick changed nothing because their commit was empty when the
	// series was written — its tree is its parent's — so nothing about them was upstream.
	Empty []string
	// Tree is the PATCHED TREE (§5.3, PF-D15) of a clean replay: the git tree of the source's
	// subdirectory (the whole tree at the repository root) in the replay's last commit.
	Tree string
	// Conflict is the member that stopped the replay and its conflicting paths; nil otherwise.
	Conflict *ReplayConflict
	// Err is an APPLY ERROR: anything else that stopped the replay. The entry stays pending.
	Err error
	// PatchFailure is set only for a non-cancelled member-application command failure.
	PatchFailure *PatchFailure
	// head is a clean replay's result in the scratch repository, for OnFit's checkout: its last
	// commit, or the tree of its last pick, which pick does not commit.
	head string
}

// ReplayConflict is the member a conflicting replay stopped on.
type ReplayConflict struct {
	Member string
	Paths  []string
}

// SeriesBaseError is a series that does not apply at its own base: a malformed series, the fork's
// reason, which the next step repairs by re-exporting it.
type SeriesBaseError struct {
	Member, Base, Detail string
}

func (e *SeriesBaseError) Error() string {
	return e.Member + " does not apply at the series' base " + shortCommit(e.Base) + " (" + e.Detail +
		") — re-export the series from its branch with `git format-patch --base=<upstream commit>`"
}

// WalkOptions tunes WalkSeries.
type WalkOptions struct {
	// Snapshot binds this walk to the original check authority captured before replay.
	Snapshot *ReplaySnapshot
	// Timeout is the walk's bound; zero means ReplayTimeout.
	Timeout time.Duration
	// Spent is what earlier replays of the same act took of that bound: one advance's replays
	// before a build — its walk, the base's walk when nothing on the list fits, and the build's own
	// replay into src/ — share the one bound, so git holds a launch at most a check's fetch and one
	// replay bound before a build (§6.4, PF-D44). A walk with nothing left is an apply error at
	// once, naming the whole bound.
	Spent time.Duration
	// Waiting is told when the walk is about to block on the mirror's lock.
	Waiting func(string)
	// All replays every entry instead of stopping at the first fit (a status survey).
	All bool
	// StopOnPatchFailure ends at the first concrete conflict instead of reaching an older fit.
	StopOnPatchFailure bool
	// LocalOnly reclassifies legacy failures without prefetching or lazy fetching.
	LocalOnly bool
	// OnFit, when non-nil, is handed the newest fit's source subdirectory (the whole tree for a
	// repository-root source) checked out of the replay's last commit into a directory inside
	// the walk's private scratch space, with its links as links and no .git, before the scratch
	// repository is deleted: the build act's copy into its src/ (§5.1, PF-D18). The directory is
	// gone once the walk returns. An error from it is the fit's apply error, which leaves the
	// entry pending, since nothing about the upstream or the series failed.
	OnFit func(tree string) error
}

// WalkResult is a walk's answer.
type WalkResult struct {
	// Results holds one result per entry replayed, in list order: every entry above the newest
	// fit (each a conflict) and the fit itself, or up to the entry an apply error stopped at.
	Results []ReplayResult
	// Fit is the newest fit's index in Results, -1 when no entry took the series.
	Fit int
	// Base is a series that does not apply at its own base; Results is empty then.
	Base *SeriesBaseError
	// Err is an apply error before any entry was reached (the base's blobs, the scratch
	// repository, a git too old); Results is empty then.
	Err error
	// Git is the git version the replay ran, for the outcome key.
	Git string
	// snapshot is a private copy of the original authority captured at WalkSeries entry.
	snapshot *ReplaySnapshot
}

func (w WalkResult) PatchFailure() *PatchFailure {
	if w.Base != nil {
		return &PatchFailure{Target: ListEntry{Commit: w.Base.Base}, Kind: "base", Member: w.Base.Member, Detail: w.Base.Detail}
	}
	for _, r := range w.Results {
		if r.Conflict != nil {
			return &PatchFailure{Target: r.Entry, Kind: "conflict", Member: r.Conflict.Member, Paths: append([]string(nil), r.Conflict.Paths...)}
		}
		if r.PatchFailure != nil {
			f := clonePatchFailure(r.PatchFailure)
			f.Target = r.Entry
			return f
		}
	}
	return nil
}

// WalkSeries replays series down list, newest first, until an entry takes it: the walk of §6.4.
// repo is the upstream's repository and subdir the source's subdirectory, for the patched tree.
func (s *Store) WalkSeries(repo, subdir string, series *Series, list []ListEntry, opts WalkOptions) WalkResult {
	res := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(opts.Snapshot)}
	if opts.Snapshot != nil {
		if err := invalidSnapshot(*res.snapshot); err != nil {
			res.Err = err
			return res
		}
		if series == nil || res.snapshot.Inputs.Repo != repo || res.snapshot.Inputs.Subdir != subdir ||
			res.snapshot.Series != series.Digest {
			res.Err = errors.New("replay repository, subdirectory, or series does not match its original snapshot")
			return res
		}
	}
	if strings.HasPrefix(repo, NpmSourcePrefix) {
		n := walkNpm(list, opts)
		n.snapshot = res.snapshot
		return n
	}
	if series == nil {
		res.Err = errors.New("cannot replay a nil series")
		return res
	}
	if series.PatchBranch != nil && series.PatchBranch.Commit == "" {
		res.Err = &PatchBranchUnresolvedError{Source: series.PatchBranch.Source}
		return res
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = ReplayTimeout
	}
	left := timeout - opts.Spent
	if left <= 0 {
		res.Err = fmt.Errorf("the series' replay ran out of its %s", timeout)
		return res
	}
	ctx, cancel := context.WithTimeout(s.parentCtx(), left)
	defer cancel()
	gitVer, err := s.GitVersionContext(ctx)
	res.Git = gitVer
	if err != nil {
		res.Err = err
		return res
	}
	if !gitAtLeast(gitVer, replayMinGit) {
		// ONE FLOOR FOR BOTH (OQ-PFK6): a patch branch's `merge-tree --write-tree` is git 2.38's, under the
		// replay's 2.40, so an older git is refused by the same error and never merged another way.
		res.Err = &GitTooOldError{Have: gitVer, PatchBranch: series.PatchBranch != nil}
		return res
	}
	// A PATCH BRANCH'S MIRROR is its own repository's (patchbranch.go): locked beside the upstream's, in one order,
	// for the walk's whole length, and borrowed by the scratch repository with it.
	mirror := s.mirrorPath(repo)
	mirrors, locks := []string{mirror}, []string{repo}
	patchBranchMirror := ""
	if series.PatchBranch != nil {
		patchBranchMirror = s.mirrorPath(series.PatchBranch.mirrorRepo())
		mirrors, locks = append(mirrors, patchBranchMirror), append(locks, series.PatchBranch.mirrorRepo())
	}
	unlock, err := s.lockMirrors(locks, opts.Waiting)
	if err != nil {
		res.Err = err
		return res
	}
	defer unlock()
	b := budget{ctx: ctx, d: timeout}
	if !mirrorExists(mirror) {
		res.Err = fmt.Errorf("this machine holds no copy of %s to replay the series in — a check fetches it", repo)
		return res
	}
	if patchBranchMirror != "" && !mirrorExists(patchBranchMirror) {
		res.Err = fmt.Errorf("this machine holds no copy of the patch branch %s to merge — a check fetches it", series.PatchBranch.Source)
		return res
	}

	// The base's files, into the mirror, then the series made into commits there. An EMPTY SERIES
	// (an unmodified extension, EmptySeries) has no base and makes no commit: each entry's replay
	// is its own tree, which every entry fits.
	// A probe that found nothing missing is kept, so the check after it asks no second time.
	var baseProbe, patchBranchProbe string
	if series.Replays() && !opts.LocalOnly {
		baseProbe = s.prefetchBlobs(b, mirror, series.Base, "")
		if series.PatchBranch != nil {
			// The patch branch's own files, from the patch branch's repository into its blobless mirror.
			patchBranchProbe = s.prefetchBlobs(b, patchBranchMirror, series.PatchBranch.Commit, "")
		}
	}
	sc, err := newScratchOf(s.git(), b, mirrors...)
	if err != nil {
		res.Err = err
		return res
	}
	defer sc.remove()
	sc.base = series.Base
	if !opts.LocalOnly {
		sc.fetch = func(oids []string) { s.prefetchObjects(b, mirror, oids) }
	}
	if series.Replays() {
		type need struct{ mirror, commit, probe string }
		needs := []need{{mirror, series.Base, baseProbe}}
		if series.PatchBranch != nil {
			needs = append(needs, need{patchBranchMirror, series.PatchBranch.Commit, patchBranchProbe})
		}
		for _, n := range needs {
			if opts.LocalOnly {
				if _, err := missingBlobsScratch(sc, n.commit); err != nil {
					res.Err = err
					return res
				}
			} else if _, err := s.missingBlobs(b, n.mirror, n.commit, n.probe); err != nil {
				res.Err = err
				return res
			}
		}
	}
	var commits []string
	if series.Len() > 0 {
		var baseErr *SeriesBaseError
		commits, baseErr, err = sc.applyAtBase(series)
		switch {
		case baseErr != nil:
			res.Base = baseErr
			return res
		case err != nil:
			res.Err = err
			return res
		}
	}

	for _, e := range list {
		r := ReplayResult{Entry: e}
		probe := ""
		switch {
		case opts.LocalOnly:
		case e.Commit == series.Base && baseProbe != "":
			// AN ENTRY THAT IS THE BASE (the upstream has not moved) was probed complete above, under
			// the lock this walk still holds: a fetch since can only have added objects, so a second
			// `rev-list` could only repeat the answer. One git start saved per such walk.
			probe = baseProbe
		default:
			probe = s.prefetchBlobs(b, mirror, e.Commit, "")
		}
		// The check's listing names the entry's root tree first, which the pick compares against.
		var missingErr error
		var entryTree string
		if opts.LocalOnly {
			entryTree, missingErr = missingBlobsScratch(sc, e.Commit)
		} else {
			entryTree, missingErr = s.missingBlobs(b, mirror, e.Commit, probe)
		}
		if missingErr == nil && series.PatchBranch != nil && !opts.LocalOnly {
			// An entry that does not contain the patch branch's base (the good build's own commit after the
			// patch branch moved) merges from an older base, whose files the mirror may lack.
			if mb, err := sc.git("", "merge-base", e.Commit, series.PatchBranch.Commit); err == nil {
				if mb = strings.TrimSpace(mb); mb != "" && mb != series.Base {
					s.prefetchBlobs(b, mirror, mb, "")
				}
			}
		}
		if missingErr != nil {
			r.Err = missingErr
			res.Results = append(res.Results, r)
			return res
		}
		if series.PatchBranch != nil {
			sc.mergePatchBranch(&r, series.PatchBranch, subdir)
		} else {
			sc.pick(&r, series, commits, subdir, entryTree)
		}
		if r.Clean && res.Fit < 0 && opts.OnFit != nil {
			if err := sc.export(r.head, subdir, opts.OnFit); err != nil {
				r.Clean, r.Tree, r.Err = false, "", err
			}
		}
		res.Results = append(res.Results, r)
		switch {
		case r.Conflict != nil:
			if opts.StopOnPatchFailure {
				return res
			}
		case r.Err != nil:
			// An apply error ends the walk: the entries below it stay unsettled, and pending.
			return res
		case r.Clean && res.Fit < 0:
			res.Fit = len(res.Results) - 1
			if !opts.All {
				return res
			}
		}
	}
	return res
}

// walkNpm is the walk of an npm source's list (npm.go): there is no series to replay and no
// repository to replay it in, so the list's first entry — the registry's answer for the spec — is
// the fit, and OnFit is handed an empty directory, the checkout an npm tree's install starts from
// (docs/design/pi-extension-store-builds.md §4.2).
func walkNpm(list []ListEntry, opts WalkOptions) WalkResult {
	res := WalkResult{Fit: -1}
	if len(list) == 0 {
		return res
	}
	r := ReplayResult{Entry: list[0], Clean: true}
	if opts.OnFit != nil {
		dir, err := os.MkdirTemp("", "yolo-npm-tree-")
		if err == nil {
			err = opts.OnFit(dir)
			_ = os.RemoveAll(dir)
		}
		if err != nil {
			r.Clean, r.Err = false, fmt.Errorf("copying the empty checkout: %w", err)
			res.Results = append(res.Results, r)
			return res
		}
	}
	res.Results = append(res.Results, r)
	res.Fit = 0
	return res
}

// missingBlobsScratch checks object availability through the isolated non-promisor replay repository.
// It cannot trigger lazy fetches from a partial mirror, even with Git versions that ignore the
// GIT_NO_LAZY_FETCH environment variable.
// Its first result is commit's root tree when the listing names it (listedRootTree).
func missingBlobsScratch(sc *scratch, commit string) (string, error) {
	out, err := sc.git("", "rev-list", "--objects", "--missing=print", commit+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("could not read the upstream's files at %s: %s", shortCommit(commit), oneLine(err))
	}
	if n := countMissing(out); n > 0 {
		return "", fmt.Errorf("could not fetch the upstream's files at %s (%d missing after the local-only probe)",
			shortCommit(commit), n)
	}
	return listedRootTree(out), nil
}

// missingBlobs is an apply error when the mirror still lacks a file of commit after its prefetch:
// a blob that could not be fetched is never a conflict. probe is that prefetch's listing when it
// found nothing missing (prefetchBlobs), which is this check's answer already; "" asks the mirror.
// Its first result is commit's root tree when the listing names it (listedRootTree).
func (s *Store) missingBlobs(b budget, mirror, commit, probe string) (string, error) {
	out := probe
	if out == "" {
		var err error
		out, err = s.runIn(b, mirror, "rev-list", "--objects", "--missing=print", commit+"^{tree}")
		if err != nil {
			return "", fmt.Errorf("could not read the upstream's files at %s: %s", shortCommit(commit), oneLine(err))
		}
	}
	if n := countMissing(out); n > 0 {
		return "", fmt.Errorf("could not fetch the upstream's files at %s (%d missing after the prefetch) — "+
			"the next check retries, or `yolo pack update` retries now", shortCommit(commit), n)
	}
	return listedRootTree(out), nil
}

// countMissing counts the objects a `rev-list --missing=print` listing names as missing.
func countMissing(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "?") {
			n++
		}
	}
	return n
}

// listedRootTree is the tree a `rev-list --objects <commit>^{tree}` listing starts with — git
// lists the tree it was given before anything in it, with no path — or "" when the first line is
// not a present object's id, and the caller asks git for the tree instead.
func listedRootTree(out string) string {
	first, _, _ := strings.Cut(out, "\n")
	f := strings.Fields(first)
	if len(f) != 1 || !isHexID(f[0]) {
		return ""
	}
	return f[0]
}

// scratch is the replay's scratch repository.
type scratch struct {
	gitBin string
	dir    string // the private temporary directory holding both
	repo   string // the repository, its work tree at repo and its git dir at repo/.git
	b      budget
	// base is the series' base, where the upstream range a conflicting member is compared against
	// starts (upstreamEquivalent); "" compares nothing.
	base string
	// fetch fetches objects into the mirror the scratch repository borrows from, nil for a replay
	// that reads only what is already here.
	fetch func(oids []string)
}

// newScratchOf makes a scratch repository borrowing every mirror's objects, run by the git binary
// gitBin: the upstream's, and a patch branch's (patchbranch.go), whose commits and files live in a mirror of
// their own.
func newScratchOf(gitBin string, b budget, mirrors ...string) (*scratch, error) {
	dir, err := os.MkdirTemp("", "yolo-patched-replay-")
	if err != nil {
		return nil, err
	}
	sc := &scratch{gitBin: gitBin, dir: dir, repo: filepath.Join(dir, "repo"), b: b}
	// `--template=` copies no template: a user's template directory (init.templateDir, or
	// GIT_TEMPLATE_DIR, which replayEnv drops anyway) would otherwise land as this repository's own
	// config and info/attributes, which no `-c` here fully overrides — merge.renames among them.
	if _, err := sc.git("", "init", "--template=", "-q", "-b", "yolo-replay", sc.repo); err != nil {
		sc.remove()
		return nil, err
	}
	info := filepath.Join(sc.repo, ".git", "objects", "info")
	var alternates strings.Builder
	for _, m := range mirrors {
		objects, aerr := filepath.Abs(filepath.Join(m, "objects"))
		if aerr != nil {
			err = aerr
			break
		}
		alternates.WriteString(objects + "\n")
	}
	if err == nil {
		err = os.MkdirAll(info, 0o755)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(info, "alternates"), []byte(alternates.String()), 0o644)
	}
	if err != nil {
		sc.remove()
		return nil, fmt.Errorf("borrowing the mirror's objects: %w", err)
	}
	return sc, nil
}

func (sc *scratch) remove() { _ = os.RemoveAll(sc.dir) }

// replayConfig is every `-c` a replay run carries: no hook, no fsmonitor, no signing, no line-ending
// or attributes file of the user's, no rerere, no gc, no network, and whitespace not judged.
var replayConfig = []string{
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.fsmonitor=false",
	"-c", "core.autocrlf=false",
	"-c", "core.attributesFile=" + os.DevNull,
	"-c", "commit.gpgSign=false",
	"-c", "tag.gpgSign=false",
	"-c", "rerere.enabled=false",
	"-c", "gc.auto=0",
	"-c", "maintenance.auto=false",
	"-c", "protocol.allow=never",
	"-c", "apply.whitespace=nowarn",
	"-c", "advice.detachedHead=false",
}

// replayIdentity is the identity and date every replay commit carries, so a replay is a function
// of the series and the upstream alone. `git am` takes each member's author from its mail; the
// picks' commits (commit-tree) take this author.
var replayIdentity = []string{
	"GIT_COMMITTER_NAME=yolo patch replay", "GIT_COMMITTER_EMAIL=yolo@localhost",
	"GIT_COMMITTER_DATE=@0 +0000",
	"GIT_AUTHOR_NAME=yolo patch replay", "GIT_AUTHOR_EMAIL=yolo@localhost",
	"GIT_AUTHOR_DATE=@0 +0000",
}

// replayKeepsGitEnv is the one GIT_ variable a replay inherits: where this git finds its own
// programs (`git am` is one), which a relocated git install may need set.
const replayKeepsGitEnv = "GIT_EXEC_PATH"

// replayEnv is the replay's environment: the process's with EVERY git variable dropped but
// replayKeepsGitEnv — git's repository state, every source of configuration but the run's own
// `-c` (no global or system file, nothing carried in GIT_CONFIG_PARAMETERS or GIT_CONFIG_COUNT and
// its pairs), and every other variable that changes git's answer: GIT_TEMPLATE_DIR (a template's
// config and attributes become the scratch repository's own), GIT_ATTR_SOURCE (attributes read
// from a tree), GIT_DEFAULT_HASH (a scratch repository that cannot borrow the mirror's objects),
// the identity — then no system attributes file, no lazy fetch, no prompt, and the fixed
// committer. An allowlist, so a variable a later git adds cannot reach the replay either.
func replayEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "GIT_") && key != replayKeepsGitEnv {
			continue
		}
		out = append(out, kv)
	}
	return append(append(out, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS="), replayIdentity...)
}

// git runs one replay git in dir (the scratch repository unless ""), returning stdout, and an
// error carrying stderr. An exit status other than 0 is returned as *exec.ExitError inside it.
func (sc *scratch) git(dir string, args ...string) (string, error) {
	out, _, err := sc.gitStatus(dir, args...)
	return out, err
}

// gitStatus is git that also returns the exit status, -1 when git did not exit by itself.
func (sc *scratch) gitStatus(dir string, args ...string) (string, int, error) {
	return sc.gitStatusEnv(dir, nil, args...)
}

// gitStatusEnv is gitStatus with env appended after replayEnv: the one GIT_ variable a replay
// run sets itself, the private index of the export's checkout.
func (sc *scratch) gitStatusEnv(dir string, env []string, args ...string) (string, int, error) {
	return sc.gitRun(dir, env, nil, args...)
}

// gitInput is git with stdin fed to it, in the scratch repository: `git patch-id` reads a diff there.
func (sc *scratch) gitInput(stdin string, args ...string) (string, error) {
	out, _, err := sc.gitRun("", nil, strings.NewReader(stdin), args...)
	return out, err
}

// gitRun is every replay git run: gitStatusEnv's, with stdin when it is not nil.
func (sc *scratch) gitRun(dir string, env []string, stdin io.Reader, args ...string) (string, int, error) {
	full := append(append([]string{}, replayConfig...), args...)
	if dir == "" && len(args) > 0 && args[0] != "init" {
		dir = sc.repo
	}
	cmd := exec.CommandContext(sc.b.ctx, sc.gitBin, full...)
	cmd.Dir = dir
	cmd.Env = append(replayEnv(), env...)
	cmd.WaitDelay = gitWaitDelay
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if sc.b.ctx.Err() != nil {
		return "", -1, fmt.Errorf("the series' replay ran out of its %s", sc.b.d)
	}
	if err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		said := strings.TrimSpace(strings.TrimSpace(stderr.String()) + "\n" + strings.TrimSpace(stdout.String()))
		return stdout.String(), code, fmt.Errorf("git %s: %w\n%s", gitLabel(args), err, said)
	}
	return stdout.String(), 0, nil
}

// amArgs is the `git am` every member is applied with, its files after it.
var amArgs = []string{"am", "-q", "--no-3way", "--whitespace=nowarn"}

// applyAtBase checks the series' base out, detached, in the scratch work tree and makes each member
// a commit with `git am`. It returns the commits in series order.
//
// ONE `git am` FOR THE WHOLE SERIES when that is sure to make the same commits (amWhole), since
// every git start costs (on a macOS host about 25 ms, more than the apply itself); otherwise, and
// whenever that one run does not take the whole series, ONE MEMBER AT A TIME (amEach), so a
// failure names its member and says what it always said.
func (sc *scratch) applyAtBase(series *Series) ([]string, *SeriesBaseError, error) {
	if _, err := sc.git("", "checkout", "-q", "--detach", series.Base); err != nil {
		return nil, nil, fmt.Errorf("checking the series' base %s out: %s", shortCommit(series.Base), oneLine(err))
	}
	files := filepath.Join(sc.dir, "series")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return nil, nil, err
	}
	if wholeSeriesAppliable(series) {
		commits, err := sc.amWhole(series, files)
		if err != nil || commits != nil {
			return commits, nil, err
		}
		// The one run did not take the series: back to the base, and member by member.
		if _, err := sc.git("", "checkout", "-q", "--detach", series.Base); err != nil {
			return nil, nil, fmt.Errorf("checking the series' base %s out: %s", shortCommit(series.Base), oneLine(err))
		}
	}
	return sc.amEach(series, files)
}

// wholeSeriesAppliable reports whether one `git am` over every member makes exactly the commits
// one run per member makes, in the same order: each member is ONE MAIL, which git's mbox split
// finds only at a line starting "From ", so a member that starts with one and has no other is
// exactly one patch, and one run applies the mails in the order of its files. A member with no
// such line, or more than one, applies member by member.
func wholeSeriesAppliable(series *Series) bool {
	if len(series.Members) < 2 {
		return false
	}
	for _, m := range series.Members {
		if !bytes.HasPrefix(m.Data, []byte("From ")) || bytes.Contains(m.Data, []byte("\nFrom ")) {
			return false
		}
	}
	return true
}

// amWhole applies every member in one `git am` and returns the commits it made, in series order.
// nil commits and a nil error mean the run did not take the whole series, left nothing in progress,
// and the caller applies member by member, which names the member that fails; an error is one
// amEach would have returned too (a run ended by the walk's bound or by a signal).
func (sc *scratch) amWhole(series *Series, files string) ([]string, error) {
	args := append([]string{}, amArgs...)
	for _, m := range series.Members {
		p := filepath.Join(files, m.Name)
		if err := os.WriteFile(p, m.Data, 0o600); err != nil {
			return nil, err
		}
		args = append(args, p)
	}
	if _, err := sc.git("", args...); err != nil {
		var exit *exec.ExitError
		if sc.b.ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() < 1 {
			return nil, err
		}
		// git's own way out of a stopped `git am`; the state directory goes by hand if that fails,
		// since the next `git am` refuses to start over one.
		if _, err := sc.git("", "am", "--abort"); err != nil {
			if sc.b.ctx.Err() != nil {
				return nil, err
			}
			if rerr := os.RemoveAll(filepath.Join(sc.repo, ".git", "rebase-apply")); rerr != nil {
				return nil, rerr
			}
		}
		return nil, nil
	}
	out, err := sc.git("", "rev-list", "--reverse", "HEAD", "^"+series.Base)
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(out)
	if len(commits) != len(series.Members) {
		return nil, nil
	}
	return commits, nil
}

// amEach applies the members one `git am` at a time, so a failure names its member.
func (sc *scratch) amEach(series *Series, files string) ([]string, *SeriesBaseError, error) {
	var commits []string
	for _, m := range series.Members {
		p := filepath.Join(files, m.Name)
		if err := os.WriteFile(p, m.Data, 0o600); err != nil {
			return nil, nil, err
		}
		if _, err := sc.git("", append(append([]string{}, amArgs...), p)...); err != nil {
			if sc.b.ctx.Err() != nil {
				return nil, nil, err
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() >= 1 {
				return nil, &SeriesBaseError{Member: m.Name, Base: series.Base, Detail: err.Error()}, nil
			}
			return nil, nil, err
		}
		head, err := sc.git("", "rev-parse", "HEAD")
		if err != nil {
			return nil, nil, err
		}
		commits = append(commits, strings.TrimSpace(head))
	}
	return commits, nil, nil
}

// pick picks commits onto r.Entry's commit one at a time, filling r. entryTree is the entry's root
// tree when the caller already read it, "" to ask git.
func (sc *scratch) pick(r *ReplayResult, series *Series, commits []string, subdir, entryTree string) {
	onto, ontoTree := r.Entry.Commit, entryTree
	if ontoTree == "" {
		out, err := sc.git("", "rev-parse", onto+"^{tree}")
		if err != nil {
			r.Err = fmt.Errorf("reading %s: %s", shortCommit(onto), oneLine(err))
			return
		}
		ontoTree = strings.TrimSpace(out)
	}
	for i, c := range commits {
		member := series.Members[i].Name
		out, code, err := sc.gitStatus("", "merge-tree", "--write-tree", "-z", "--name-only",
			"--no-messages", "--merge-base="+c+"^", onto, c)
		fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
		switch {
		case code == 1 && len(fields) > 0 && fields[0] != "" && sc.upstreamEquivalent(c, r.Entry.Commit):
			// MERGED UPSTREAM, THEN EDITED (PF-D86): the pick conflicts with the later edit, but
			// the member's change is a commit of the upstream's, which `git rebase` drops.
			r.Upstream = append(r.Upstream, member)
			continue
		case code == 1 && len(fields) > 0 && fields[0] != "":
			var paths []string
			for _, p := range fields[1:] {
				if p != "" && (len(paths) == 0 || paths[len(paths)-1] != p) {
					paths = append(paths, p)
				}
			}
			r.Conflict = &ReplayConflict{Member: member, Paths: paths}
			return
		case err != nil && code > 1 && sc.b.ctx.Err() == nil:
			r.PatchFailure = &PatchFailure{Kind: "application-command", Member: member, Detail: err.Error()}
			r.Err = fmt.Errorf("applying %s onto %s: %w", member, shortCommit(r.Entry.Commit), err)
			return
		case err != nil:
			r.Err = fmt.Errorf("picking %s onto %s: %s", member, shortCommit(r.Entry.Commit), oneLine(err))
			return
		}
		tree := fields[0]
		if tree == ontoTree {
			// The pick changed nothing. Two reasons name the same tree: the member is already in
			// the upstream, or its commit was empty when the series was written, which is no
			// upstream fact at all. Only the first may be reported as already upstream.
			empty, err := sc.memberEmpty(c)
			if err != nil {
				r.Err = fmt.Errorf("reading %s: %s", shortCommit(c), oneLine(err))
				return
			}
			if empty {
				r.Empty = append(r.Empty, member)
			} else {
				r.Upstream = append(r.Upstream, member)
			}
			continue
		}
		if i == len(commits)-1 {
			// THE LAST PICK IS NOT COMMITTED: nothing is picked onto it, and its tree is all the
			// replay's result is read for (settle, and export's checkout, which takes a tree as it
			// takes a commit), so a commit of it would be one git start for nothing.
			onto, ontoTree = tree, tree
			break
		}
		next, err := sc.git("", "commit-tree", "--no-gpg-sign", tree, "-p", onto, "-m", member)
		if err != nil {
			r.Err = fmt.Errorf("committing %s onto %s: %s", member, shortCommit(r.Entry.Commit), oneLine(err))
			return
		}
		onto, ontoTree = strings.TrimSpace(next), tree
	}
	sc.settle(r, onto, ontoTree, subdir)
}

// mergePatchBranch merges the patch branch's commit into r.Entry's commit with no work tree — `git merge-tree
// --write-tree`, git computing the merge base — filling r as pick does: a CONFLICT names the patch branch
// and the conflicting paths; a clean merge commits its tree with both commits as parents, so the
// export and the patched tree read it as they read a replay's last commit.
func (sc *scratch) mergePatchBranch(r *ReplayResult, patchBranch *SeriesPatchBranch, subdir string) {
	onto := r.Entry.Commit
	member := "the patch branch " + patchBranch.Label()
	out, code, err := sc.gitStatus("", "merge-tree", "--write-tree", "-z", "--name-only", "--no-messages",
		onto, patchBranch.Commit)
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	switch {
	case code == 1 && len(fields) > 0 && fields[0] != "":
		var paths []string
		for _, p := range fields[1:] {
			if p != "" && (len(paths) == 0 || paths[len(paths)-1] != p) {
				paths = append(paths, p)
			}
		}
		r.Conflict = &ReplayConflict{Member: member, Paths: paths}
		return
	case err != nil && strings.Contains(err.Error(), "unrelated histories"):
		r.Err = fmt.Errorf("%s shares no history with %s — git refuses to merge unrelated histories; merge the "+
			"upstream into the patch branch", member, shortCommit(onto))
		return
	case err != nil && code > 1 && sc.b.ctx.Err() == nil:
		r.PatchFailure = &PatchFailure{Kind: "application-command", Member: member, Detail: err.Error()}
		r.Err = fmt.Errorf("merging %s into %s: %w", member, shortCommit(onto), err)
		return
	case err != nil:
		r.Err = fmt.Errorf("merging %s into %s: %s", member, shortCommit(onto), oneLine(err))
		return
	}
	next, err := sc.git("", "commit-tree", "--no-gpg-sign", fields[0], "-p", onto, "-p", patchBranch.Commit,
		"-m", "merge "+member)
	if err != nil {
		r.Err = fmt.Errorf("committing the merge of %s into %s: %s", member, shortCommit(onto), oneLine(err))
		return
	}
	sc.settle(r, strings.TrimSpace(next), fields[0], subdir)
}

// settle fills a clean replay's result from its result onto (its last commit, or its last pick's
// tree): the source's subdirectory's tree (the whole tree at the repository root), which must be a
// directory. headTree is onto's root tree, which the replay has just made or read, so a repository-root source asks git nothing; a
// subdirectory is looked up and its kind read in one `cat-file --batch-check`, and anything but a
// tree there is asked again the long way, for the words the failure has always had.
func (sc *scratch) settle(r *ReplayResult, onto, headTree, subdir string) {
	if subdir == "" && headTree != "" {
		r.Clean, r.Tree, r.head = true, headTree, onto
		return
	}
	if subdir != "" && !strings.ContainsAny(subdir, "\n\r") {
		out, err := sc.gitInput(onto+":"+subdir+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
		if f := strings.Fields(out); err == nil && len(f) == 2 && f[1] == "tree" && isHexID(f[0]) {
			r.Clean, r.Tree, r.head = true, f[0], onto
			return
		}
	}
	spec := onto + "^{tree}"
	if subdir != "" {
		spec = onto + ":" + subdir
	}
	tree, err := sc.git("", "rev-parse", "--verify", spec)
	if err != nil {
		r.Err = fmt.Errorf("the replay at %s has no %s: %s", shortCommit(r.Entry.Commit), subdirName(subdir), oneLine(err))
		return
	}
	if kind, err := sc.git("", "cat-file", "-t", strings.TrimSpace(tree)); err != nil || strings.TrimSpace(kind) != "tree" {
		r.Err = fmt.Errorf("the replay at %s has no %s as a directory", shortCommit(r.Entry.Commit), subdirName(subdir))
		return
	}
	r.Clean, r.Tree, r.head = true, strings.TrimSpace(tree), onto
}

// equivalentScanLimit bounds the upstream commits upstreamEquivalent compares one member against:
// the newest that change exactly the member's paths, between the series' base and the entry.
const equivalentScanLimit = 64

// upstreamEquivalent reports whether the member commit c's change is a commit of the upstream's
// between the series' base and entry: one whose patch-id (`git patch-id --stable`, which ignores
// the message, line numbers and whitespace at the ends of lines) equals c's, the rule `git rebase`
// drops a commit by as already applied. Only commits that change exactly c's paths can have its
// patch-id, so those are found from the trees alone, the newest equivalentScanLimit of them, and
// only their blobs are fetched. false on anything it cannot read, so a doubt leaves the conflict.
func (sc *scratch) upstreamEquivalent(c, entry string) bool {
	if sc.base == "" {
		return false
	}
	paths, err := sc.git("", "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", c)
	if err != nil {
		return false
	}
	want := strings.Split(strings.TrimRight(paths, "\x00"), "\x00")
	if len(want) == 0 || want[0] == "" {
		return false
	}
	args := []string{"rev-list", "--no-merges", "--max-count=" + strconv.Itoa(equivalentScanLimit), entry,
		"^" + sc.base, "--"}
	for _, p := range want {
		args = append(args, literalPathspec(p))
	}
	out, err := sc.git("", args...)
	if err != nil {
		return false
	}
	upstream := strings.Fields(out)
	raws, batched := sc.rawChanges(upstream)
	var candidates, oids []string
	for _, u := range upstream {
		raw := raws[u] // a commit the batch printed nothing for changes nothing
		if !batched {
			var err error
			if raw, err = sc.git("", "diff-tree", "--no-commit-id", "-r", "-z", "--no-renames", u); err != nil {
				return false
			}
		}
		got, blobs := rawPathsAndBlobs(raw)
		if !slices.Equal(got, want) {
			continue
		}
		candidates, oids = append(candidates, u), append(oids, blobs...)
	}
	if len(candidates) == 0 {
		return false
	}
	if sc.fetch != nil {
		sc.fetch(oids)
	}
	if ids, ok := sc.patchIDs(append([]string{c}, candidates...)); ok {
		mine := ids[c]
		if mine == "" {
			return false
		}
		for _, u := range candidates {
			if ids[u] == mine {
				return true
			}
		}
		return false
	}
	mine := sc.patchID(c)
	if mine == "" {
		return false
	}
	for _, u := range candidates {
		if sc.patchID(u) == mine {
			return true
		}
	}
	return false
}

// rawChanges is `git diff-tree -r -z --no-renames` of every commit in one run (`--stdin`): each
// commit's raw records as a run of it alone prints them, a commit that changes nothing absent. ok
// is false when that run failed or printed what this does not read, and the caller asks commit by
// commit, so one unreadable commit costs no other its answer.
func (sc *scratch) rawChanges(commits []string) (map[string]string, bool) {
	if len(commits) < 2 {
		return nil, false
	}
	out, err := sc.gitInput(strings.Join(commits, "\n")+"\n", "diff-tree", "--stdin", "-r", "-z", "--no-renames")
	if err != nil {
		return nil, false
	}
	// Each commit's id, then its records — a ":"-led status and its one path (no renames, so never
	// two) — until the next commit's id. A path is read by its position, whatever it spells.
	raws := map[string]string{}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	cur := ""
	for i := 0; i < len(fields); i++ {
		switch f := fields[i]; {
		case strings.HasPrefix(f, ":") && cur != "" && i+1 < len(fields):
			raws[cur] += f + "\x00" + fields[i+1] + "\x00"
			i++
		case slices.Contains(commits, f):
			cur = f
		default:
			return nil, false
		}
	}
	return raws, true
}

// patchIDs is the stable patch-id of every commit in two runs: one `git diff-tree --stdin -p`,
// whose output `git patch-id --stable` reads commit by commit as it reads `git log -p`. A commit
// with no diff has no id, "" as patchID says. ok is false when either run failed, and the caller
// asks commit by commit (patchID), so one unreadable commit costs no other its answer.
func (sc *scratch) patchIDs(commits []string) (map[string]string, bool) {
	diff, err := sc.gitInput(strings.Join(commits, "\n")+"\n", "diff-tree", "--stdin", "-p", "--no-renames",
		"--full-index")
	if err != nil {
		return nil, false
	}
	ids := map[string]string{}
	if strings.TrimSpace(diff) == "" {
		return ids, true
	}
	out, err := sc.gitInput(diff, "patch-id", "--stable")
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if id, commit, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			ids[commit] = id
		}
	}
	return ids, true
}

// patchID is commit c's stable patch-id, "" when it cannot be read.
func (sc *scratch) patchID(c string) string {
	diff, err := sc.git("", "diff-tree", "--no-commit-id", "-p", "--no-renames", "--full-index", c)
	if err != nil || strings.TrimSpace(diff) == "" {
		return ""
	}
	out, err := sc.gitInput(diff, "patch-id", "--stable")
	if err != nil {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	return id
}

// rawPathsAndBlobs reads `git diff-tree -r -z` raw output: the paths it changes, sorted as git
// names them, and the blobs on either side of each change but the null one.
func rawPathsAndBlobs(raw string) (paths, blobs []string) {
	fields := strings.Split(strings.TrimRight(raw, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		meta := strings.Fields(fields[i]) // ":<mode> <mode> <old> <new> <status>"
		if len(meta) == 5 {
			for _, oid := range meta[2:4] {
				if strings.Trim(oid, "0") != "" {
					blobs = append(blobs, oid)
				}
			}
		}
		paths = append(paths, fields[i+1])
	}
	return paths, blobs
}

// memberEmpty reports whether the commit `git am` made for a series member is empty: its tree
// equals its parent's, so the member makes no change of its own and its pick cannot change anything.
// `git diff-tree` names the paths a commit changes, and prints none for an empty one.
func (sc *scratch) memberEmpty(c string) (bool, error) {
	out, _, err := sc.gitStatus("", "diff-tree", "--no-commit-id", "--name-only", "-r", c)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// export checks the source's subdirectory of commit (a commit or a tree) out of the scratch repository into a
// directory of the scratch space, through a private empty index as the store's own checkout does
// (Store.checkoutTree), under the replay's config regime — no hook runs, no filter or attribute of
// the user's applies — and hands its path to fn.
func (sc *scratch) export(commit, subdir string, fn func(string) error) error {
	dst := filepath.Join(sc.dir, "export")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	idx := filepath.Join(sc.dir, "export-index")
	if _, _, err := sc.gitStatusEnv("", []string{"GIT_INDEX_FILE=" + idx}, "--work-tree="+dst,
		"checkout", "--force", commit, "--", literalPathspec(subdir)); err != nil {
		return fmt.Errorf("checking the patched tree out: %s", oneLine(err))
	}
	tree := dst
	if subdir != "" {
		tree = filepath.Join(dst, filepath.FromSlash(subdir))
	}
	if err := fn(tree); err != nil {
		return fmt.Errorf("copying the patched tree: %w", err)
	}
	return nil
}

func subdirName(subdir string) string {
	if subdir == "" {
		return "tree"
	}
	return "subdirectory " + subdir
}

// gitVersions is this process's answer to `git version` per git binary.
var gitVersions sync.Map

// GitVersion is the store's git's version, "2.55.0" for "git version 2.55.0", asked once per
// process: it is part of a conflict's key, since another git may merge differently.
func (s *Store) GitVersion() (string, error) {
	return s.GitVersionContext(context.Background())
}

// GitVersionContext is GitVersion with a caller-owned execution bound.
func (s *Store) GitVersionContext(ctx context.Context) (string, error) {
	if v, ok := gitVersions.Load(s.git()); ok {
		return v.(string), nil
	}
	cmd := exec.CommandContext(ctx, s.git(), "version")
	waitDelay := gitWaitDelay
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < waitDelay {
			waitDelay = remaining
		}
	}
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("running %s version: %w", s.git(), err)
	}
	v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "git version "))
	if v == "" {
		return "", fmt.Errorf("%s version printed no version", s.git())
	}
	gitVersions.Store(s.git(), v)
	return v, nil
}

// gitAtLeast reports whether a `git version` string is at least min (major, minor). A version it
// cannot read is taken as new enough: git then says itself what it cannot do.
func gitAtLeast(v string, min [2]int) bool {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return true
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	if err1 != nil || err2 != nil {
		return true
	}
	return major > min[0] || major == min[0] && minor >= min[1]
}
