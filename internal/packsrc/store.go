package packsrc

// store.go is the host-side fetch and materialize half of C5.
//
// It runs ON THE HOST, always. The jail has no git credentials by design (that is
// the credential boundary), so a fetch inside one could only ever fail — or worse,
// succeed by finding credentials that were not supposed to be there.
//
// A HOST LAUNCH FETCHES (maintainer ruling, 2026-09-25). Before it, fetching happened
// only in `yolo pack install`/`update` and a launch resolved strictly from the store.
// Now a launch runs Refresh first (refresh.go): a never-fetched pack is fetched, a
// branch is re-fetched at most hourly, and a tag or full commit SHA is never re-fetched
// — "the ref decides" what moves. Resolve itself is still offline; the network
// happens in fetchMirror and fetchPinnedCommits, which Sync and Refresh call, and in a
// checkout's fetch of the files it needs (prefetchBlobs, checkoutTree). No other git run
// fetches (gitCmd).
//
// Layout, and the mirror/tree split is load-bearing:
//
//	<PacksDir>/mirrors/<repo-slug>              a bare git mirror per repository
//	<PacksDir>/trees/<sha>/                     a checkout of a repository-root pack at one commit
//	<PacksDir>/trees/<sha>-<subdir-key>/        a checkout of ONE subdirectory at one commit
//	<PacksDir>/locks/<repo-slug>.lock           the per-mirror flock (fetch + checkout)
//	<PacksDir>/locks/lockfile-<path-slug>.lock  the flock around a lockfile's rewrite
//	<PacksDir>/stamps/<repo-slug>/<ref-slug>    when a branch last fetched successfully
//
// One mirror serves every ref and subpath of a repo, so N packs from one monorepo
// cost one fetch. Trees are keyed by COMMIT, not by ref, so two packs pinned to the
// same commit and subdirectory share a checkout and a ref moving does not corrupt an
// existing tree. A pack in a subdirectory gets a tree holding ONLY that subdirectory
// (treeDir), so a pack in a large repository checks out, and fetches the file contents
// of, its own directory rather than the whole repository. An earlier yolo kept every git
// pack's checkout, of the whole commit, at trees/<sha>; ResolveExisting still reads a
// subdirectory pack from one, and the next launch checks the subdirectory out on its own.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Store is a pack source store rooted at Dir.
type Store struct {
	// Dir is the store root (paths.PacksDir()).
	Dir string
	// Git is the git binary to run. Empty means "git" from PATH.
	Git string
	// Timeout bounds git's network work. A standalone git run gets it to itself; a
	// Refresh of one mirror SHARES it across the clone, the fetch and the checkouts after
	// them (one fetch, one budget). Zero means a 2-minute default: a hung fetch must not
	// hang a jail launch forever, and a clear timeout beats a wedge.
	Timeout time.Duration
	// Detached runs every git in a NEW SESSION with no controlling terminal, and a timeout
	// then kills git's whole process group rather than git alone. A launch sets it
	// (run.RefreshConfiguredPacks), for two reasons:
	//
	//   - ssh reads its host-key and passphrase prompts from /dev/tty, not through git's
	//     askpass, so GIT_TERMINAL_PROMPT=0 does not stop it asking. With no controlling
	//     terminal the open fails and ssh fails at once ("Host key verification failed")
	//     instead of stopping a launch at a prompt, or trusting a host on a typed "yes".
	//   - git hands the transfer to a helper process (git-remote-http, ssh). Killing git
	//     alone leaves the helper holding git's output pipe, so the timeout did not end the
	//     wait (measured 2026-09-25: a stalled http remote hung past a 2s timeout until the
	//     server closed the socket). Killing the group ends the helper too.
	//
	// `yolo pack install`/`update` leave it false: the user runs them at a terminal and may
	// answer an ssh prompt there, which is how a first contact with a host gets trusted.
	Detached bool
	// Env, when non-nil, replaces the git subprocess environment.
	Env []string
	// Getenv reads environment variables. Nil means os.Getenv.
	//
	// One variable is read, YOLO_PACK_ROOT, for Resolve's staged-tree fallback. It is
	// an injectable seam rather than a direct os.Getenv call because the fallback's
	// tests have to state the delivery situation they are testing: this repo is
	// developed from inside its own jail, where YOLO_PACK_ROOT is always set to a real
	// tree, so a test reading the ambient environment would be measuring the machine.
	Getenv func(string) string
	// Ctx, when non-nil, is the parent of every budget this store starts (newBudget, and a
	// replay's walk): cancelling it ends the git it runs. A launch's patched-fork advance sets it,
	// so a Ctrl-C ends the advance's check and walk and the launch starts on the good build
	// (docs/design/patched-forks.md PF-D25). Nil means none, as every other store has.
	Ctx context.Context
}

// Resolved is a fetched, materialized source ready to stage.
type Resolved struct {
	// Root is the directory to stage FROM: the materialized tree plus the address's
	// subpath.
	Root string
	// Commit is the full SHA the ref resolved to. This is what the lockfile records:
	// a branch name says what you asked for, a SHA says what you got.
	Commit string
	// StagedFrom is the DELIVERED tree this resolution fell back to, and is empty for
	// every resolution that came from the address itself.
	//
	// Non-empty means Root is a copy a launcher already staged under YOLO_PACK_ROOT
	// rather than the source the address names — the pack works, but its source is not
	// visible from here (see Resolve). It exists so a caller can report that
	// distinctly: `yolo check` prints "staged at <path>" plus a note naming the
	// host-side source, and would otherwise have to re-derive the answer it was just
	// given, which is the second implementation this field exists to prevent.
	StagedFrom string
}

// mirrorSlug is a filesystem-safe, collision-free name for a repo URL. Hashed rather
// than escaped: repo URLs contain characters that vary in legality across
// filesystems, and a hash is stable, short, and cannot collide by accident. The
// human-readable tail is a debugging affordance only.
func mirrorSlug(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	tail := repo
	if i := strings.LastIndexAny(tail, "/:"); i >= 0 {
		tail = tail[i+1:]
	}
	tail = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, tail)
	if tail == "" {
		tail = "repo"
	}
	return tail + "-" + hex.EncodeToString(sum[:6])
}

func (s *Store) git() string {
	if s.Git != "" {
		return s.Git
	}
	return "git"
}

func (s *Store) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 2 * time.Minute
}

