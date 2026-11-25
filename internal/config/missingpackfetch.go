package config

// missingpackfetch.go is the fetch that makes a missing pack stop being missing. A broker's
// authority read that meets a selected pack the pack store lacks fetches it IN LINE, the fetch a
// launch makes, and reads the pack; the missing state normally just disappears. Only when that
// fetch fails is the pack skipped for approval authority (missingpack.go), with the fetch's own
// error in the note. A launch is untouched: it fetches before it resolves and still refuses a
// pack it cannot fetch.
//
// Owner, 2026-11-25: "auto pack fetch seems safe? so we can eliminate that state, then just fetch
// when we need it, and only if that fetch fails, then we fall back to old approval postures, but
// at jail launch, it's still a fatal error. just the approval sneaks through because it's a
// background thing". So there is no attempt record and no retry timer: concurrent evaluations of
// one pack share ONE fetch, and each later evaluation that still finds it missing tries again.
//
// WHY THE READ PATH ITSELF STILL NEVER FETCHES (packresolve.go): `yolo check`, the agent footer
// and config validation run on every refresh and must write nothing, and a jail has no pack store
// and no git credentials. The fetch here is made only by a process that opted in
// (EnableMissingPackFetch: the host broker daemons and `yolo approve`), under the pack store's own
// mirror locking, and never writes `packs.lock.json`: the lock's previous pin is what a launch
// compares against to say "Fetched pack" / "Updated pack" before host code runs, so recording a
// commit behind a launch's back would swallow that disclosure. The mirror and checkout are the
// store's content-addressed cache, which a launch would fill the same way.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// missingFetchGuard bounds a hung fetch. The launch's own per-mirror budget is
// packsrc.LaunchFetchTimeout; this only keeps a wedged transport from holding an approval check
// forever.
const missingFetchGuard = 5 * time.Minute

var (
	missingFetchMu  sync.Mutex
	missingFetchLog io.Writer
	missingFetching = map[string]*missingFetchCall{}
	// missingFetchRun is the fetch itself; tests replace it.
	missingFetchRun = fetchMissingPack
	// missingFetchJoined is told when an evaluation joins a fetch already under way; tests wait on it.
	missingFetchJoined = func(PackEntry) {}
)

// missingFetchCall is one fetch under way, shared by every evaluation that wants the same pack.
type missingFetchCall struct {
	done chan struct{}
	err  error
}

// EnableMissingPackFetch lets this process fetch a pack its broker-authority reads find missing,
// logging each fetch to log. Only a host process that evaluates authority calls it; a process
// that never does (a test, `yolo check`, a launch, the jail) fetches nothing.
func EnableMissingPackFetch(log io.Writer) {
	missingFetchMu.Lock()
	defer missingFetchMu.Unlock()
	missingFetchLog = log
}

// MissingPackFetchEnabled reports whether EnableMissingPackFetch has been called in this process.
func MissingPackFetchEnabled() bool {
	missingFetchMu.Lock()
	defer missingFetchMu.Unlock()
	return missingFetchLog != nil
}

// fetchMissingPackNow fetches entry and returns when the fetch is over. attempted is false when
// this process does not fetch (not enabled, or in a jail), so the caller treats the pack as
// missing without a fetch error. Concurrent calls for one pack share one fetch, and each gets its
// result.
func fetchMissingPackNow(entry PackEntry) (attempted bool, err error) {
	if InJail() {
		return false, nil
	}
	missingFetchMu.Lock()
	log := missingFetchLog
	if log == nil {
		missingFetchMu.Unlock()
		return false, nil
	}
	key := entry.Name + "\x00" + entry.Source + "\x00" + paths.PacksDir()
	if call, running := missingFetching[key]; running {
		missingFetchMu.Unlock()
		missingFetchJoined(entry)
		<-call.done
		return true, call.err
	}
	call := &missingFetchCall{done: make(chan struct{})}
	missingFetching[key] = call
	run := missingFetchRun
	missingFetchMu.Unlock()

	fmt.Fprintf(log, "pack-fetch: selected pack %s is not in the pack store; fetching it\n", entry.Name)
	call.err = run(entry)
	missingFetchMu.Lock()
	delete(missingFetching, key)
	missingFetchMu.Unlock()
	close(call.done)
	if call.err != nil {
		fmt.Fprintf(log, "pack-fetch: could not fetch selected pack %s: %s; its authority is skipped until it can be fetched (`yolo pack install` fetches it from the host)\n", entry.Name, oneLineErr(call.err))
	} else {
		fmt.Fprintf(log, "pack-fetch: fetched selected pack %s\n", entry.Name)
	}
	return true, call.err
}

// fetchMissingPack is the launch's fetch for one entry (packsrc.Store.Refresh through the launch
// store), without recording the lock file. An omitted-ref entry is fetched at its lock pin when
// it has one; one with none is left to the launch, which pins it and says so.
func fetchMissingPack(entry PackEntry) error {
	spec := entry.RefreshSpec(false)
	options := packsrc.RefreshOptions{}
	if entry.AllowDefaultRef {
		addr, err := entry.PackAddress()
		if err != nil {
			return err
		}
		if addr.Ref == "" {
			lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
			if err != nil {
				return err
			}
			if pin, ok := lock.Get(entry.Name); !ok || pin.Source != entry.Source || pin.Commit == "" {
				return fmt.Errorf("it has no recorded pin yet; the next host launch pins and fetches it")
			}
			// The pin is read from the lock and, unchanged by a fetch of that very commit, not rewritten.
			options.LockPath = packsrc.LockPath(paths.UserConfigPath())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), missingFetchGuard)
	defer cancel()
	store := packsrc.LaunchStore(paths.PacksDir())
	store.Ctx = ctx
	outcomes, err := store.Refresh([]packsrc.RefreshPack{spec}, options)
	if err != nil {
		return err
	}
	if len(outcomes) == 1 && outcomes[0].Err != nil {
		return outcomes[0].Err
	}
	return nil
}

func oneLineErr(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}
