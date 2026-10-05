package packsrc

// rebase.go is `yolo pack rebase`'s REBASE CLONE (docs/design/patched-forks.md §8.4, PF-D13,
// PF-D26, PF-D47): a clone of a patched fork's upstream, in a directory the user names and never in
// yolo's state directory, where the series is replayed onto a target and, when a member conflicts,
// left mid-rebase with the conflict markers in its work tree, for the user (or an agent in a jail
// started there) to resolve, `git rebase --continue` and export. Two terms are coined here: the
// REBASE CLONE is that clone, and its MARKER is the file in its git directory that says whose clone
// it is and what it was rebased onto, so a second `yolo pack rebase` knows its own clone from a
// directory it must not touch.
//
// # How it runs
//
//  1. THE CLONE, of the upstream's URL, blobless and with no checkout, so its promisor is the
//     upstream and the user's own git can fetch what a later command needs. It is a fetch, so it
//     runs in the store's regime (gitCmd: the user's git config honored for its credential
//     helpers and insteadOf rewrites, hooks and fsmonitor off, objects checked) — and with
//     `--template=`, so no template directory of the user's lands its config, attributes or hooks
//     in the clone, where the replay below would read them as the repository's own.
//  2. THE BLOBS the replay reads — the whole commit of the series' base and of the target — are
//     fetched into the clone in the same regime, in one request each (prefetchBlobs), and a blob
//     still missing is an apply error. The replay itself can fetch nothing.
//  3. THE REPLAY, in the replay's regime (replay.go: no user or system config, no template, no
//     hook, no signing, no lazy fetch, a fixed identity): the series applied with `git am` at its
//     base on a branch, then picked onto the target with the walk's own pick, which says whether
//     the series is clean there, which members are already upstream and where it stops. Only when
//     a member conflicts does `git rebase --onto <target> <base>` run, which stops at that member
//     with the markers in the work tree, so `git rebase --continue` resumes it.
//
// The directory is CLAIMED before the clone: an absent one is made by this run, so one another
// process made since the inspection is refused, and a clone that is not kept removes only what this
// run made (RebaseClone). The marker is written once the clone is made and again once the series is
// applied at its base, with that APPLIED tip, which is what tells a finished rebase from an aborted
// one, or from a clone a killed run left before its replay ended (RebaseFinished), and what the
// printed export's guard refuses (RebaseExportGuard).
//
// Nothing here writes the fork pack, the pack store's trees or the check record: the caller ran
// the check, and the clone is the user's. A clone the replay did not leave mid-rebase is removed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RebaseMarkerName is the marker's file name, in the rebase clone's git directory, so it is never
// in the work tree a `git status` or an export reads.
const RebaseMarkerName = "yolo-rebase.json"

// RebaseMarkerSchema is the marker's schema version.
const RebaseMarkerSchema = 1

// RebaseBranch is the branch the rebase clone's replay is made on, and RebaseBranchRef its full
// name: what an export reads, never HEAD, which a stopped rebase detaches at a partial replay
// and a clone not yet replayed leaves on the upstream's default branch.
const (
	RebaseBranch    = "yolo-rebase"
	RebaseBranchRef = "refs/heads/" + RebaseBranch
)

// RebaseCloneTimeout bounds the rebase clone's network work: the clone and the blob prefetches.
// Longer than a launch's fetch bound, since a blobless clone of a large upstream is the whole
// history's commits and trees, and the verb runs at the user's terminal, where a Ctrl-C ends it.
const RebaseCloneTimeout = 10 * time.Minute

// RebaseMarker is what a rebase clone's marker records.
type RebaseMarker struct {
	Schema int `json:"schema"`
	// Owner is the owner key of the patched fork the clone is for.
	Owner string `json:"owner"`
	// Repo is the upstream repository cloned.
	Repo string `json:"repo"`
	// Base is the series' base the replay applied it at; Target what it was rebased onto.
	Base   string    `json:"base"`
	Target ListEntry `json:"target"`
	// At is when the clone was made, in unix seconds.
	At int64 `json:"at"`
	// Applied is the series as `git am` applied it at its base: the branch's tip before the
	// rebase, written once the replay has made it. A branch still holding it is no finished
	// rebase (RebaseFinished), and the printed export refuses it. "" while the clone has not been
	// replayed, which is no finished rebase either.
	Applied string `json:"applied,omitempty"`
}