// gitStateEnv are the environment variables git sets for its own subprocesses
// (hooks, aliases, filters). They are RELATIVE to the invoking repository and
// must not leak into a git run against a DIFFERENT repo: a leaked
// GIT_INDEX_FILE=.git/index from a host `git commit` hook makes a bare mirror's
// `--work-tree` checkout try to write <tree>/.git/index.lock, which does not
// exist, so every checkout fails. Strip them and let git re-derive its own
// git-dir/work-tree/index from the arguments.
//
// The four *_PATHSPECS variables are the same hazard for a different argument: git
// exports them to its subprocesses when it is run as `git --icase-pathspecs` (and so
// on), and they change what a pathspec MATCHES. A pack's subdirectory is checked out by
// a literal pathspec (literalPathspec), and GIT_ICASE_PATHSPECS=1 would widen it to
// every case-variant of the name, while `ls-tree` refuses to run under it at all.
var gitStateEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
	"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS",
}

// CleanGitEnv drops gitStateEnv keys from env so a git run against one repo
// cannot be redirected by state git set for another.
//
// Exported for the TEST helpers that shell out to git against scratch
// repositories (internal/cli, internal/cli/run): a git hook may run the
// test suite, git exports its own state to hooks, and from a LINKED WORKTREE
// that state is ABSOLUTE (GIT_DIR, GIT_INDEX_FILE, GIT_WORK_TREE) — so a test
// helper that inherits it would operate on the COMMITTER's worktree and index
// instead of its scratch repo, staging the committer's tree wholesale
// (measured 2026-09-02: 1441 bogus deletions staged into a worktree index by
// `git add -A` from a temp dir). This is the same bug this function already
// fixed for the mirror checkout at the call site below; the helpers get the
// same treatment.
func CleanGitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		drop := false
		for _, g := range gitStateEnv {
			if key == g {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// gitWaitDelay is how long a git run's Wait may block on output pipes a child left open
// after git itself was stopped (os/exec's WaitDelay). It is the backstop for a
// non-Detached store, where a timeout can kill git but not its transport helper.
const gitWaitDelay = 3 * time.Second

// budget is one deadline shared by a sequence of git runs, and the duration its timeout
// message names.
type budget struct {
	ctx context.Context
	d   time.Duration
}

// newBudget starts a budget of the store's Timeout. The caller cancels it when done.
func (s *Store) newBudget() (budget, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(s.parentCtx(), s.timeout())
	return budget{ctx: ctx, d: s.timeout()}, cancel
}

// parentCtx is the context every budget of this store starts under: Ctx, or none.
func (s *Store) parentCtx() context.Context {
	if s.Ctx != nil {
		return s.Ctx
	}
	return context.Background()
}

// fsckArgs turn on object checking for everything a git run receives over the network:
// transfer.fsckObjects covers a clone and a fetch, and because `git -c` settings reach
// git's child processes (GIT_CONFIG_PARAMETERS), it also covers the lazy blob fetch a
// checkout of the partial (--filter=blob:none) mirror makes. A pack is third-party
// content, so a malformed object is rejected at the boundary rather than after it is in
// the store. A run not given them fetches nothing (receivesObjects, gitCmd).
var fsckArgs = []string{"-c", "transfer.fsckObjects=true"}

// withFsck prefixes args with fsckArgs.
func withFsck(args ...string) []string {
	return append(append([]string{}, fsckArgs...), args...)
}

// receivesObjects reports whether a git run is one the store makes TO RECEIVE OBJECTS: the
// clone, the fetches (the mirror's, a pinned commit's, a checkout's prefetch) and the checkout,
// whose own lazy fetch of a file is the prefetch's fallback. Those, and only those, are given
// fsckArgs, so the argument list says which a run is, and gitCmd lets no other run fetch.
func receivesObjects(args []string) bool {
	return len(args) >= len(fsckArgs) && slices.Equal(args[:len(fsckArgs)], fsckArgs)
}

// gitLabel is the git subcommand args runs, for an error message: the first argument
// that is neither an option nor the value of a `-c`. Without it a `git -c k=v fetch`
// failure read "git -c: exit status 1".
func gitLabel(args []string) string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-c" || a == "-C":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	if len(args) > 0 {
		return args[0]
	}
	return "git"
}

// storeGitConfig is the `-c` settings every store run carries. Each overrides a setting of the
// host user's own git config that is about the user's repositories and breaks, or leaks from,
// a run in the store's. The rest of that config is honored on purpose: its credential helpers
// and insteadOf rewrites are what a private pack's fetch needs. A `-c` setting reaches the
// git processes a run starts too (GIT_CONFIG_PARAMETERS), such as a checkout's lazy fetch.
//
//   - core.hooksPath=/dev/null: NO HOOK RUNS in the store. A user's hooks reach the mirror
//     from a global core.hooksPath, or from an init.templateDir whose hooks `git clone --bare`
//     copies into the new mirror, and core.hooksPath replaces $GIT_DIR/hooks, so this covers
//     both. Measured with git 2.55: a post-checkout hook exiting 1 made every checkout into a
//     pack tree fail (git makes that hook's status the checkout's), and a failing
//     reference-transaction hook aborted the clone.
//   - core.fsmonitor=false: NO FSMONITOR DAEMON. With core.fsmonitor=true, a setting GitHub
//     recommends for large repositories, git starts a `git fsmonitor--daemon` for a worktree
//     the first time it reads that worktree's index, and the daemon outlives the command. The
//     checkout into a pack tree reads one, so each materialized pack left a resident daemon
//     watching a tree nothing edits, after yolo had exited (measured with git 2.55 on Linux).
var storeGitConfig = []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false"}

