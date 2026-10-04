package packsrc

// replay.go is a patched fork's REPLAY and the NEWEST-FIT WALK over it
// (docs/design/patched-forks.md §5, §6.4; PF-D6, PF-D10, PF-D18): the series made into commits at
// its base, then those commits picked onto each entry of the walk's list, newest first, until one
// takes the whole series cleanly.
//
//  1. AT THE BASE: `git am` applies each member, in series order, onto the series' base commit. A
//     series exported with `--base` applies there by construction; one that does not is a
//     malformed series (SeriesBaseError), the fork's reason, never a conflict with the upstream.
//  2. ONTO AN ENTRY: each commit step 1 made is picked three-way onto the entry, one merge per
//     commit whose base is that commit's parent — `git merge-tree --write-tree --merge-base`, the
//     merge `git cherry-pick` and `git rebase` make, with no work tree. A pick whose result is the
//     tree it was picked onto changed nothing: the member is ALREADY UPSTREAM, which is clean. A
//     pick that stops with conflicting paths is a CONFLICT; anything else that stops the replay (a
//     blob that is not there, a git that fails or runs out of time) is an APPLY ERROR.
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
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReplayTimeout bounds one walk, its prefetches included: a budget of its own, apart from the
// check's fetch (§6.4), so git holds a launch at most twice LaunchFetchTimeout before a build.
const ReplayTimeout = 60 * time.Second

// replayMinGit is the oldest git whose merge-tree takes --merge-base.
var replayMinGit = [2]int{2, 40}

// ReplayResult is one entry's replay.
type ReplayResult struct {
	Entry ListEntry
	// Clean is true when the whole series took: the entry is a FIT.
	Clean bool
	// Upstream lists the members whose pick changed nothing, already in the upstream.
	Upstream []string
	// Tree is the PATCHED TREE (§5.3, PF-D15) of a clean replay: the git tree of the source's
	// subdirectory (the whole tree at the repository root) in the replay's last commit.
	Tree string
	// Conflict is the member that stopped the replay and its conflicting paths; nil otherwise.
	Conflict *ReplayConflict
	// Err is an APPLY ERROR: anything else that stopped the replay. The entry stays pending.
	Err error
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
	// Timeout is the walk's bound; zero means ReplayTimeout.
	Timeout time.Duration
	// Waiting is told when the walk is about to block on the mirror's lock.
	Waiting func(string)
	// All replays every entry instead of stopping at the first fit (a status survey).
	All bool
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
}

