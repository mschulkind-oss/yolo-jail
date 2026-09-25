package packsrc

// refresh.go is the LAUNCH-TIME FETCH (maintainer ruling, 2026-09-25: "yes, I want
// this"). A host launch fetches and refreshes git packs itself, and THE REF DECIDES what
// moves:
//
//	never fetched (no mirror, or the ref does not resolve)  fetch and check out
//	a full 40-hex commit SHA that resolves                   never fetch — frozen
//	a tag that resolves (refs/tags/<ref>)                    never fetch — frozen
//	a branch that resolves (refs/heads/<ref>)                fetch when the last good
//	                                                         fetch is over an hour old
//
// Anything else that resolves (an abbreviated SHA, HEAD) never triggers a fetch, since it
// does not name itself as moving. (HEAD is symbolic, so it still follows its branch when
// the mirror is fetched for another pack; that move is disclosed like any other.)
// `yolo pack update` and `yolo pack install` (Force) fetch every pack regardless.
//
// WHY A LAUNCH MAY FETCH AT ALL, when it used to be deliberately offline. The two reasons
// that kept it offline are gone: the y/N host-access approval at install was deleted by
// OQ-TP9 (docs/design/trust-paths.md), and "a launch is offline" was never true (the nix
// build, bootstrap npm and the evergreen agent launchers all use the network). What stays
// is the reason content was frozen — a pack can run HOST code — and that is kept by the
// ref rule above and by the disclosure lines a launch prints (Outcome.Disclosure), which
// are never suppressible.
//
// A FAILED FETCH IS NOT FATAL when the ref already resolves: the cached commit is used
// and one warning names the pack and the error. With no usable local copy, resolution
// fails later exactly as it did before, by name, and its message carries the fetch error
// (fetchFailure) instead of advice to run a command that would fail the same way.
//
// CONCURRENCY. Fetch and checkout run under an exclusive flock per mirror, and the
// lockfile's load-modify-save under an exclusive flock of its own, so two launches at
// once cannot corrupt a mirror, a tree or the lockfile. Every decision — including
// "is the stamp fresh" — is taken UNDER the mirror lock, so a launch that waited for
// another's fetch re-reads the stamp that fetch wrote and does not fetch again.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// BranchRefreshInterval is how old a branch's last successful fetch must be before a
// launch fetches it again. The same interval as the in-jail launchers' UPDATE_INTERVAL
// (internal/entrypoint/shims.go), so "how often does something a pack follows move" has
// one answer across the product.
const BranchRefreshInterval = 3600 * time.Second

// LaunchFetchTimeout bounds each LAUNCH-TIME fetch, through the store's own Timeout: one
// budget per repository covers its clone, its fetch and the checkouts after them (a
// checkout of the partial mirror fetches blobs too), so a never-fetched pack cannot take
// three times this. Shorter than the store's 2-minute default because a launch is waiting
// on it; a timeout is a fetch failure like any other (cached copy used, or the pack
// reported unresolvable by name).
const LaunchFetchTimeout = 60 * time.Second

// RefreshPack is one configured git pack to refresh: its config NAME (the lockfile key)
// and its address as written.
type RefreshPack struct {
	Name   string
	Source string
}

// RefreshOptions tunes one Refresh call.
type RefreshOptions struct {
	// Force fetches every pack, whatever its ref and stamp — `yolo pack install` and
	// `yolo pack update`, the explicit acts. A launch leaves it false.
	Force bool
	// LockPath is the lockfile to record resolutions in. Empty records nothing.
	LockPath string
	// EditLock, when non-nil, runs inside the same locked load-modify-save as the
	// recording, after it — install uses it to record local packs and prune.
	EditLock func(*Lock)
	// Now is the clock for stamps. Nil means time.Now.
	Now func() time.Time
	// Interval overrides BranchRefreshInterval (tests). Zero means the constant.
	Interval time.Duration
	// Waiting, when non-nil, is told when this call is about to block on a lock another
	// process holds, so a pause reads as serialisation rather than as a hang.
	Waiting func(string)
}