// gitCmd builds the git command for one store run: the store's git, the environment
// hygiene, and — when Detached — a new session whose whole process group the budget's
// cancellation kills. Split from run so the hygiene is testable as a property of the
// command every run executes.
//
// dir is the repository the run is IN — the mirror, for every run but the clone, which
// passes "" — and it is NAMED to git with --git-dir as well as being the working directory.
// A run that left git to discover the bare mirror from its working directory failed on a
// host whose global config sets safe.bareRepository=explicit (git 2.38), a hardening setting
// that refuses a discovered bare repository and allows one named by --git-dir or GIT_DIR, so
// every refresh and resolution failed with "cannot use bare repository". The path is made
// absolute because git resolves --git-dir against the working directory, which is dir itself.
//
// Every run also carries storeGitConfig, ahead of its own arguments.
func (s *Store) gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	pre := append([]string{}, storeGitConfig...)
	if dir != "" {
		gitDir := dir
		if abs, err := filepath.Abs(dir); err == nil {
			gitDir = abs
		}
		pre = append(pre, "--git-dir="+gitDir)
	}
	cmd := exec.CommandContext(ctx, s.git(), append(pre, args...)...)
	cmd.Dir = dir
	// GIT_TERMINAL_PROMPT=0 turns a missing credential into an immediate error
	// instead of a 30-second askpass hang during a jail launch — the difference
	// between a diagnosable failure and one that looks like yolo wedging.
	//
	// NO RUN BUT ONE THAT RECEIVES OBJECTS MAY FETCH ANY. In the partial mirror, git fetches an
	// object it lacks from the remote on demand, inside whatever command reads it, and its exit
	// status does not say so. Measured with git 2.55: a `rev-parse`, an `ls-tree` or an
	// `update-ref` naming a commit the mirror lacks fetched it and exited 0. So a lookup
	// documented as offline went to the network, received objects without fsckArgs (its
	// index-pack ran without --fsck-objects, and took a commit fsck rejects), and, from a remote
	// that ignores the blobless filter, printed "warning: filtering not recognized by server,
	// ignoring". GIT_NO_LAZY_FETCH=1 makes the missing object an answer instead: rev-parse exits
	// 1 (absent), so the refresh fetches the commit through its own, checked fetch. It is on
	// every run but the receiving ones, rather than on a list of lookups, so a run added later
	// is offline unless it is given fsck. Not on those: the checkout's lazy fetch is what still
	// delivers a file the best-effort prefetch did not, and it inherits fsck through `-c`. They
	// get GIT_NO_LAZY_FETCH=0 instead, so a caller environment that set it cannot take that
	// fallback away. git added the variable in 2.45; a git without it ignores it and still
	// fetches on demand, so there only runEnv keeps what that fetch prints out of the answer.
	lazy := "GIT_NO_LAZY_FETCH=1"
	if receivesObjects(args) {
		lazy = "GIT_NO_LAZY_FETCH=0"
	}
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(CleanGitEnv(env),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", lazy)
	if s.Detached {
		DetachGit(cmd)
	}
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// DetachGit gives a git command built with exec.CommandContext the hygiene a
// Detached store's runs have (see Store.Detached for the measurements behind
// each part): a NEW SESSION with no controlling terminal, so ssh cannot stop
// at a prompt on /dev/tty; a Cancel that kills git's whole process group when
// the context ends, so a transport helper holding the output pipe dies with
// it; and a WaitDelay backstop on pipes something else still holds. Exported
// so every caller that runs git against a remote uses this one implementation
// (internal/selfupdate's source check is the other).
func DetachGit(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		// Setsid made git a process-group leader, so -pid is git and every helper
		// it started.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = gitWaitDelay
}

// run executes git with a budget of the store's own timeout, returning what git printed on
// stdout, and on failure an error carrying its stderr too, so the caller can surface git's own
// diagnosis rather than a bare exit code.
func (s *Store) run(dir string, args ...string) (string, error) {
	b, cancel := s.newBudget()
	defer cancel()
	return s.runIn(b, dir, args...)
}

// runIn is run inside a budget another run may already have spent part of.
func (s *Store) runIn(b budget, dir string, args ...string) (string, error) {
	return s.runEnv(b, dir, nil, args...)
}

// runEnv is runIn with env appended to the run's environment, after gitCmd's hygiene.
//
// THE OUTPUT IS STDOUT ALONE, the only stream that carries what a command answers; every
// caller parses it (a commit id, ls-tree records, rev-list lines, a tag list). git's stderr
// carries warnings even when it succeeds, and read with stdout one led the commit id revParse
// returned ("warning: filtering not recognized by server, ignoring" from a lookup's lazy
// fetch, gitCmd), so the checkout of that "commit" failed on the word "warning:". stderr goes
// into the error of a run that fails, stdout after it, which is git's diagnosis either way.
func (s *Store) runEnv(b budget, dir string, env []string, args ...string) (string, error) {
	cmd := s.gitCmd(b.ctx, dir, args...)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if b.ctx.Err() != nil {
		return "", fmt.Errorf("git %s timed out after %s", gitLabel(args), b.d)
	}
	if err != nil {
		said := strings.TrimSpace(strings.TrimSpace(stderr.String()) + "\n" + strings.TrimSpace(stdout.String()))
		return stdout.String(), fmt.Errorf("git %s: %w\n%s", gitLabel(args), err, said)
	}
	return stdout.String(), nil
}

// Sync fetches the address's repository into the store's mirror and returns the full
// commit SHA its ref resolves to. It fetches UNCONDITIONALLY; the launch-time policy
// that decides whether to fetch at all is Refresh's.
//
// A fetch failure on an EXISTING mirror is not fatal by itself: the ref may already be
// present, and then Sync answers from it. Refresh reports that failure instead of
// swallowing it. No production path calls Sync any more — `yolo pack install` and the
// launch both go through Refresh — and it is kept as the plain unconditional fetch the
// store's tests and other packages' fixtures build a store with. A successful Sync
// stamps the ref and records the commit it resolved, as a Refresh fetch does, so
// resolution after it reads the same way.
//
// A local (file://) address needs no fetch and returns an empty commit.
func (s *Store) Sync(a Addr) (string, error) {
	if a.IsLocal() {
		return "", nil
	}
	unlock, err := s.lockMirror(a.Repo, nil)
	if err != nil {
		return "", err
	}
	defer unlock()
	mirror := s.mirrorPath(a.Repo)
	b, cancel := s.newBudget()
	defer cancel()
	ferr := s.fetchMirror(b, a, true)
	if ferr != nil && !mirrorExists(mirror) {
		return "", ferr
	}
	if ferr == nil {
		s.writeStamp(a, time.Now())
		s.fetchPinnedCommits(b, mirror, []Addr{a})
	}
	commit, err := s.resolveCommit(mirror, a)
	if err == nil {
		s.recordDecided(a, commit)
	}
	return commit, err
}

// mirrorPath is where the bare mirror of repo lives in this store.
func (s *Store) mirrorPath(repo string) string {
	return filepath.Join(s.Dir, "mirrors", mirrorSlug(repo))
}

// mirrorExists reports whether a mirror has been cloned at all.
func mirrorExists(mirror string) bool {
	_, err := os.Stat(filepath.Join(mirror, "HEAD"))
	return err == nil
}

// fetchMirror is THE NETWORK ACCESS: clone the mirror if it is missing, then fetch every
// branch and tag into it, all inside one budget. It returns the failure rather than
// judging it — whether a failed fetch is fatal depends on whether the ref already
// resolves, which is the caller's question. The caller holds the mirror's lock.
//
// explicit is false for a launch-time fetch, which also turns automatic gc off for the
// fetch: refreshMirror puts back every tag the fetch moved or pruned, and a gc inside the
// fetch could otherwise drop the objects a tag it is about to restore still needs.
func (s *Store) fetchMirror(b budget, a Addr, explicit bool) error {
	mirror := s.mirrorPath(a.Repo)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		return err
	}
	if !mirrorExists(mirror) {
		// A BARE mirror, not a worktree clone: one mirror serves every ref and
		// subpath of the repo, so N packs from a monorepo cost one fetch.
		if _, err := s.runIn(b, "", withFsck("clone", "--bare", "--filter=blob:none",
			a.Repo, mirror)...); err != nil {
			// A clone that died partway (a timeout, a dropped connection) can leave a
			// directory with a HEAD and no refs, which the next attempt would take for an
			// existing mirror. Remove it so the next attempt clones again.
			_ = os.RemoveAll(mirror)
			return fmt.Errorf("cloning %s: %w", a.Repo, err)
		}
	}
	// fsckObjects on fetch (fsckArgs says why).
	args := append([]string{}, fsckArgs...)
	if !explicit {
		args = append(args, "-c", "gc.auto=0")
	}
	args = append(args, "fetch", "--prune", "origin",
		"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	if _, err := s.runIn(b, mirror, args...); err != nil {
		return fmt.Errorf("fetching %s: %w", a.Repo, err)
	}
	return nil
}

