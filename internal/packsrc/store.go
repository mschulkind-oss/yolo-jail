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
// happens in fetchMirror, which Sync and Refresh call.
//
// Layout, and the mirror/tree split is load-bearing:
//
//	<PacksDir>/mirrors/<repo-slug>              a bare git mirror per repository
//	<PacksDir>/trees/<sha>/                     a materialized checkout per resolved commit
//	<PacksDir>/locks/<repo-slug>.lock           the per-mirror flock (fetch + checkout)
//	<PacksDir>/locks/lockfile-<path-slug>.lock  the flock around a lockfile's rewrite
//	<PacksDir>/stamps/<repo-slug>/<ref-slug>    when a branch last fetched successfully
//
// One mirror serves every ref and subpath of a repo, so N packs from one monorepo
// cost one fetch. Trees are keyed by COMMIT, not by ref, so two packs pinned to the
// same commit share a checkout and a ref moving does not corrupt an existing tree.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
var gitStateEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
}

// CleanGitEnv drops gitStateEnv keys from env so a git run against one repo
// cannot be redirected by state git set for another.
//
// Exported for the TEST helpers that shell out to git against scratch
// repositories (internal/cli, internal/cli/run): a pre-commit hook runs the
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
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout())
	return budget{ctx: ctx, d: s.timeout()}, cancel
}

// fsckArgs turn on object checking for everything a git run receives over the network:
// transfer.fsckObjects covers a clone and a fetch, and because `git -c` settings reach
// git's child processes (GIT_CONFIG_PARAMETERS), it also covers the lazy blob fetch a
// checkout of the partial (--filter=blob:none) mirror makes. A pack is third-party
// content, so a malformed object is rejected at the boundary rather than after it is in
// the store.
var fsckArgs = []string{"-c", "transfer.fsckObjects=true"}

// withFsck prefixes args with fsckArgs.
func withFsck(args ...string) []string {
	return append(append([]string{}, fsckArgs...), args...)
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

// gitCmd builds the git command for one store run: the store's git, the environment
// hygiene, and — when Detached — a new session whose whole process group the budget's
// cancellation kills. Split from run so the hygiene is testable as a property of the
// command every run executes.
func (s *Store) gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.git(), args...)
	cmd.Dir = dir
	// GIT_TERMINAL_PROMPT=0 turns a missing credential into an immediate error
	// instead of a 30-second askpass hang during a jail launch — the difference
	// between a diagnosable failure and one that looks like yolo wedging.
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(CleanGitEnv(env),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	if s.Detached {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Cancel = func() error {
			// Setsid made git a process-group leader, so -pid is git and every helper
			// it started.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// run executes git with a budget of the store's own timeout, returning combined output on
// failure so the caller can surface git's own diagnosis rather than a bare exit code.
func (s *Store) run(dir string, args ...string) (string, error) {
	b, cancel := s.newBudget()
	defer cancel()
	return s.runIn(b, dir, args...)
}

// runIn is run inside a budget another run may already have spent part of.
func (s *Store) runIn(b budget, dir string, args ...string) (string, error) {
	out, err := s.gitCmd(b.ctx, dir, args...).CombinedOutput()
	if b.ctx.Err() != nil {
		return "", fmt.Errorf("git %s timed out after %s", gitLabel(args), b.d)
	}
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", gitLabel(args), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
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

// revParse resolves one revision to a full commit SHA in the mirror, or "" when it does
// not name a commit there. It reads refs and objects only.
func (s *Store) revParse(mirror, rev string) string {
	out, err := s.run(mirror, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
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
// launch does).
func (s *Store) resolveCommit(mirror string, a Addr) (string, error) {
	// Try the ref as written, then as a branch/tag. rev-parse on a bare repo
	// resolves a SHA, a branch, or a tag without touching the network.
	for _, cand := range []string{a.Ref, "refs/heads/" + a.Ref, "refs/tags/" + a.Ref} {
		if sha := s.revParse(mirror, cand); sha != "" {
			return sha, nil
		}
	}
	if ferr := s.fetchFailure(a.Repo); ferr != nil {
		return "", &storeMiss{kind: ErrNotFetched, msg: fmt.Sprintf("ref %q not found in the pack "+
			"mirror of %s, and fetching it failed: %s — the next host launch retries, or run "+
			"`yolo pack install` to fetch it now", a.Ref, a.Repo, oneLine(ferr))}
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
	tree := filepath.Join(s.Dir, "trees", commit)
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
func (s *Store) materialize(b budget, a Addr, commit string) (*Resolved, error) {
	tree := filepath.Join(s.Dir, "trees", commit)
	marker := filepath.Join(tree, treeCompleteMarker)
	if _, err := os.Stat(marker); err != nil {
		// Not present, or a previous attempt died partway. Start clean: a partial
		// tree staged silently would be worse than a re-checkout.
		if err := os.RemoveAll(tree); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(tree, 0o755); err != nil {
			return nil, err
		}
		mirror := s.mirrorPath(a.Repo)
		if _, err := s.runIn(b, mirror, withFsck("--work-tree="+tree, "checkout", "--force", commit, "--", ".")...); err != nil {
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

// treeCompleteMarker is the file Materialize writes LAST into a checked-out tree, so a tree
// without it is one an interrupted checkout left behind.
const treeCompleteMarker = ".yolo-pack-complete"

// treeResolved is the pack root inside a complete tree: the tree itself, or the address's
// subdirectory of it. It only reads.
func treeResolved(a Addr, tree, commit string) (*Resolved, error) {
	root := tree
	if a.Path != "" {
		root = filepath.Join(tree, filepath.FromSlash(a.Path))
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("pack subdirectory %q not found at %s in commit %s",
				a.Path, a.Repo, commit[:min(8, len(commit))])
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
// The one command it runs is `git rev-parse` in the mirror, which reads refs and objects only.
// Resolution stays the launch's: the same mirror, the same ref, the same commit, so a pack this
// answers for is the pack a launch would stage.
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
	tree := filepath.Join(s.Dir, "trees", commit)
	if _, err := os.Stat(filepath.Join(tree, treeCompleteMarker)); err != nil {
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