// Outcome is what Refresh did for one pack.
type Outcome struct {
	Name string
	Ref  string
	// Commit is the full SHA the pack resolved to; empty when it did not resolve.
	Commit string
	// Prev is the commit this pack was at before: its lockfile entry's, else what the
	// mirror resolved the ref to before any fetch. Empty when there was none.
	Prev string
	// Locked is true when Prev came from the pack's lockfile entry, i.e. the lockfile
	// recorded a commit for this name before this call.
	Locked bool
	// Fetched is true when a fetch ran and succeeded for this pack's mirror.
	Fetched bool
	// First is true when this pack is DELIVERED for the first time: there was no usable
	// local copy before this call, or (with a lockfile) the lockfile had no entry for this
	// pack's repository and subdirectory. The second case is a monorepo's second subpath,
	// or a pack whose address moved: its content arrives without any fetch.
	First bool
	// FetchErr is a fetch that failed. With Commit set, the cached copy is in use.
	FetchErr error
	// Err is why the pack is unusable (bad address, unresolvable ref, failed checkout).
	Err error
}

// Disclosure is the line a launch prints for this outcome, or "" when nothing moved.
// Never suppressible (AGENTS.md, "A LAUNCH HAS NO QUIET MODE"): a pack can run host code,
// so content arriving or moving is always said.
func (o Outcome) Disclosure() string {
	if o.Commit == "" || o.Err != nil {
		return ""
	}
	if o.First {
		return fmt.Sprintf("Fetched pack %s: %s → %s", o.Name, o.Ref, shortCommit(o.Commit))
	}
	if o.Prev != "" && o.Prev != o.Commit {
		return fmt.Sprintf("Updated pack %s: %s %s → %s", o.Name, o.Ref,
			shortCommit(o.Prev), shortCommit(o.Commit))
	}
	return ""
}

// Warning is the one line for a fetch that failed while a cached copy stayed usable, or
// "" otherwise. A pack with no usable copy is not warned about here: resolution fails
// for it later, by name, carrying the same error.
func (o Outcome) Warning() string {
	if o.FetchErr == nil || o.Commit == "" {
		return ""
	}
	return fmt.Sprintf("pack %s: could not refresh %s (%s) — using the cached %s",
		o.Name, o.Ref, oneLine(o.FetchErr), shortCommit(o.Commit))
}

// Refresh brings every git pack in packs up to the ref rule above, checks each resolved
// commit out, and records {name, source, commit, ref} in the lockfile. Local packs and
// unparseable addresses are the caller's to filter; one that slips through is reported
// in its Outcome and touches nothing.
//
// It NEVER PRUNES the lockfile and never touches an entry it did not process: pruning is
// install's (EditLock), and a launch describes only the packs it resolved.
//
// The returned error is about the LOCKFILE only; every per-pack failure is in its
// Outcome, so one bad pack cannot hide what happened to the others.
func (s *Store) Refresh(packs []RefreshPack, opts RefreshOptions) ([]Outcome, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = BranchRefreshInterval
	}
	outcomes := make([]Outcome, len(packs))
	// GROUPED BY MIRROR, in first-appearance order: every pack on one repository is
	// decided under one hold of that mirror's lock, so N packs from a monorepo cost at
	// most one fetch, and a fetch one pack needs cannot move another pack's frozen ref
	// behind its back (refreshMirror).
	var order []string
	groups := map[string][]refreshItem{}
	addrs := make([]Addr, len(packs))
	for i, p := range packs {
		outcomes[i].Name = p.Name
		a, err := Parse(p.Source)
		if err != nil {
			outcomes[i].Err = err
			continue
		}
		addrs[i] = a
		outcomes[i].Ref = a.Ref
		if a.IsLocal() {
			outcomes[i].Err = fmt.Errorf("pack %s is local (%s): there is nothing to fetch", p.Name, a.Path)
			continue
		}
		if _, seen := groups[a.Repo]; !seen {
			order = append(order, a.Repo)
		}
		groups[a.Repo] = append(groups[a.Repo], refreshItem{idx: i, addr: a})
	}
	for _, repo := range order {
		s.refreshMirror(repo, groups[repo], outcomes, opts.Force, now(), interval, opts.Waiting)
	}
	if opts.LockPath == "" {
		return outcomes, nil
	}
	err := WithLock(s.Dir, opts.LockPath, opts.Waiting, func(l *Lock) (bool, error) {
		changed := false
		for i := range outcomes {
			o := &outcomes[i]
			prev, had := l.Get(o.Name)
			// The lock describes THIS pack's content only when it names the same repository
			// and subdirectory; the ref may differ, and a ref edit is exactly the move the
			// Updated line reports.
			same := had && prev.Commit != "" && sameContent(prev.Source, addrs[i])
			if same {
				o.Prev, o.Locked = prev.Commit, true
			}
			if o.Commit == "" || o.Err != nil {
				continue
			}
			if !same {
				o.First = true
			}
			entry := LockEntry{Name: o.Name, Source: packs[i].Source, Commit: o.Commit, Ref: o.Ref}
			if !had || prev != entry {
				l.Set(entry)
				changed = true
			}
		}
		if opts.EditLock != nil {
			opts.EditLock(l)
			changed = true
		}
		return changed, nil
	})
	return outcomes, err
}