// fetchPinnedCommits asks the remote for every commit that addrs pin by full SHA and the mirror
// still lacks after fetchMirror: a commit on no branch or tag of the remote, such as a deleted
// branch's or a pull request's, which a fetch of the branches and tags does not bring. The
// caller holds the mirror's lock and has just fetched it successfully.
//
// This is how such a pin is delivered now that no lookup fetches (gitCmd). The lookup's own
// fetch used to deliver it, unchecked; this one runs with fsckArgs. It moves no ref, and never
// starts a gc, which is the fetch of the branches and tags' to start or not (fetchMirror).
//
// ONE FETCH PER COMMIT, so a pin the remote lacks (a typo) cannot fail the fetch of another. A
// failure is not the mirror's either: the fetch of its branches and tags succeeded, and a pack
// that pins no missing commit is unaffected. It is recorded for the addresses it was for, and
// resolution names it (resolveCommit) instead of calling the commit missing from the remote,
// since a remote that refused it, or sent an object fsck rejects, may well have it.
func (s *Store) fetchPinnedCommits(b budget, mirror string, addrs []Addr) {
	var want []string
	askers := map[string][]Addr{}
	for _, a := range addrs {
		if !isFullSHA(a.Ref) {
			continue
		}
		pinnedFetchFailures.Delete(s.decidedKey(a))
		if _, seen := askers[a.Ref]; !seen {
			if sha, err := s.revParse(mirror, a.Ref); err != nil || sha != "" {
				continue // the mirror holds it, or git could not say: resolution reports which
			}
			want = append(want, a.Ref)
		}
		askers[a.Ref] = append(askers[a.Ref], a)
	}
	for _, sha := range want {
		_, err := s.runIn(b, mirror, withFsck("-c", "gc.auto=0", "fetch", "origin", "--no-tags",
			"--no-write-fetch-head", "--recurse-submodules=no", sha)...)
		if err == nil {
			continue
		}
		for _, a := range askers[sha] {
			pinnedFetchFailures.Store(s.decidedKey(a), err)
		}
	}
}

