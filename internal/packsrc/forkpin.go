package packsrc

// forkpin.go is THE LAUNCH'S FORK PIN (docs/design/forked-programs-as-packs.md FP-D18, applying the
// maintainer's OQ-PF1 ruling of 2026-09-25: "yolo pack install and update still exist, but neither
// is required"): a launch that carries a fork the fork lock does not pin for its declared source
// resolves the fork's ref once and records the commit, and a launch that finds a fork pinned leaves
// the pin where it is.
//
// # Why this does not break "no rebuild on a timer"
//
// FP-D7 kept every pin out of the launch because re-resolving a branch at launch is the rebuild on a
// timer §9 forbids. That reason forbids MOVING a pin, and this moves none: a STANDING pin — an entry
// whose source is the one the fork declares — is returned as it is, with no git run, however far its
// branch has moved. Only a fork with no such entry is resolved, once, and `yolo pack update` is
// still the only act that moves a pin.
//
// # The resolution is the launch's ref rule
//
// The unpinned forks go through refreshMirror, the launch-time refresh's per-mirror step, with no
// pack lockfile: a ref the mirror lacks is fetched, a branch whose last good fetch is over an hour
// old is fetched, a tag or a full commit the mirror holds is never fetched, and a fetch puts back
// every tag it moved, so pinning a fork never moves a tag pack sharing its repository. The resolved
// commit is checked out, so the build act's checkout reads the pack store. A fetch that fails while
// the mirror still resolves the ref pins the commit the mirror holds, and says so (FetchErr).
//
// # Concurrency
//
// Two launches pinning one fork at once must not record two commits. The pin is made under the fork
// lock's flock (WithForkLock), and the lock is RE-READ under it: a launch that waited for another's
// pin finds it there and takes it, with no git run. A launch that finds every fork pinned on its
// first, unlocked read takes no lock at all, which is every launch after a fork's first.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ForkWant is one fork a pinner is asked about: its key in the fork lock ("<fork pack>/<bin>") and
// its source as the manifest writes it.
type ForkWant struct {
	Key    string
	Source string
}

// ForkPinOutcome is what PinForks did for one fork.
type ForkPinOutcome struct {
	Key, Source string
	// Entry is the fork's pin when PinForks returns, standing or made by it. Its Commit is "" when
	// the fork has none, and Err then says why.
	Entry ForkLockEntry
	// Pinned is true when this call made the pin.
	Pinned bool
	// FetchErr is the fetch that failed while the pin was made from the commit the mirror already
	// held. nil otherwise.
	FetchErr error
	// Err is why the fork has no pin: the fork lock could not be read or written, or its source
	// could not be resolved. One line.
	Err error
	// LockErr is the fork lock's read error, as LoadForkLock returned it, when that is why: the
	// lock may hold this fork's pin, so the fork is not one PinForks failed to pin, and a caller
	// says so in the words every other reader of the lock uses. nil otherwise.
	LockErr error
}

// ForkPinOptions tunes PinForks.
type ForkPinOptions struct {
	// Begin, when non-nil, is called once, before the first lock wait or git run, and returns the
	// lock-wait notice and the function that ends what Begin started (a launch's progress line). A
	// call that finds every fork pinned never calls it.
	Begin func() (waiting func(string), end func())
}