// sameContent reports whether a lockfile source names the same repository and subdirectory
// as a, whatever its ref.
func sameContent(lockedSource string, a Addr) bool {
	la, err := Parse(lockedSource)
	return err == nil && !la.IsLocal() && la.Repo == a.Repo && la.Path == a.Path
}

// refreshItem is one pack of a mirror group: its index in the caller's list and its
// parsed address.
type refreshItem struct {
	idx  int
	addr Addr
}

// refreshMirror is Refresh for every pack on one repository, under that mirror's lock.
//
// THE DECISION IS TAKEN UNDER THE LOCK, from what the mirror and the stamps say NOW: a
// launch that waited for another's fetch sees the stamp that fetch wrote and does not
// fetch again inside the interval.
//
// A FROZEN REF STAYS FROZEN WHEN ITS MIRROR IS FETCHED FOR ANOTHER PACK. The fetch
// force-updates (and prunes) tags, which is what lets `yolo pack install`/`update` move to
// a re-pointed tag, so a launch-time fetch a branch pack needed would otherwise move a tag
// pack sharing its repository — and the next launch, finding the tag resolved, would
// never fetch and stage the new content. So a non-forced fetch puts EVERY tag it moved or
// pruned back where it was (restoreTags), not only the tags of the packs in this call: a
// tag pack of another workspace, or one this config dropped for a while, shares the
// mirror too. A tag the fetch ADDED stays, since nothing was pinned to it. A full SHA
// needs nothing: a commit id cannot be re-pointed.
//
// THE WHOLE MIRROR SHARES ONE BUDGET (the store's Timeout): its clone, its fetch and every
// checkout after them.
func (s *Store) refreshMirror(repo string, items []refreshItem, outcomes []Outcome, force bool,
	now time.Time, interval time.Duration, waiting func(string)) {
	unlock, err := s.lockMirror(repo, waiting)
	if err != nil {
		for _, it := range items {
			outcomes[it.idx].Err = err
		}
		return
	}
	defer unlock()
	b, cancel := s.newBudget()
	defer cancel()

	mirror := s.mirrorPath(repo)
	type pre struct {
		kind  refKind
		name  string // the full ref name for a tag or branch
		local string // the commit the ref resolved to before any fetch
	}
	before := make([]pre, len(items))
	needFetch := force
	existed := mirrorExists(mirror)
	for i, it := range items {
		if existed {
			before[i].kind, before[i].name, before[i].local = s.classifyRef(mirror, it.addr.Ref)
		}
		o := &outcomes[it.idx]
		o.Prev = before[i].local
		o.First = before[i].local == ""
		switch {
		case before[i].local == "":
			needFetch = true
		case before[i].kind == refBranch && !s.stampFresh(it.addr, now, interval):
			needFetch = true
		}
	}

	var fetchErr error
	fetched := false
	if needFetch {
		var tags map[string]string
		var tagsErr error
		if existed && !force {
			tags, tagsErr = s.tagSnapshot(mirror)
		}
		fetchErr = s.fetchMirror(b, items[0].addr, force)
		if fetchErr == nil {
			fetched = true
			s.markFetched(repo)
			// Every ref on the mirror is current after a good fetch, so each pack's ref is
			// stamped from the same one.
			for _, it := range items {
				s.writeStamp(it.addr, now)
			}
			switch {
			case force:
			case tagsErr == nil && tags != nil:
				s.restoreTags(mirror, tags)
			default:
				// No snapshot: at least the tags this call's packs are pinned to go back.
				for i := range items {
					if before[i].kind == refTag {
						s.restoreRef(mirror, before[i].name, before[i].local)
					}
				}
			}
		} else {
			s.recordFetchFailure(repo, fetchErr)
		}
	}

	for i, it := range items {
		o := &outcomes[it.idx]
		o.Fetched = fetched
		if needFetch && !fetched {
			o.FetchErr = fetchErr
		}
		commit := before[i].local
		switch {
		case fetched:
			// The fetch is the truth now, for every ref including one it removed.
			c, rerr := s.resolveCommit(mirror, it.addr)
			if rerr != nil {
				o.Err = rerr
				continue
			}
			commit = c
		case commit == "":
			// Nothing local and nothing fetched: unresolvable, and the error names the fetch
			// failure (recorded above) so resolution later says the same thing.
			if !mirrorExists(mirror) {
				o.Err = s.neverFetched(it.addr)
				continue
			}
			// The mirror exists now although it did not resolve the ref before: a clone
			// that succeeded ahead of a fetch that failed. What the clone brought is the
			// cached copy, used with the fetch error warned about like any other.
			c, rerr := s.resolveCommit(mirror, it.addr)
			if rerr != nil {
				o.Err = rerr
				continue
			}
			commit = c
		}
		if _, err := s.materialize(b, it.addr, commit); err != nil {
			o.Err = err
			continue
		}
		o.Commit = commit
		s.recordDecided(it.addr, commit)
	}
}

