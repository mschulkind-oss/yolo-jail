package packsrc

// store.go is the host-side fetch and materialize half of C5.
//
// It runs ON THE HOST, always. The jail has no git credentials by design (that is
// the credential boundary), so a fetch inside one could only ever fail — or worse,
// succeed by finding credentials that were not supposed to be there. Launch is
// strictly offline: it resolves from the store and errors if a pinned commit is
// missing, rather than reaching out mid-boot.
//
// Two-stage layout, and the split is load-bearing:
//
//	<PacksDir>/mirrors/<repo-slug>   a bare git mirror per repository
//	<PacksDir>/trees/<sha>/          a materialized checkout per resolved commit
//
// One mirror serves every ref and subpath of a repo, so N packs from one monorepo
// cost one fetch. Trees are keyed by COMMIT, not by ref, so two packs pinned to the
// same commit share a checkout and a ref moving does not corrupt an existing tree.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Store is a pack source store rooted at Dir.
type Store struct {
	// Dir is the store root (paths.PacksDir()).
	Dir string
	// Git is the git binary to run. Empty means "git" from PATH.
	Git string
	// Timeout bounds each git invocation. Zero means a 2-minute default: a hung
	// fetch must not hang a jail launch forever, and a clear timeout beats a wedge.
	Timeout time.Duration
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

// cleanGitEnv drops gitStateEnv keys from env so a git run against one repo
// cannot be redirected by state git set for another.
func cleanGitEnv(env []string) []string {
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

// run executes git with the store's timeout, returning combined output on failure so
// the caller can surface git's own diagnosis rather than a bare exit code.
func (s *Store) run(dir string, args ...string) (string, error) {
	cmd := exec.Command(s.git(), args...)
	cmd.Dir = dir
	// GIT_TERMINAL_PROMPT=0 turns a missing credential into an immediate error
	// instead of a 30-second askpass hang during a jail launch — the difference
	// between a diagnosable failure and one that looks like yolo wedging.
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(cleanGitEnv(env),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")

	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(s.timeout()):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return "", fmt.Errorf("git %s timed out after %s", args[0], s.timeout())
	}
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Sync fetches the address's repository into the store's mirror and returns the full
// commit SHA its ref resolves to. NETWORK ACCESS HAPPENS HERE and nowhere else.
//
// A local (file://) address needs no fetch and returns an empty commit.
func (s *Store) Sync(a Addr) (string, error) {
	if a.IsLocal() {
		return "", nil
	}
	mirror := filepath.Join(s.Dir, "mirrors", mirrorSlug(a.Repo))
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		// A BARE mirror, not a worktree clone: one mirror serves every ref and
		// subpath of the repo, so N packs from a monorepo cost one fetch.
		if outStr, err := s.run("", "clone", "--bare", "--filter=blob:none",
			a.Repo, mirror); err != nil {
			return "", fmt.Errorf("cloning %s: %w\n%s", a.Repo, err, outStr)
		}
	}
	// fsckObjects on fetch: a pack is third-party content, so a malformed object
	// should be rejected at the boundary rather than after it is in the store.
	if _, err := s.run(mirror, "-c", "transfer.fsckObjects=true",
		"fetch", "--prune", "origin",
		"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"); err != nil {
		// A fetch failure on an EXISTING mirror is not fatal by itself: the ref may
		// already be present, which is what makes offline resolution work. Let
		// Resolve decide.
		_ = err
	}
	return s.resolveCommit(mirror, a.Ref)
}

// resolveCommit maps a ref to a full SHA inside the mirror, OFFLINE.
func (s *Store) resolveCommit(mirror, ref string) (string, error) {
	// Try the ref as written, then as a branch/tag. rev-parse on a bare repo
	// resolves a SHA, a branch, or a tag without touching the network.
	for _, cand := range []string{ref, "refs/heads/" + ref, "refs/tags/" + ref} {
		out, err := s.run(mirror, "rev-parse", "--verify", "--quiet", cand+"^{commit}")
		if err == nil {
			if sha := strings.TrimSpace(out); sha != "" {
				return sha, nil
			}
		}
	}
	return "", fmt.Errorf("ref %q not found in the pack mirror — run `yolo pack update` "+
		"to fetch it (launch is deliberately offline, so a missing pin is an error "+
		"rather than a surprise network call)", ref)
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
	marker := filepath.Join(tree, ".yolo-pack-complete")
	if _, err := os.Stat(marker); err != nil {
		// Not present, or a previous attempt died partway. Start clean: a partial
		// tree staged silently would be worse than a re-checkout.
		if err := os.RemoveAll(tree); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(tree, 0o755); err != nil {
			return nil, err
		}
		mirror := filepath.Join(s.Dir, "mirrors", mirrorSlug(a.Repo))
		if _, err := s.run(mirror, "--work-tree="+tree, "checkout", "--force", commit, "--", "."); err != nil {
			return nil, fmt.Errorf("checking out %s: %w", commit[:min(8, len(commit))], err)
		}
		// The completion marker is written LAST, so an interrupted checkout is
		// detected as incomplete next time rather than mistaken for a good tree.
		if err := os.WriteFile(marker, []byte(commit+"\n"), 0o644); err != nil {
			return nil, err
		}
	}
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
// network. This is the launch path: a jail start must not depend on a reachable git
// server, and a missing pin must be a clear error rather than a hang.
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
// (docs/design/storage-and-config.md OQ-SC1, ruled option (i)). The fallback was
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
	mirror := filepath.Join(s.Dir, "mirrors", mirrorSlug(a.Repo))
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		return nil, fmt.Errorf("pack %s has never been fetched — run `yolo pack install`", a.Repo)
	}
	commit, err := s.resolveCommit(mirror, a.Ref)
	if err != nil {
		return nil, err
	}
	return s.Materialize(a, commit)
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