// PinForks pins every fork in want that the fork lock at lockPath does not pin for its declared
// source, and returns each fork's pin. It never moves a standing pin and never fails as a whole:
// every failure is its fork's Err.
func (s *Store) PinForks(lockPath string, want []ForkWant, opts ForkPinOptions) []ForkPinOutcome {
	out := make([]ForkPinOutcome, len(want))
	addrs := make([]Addr, len(want))
	for i, w := range want {
		out[i].Key, out[i].Source = w.Key, w.Source
		a, err := Parse(w.Source)
		switch {
		case err != nil:
			out[i].Err = err
		case a.IsLocal():
			out[i].Err = fmt.Errorf("%s is a directory, which has no revision to pin", w.Source)
		}
		addrs[i] = a
	}
	lock, err := LoadForkLock(lockPath)
	if err != nil {
		for i := range out {
			if out[i].Err == nil {
				out[i].Err, out[i].LockErr = errors.New(oneLine(err)), err
			}
		}
		return out
	}
	var unpinned []int
	for i := range want {
		if out[i].Err != nil {
			continue
		}
		if e, ok := standingPin(lock, want[i]); ok {
			out[i].Entry = e
			continue
		}
		unpinned = append(unpinned, i)
	}
	if len(unpinned) == 0 {
		return out
	}
	waiting, end := (func(string))(nil), func() {}
	if opts.Begin != nil {
		waiting, end = opts.Begin()
	}
	defer end()
	err = WithForkLock(s.Dir, lockPath, waiting, func(l *ForkLock) (bool, error) {
		// RE-READ UNDER THE LOCK: another launch may have pinned a fork while this one waited.
		var todo []int
		for _, i := range unpinned {
			if e, ok := standingPin(l, want[i]); ok {
				out[i].Entry = e
				continue
			}
			todo = append(todo, i)
		}
		if len(todo) == 0 {
			return false, nil
		}
		items := make([]refreshItem, len(todo))
		for j, i := range todo {
			items[j] = refreshItem{idx: j, addr: addrs[i]}
		}
		outcomes := s.refreshItems(items, waiting)
		changed := false
		for j, i := range todo {
			o := outcomes[j]
			if o.Err != nil || o.Commit == "" {
				out[i].Err = s.forkResolveError(addrs[i], o)
				continue
			}
			e := ForkLockEntry{Key: want[i].Key, Source: want[i].Source, Ref: o.Ref, Commit: o.Commit}
			l.Set(e)
			changed = true
			out[i].Entry, out[i].Pinned = e, true
			if o.FetchErr != nil {
				out[i].FetchErr = errors.New(oneLine(o.FetchErr))
			}
		}
		return changed, nil
	})
	if err != nil {
		// Nothing this call resolved is a pin until the lock records it: a commit no file names would
		// be resolved again by the next launch, which may find the branch somewhere else.
		for _, i := range unpinned {
			if out[i].Pinned || out[i].Entry.Commit == "" && out[i].Err == nil {
				out[i].Entry, out[i].Pinned, out[i].FetchErr = ForkLockEntry{}, false, nil
				out[i].Err = fmt.Errorf("recording the pin in %s: %s", lockPath, oneLine(err))
			}
		}
	}
	return out
}

// standingPin is l's pin of w when it is one for the source w declares: a pin made for another
// source is no pin, since an edited `source` is a new address.
func standingPin(l *ForkLock, w ForkWant) (ForkLockEntry, bool) {
	e, ok := l.Get(w.Key)
	if !ok || e.Commit == "" || e.Source != w.Source {
		return ForkLockEntry{}, false
	}
	return e, true
}

// forkResolveError is why a fork's source resolved to no commit, on one line. A repository the store
// has never held reads as the fetch that failed, not as the pack refresh's "never been fetched",
// whose next step is a pack's.
func (s *Store) forkResolveError(a Addr, o Outcome) error {
	err := o.Err
	if errors.Is(err, ErrNotFetched) {
		if ferr := s.fetchFailure(a.Repo); ferr != nil {
			err = ferr
		}
	}
	if err == nil {
		err = o.FetchErr
	}
	if err == nil {
		err = fmt.Errorf("%s resolved to no commit", a.Raw)
	}
	return errors.New(oneLine(err))
}

// FetchForkCommit makes commit of a's repository checked out in the store, fetching it by its id
// when the store does not hold it: a fork pin that arrived with the config from another machine
// (FP-D18). It moves no pin and no tag — a commit cannot be re-pointed, and the fetch is the launch
// refresh's, which puts every tag it moved back. A commit already checked out runs no git.
func (s *Store) FetchForkCommit(a Addr, commit string, waiting func(string)) error {
	if a.IsLocal() || commit == "" {
		return fmt.Errorf("%s names no commit to fetch", a.Raw)
	}
	if _, err := os.Stat(filepath.Join(s.treeDir(a, commit), treeCompleteMarker)); err == nil {
		return nil
	}
	at := a
	at.Ref = commit
	o := s.refreshItems([]refreshItem{{idx: 0, addr: at}}, waiting)[0]
	if o.Err != nil || o.Commit == "" {
		return s.forkResolveError(at, o)
	}
	return nil
}

// refreshItems is the launch's ref rule (refreshGrouped, never forced) for items, recorded in no
// lockfile. Each item's idx indexes the returned outcomes.
func (s *Store) refreshItems(items []refreshItem, waiting func(string)) []Outcome {
	outcomes := make([]Outcome, len(items))
	for _, it := range items {
		outcomes[it.idx].Ref = it.addr.Ref
	}
	s.refreshGrouped(items, outcomes, false, time.Now(), BranchRefreshInterval, waiting)
	return outcomes
}