// RebaseDirState is what a rebase clone's directory holds before a rebase.
type RebaseDirState int

const (
	// RebaseDirAbsent: nothing is there; the clone makes the directory.
	RebaseDirAbsent RebaseDirState = iota
	// RebaseDirEmpty: an empty directory, which the clone fills.
	RebaseDirEmpty
	// RebaseDirClone: a rebase clone, whose marker InspectRebaseDir returns.
	RebaseDirClone
	// RebaseDirOther: anything else — a file, a directory with something in it, a git repository
	// no rebase made. Never touched.
	RebaseDirOther
)

// InspectRebaseDir says what dir holds, and the marker of a rebase clone. A marker that cannot be
// read makes the directory RebaseDirOther, never a clone this verb may remove.
func InspectRebaseDir(dir string) (RebaseDirState, *RebaseMarker, error) {
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return RebaseDirAbsent, nil, nil
	case err != nil:
		return RebaseDirOther, nil, err
	case !fi.IsDir():
		return RebaseDirOther, nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return RebaseDirOther, nil, err
	}
	if len(ents) == 0 {
		return RebaseDirEmpty, nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", RebaseMarkerName))
	if err != nil {
		return RebaseDirOther, nil, nil
	}
	var m RebaseMarker
	if err := json.Unmarshal(data, &m); err != nil || m.Schema != RebaseMarkerSchema || m.Owner == "" {
		return RebaseDirOther, nil, nil
	}
	return RebaseDirClone, &m, nil
}

