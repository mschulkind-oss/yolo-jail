package hostservice

// statedir.go makes a long-lived host daemon EXIT when the state directory it was started
// against goes away, instead of carrying on and recreating it.
//
// # Why a daemon can outlive its state dir at all
//
// A host-wide daemon (`host_daemon.scope: "host"`, internal/broker) is spawned detached and
// reused by every later launch that finds it alive, keyed by its loophole NAME alone. Its
// state dir is not: it is a path under the HOME of whichever launch spawned it
// (loopholes.StateDirFor). Two things remove that dir while the daemon keeps running:
//
//   - RETIREMENT. A launch whose `packs` no longer selects the loophole's pack archives the
//     dir into `state/.retired/<stamp>/` (internal/packstage.RetireLoopholeState). Nothing
//     stops the daemon, by ruling — selection controls activation, not revocation
//     (internal/cli/run/loopholeretire.go). Observed in the integration suite on 2026-09-28:
//     an openai-auth-broker still serving, its log descriptor pointing into
//     `state/.retired/...`, after a later launch that did not select it retired its dir.
//   - A DELETED HOME. The integration suite spawns these daemons under temp HOMEs that
//     t.TempDir removes, and found them still alive afterwards.
//
// Carrying on is wrong in both, and in the same two ways. Every write the daemon makes goes
// through a MkdirAll of the dir, so the first one after the removal RECREATES it: a
// credential file reappears in a dir the launch just archived, or a temp dir reappears
// under t.TempDir's RemoveAll and fails it with "directory not empty". And a later launch
// that selects the pack again ADOPTS the live daemon rather than starting one, so whatever
// the daemon made once at startup and the retirement archived — the Claude broker's CA and
// leaf, minted only at spawn (internal/oauthbroker.EnsureCAAndLeaf) — is never made again.
//
// Exiting fixes both: the next launch that needs the daemon finds it dead and spawns a fresh
// one against the state dir that exists now.
//
// # What counts as gone
//
// The directory's IDENTITY is taken once, when the watch starts, and compared on every
// poll: absent (ENOENT, or a path component that is no longer a directory) or a different
// directory at the same path (a retirement followed by a fresh mkdir) both end the daemon.
// Any other stat error — a permission change, a transient I/O fault — is not evidence the
// dir is gone, and the daemon keeps serving.
//
// Polling rather than inotify: this runs on Linux and darwin, the dir is one path, and the
// cost is one stat per interval per daemon.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

// StateDirPollInterval is how often WatchStateDir looks at the directory. A variable so a
// package's tests can shorten it for an in-process daemon; production never writes it.
var StateDirPollInterval = 2 * time.Second

// WatchStateDir calls gone ONCE, from its own goroutine, when dir is removed or replaced
// after this call, and returns without calling it when stop closes first. gone receives a
// one-line reason naming the dir, for the daemon's log.
//
// The error is for a dir that cannot be stat'ed NOW: the caller creates its state dir
// before watching it, so a failure here means there is nothing to watch, and the caller
// decides whether that is fatal.
func WatchStateDir(dir string, stop <-chan struct{}, gone func(reason string)) error {
	start, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !start.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	interval := StateDirPollInterval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			if reason := stateDirGone(dir, start); reason != "" {
				gone(reason)
				return
			}
		}
	}()
	return nil
}

// stateDirGone is WatchStateDir's predicate: a reason when dir is no longer the directory
// start described, "" while it still is or when the answer is unknown.
func stateDirGone(dir string, start fs.FileInfo) string {
	now, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return "state directory " + dir + " was removed"
	case err != nil:
		return ""
	case !os.SameFile(start, now):
		return "state directory " + dir + " was replaced by a different directory"
	}
	return ""
}

// StateDirGoneExitLine is what a daemon logs as it shuts down for WatchStateDir's reason,
// one spelling for all of them so a log reader greps one sentence.
func StateDirGoneExitLine(daemon, reason string) string {
	return daemon + ": " + reason + "; exiting rather than recreating it — the next launch " +
		"that needs this daemon starts a fresh one"
}
