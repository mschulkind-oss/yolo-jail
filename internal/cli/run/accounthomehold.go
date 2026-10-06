package run

// accounthomehold.go is the macos-user ACCOUNT-HOME HOLD — a term coined here: a session's claim on
// the sandbox account's home for as long as it runs, which a launch of ANOTHER workspace finds and is
// refused by (docs/reference/macos-user-home-tiers.md#ht-d15).
//
// WHY THERE HAS TO BE ONE. Every macos-user session runs as the one account, in the one home
// (/Users/_yolojail), and that home holds ONE set of links into one workspace's sidecar — the agent
// state, `.config`, `.local`, the npm prefix, `go` and the generated `.yolo/bin` all resolve through
// it. A second workspace's bootstrap repoints those links: measured on Linux by laying workspace B's
// layout after A's with disjoint pack sets, B took five of A's core links (`.npm-global`, `.local`,
// `go`, `.yolo/bin`, `.config`) whatever packs either selected
// (TestASecondWorkspaceLayoutRepointsTheFirstsLinks). The session still running in A then reads B's
// skills, configs and shims, and its own `~/.claude` names a directory its profile denies. The
// per-workspace launch lock never covered it: it is keyed per workspace and let go before the agent.
//
// WHAT IT DOES, IN OQ-JL7'S DIRECTION (refuse an arrival that cannot be served safely): it refuses
// the second workspace's launch, naming the live one and the next step. Concurrent sessions of
// different workspaces are a question for the maintainer, not this file; until that is decided, a
// launch that would corrupt a live session's home does not start.
//
// THE LOCKS, host-side under <global storage>/locks/macos-user-home, which no sandbox can write:
//
//   - `<cname>.lock`, one per workspace, held LOCK_SH by host yolo for the session: shared, so two
//     launches of ONE workspace both hold it (they lay identical links, OQ-HT3);
//   - `<cname>.workspace`, beside it, the workspace's path, so a refusal can name it;
//   - `.mutex`, held LOCK_EX for the few milliseconds a launch probes the others and takes its own,
//     so two arrivals cannot both find the home free.
//
// A probe takes LOCK_EX|LOCK_NB on another workspace's file through a fresh descriptor: success says
// its holder is gone, EWOULDBLOCK says it is live, and any other answer — a file that cannot be opened
// or locked — is UNKNOWN and counts as live (JL-P3: "could not count" is never zero), the refusal
// naming the file. No lock file is ever unlinked or renamed over (JL-D28): a probe that unlinked a
// free file could race a holder that had just opened it.
//
// KNOWN LIMITS. A SIGKILLed host yolo drops its hold while the session it started may run on: the
// sandbox runs as another uid, so nothing of the session can hold a host lock for it. And the hold
// is the invoking macOS user's, under that user's own state dir, as the session records are
// (macosuser's sessionfiles.go): a second macOS user launching on the same Mac is not seen.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// accountHomeHoldLeaf is the hold's directory under <global storage>/locks.
const accountHomeHoldLeaf = "macos-user-home"

// accountHomeMutexName is the probe's mutex in that directory.
const accountHomeMutexName = ".mutex"

// accountHomeMutexBound is how long an arrival waits for another's probe to finish. A probe holds
// the mutex for milliseconds, so a wait this long means something holds it that is not probing.
const accountHomeMutexBound = 10 * time.Second

// accountHomeHoldDir is the directory of the hold's files.
func accountHomeHoldDir() string {
	return filepath.Join(paths.GlobalStorage(), "locks", accountHomeHoldLeaf)
}

// HoldAccountHome takes workspace's hold on the sandbox account's home, cname its container name,
// for one session (macosuser.Deps.HoldAccountHome). It admits, returning the release (idempotent),
// when no other workspace's session holds it, and refuses otherwise, the refusal naming the live
// workspace and the next steps, the container-runtime one ending with step (the clause this
// launch's notch needs). There is no override: a launch admitted here would repoint a live
// session's home.
func HoldAccountHome(workspace, cname, step string) (release func(), refusal string) {
	return holdAccountHomeIn(accountHomeHoldDir(), workspace, cname, step, accountHomeMutexBound)
}