// revParse resolves one revision to a full commit SHA in the mirror. It reads the mirror's
// refs and objects only, and FETCHES NOTHING: a full SHA the partial mirror lacks is absent
// here even when the remote has it (GIT_NO_LAZY_FETCH, gitCmd), so the refresh fetches it
// through its own checked fetch. The SHA is read from git's stdout alone (runEnv).
//
// "" WITH A NIL ERROR IS GIT'S OWN ANSWER that rev names no commit in the mirror, and it is
// the only answer that means absent. `rev-parse --verify --quiet` gives it as exit status 1,
// whatever it printed (measured, git 2.55): silently for a ref the mirror lacks and for a full
// SHA it lacks, and with a message on stderr for a ref naming a tree or a blob and for a broken
// loose ref git ignores. Every one of those is repaired by a fetch, or is a ref no fetch will
// find, so each must read as absent; a stricter test on stderr would stop a ref naming a tree
// from reading as missing.
//
// ANYTHING ELSE IS AN ERROR, never absent: a run that overran the store's timeout, git's own
// fatal exit 128 (not a repository, an unparseable config or packed-refs, a corrupt object), a
// signal, or a git that could not be run. Reading those as "no such ref" made the launch's
// refresh skip a due fetch in silence (classifyRef).
func (s *Store) revParse(mirror, rev string) (string, error) {
	out, err := s.run(mirror, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err == nil {
		return strings.TrimSpace(out), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return "", err
}

// ErrNotFetched marks a resolution failure that a FETCH is what repairs: the store has no
// mirror of the pack's repository, or the mirror does not hold its ref, and no fetch in
// this process has come back to say the remote lacks it. A launch's refresh fetches
// exactly those, so a read-only caller (`yolo check`) reports one as "the next launch
// fetches it" rather than as a broken pack, and the host-apply remedy offers the fetch.
// Test with errors.Is.
var ErrNotFetched = errors.New("not fetched into the pack store")

// ErrNotCheckedOut marks a ResolveExisting failure for a commit the store holds but has not
// checked out yet — which the launch's refresh (or any Resolve) does, and a read-only
// caller must not.
var ErrNotCheckedOut = errors.New("not checked out in the pack store")

// storeMiss is an error carrying one of the sentinels above under its own message.
type storeMiss struct {
	msg  string
	kind error
}

func (e *storeMiss) Error() string { return e.msg }
func (e *storeMiss) Unwrap() error { return e.kind }

// resolveCommit maps an address's ref to a full SHA inside the mirror, with no network.
//
// Its failure says which of three things is true, because each sends the user somewhere
// different: the fetch this process tried FAILED (the error names why); a fetch
// SUCCEEDED and the remote does not have the ref (a typo in ?ref=, or a deleted branch —
// no launch repairs it); or nothing has fetched it yet (ErrNotFetched — the next host
// launch does). A commit pinned by its id adds one between the first two: the fetch of the
// branches and tags succeeded without it, and the fetch of it by its id failed
// (fetchPinnedCommits), named by that fetch's error. And one failure is not about the ref at
// all: git could not read the mirror (a rev-parse that timed out or failed, revParse), and
// the error is git's own, since neither a fetch nor an edit of ?ref= is what repairs it.
func (s *Store) resolveCommit(mirror string, a Addr) (string, error) {
	// Try the ref as written, then as a branch/tag. rev-parse on a bare repo resolves a
	// SHA, a branch, or a tag without touching the network, a commit the partial mirror
	// lacks included (revParse). A lookup that FAILED ends the search: a later candidate
	// cannot answer for the one before it.
	for _, cand := range []string{a.Ref, "refs/heads/" + a.Ref, "refs/tags/" + a.Ref} {
		sha, err := s.revParse(mirror, cand)
		if err != nil {
			return "", fmt.Errorf("could not read ref %q in the pack mirror of %s: %s", a.Ref, a.Repo, oneLine(err))
		}
		if sha != "" {
			return sha, nil
		}
	}
	if ferr := s.fetchFailure(a.Repo); ferr != nil {
		return "", &storeMiss{kind: ErrNotFetched, msg: fmt.Sprintf("ref %q not found in the pack "+
			"mirror of %s, and fetching it failed: %s — the next host launch retries, or run "+
			"`yolo pack install` to fetch it now", a.Ref, a.Repo, oneLine(ferr))}
	}
	if perr := s.pinnedFetchFailure(a); perr != nil {
		return "", fmt.Errorf("commit %s is on no branch or tag of %s, and fetching it by its id "+
			"failed: %s", a.Ref, a.Repo, oneLine(perr))
	}
	if s.fetchedRef(a) {
		return "", fmt.Errorf("ref %q not found on the remote %s: the pack mirror was fetched "+
			"and has no such branch, tag or commit — check the pack's ?ref=", a.Ref, a.Repo)
	}
	return "", &storeMiss{kind: ErrNotFetched, msg: fmt.Sprintf("ref %q not found in the pack "+
		"mirror of %s — the next host launch fetches it, or run `yolo pack install` to fetch it "+
		"now", a.Ref, a.Repo)}
}

// fetchedRef reports whether a fetch of a's repository is known to have SUCCEEDED while a's
// ref was asked for: the ref carries a stamp, which only a successful fetch writes, for
// every ref it was run for. It is how resolveCommit tells "not on the remote" from "not
// fetched yet", in the launch that just fetched and in a later process that did not, such
// as `yolo check`. On disk rather than in memory for that second reader. A stamp that
// could not be written degrades the message to "not fetched yet", never the launch.
func (s *Store) fetchedRef(a Addr) bool {
	_, err := os.Stat(s.stampPath(a))
	return err == nil
}

// Materialize checks the resolved commit out into a content tree and returns the
// directory to stage from.
//
// Idempotent: an existing tree for the same commit is reused, because a commit is
// immutable so there is nothing to refresh. That is what keeps repeat launches from
// re-checking-out.
func (s *Store) Materialize(a Addr, commit string) (*Resolved, error) {
	if a.IsLocal() {
		if fi, err := os.Stat(a.Path); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("local pack %s is not a directory", a.Path)
		}
		return &Resolved{Root: a.Path}, nil
	}
	if commit == "" {
		return nil, fmt.Errorf("no resolved commit for %s", a.Raw)
	}
	tree := s.treeDir(a, commit)
	if _, err := os.Stat(filepath.Join(tree, treeCompleteMarker)); err == nil {
		return treeResolved(a, tree, commit)
	}
	b, cancel := s.newBudget()
	defer cancel()
	// A checkout is about to happen, so take the mirror's lock: two launches checking one
	// commit out at once would each RemoveAll the other's half-written tree. Re-checked
	// under the lock by materialize, since the holder may have just completed it.
	unlock, err := s.lockMirror(a.Repo, nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.materialize(b, a, commit)
}

// materialize is Materialize for a caller that already holds the mirror's lock, inside the
// caller's budget: a checkout of the partial mirror may fetch blobs, so it is part of the
// fetch the budget bounds.
//
// A REF CHANGE LEAVES NOTHING STALE, by construction rather than by cleanup: every tree is
// keyed by the commit it holds (treeDir), is created empty, and is checked out through an
// index of its own that starts empty (checkoutTree), so a file of an earlier commit has no
// way into it. A tree already complete for this commit is never written again, because a
// commit is immutable and another launch may be staging from it.
func (s *Store) materialize(b budget, a Addr, commit string) (*Resolved, error) {
	tree := s.treeDir(a, commit)
	marker := filepath.Join(tree, treeCompleteMarker)
	if _, err := os.Stat(marker); err != nil {
		mirror := s.mirrorPath(a.Repo)
		// Asked of the commit's trees before anything is written, so a mistyped
		// subdirectory is named plainly instead of as git's "pathspec did not match".
		if err := s.checkSubdir(b, mirror, a, commit); err != nil {
			return nil, err
		}
		// Not present, or a previous attempt died partway. Start clean: a partial
		// tree staged silently would be worse than a re-checkout.
		if err := os.RemoveAll(tree); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(tree, 0o755); err != nil {
			return nil, err
		}
		if err := s.checkoutTree(b, mirror, tree, commit, a.Path); err != nil {
			return nil, fmt.Errorf("checking out %s: %w", commit[:min(8, len(commit))], err)
		}
		// The completion marker is written LAST, so an interrupted checkout is
		// detected as incomplete next time rather than mistaken for a good tree.
		if err := os.WriteFile(marker, []byte(commit+"\n"), 0o644); err != nil {
			return nil, err
		}
	}
	return treeResolved(a, tree, commit)
}

// treeDir is where the checkout of commit for a lives. A pack at the repository root keeps
// trees/<commit>, the layout it always had. A pack in a subdirectory gets a tree of its own,
// trees/<commit>-<subdir key>, holding only that subdirectory at its path in the repository
// (so the pack root is <tree>/<subdir>, as it was): one commit's subdirectories cannot share a
// tree once no tree holds the whole commit. The key is a hash, like mirrorSlug's, because a
// subdirectory may contain any character a path can; the commit comes first so a directory
// listing still reads by commit.
func (s *Store) treeDir(a Addr, commit string) string {
	if a.Path == "" {
		return s.wholeCommitTree(commit)
	}
	sum := sha256.Sum256([]byte(a.Path))
	return filepath.Join(s.Dir, "trees", commit+"-"+hex.EncodeToString(sum[:6]))
}

// wholeCommitTree is where a checkout of the whole of commit lives: a repository-root pack's
// tree, and the tree an earlier yolo made for every git pack, a subdirectory pack's included.
func (s *Store) wholeCommitTree(commit string) string {
	return filepath.Join(s.Dir, "trees", commit)
}

// literalPathspec is the pathspec naming exactly the subdirectory sub, or the whole tree for
// the repository root. `top` anchors it at the repository root whatever directory git runs
// in; `literal` turns off every wildcard, so a directory named `p*` or `p[ab]` checks out that
// directory and no sibling its name would match as a glob. A subdirectory name cannot reach
// the magic either: it is parsed only at the start of the argument, which this prefix owns.
func literalPathspec(sub string) string {
	if sub == "" {
		return "."
	}
	return ":(top,literal)" + sub
}

// checkoutTree checks sub of commit (the whole commit when sub is "") out of the mirror into
// tree, which the caller has just created empty, having fetched the file contents it needs in
// one request first (prefetchBlobs).
//
// THROUGH A PRIVATE, EMPTY INDEX. A bare mirror's own index is state shared by every checkout
// the mirror has served: an earlier whole-repository checkout leaves every path of that
// commit in it, and a later checkout would read and rewrite them all for one small
// subdirectory. Starting each checkout from an empty index means the only entries it knows
// are the ones this commit and this pathspec put there.
func (s *Store) checkoutTree(b budget, mirror, tree, commit, sub string) error {
	idx, err := os.MkdirTemp("", "yolo-pack-index-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(idx)
	s.prefetchBlobs(b, mirror, commit, sub)
	_, err = s.runEnv(b, mirror, []string{"GIT_INDEX_FILE=" + filepath.Join(idx, "index")},
		withFsck("--work-tree="+tree, "checkout", "--force", commit, "--", literalPathspec(sub))...)
	return err
}

// prefetchBlobs fetches, in ONE request, every file's contents the checkout of sub at commit
// (the whole commit when sub is "") needs and the partial mirror lacks.
//
// Without it the checkout fetches them itself, ONE `git fetch` PER FILE: a pathspec checkout
// has no batch prefetch, so each missing blob is its own fetch process and, against a remote,
// its own connection. Measured 2026-09-29 against a GitHub HTTPS remote of 2,992 files: a
// 54-file directory took 12.6s that way (about 233ms a file) and 0.46s as one fetch; the
// whole commit took 4.9s as one fetch; and the whole-commit checkout this store ran before
// it checked out subdirectories alone ran out of the launch's budget (LaunchFetchTimeout)
// after about 425 per-file fetches, leaving the pack unusable, where a launch's refresh of the
// 54-file directory now takes about 1.5s from an empty store.
//
// It asks the mirror which blobs under the tree are missing (`rev-list --missing=print`,
// which reads trees and fetches nothing; `<commit>:<sub>` looks the path up literally), then
// fetches exactly those with the arguments git uses for one lazy fetch of its own, given all
// of them at once. BEST-EFFORT: a failure here leaves the checkout to fetch lazily, as it
// always could, and a remote that is really unreachable is then the checkout's error.
func (s *Store) prefetchBlobs(b budget, mirror, commit, sub string) {
	treeish := commit + "^{tree}"
	if sub != "" {
		treeish = commit + ":" + sub
	}
	out, err := s.runIn(b, mirror, "rev-list", "--objects", "--missing=print", treeish)
	if err != nil {
		return
	}
	var missing strings.Builder
	for _, line := range strings.Split(out, "\n") {
		if oid, ok := strings.CutPrefix(strings.TrimSpace(line), "?"); ok {
			missing.WriteString(oid + "\n")
		}
	}
	if missing.Len() == 0 {
		return
	}
	cmd := s.gitCmd(b.ctx, mirror, withFsck("-c", "fetch.negotiationAlgorithm=noop",
		"fetch", "origin", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no",
		"--filter=blob:none", "--stdin")...)
	cmd.Stdin = strings.NewReader(missing.String())
	_ = cmd.Run()
}

// checkSubdir reports whether a's subdirectory is a DIRECTORY in commit, the one thing a pack
// root can be, reading the mirror's trees alone: `git ls-tree` names an entry's mode without
// reading its contents, so this fetches nothing even from the partial mirror, which holds
// every tree. nil for a repository-root pack.
//
// A symlink is refused rather than followed. The checkout writes the link itself, not the
// directory it names, so the pack would be empty or, through an absolute or `..` target, a
// directory outside the store. So is a path THROUGH a symlink (a link `tools` and the address
// `//tools/pack`), and by name: git's trees have no `tools/pack` entry, so the lookup of the
// whole path finds nothing, but "not found" would be wrong about a commit whose every full
// checkout has that directory, and an earlier yolo, checking out the whole commit, followed
// such a link, an absolute one to a host directory included. A submodule is refused the same
// two ways.
func (s *Store) checkSubdir(b budget, mirror string, a Addr, commit string) error {
	if a.Path == "" {
		return nil
	}
	mode, err := s.treeEntryMode(b, mirror, commit, a.Path)
	if err != nil {
		return err
	}
	what := "a file"
	switch mode {
	case "040000":
		return nil
	case "":
		return s.subdirPathBlocked(b, mirror, a, commit)
	case "120000":
		what = "a symlink"
	case "160000":
		what = "a submodule, which a pack fetch does not follow"
	}
	return fmt.Errorf("pack subdirectory %q is %s in commit %s of %s, not a directory",
		a.Path, what, shortCommit(commit), a.Repo)
}

// subdirPathBlocked is checkSubdir's answer for a subdirectory the commit has no entry for:
// the leading directory of it that is a symlink or a submodule, named, when one is, since git
// records nothing below either; otherwise "not found". It asks one leading path at a time,
// which only this failure pays for.
func (s *Store) subdirPathBlocked(b budget, mirror string, a Addr, commit string) error {
	segs := strings.Split(a.Path, "/")
	for i := 1; i < len(segs); i++ {
		prefix := strings.Join(segs[:i], "/")
		mode, err := s.treeEntryMode(b, mirror, commit, prefix)
		if err != nil {
			return err
		}
		switch mode {
		case "120000":
			return fmt.Errorf("pack subdirectory %q passes through a symlink (%s) in commit %s of %s",
				a.Path, prefix, shortCommit(commit), a.Repo)
		case "160000":
			return fmt.Errorf("pack subdirectory %q passes through a submodule (%s), which a pack "+
				"fetch does not follow, in commit %s of %s", a.Path, prefix, shortCommit(commit), a.Repo)
		}
		if mode != "040000" {
			break // absent, or a file: nothing below it either
		}
	}
	return subdirNotFound(a, commit)
}

// treeEntryMode is the mode git's tree for commit records at path ("040000" a directory,
// "120000" a symlink, "160000" a submodule, anything else a file), or "" when there is no
// entry at path. `ls-tree` reads trees only, so it fetches nothing from the partial mirror;
// the pathspec is literal, so the entry is the one at exactly path.
func (s *Store) treeEntryMode(b budget, mirror, commit, path string) (string, error) {
	out, err := s.runIn(b, mirror, "ls-tree", "-z", commit, "--", literalPathspec(path))
	if err != nil {
		return "", fmt.Errorf("reading pack subdirectory %q in commit %s: %w", path, shortCommit(commit), err)
	}
	for _, rec := range strings.Split(out, "\x00") {
		if meta, name, ok := strings.Cut(rec, "\t"); ok && name == path {
			mode, _, _ := strings.Cut(meta, " ")
			return mode, nil
		}
	}
	return "", nil
}

// subdirNotFound is the error for an address whose subdirectory the commit does not have.
func subdirNotFound(a Addr, commit string) error {
	return fmt.Errorf("pack subdirectory %q not found at %s in commit %s", a.Path, a.Repo, shortCommit(commit))
}

// treeCompleteMarker is the file Materialize writes LAST into a checked-out tree, so a tree
// without it is one an interrupted checkout left behind.
const treeCompleteMarker = ".yolo-pack-complete"

// treeResolved is the pack root inside a complete tree: the tree itself, or the address's
// subdirectory of it. It only reads.
//
// EVERY COMPONENT OF THE SUBDIRECTORY MUST BE A REAL DIRECTORY, checked with Lstat: a symlink
// anywhere on the way would make the pack root a directory the link names, which may be
// outside the store. checkSubdir already refuses that before a checkout; this holds the same
// line for whatever tree is on disk.
func treeResolved(a Addr, tree, commit string) (*Resolved, error) {
	root := tree
	for _, seg := range strings.Split(a.Path, "/") {
		if seg == "" {
			continue
		}
		root = filepath.Join(root, seg)
		fi, err := os.Lstat(root)
		switch {
		case err != nil:
			return nil, subdirNotFound(a, commit)
		case fi.Mode()&os.ModeSymlink != 0:
			return nil, fmt.Errorf("pack subdirectory %q passes through a symlink (%s) in commit %s of %s",
				a.Path, strings.TrimPrefix(root, tree+string(filepath.Separator)), shortCommit(commit), a.Repo)
		case !fi.IsDir():
			return nil, subdirNotFound(a, commit)
		}
	}
	return &Resolved{Root: root, Commit: commit}, nil
}

// Resolve materializes an address using ONLY what is already in the store — no
// network. A launch runs Refresh before it (refresh.go), which is where a missing or
// branch-following pack is fetched; Resolve is the offline half that reads the result,
// and a pack Refresh could not fetch is a clear error here that names why.
//
// IT RESOLVES TO THE COMMIT THIS PROCESS'S REFRESH DECIDED, when there was one, rather
// than re-reading the ref from the mirror. The mirror is shared: another launch, or a
// `yolo pack install`, may fetch it between this launch's Refresh and its staging (the
// nix build sits between them), and re-reading the ref would then stage a commit this
// launch never disclosed — or one it had just warned it was NOT using.
//
// name is the pack's STAGED DIRECTORY NAME — config.PackEntry.Slug(), what a launcher
// named the tree it delivered. It is threaded in rather than derived here because the
// name is a CONFIG fact (an explicit `name:`, else the source URL's last path
// segment) that an Addr does not carry, and re-deriving it in this package would be a
// second spelling of a rule config already owns.
//
// A SOURCE THAT IS NOT VISIBLE FROM HERE IS NOT A BROKEN PACK.
//
// Run inside a jail, every pack address in the inherited config names a HOST path or
// a host-side store — `/home/you/.dotfiles/packs/foo` is not a directory in here and
// never will be, and the mirror for a fetched pack is not mounted either. Reporting
// that as a failure told the user three of their working packs were broken while the
// delivered copies sat in /ctx/packs, and it REFUSED a nested launch outright — which
// breaks the nested-verification workflow AGENTS.md mandates.
//
// What was actually delivered is the STAGED TREE the launcher mounted, so ask that
// when resolution from the address fails. Keyed on the FILESYSTEM rather than on "am
// I in a jail" deliberately: the question is whether a staged copy exists, which is
// the thing that decides whether the pack works, and it cannot misfire on a host
// (where no staged tree is mounted, so this branch never fires).
//
// It lives HERE, in the one function that owns resolution, rather than at a call site
// (docs/reference/storage-and-config.md OQ-SC1, ruled option (i)). The fallback was
// written once in `yolo check` and the launcher never learned it — two callers of one
// resolution rule, only one of them correct, with the test pinning only the caller
// that was. Every caller is now correct by construction, including a future one. The
// honest cost, named rather than pretended away: a store that resolves ADDRESSES now
// knows about the jail's DELIVERY convention.
func (s *Store) Resolve(a Addr, name string) (*Resolved, error) {
	res, err := s.resolveFromStore(a)
	if err == nil {
		return res, nil
	}
	if staged, ok := s.stagedPackDir(name); ok {
		return &Resolved{Root: staged, StagedFrom: staged}, nil
	}
	// No staged copy either: the pack really is unusable, and the address's own error
	// is the one that names what to fix.
	return nil, err
}

// resolveFromStore is resolution from the ADDRESS alone — the store's mirrors and
// trees for a fetched pack, the named path for a local one.
//
// Split out of Resolve so the staged-tree fallback is the only thing wrapping it, and
// so "what the address says" stays answerable on its own.
func (s *Store) resolveFromStore(a Addr) (*Resolved, error) {
	if a.IsLocal() {
		return s.Materialize(a, "")
	}
	commit, err := s.storeCommit(a)
	if err != nil {
		return nil, err
	}
	return s.Materialize(a, commit)
}

// storeCommit is the commit a git address resolves to in this store: the one this
// process's Refresh decided for it, else what the mirror's ref names now.
func (s *Store) storeCommit(a Addr) (string, error) {
	if c := s.decidedCommit(a); c != "" {
		return c, nil
	}
	mirror := s.mirrorPath(a.Repo)
	if !mirrorExists(mirror) {
		return "", s.neverFetched(a)
	}
	return s.resolveCommit(mirror, a)
}

// ResolveExisting is Resolve for a caller that must WRITE NOTHING, such as the agent footer's
// host profile read, which runs on every status-line refresh (docs/design/agent-footer.md
// §2.1). A fetched pack resolves only when the tree for the commit its ref names is already
// complete in the store; a missing or interrupted tree is an error, never the RemoveAll and
// checkout Materialize would run. A local pack, and the staged-tree fallback, resolve as
// Resolve resolves them, since neither writes.
//
// The commands it runs in the mirror read refs and objects only: `git rev-parse`, and, for a
// subdirectory pack whose tree is not checked out, `git ls-tree` (checkSubdir), which reads
// trees. Neither fetches, a commit the partial mirror lacks included (gitCmd). Resolution
// stays the launch's: the same mirror, the same ref, the same commit, so a pack this answers
// for is the pack a launch would stage. A subdirectory pack with no tree of its own is also read from a complete
// whole-commit tree of that commit, which an earlier yolo left for it (existingFromStore):
// the same directory of the same commit.
func (s *Store) ResolveExisting(a Addr, name string) (*Resolved, error) {
	res, err := s.existingFromStore(a)
	if err == nil {
		return res, nil
	}
	if staged, ok := s.stagedPackDir(name); ok {
		return &Resolved{Root: staged, StagedFrom: staged}, nil
	}
	return nil, err
}

// existingFromStore is resolveFromStore without the checkout.
func (s *Store) existingFromStore(a Addr) (*Resolved, error) {
	if a.IsLocal() {
		return s.Materialize(a, "") // a local address is only stat'ed
	}
	commit, err := s.storeCommit(a)
	if err != nil {
		return nil, err
	}
	tree := s.treeDir(a, commit)
	if _, err := os.Stat(filepath.Join(tree, treeCompleteMarker)); err != nil {
		// A subdirectory the commit does not have is not a checkout the next launch
		// makes: that launch fails on it, so say so rather than "not checked out".
		// ls-tree reads trees only, which the partial mirror holds.
		b, cancel := s.newBudget()
		defer cancel()
		if serr := s.checkSubdir(b, s.mirrorPath(a.Repo), a, commit); serr != nil {
			return nil, serr
		}
		// AN EARLIER YOLO'S TREE STILL ANSWERS. Before subdirectory checkouts every git
		// pack's tree was the whole commit at trees/<commit>, which holds this same
		// subdirectory of this same commit (and a repository-root pack's tree today is
		// that same layout). Without it, every read-only reader lost an upgraded user's
		// subdirectory pack until the next launch checked it out again. Read through
		// treeResolved, so a link on the way is refused there as anywhere; only the
		// launch replaces it, by checking the subdirectory out on its own.
		if a.Path != "" {
			whole := s.wholeCommitTree(commit)
			if _, err := os.Stat(filepath.Join(whole, treeCompleteMarker)); err == nil {
				return treeResolved(a, whole, commit)
			}
		}
		return nil, &storeMiss{kind: ErrNotCheckedOut, msg: fmt.Sprintf("pack %s: commit %s is "+
			"not checked out in the pack store; the next host launch or `yolo pack install` "+
			"checks it out", a.Repo, commit[:min(8, len(commit))])}
	}
	return treeResolved(a, tree, commit)
}

// neverFetched is the error for a git pack with no mirror at all. It names the fetch
// failure when this process's Refresh tried and failed, because "never fetched" alone
// would send the user to retry a fetch that just told us why it cannot work.
//
// "The next HOST launch": a launch inside a jail never fetches (the jail has no pack store
// and no git credentials), so a nested launch cannot be what repairs this.
func (s *Store) neverFetched(a Addr) error {
	if ferr := s.fetchFailure(a.Repo); ferr != nil {
		return &storeMiss{kind: ErrNotFetched, msg: fmt.Sprintf("pack %s has never been fetched, "+
			"and fetching it failed: %s — the next host launch retries, or run `yolo pack install` "+
			"to fetch it now", a.Repo, oneLine(ferr))}
	}
	return &storeMiss{kind: ErrNotFetched, msg: fmt.Sprintf("pack %s has never been fetched — the "+
		"next host launch fetches it, or run `yolo pack install` to fetch it now", a.Repo)}
}

// stagedPackDir reports where a pack was actually DELIVERED, when it was.
//
// The launcher mounts the staged tree and names it in YOLO_PACK_ROOT rather than
// hardcoding a path, because the mount point differs by backend (on Apple Container
// and macos-user the trees are read from their host path, with no nested mount).
// Absent that variable — the ordinary host case — there is no staged tree and the
// answer is no.
func (s *Store) stagedPackDir(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	root := s.getenv("YOLO_PACK_ROOT")
	if root == "" {
		return "", false
	}
	dir := filepath.Join(root, name)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir, true
	}
	return "", false
}

// getenv is nil-safe: most callers build a Store with Dir alone, and a nil func there
// must read the real environment rather than panic.
func (s *Store) getenv(key string) string {
	if s.Getenv != nil {
		return s.Getenv(key)
	}
	return os.Getenv(key)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