// RebaseInProgress reports whether the rebase clone at dir is stopped mid-rebase (or mid-am), as
// git records it in its git directory.
func RebaseInProgress(dir string) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if fi, err := os.Stat(filepath.Join(dir, ".git", d)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// rebaseReadTimeout bounds the offline reads RebaseFinished makes in a rebase clone.
const rebaseReadTimeout = 30 * time.Second

// RebaseFinished reports whether the rebase clone at dir, whose marker is m, holds a FINISHED
// rebase, one an export may replace the series with: git records no rebase in progress, its branch
// holds a commit that neither the marker's target nor the series as applied at its base reaches
// (the very test the printed export makes before it writes anything, RebaseExportGuard), and the
// target is an ancestor of the branch, which `git format-patch --base` needs. false for a rebase
// the user aborted, which leaves the branch at the applied series; for a clone a run left before its
// replay ended (a SIGKILL, an OOM kill), whose marker names no applied series, or whose branch does
// not exist; and for a rebase that skipped every member, which leaves nothing above the target. Read
// in the replay's regime, offline.
func (s *Store) RebaseFinished(dir string, m *RebaseMarker) bool {
	if m == nil || m.Applied == "" || m.Target.Commit == "" || RebaseInProgress(dir) {
		return false
	}
	ctx, cancel := context.WithTimeout(s.parentCtx(), rebaseReadTimeout)
	defer cancel()
	sc := &scratch{gitBin: s.git(), repo: dir, b: budget{ctx: ctx, d: rebaseReadTimeout}}
	out, err := sc.git("", "rev-list", "-n", "1", RebaseBranchRef, "--not", m.Target.Commit, m.Applied, "--")
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	_, code, _ := sc.gitStatus("", "merge-base", "--is-ancestor", m.Target.Commit, RebaseBranchRef)
	return code == 0
}

// RebaseExportGuard is the shell test a printed export makes first, so a pasted export changes
// nothing until the rebase in the clone at dir is finished (RebaseFinished's own first test): the
// rebase clone's branch holds a commit that neither target nor applied, the series as applied at its
// base, reaches. A stopped rebase leaves the branch at applied (git moves it only once the rebase
// finishes), as does an aborted one; a skipped-through one leaves it at target; and a clone with no
// branch answers nothing. quote quotes dir for the shell.
func RebaseExportGuard(dir, target, applied string, quote func(string) string) string {
	return `test -n "$(git -C ` + quote(dir) + " rev-list -n 1 " + RebaseBranchRef + " --not " + target + " " +
		applied + ` --)"`
}

// TryLockRebaseDir takes the REBASE DIRECTORY LOCK (a term coined here) for the rebase clone at
// dir, an exclusive flock in the pack store keyed by the path, without waiting: held is true when
// another process holds it. `yolo pack rebase` holds it from the inspection of its directory to its
// end, so a second run on the same directory — a second terminal — neither takes the first run's
// clone, half made, for its own nor removes it. dir should be resolved, so two spellings of one
// directory share a lock.
func (s *Store) TryLockRebaseDir(dir string) (unlock func(), held bool, err error) {
	path := filepath.Join(s.Dir, "locks", "rebase-"+mirrorSlug(filepath.Clean(dir))+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, false, nil
}

// RemoveRebaseClone removes the rebase clone of owner at dir (`--restart`), and nothing else: a
// directory whose marker names another owner, or that has none, is refused.
func RemoveRebaseClone(dir, owner string) error {
	state, m, err := InspectRebaseDir(dir)
	switch {
	case err != nil:
		return err
	case state == RebaseDirAbsent:
		return nil
	case state != RebaseDirClone:
		return fmt.Errorf("%s is not a rebase clone yolo made, so it is not removed", dir)
	case m.Owner != owner:
		return fmt.Errorf("%s is the rebase clone of %s, not of %s, so it is not removed", dir, m.Owner, owner)
	}
	return os.RemoveAll(dir)
}

// RebaseOptions is one rebase clone to make.
type RebaseOptions struct {
	// Owner is the patched fork's owner key, for the marker.
	Owner string
	// Repo and Subdir are the upstream's repository and the source's subdirectory.
	Repo, Subdir string
	// Series is the series as the caller read it, once.
	Series *Series
	// Target is the commit to rebase the series onto.
	Target ListEntry
	// Dir is the clone's directory, absolute: absent, or an empty directory (InspectRebaseDir).
	Dir string
	// Now is the clock the marker is stamped by; zero means time.Now.
	Now time.Time
}

// RebaseResult is what a rebase clone came to.
type RebaseResult struct {
	// Clean is true when the series takes the target as it stands; Upstream then names the
	// members already in the upstream, whose pick changed nothing. The clone is removed.
	Clean    bool
	Upstream []string
	// Conflict is the member the rebase stopped at and its conflicting paths: the clone is left
	// mid-rebase in Dir (Kept), its markers in the work tree.
	Conflict *ReplayConflict
	// Base is a series that does not apply at its own base; Err an apply error (the clone or a
	// blob that could not be fetched, a git that failed or is too old). The clone is removed.
	Base *SeriesBaseError
	Err  error
	// Kept is whether the clone is left in Dir.
	Kept bool
	// Applied is the series as `git am` applied it at its base (RebaseMarker.Applied), set once the
	// replay made it.
	Applied string
	// Git is the git version the replay ran.
	Git string
}

// RebaseClone makes the rebase clone o describes and replays the series there (see the file doc).
func (s *Store) RebaseClone(o RebaseOptions) (res RebaseResult) {
	gitVer, err := s.GitVersion()
	res.Git = gitVer
	if err != nil {
		res.Err = err
		return res
	}
	if !gitAtLeast(gitVer, replayMinGit) {
		res.Err = fmt.Errorf("replaying a patch series needs git %d.%d or newer (`git merge-tree "+
			"--merge-base`), and this host's git is %s — update git", replayMinGit[0], replayMinGit[1], gitVer)
		return res
	}
	state, _, err := InspectRebaseDir(o.Dir)
	switch {
	case err != nil:
		res.Err = err
		return res
	case state != RebaseDirAbsent && state != RebaseDirEmpty:
		res.Err = fmt.Errorf("%s is not empty, and a rebase clone is made only where nothing is", o.Dir)
		return res
	}
	// THE CLAIM: an absent directory is made here, before the clone, so one another process made
	// since the inspection is refused rather than taken for this run's. Two `yolo pack rebase` runs
	// never share a directory at all: the verb holds its REBASE DIRECTORY LOCK across the inspection
	// and the clone (TryLockRebaseDir).
	made := false
	if state == RebaseDirAbsent {
		if err := os.MkdirAll(filepath.Dir(o.Dir), 0o777); err != nil {
			res.Err = err
			return res
		}
		if err := os.Mkdir(o.Dir, 0o777); err != nil {
			res.Err = fmt.Errorf("making the rebase clone's directory: %w", err)
			return res
		}
		made = true
	}
	// WHAT IS REMOVED when the clone is not kept is only what this run made. A clone git made is
	// all its directory holds, so it goes whole; a clone git refused (the directory filled since the
	// inspection) made nothing, and a clone that failed removed its own; only one a timeout or a
	// Ctrl-C killed leaves its git directory, which a --no-checkout clone is all it writes. The
	// directory itself goes when this run made it and it is empty again: a directory the user
	// made empty for the clone is left, emptied.
	cloned, killed := false, false
	defer func() {
		if res.Kept {
			return
		}
		switch {
		case cloned:
			ents, _ := os.ReadDir(o.Dir)
			for _, e := range ents {
				_ = os.RemoveAll(filepath.Join(o.Dir, e.Name()))
			}
		case killed:
			_ = os.RemoveAll(filepath.Join(o.Dir, ".git"))
		}
		if made {
			_ = os.Remove(o.Dir)
		}
	}()

	// 1-2. THE CLONE AND ITS BLOBS, in the store's regime, under one network budget.
	cctx, ccancel := context.WithTimeout(s.parentCtx(), RebaseCloneTimeout)
	defer ccancel()
	nb := budget{ctx: cctx, d: RebaseCloneTimeout}
	if _, err := s.runIn(nb, "", withFsck("clone", "--template=", "--filter=blob:none", "--no-checkout",
		"--quiet", "--", o.Repo, o.Dir)...); err != nil {
		killed = cctx.Err() != nil
		res.Err = fmt.Errorf("cloning %s: %s", o.Repo, oneLine(err))
		return res
	}
	cloned = true
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	marker := RebaseMarker{Schema: RebaseMarkerSchema, Owner: o.Owner, Repo: o.Repo, Base: o.Series.Base,
		Target: o.Target, At: now.Unix()}
	if err := writeRebaseMarker(o.Dir, marker); err != nil {
		res.Err = err
		return res
	}
	gitDir := filepath.Join(o.Dir, ".git")
	for _, c := range []string{o.Series.Base, o.Target.Commit} {
		if err := s.ensureCommit(nb, gitDir, o.Repo, c); err != nil {
			res.Err = err
			return res
		}
		s.prefetchBlobs(nb, gitDir, c, "")
		if err := s.missingBlobs(nb, gitDir, c); err != nil {
			res.Err = err
			return res
		}
	}

	// 3. THE REPLAY, in the replay's regime, in the clone's own work tree.
	tmp, err := os.MkdirTemp("", "yolo-patched-rebase-")
	if err != nil {
		res.Err = err
		return res
	}
	defer os.RemoveAll(tmp)
	rctx, rcancel := context.WithTimeout(s.parentCtx(), ReplayTimeout)
	defer rcancel()
	sc := &scratch{gitBin: s.git(), dir: tmp, repo: o.Dir, b: budget{ctx: rctx, d: ReplayTimeout}}
	commits, baseErr, err := sc.applyAtBase(o.Series, RebaseBranch)
	switch {
	case baseErr != nil:
		res.Base = baseErr
		return res
	case err != nil:
		res.Err = err
		return res
	case len(commits) == 0:
		res.Err = fmt.Errorf("the series applied no commit at its base %s", shortCommit(o.Series.Base))
		return res
	}
	// The applied tip, in the marker before the rebase moves the branch: what tells a finished
	// rebase from an aborted one, or from a run that stopped here (RebaseFinished).
	marker.Applied = commits[len(commits)-1]
	res.Applied = marker.Applied
	if err := writeRebaseMarker(o.Dir, marker); err != nil {
		res.Err = err
		return res
	}
	r := ReplayResult{Entry: o.Target}
	sc.pick(&r, o.Series, commits, o.Subdir)
	switch {
	case r.Err != nil:
		res.Err = r.Err
		return res
	case r.Clean:
		res.Clean, res.Upstream = true, r.Upstream
		return res
	}
	// A CONFLICT: the rebase the user resumes, stopped where the pick stopped.
	_, code, rerr := sc.gitStatus("", "rebase", "--onto", o.Target.Commit, o.Series.Base)
	if code == 0 {
		// git's rebase took what the pick did not: the work tree is the rebase's, so its answer is.
		res.Clean = true
		return res
	}
	stopped, err := sc.git("", "rev-parse", "--verify", "--quiet", "REBASE_HEAD")
	if err != nil {
		if rerr == nil {
			rerr = err
		}
		res.Err = fmt.Errorf("rebasing the series onto %s: %s", o.Target.Label(), oneLine(rerr))
		return res
	}
	conflict := &ReplayConflict{Member: r.Conflict.Member}
	for i, c := range commits {
		if c == strings.TrimSpace(stopped) {
			conflict.Member = o.Series.Members[i].Name
		}
	}
	if out, err := sc.git("", "diff", "--name-only", "--diff-filter=U", "-z"); err == nil {
		for _, p := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
			if p != "" {
				conflict.Paths = append(conflict.Paths, p)
			}
		}
	}
	if len(conflict.Paths) == 0 {
		conflict.Paths = r.Conflict.Paths
	}
	res.Conflict, res.Kept = conflict, true
	return res
}

// ensureCommit makes commit present in the clone's git directory, fetching it by its id from the
// upstream when no branch or tag the clone brought reaches it (a commit a re-pointed tag named once,
// or one on a branch since deleted).
func (s *Store) ensureCommit(b budget, gitDir, repo, commit string) error {
	if sha, err := s.revParse(gitDir, commit); err == nil && sha != "" {
		return nil
	}
	_, err := s.runIn(b, gitDir, withFsck("-c", "gc.auto=0", "fetch", "origin", "--no-tags",
		"--no-write-fetch-head", "--recurse-submodules=no", commit)...)
	if sha, rerr := s.revParse(gitDir, commit); rerr == nil && sha != "" {
		return nil
	}
	why := shortCommit(commit) + " is not a commit of " + repo
	if err != nil {
		why += " (fetching it by its id failed: " + oneLine(err) + ")"
	}
	return errors.New(why)
}

// CloneOrigin is the origin URL of the git work tree at dir, read from that tree's own config file
// with no repository discovery and no config of the user's, or "" when dir holds no such tree. A
// fetched fork pack's publish steps read it to tell a clone of the pack's repository they printed
// once from a directory they must not name.
func (s *Store) CloneOrigin(dir string) string {
	cfg := filepath.Join(dir, ".git", "config")
	if fi, err := os.Lstat(cfg); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	ctx, cancel := context.WithTimeout(s.parentCtx(), rebaseReadTimeout)
	defer cancel()
	sc := &scratch{gitBin: s.git(), repo: dir, b: budget{ctx: ctx, d: rebaseReadTimeout}}
	out, err := sc.git("", "config", "--file", cfg, "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// writeRebaseMarker writes m into the clone at dir's git directory.
func writeRebaseMarker(dir string, m RebaseMarker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".git", RebaseMarkerName), append(data, '\n'), 0o644)
}

// ResolveRebaseTarget resolves ref, as `yolo pack rebase --onto` names it, to a commit in repo's
// mirror, with no network: a tag (git's precedence, as classifyRef reads a ref), a branch, or any
// other revision git names a commit by, an abbreviated commit included. The entry carries the tag's
// short name for a tag, so a line names it as the walk names a version.
func (s *Store) ResolveRebaseTarget(repo, ref string, waiting func(string)) (ListEntry, error) {
	if strings.HasPrefix(ref, "-") {
		return ListEntry{}, fmt.Errorf("--onto %s is not a revision", ref)
	}
	unlock, err := s.lockMirror(repo, waiting)
	if err != nil {
		return ListEntry{}, err
	}
	defer unlock()
	mirror := s.mirrorPath(repo)
	if !mirrorExists(mirror) {
		return ListEntry{}, fmt.Errorf("this machine holds no copy of %s — a check fetches it", repo)
	}
	kind, name, sha, err := s.classifyRef(mirror, ref)
	switch {
	case err != nil:
		return ListEntry{}, err
	case kind == refUnresolved:
		return ListEntry{}, fmt.Errorf("--onto %s names no branch, tag or commit of %s", ref, repo)
	case kind == refTag:
		return ListEntry{Commit: sha, Tag: strings.TrimPrefix(name, "refs/tags/")}, nil
	}
	return ListEntry{Commit: sha}, nil
}

// RefKind says what a's ref names in its mirror, with no network: "branch", "tag" or "commit", or
// "" when the mirror does not answer (none fetched yet, or the ref names nothing there). A fetched
// pack's line reads it to say whether a push reaches the pack by a refresh or needs its ref moved.
func (s *Store) RefKind(a Addr) string {
	if a.IsLocal() {
		return ""
	}
	mirror := s.mirrorPath(a.Repo)
	if !mirrorExists(mirror) {
		return ""
	}
	kind, _, _, err := s.classifyRef(mirror, a.Ref)
	if err != nil {
		return ""
	}
	switch kind {
	case refBranch:
		return "branch"
	case refTag:
		return "tag"
	case refCommit:
		return "commit"
	}
	return ""
}