// WalkSeries replays series down list, newest first, until an entry takes it: the walk of §6.4.
// repo is the upstream's repository and subdir the source's subdirectory, for the patched tree.
func (s *Store) WalkSeries(repo, subdir string, series *Series, list []ListEntry, opts WalkOptions) WalkResult {
	res := WalkResult{Fit: -1}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = ReplayTimeout
	}
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
	unlock, err := s.lockMirror(repo, opts.Waiting)
	if err != nil {
		res.Err = err
		return res
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	b := budget{ctx: ctx, d: timeout}
	mirror := s.mirrorPath(repo)
	if !mirrorExists(mirror) {
		res.Err = fmt.Errorf("this machine holds no copy of %s to replay the series in — a check fetches it", repo)
		return res
	}

	// The base's files, into the mirror, then the series made into commits there.
	s.prefetchBlobs(b, mirror, series.Base, "")
	if err := s.missingBlobs(b, mirror, series.Base); err != nil {
		res.Err = err
		return res
	}
	sc, err := newScratch(s.git(), mirror, b)
	if err != nil {
		res.Err = err
		return res
	}
	defer sc.remove()
	commits, baseErr, err := sc.applyAtBase(series)
	switch {
	case baseErr != nil:
		res.Base = baseErr
		return res
	case err != nil:
		res.Err = err
		return res
	}

	for _, e := range list {
		r := ReplayResult{Entry: e}
		s.prefetchBlobs(b, mirror, e.Commit, "")
		if err := s.missingBlobs(b, mirror, e.Commit); err != nil {
			r.Err = err
			res.Results = append(res.Results, r)
			return res
		}
		sc.pick(&r, series, commits, subdir)
		res.Results = append(res.Results, r)
		switch {
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

// missingBlobs is an apply error when the mirror still lacks a file of commit after its prefetch:
// a blob that could not be fetched is never a conflict.
func (s *Store) missingBlobs(b budget, mirror, commit string) error {
	out, err := s.runIn(b, mirror, "rev-list", "--objects", "--missing=print", commit+"^{tree}")
	if err != nil {
		return fmt.Errorf("could not read the upstream's files at %s: %s", shortCommit(commit), oneLine(err))
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "?") {
			n++
		}
	}
	if n > 0 {
		return fmt.Errorf("could not fetch the upstream's files at %s (%d missing after the prefetch) — "+
			"the next check retries, or `yolo pack update` retries now", shortCommit(commit), n)
	}
	return nil
}

// scratch is the replay's scratch repository.
type scratch struct {
	gitBin string
	dir    string // the private temporary directory holding both
	repo   string // the repository, its work tree at repo and its git dir at repo/.git
	b      budget
}

// newScratch makes a scratch repository borrowing mirror's objects, run by the git binary gitBin.
func newScratch(gitBin, mirror string, b budget) (*scratch, error) {
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
	objects, err := filepath.Abs(filepath.Join(mirror, "objects"))
	if err == nil {
		err = os.MkdirAll(info, 0o755)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(info, "alternates"), []byte(objects+"\n"), 0o644)
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
	full := append(append([]string{}, replayConfig...), args...)
	if dir == "" && len(args) > 0 && args[0] != "init" {
		dir = sc.repo
	}
	cmd := exec.CommandContext(sc.b.ctx, sc.gitBin, full...)
	cmd.Dir = dir
	cmd.Env = replayEnv()
	cmd.WaitDelay = gitWaitDelay
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

// applyAtBase checks the series' base out in the scratch work tree and makes each member a commit
// with `git am`, one member at a time so a failure names its member. It returns the commits in
// series order.
func (sc *scratch) applyAtBase(series *Series) ([]string, *SeriesBaseError, error) {
	if _, err := sc.git("", "checkout", "-q", "--detach", series.Base); err != nil {
		return nil, nil, fmt.Errorf("checking the series' base %s out: %s", shortCommit(series.Base), oneLine(err))
	}
	files := filepath.Join(sc.dir, "series")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return nil, nil, err
	}
	var commits []string
	for _, m := range series.Members {
		p := filepath.Join(files, m.Name)
		if err := os.WriteFile(p, m.Data, 0o600); err != nil {
			return nil, nil, err
		}
		if _, err := sc.git("", "am", "-q", "--no-3way", "--whitespace=nowarn", p); err != nil {
			if sc.b.ctx.Err() != nil {
				return nil, nil, err
			}
			return nil, &SeriesBaseError{Member: m.Name, Base: series.Base, Detail: amDetail(err)}, nil
		}
		head, err := sc.git("", "rev-parse", "HEAD")
		if err != nil {
			return nil, nil, err
		}
		commits = append(commits, strings.TrimSpace(head))
	}
	return commits, nil, nil
}

// amDetail is the first line of git am's diagnosis that says what failed.
func amDetail(err error) string {
	for _, line := range strings.Split(err.Error(), "\n")[1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "hint:") || strings.HasPrefix(line, "Applying:") ||
			strings.HasPrefix(line, "Patch failed at") || strings.Contains(line, "git am --") {
			continue
		}
		return line
	}
	return oneLine(err)
}

// pick picks commits onto r.Entry's commit one at a time, filling r.
func (sc *scratch) pick(r *ReplayResult, series *Series, commits []string, subdir string) {
	onto := r.Entry.Commit
	ontoTree, err := sc.git("", "rev-parse", onto+"^{tree}")
	if err != nil {
		r.Err = fmt.Errorf("reading %s: %s", shortCommit(onto), oneLine(err))
		return
	}
	ontoTree = strings.TrimSpace(ontoTree)
	for i, c := range commits {
		member := series.Members[i].Name
		out, code, err := sc.gitStatus("", "merge-tree", "--write-tree", "-z", "--name-only",
			"--no-messages", "--merge-base="+c+"^", onto, c)
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
		case err != nil:
			r.Err = fmt.Errorf("picking %s onto %s: %s", member, shortCommit(r.Entry.Commit), oneLine(err))
			return
		}
		tree := fields[0]
		if tree == ontoTree {
			r.Upstream = append(r.Upstream, member)
			continue
		}
		next, err := sc.git("", "commit-tree", "--no-gpg-sign", tree, "-p", onto, "-m", member)
		if err != nil {
			r.Err = fmt.Errorf("committing %s onto %s: %s", member, shortCommit(r.Entry.Commit), oneLine(err))
			return
		}
		onto, ontoTree = strings.TrimSpace(next), tree
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
	r.Clean, r.Tree = true, strings.TrimSpace(tree)
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
	if v, ok := gitVersions.Load(s.git()); ok {
		return v.(string), nil
	}
	out, err := exec.Command(s.git(), "version").Output()
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