// tagSnapshot is every tag in the mirror and the object it names (a tag object for an
// annotated tag, so a restore keeps it annotated).
func (s *Store) tagSnapshot(mirror string) (map[string]string, error) {
	out, err := s.run(mirror, "for-each-ref", "--format=%(objectname) %(refname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if obj, ref, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			tags[ref] = obj
		}
	}
	return tags, nil
}

// restoreTags puts every tag of a snapshot that a fetch moved or pruned back at the object it
// named, in one update-ref. Best-effort: a tag that cannot be restored resolves to whatever
// the fetch left, and a pack's commit change is then disclosed like any other.
func (s *Store) restoreTags(mirror string, before map[string]string) {
	after, err := s.tagSnapshot(mirror)
	if err != nil {
		return
	}
	var in strings.Builder
	for ref, obj := range before {
		if after[ref] != obj {
			fmt.Fprintf(&in, "update %s %s\n", ref, obj)
		}
	}
	if in.Len() == 0 {
		return
	}
	b, cancel := s.newBudget()
	defer cancel()
	cmd := s.gitCmd(b.ctx, mirror, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(in.String())
	_ = cmd.Run()
}

// restoreRef points a ref back at the commit it named before a fetch moved or pruned it.
// Best-effort: a ref that cannot be restored resolves to whatever the fetch left, and the
// commit change is then disclosed like any other.
func (s *Store) restoreRef(mirror, ref, commit string) {
	if ref == "" || commit == "" || s.revParse(mirror, ref) == commit {
		return
	}
	_, _ = s.run(mirror, "update-ref", ref, commit)
}

// refKind is what a ref names in a mirror, which is what decides whether it moves.
type refKind int

const (
	refUnresolved refKind = iota
	refCommit             // a full SHA
	refTag                // refs/tags/<ref>
	refBranch             // refs/heads/<ref>
	refOther              // resolves some other way (abbreviated SHA, HEAD): never fetched for
)

// classifyRef says what ref names in the mirror, the full ref name for a tag or branch,
// and the commit it resolves to, with no network. A TAG IS CHECKED BEFORE A BRANCH
// because that is git's own precedence for a bare name, and so the commit resolveCommit
// picks for the ref as written.
func (s *Store) classifyRef(mirror, ref string) (refKind, string, string) {
	if isFullSHA(ref) {
		if sha := s.revParse(mirror, ref); sha != "" {
			return refCommit, "", sha
		}
		return refUnresolved, "", ""
	}
	switch {
	case strings.HasPrefix(ref, "refs/tags/"):
		if sha := s.revParse(mirror, ref); sha != "" {
			return refTag, ref, sha
		}
		return refUnresolved, "", ""
	case strings.HasPrefix(ref, "refs/heads/"):
		if sha := s.revParse(mirror, ref); sha != "" {
			return refBranch, ref, sha
		}
		return refUnresolved, "", ""
	}
	if sha := s.revParse(mirror, "refs/tags/"+ref); sha != "" {
		return refTag, "refs/tags/" + ref, sha
	}
	if sha := s.revParse(mirror, "refs/heads/"+ref); sha != "" {
		return refBranch, "refs/heads/" + ref, sha
	}
	if sha := s.revParse(mirror, ref); sha != "" {
		return refOther, "", sha
	}
	return refUnresolved, "", ""
}

// isFullSHA reports whether ref is a full 40-hex commit id.
func isFullSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, r := range ref {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// stampPath is where the last successful fetch of a mirror for one ref is recorded.
// The ref goes through mirrorSlug too: a branch name can contain '/', and a hashed slug
// cannot collide or escape the directory.
func (s *Store) stampPath(a Addr) string {
	return filepath.Join(s.Dir, "stamps", mirrorSlug(a.Repo), mirrorSlug(a.Ref))
}

// writeStamp records a successful fetch. Best-effort: a stamp that cannot be written
// costs one extra fetch next launch, never a failed launch.
func (s *Store) writeStamp(a Addr, at time.Time) {
	p := s.stampPath(a)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// stampFresh reports whether the ref's last successful fetch is within interval of now.
// A missing or unreadable stamp is stale: the answer that fetches.
func (s *Store) stampFresh(a Addr, now time.Time, interval time.Duration) bool {
	data, err := os.ReadFile(s.stampPath(a))
	if err != nil {
		return false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(secs, 0))
	return age >= 0 && age < interval
}

// fetchFailures is this PROCESS's record of the last failed fetch per store+repo, so the
// resolution that runs after a launch's Refresh can name the error. In memory on purpose:
// it describes this launch's attempt, and a file would outlive the network condition it
// describes. (The other half — a fetch that SUCCEEDED — is the ref's stamp, on disk,
// because a later process such as `yolo check` needs it too: fetchedRef.)
var fetchFailures sync.Map

func (s *Store) failureKey(repo string) string { return s.Dir + "\x00" + repo }

func (s *Store) recordFetchFailure(repo string, err error) {
	fetchFailures.Store(s.failureKey(repo), err)
}

// markFetched records a successful fetch of repo, which clears any failure recorded for it.
func (s *Store) markFetched(repo string) { fetchFailures.Delete(s.failureKey(repo)) }

// decidedCommits is this PROCESS's record of the commit its Refresh (or Sync) resolved each
// store+repo+ref to, which resolution then uses instead of re-reading the shared mirror
// (Resolve says why). In memory for the same reason as fetchFailures: it is a fact about
// this launch, and the lockfile is the durable record.
var decidedCommits sync.Map

func (s *Store) decidedKey(a Addr) string { return s.Dir + "\x00" + a.Repo + "\x00" + a.Ref }

func (s *Store) recordDecided(a Addr, commit string) { decidedCommits.Store(s.decidedKey(a), commit) }

func (s *Store) decidedCommit(a Addr) string {
	if v, ok := decidedCommits.Load(s.decidedKey(a)); ok {
		return v.(string)
	}
	return ""
}

func (s *Store) fetchFailure(repo string) error {
	if v, ok := fetchFailures.Load(s.failureKey(repo)); ok {
		return v.(error)
	}
	return nil
}

// lockMirror takes the exclusive flock for one repository's mirror and returns its
// release. Fetch and checkout both run under it.
func (s *Store) lockMirror(repo string, waiting func(string)) (func(), error) {
	return flockPath(filepath.Join(s.Dir, "locks", mirrorSlug(repo)+".lock"),
		"the pack mirror of "+repo, waiting)
}

// WithLock is the lockfile's load-modify-save under an exclusive flock kept in the pack
// store (storeDir/locks), so two processes recording at once cannot lose each other's
// entries. fn reports whether it changed anything; nothing is written when it did not,
// so a launch that moved nothing leaves the file (and its mtime) alone.
func WithLock(storeDir, lockPath string, waiting func(string), fn func(*Lock) (bool, error)) error {
	unlock, err := flockPath(filepath.Join(storeDir, "locks", "lockfile-"+mirrorSlug(lockPath)+".lock"),
		"the pack lockfile "+lockPath, waiting)
	if err != nil {
		return err
	}
	defer unlock()
	l, err := LoadLock(lockPath)
	if err != nil {
		return err
	}
	changed, err := fn(l)
	if err != nil || !changed {
		return err
	}
	return l.Save(lockPath)
}

// flockPath opens path and takes an exclusive flock on it, NON-BLOCKING FIRST so a
// caller can be told it is about to wait (what), then blocking.
func flockPath(path, what string, waiting func(string)) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		if waiting != nil {
			waiting(fmt.Sprintf("waiting for another yolo process to release %s", what))
		}
		if err := flockRetry(fd, syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// flockRetry is a blocking flock that retries an interrupted wait.
func flockRetry(fd, how int) error {
	for {
		err := syscall.Flock(fd, how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// oneLine folds a multi-line error (git's own output follows its exit status) onto one
// line, for a warning or a resolution error that is printed as a single line. git's
// "Cloning into …" progress line is dropped: it names a store path and diagnoses nothing.
func oneLine(err error) string {
	var kept []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Cloning into ") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(strings.Fields(strings.Join(kept, " ")), " ")
}

// shortCommit abbreviates a commit for a disclosure line.
func shortCommit(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