// holdAccountHomeIn is HoldAccountHome over dir and a mutex bound, for a test.
func holdAccountHomeIn(dir, workspace, cname, step string, bound time.Duration) (func(), string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, accountHomeNoDir(dir, err, step)
	}
	mutexPath := filepath.Join(dir, accountHomeMutexName)
	mutex, err := lockBounded(mutexPath, bound)
	if err != nil {
		return nil, fmt.Sprintf("could not take %s, which keeps two launches from claiming the "+
			"sandbox account's home at once: %v. Another launch may be stuck while claiming it; if "+
			"no other yolo is starting a macos-user launch, run `yolo` again.", mutexPath, err)
	}
	defer unlockClose(mutex)

	if r := probeAccountHome(dir, cname, step); r != "" {
		return nil, r
	}
	ownPath := filepath.Join(dir, cname+".lock")
	own, err := os.OpenFile(ownPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, accountHomeUnknown(ownPath, err, step)
	}
	// Non-blocking, and it cannot be refused by contention: only a prober takes these exclusively,
	// and every prober holds the mutex this launch holds.
	if err := flockSyscall(int(own.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		_ = own.Close()
		return nil, accountHomeUnknown(ownPath, err, step)
	}
	// The record a refusal names this workspace by; a failed write costs only that name.
	_ = os.WriteFile(filepath.Join(dir, cname+".workspace"), []byte(workspace+"\n"), 0o644)
	held := &workspaceLock{f: own}
	return held.Close, ""
}

// probeAccountHome asks every other workspace's lock file in dir whether its session is live, and
// returns the refusal for the first that is, or "" when none is.
func probeAccountHome(dir, cname, step string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return accountHomeUnknown(dir, err, step)
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".lock") && n != cname+".lock" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		path := filepath.Join(dir, n)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return accountHomeUnknown(path, err, step)
		}
		err = flockSyscall(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			unlockClose(f) // its holder is gone
			continue
		}
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			other := strings.TrimSuffix(n, ".lock")
			return accountHomeLive(readAccountHomeWorkspace(dir, other), step)
		}
		return accountHomeUnknown(path, err, step)
	}
	return ""
}

// readAccountHomeWorkspace is the workspace the record beside cname's lock names, or a phrase
// saying it is unknown.
func readAccountHomeWorkspace(dir, cname string) string {
	b, err := os.ReadFile(filepath.Join(dir, cname+".workspace"))
	if ws := strings.TrimSpace(string(b)); err == nil && ws != "" {
		return ws
	}
	return "another workspace (its record, " + filepath.Join(dir, cname+".workspace") + ", is unreadable)"
}

// accountHomeLive is the refusal for a live session of the workspace other.
func accountHomeLive(other, step string) string {
	return fmt.Sprintf("a macos-user session of %s is running, and every macos-user session shares "+
		"one sandbox account, whose home holds one workspace's links at a time — this launch would "+
		"repoint them under that session. Quit that session and run `yolo` here again; or, to run "+
		"both at once, set \"runtime\": \"container\" in this project's yolo-jail.jsonc%s.", other, step)
}

// accountHomeUnknown is the refusal for a hold file whose answer is unknown, which counts as live.
func accountHomeUnknown(path string, err error, step string) string {
	return fmt.Sprintf("could not tell whether another workspace's macos-user session is running "+
		"(%s: %v), and an unknown answer counts as one. If no macos-user session is running, remove "+
		"%s and run `yolo` again; or set \"runtime\": \"container\" in this project's "+
		"yolo-jail.jsonc%s.", path, err, path, step)
}

// accountHomeNoDir is the refusal for a hold directory that cannot be made. No lock file can exist
// in it, so its next step is the directory's own.
func accountHomeNoDir(dir string, err error, step string) string {
	return fmt.Sprintf("could not make %s, where each macos-user launch records its claim on the "+
		"sandbox account's home (%v), so this launch cannot tell whether another workspace's session "+
		"holds it. Make it a directory you can write — remove the file in its place, or make its "+
		"parent writable by you — and run `yolo` again; or set \"runtime\": \"container\" in this "+
		"project's yolo-jail.jsonc%s.", dir, err, step)
}

// lockBounded opens path and takes LOCK_EX on it, waiting at most bound.
func lockBounded(path string, bound time.Duration) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(bound)
	for {
		err := flockSyscall(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("still held after %s", bound)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// unlockClose releases f's flock explicitly, then closes it: a child forked meanwhile holds a
// duplicate until its exec, and a close alone would leave the lock held through it (workspaceLock's
// Close says why).
func unlockClose(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
