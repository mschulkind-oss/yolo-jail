package modelmenu

// shared.go is the HOST's menu store (docs/design/model-lists-and-pickers.md MM-D27): a directory
// many launches of one program share, where each menu is named by its cache key, never
// overwritten, and removed only once no running program can read it.
//
// WHY NOT THE JAIL'S ONE PATH. The program reads its menu again after it starts (codex 0.159.2
// re-applies its launch's `-c` overrides at every thread start, SOURCED in §14.7), so a menu file
// must outlive the program's startup, and at the host two launches of one program can hold
// different lists at once: one with a company's `only` and one without, or one on a provider with
// no list at all. A fixed path would let one launch replace or remove the menu another running
// program still reads. In a jail every launch reads the list its boot rendered, so there one path
// serves them all, and Run keeps it.
//
// COLLECTED BY LIVENESS, NEVER BY AGE, with the exclusive-then-shared pattern by which the managed
// CODEX_HOME's live-launch lock already tells a launch it is alone (openaiauthhost's
// sharedCallerToken). Every launch that names a menu holds LiveLockFile SHARED for its program's
// life; one that writes a new menu first removes the others, only when it can take LiveLockFile
// EXCLUSIVELY, that is when no program holding a menu of this directory runs. A lock it cannot take
// removes nothing. decideLockFile serializes the decision, so no launch removes a menu between
// another's check that it exists and that launch's shared lock.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// The two lock files of a shared menu directory. Dot-named, so no cache key (hex) can collide.
// LiveLockFile is exported for the one reader outside this package, the host launch's test that
// the program it execs holds the lock.
const (
	decideLockFile = ".decide.lock"
	LiveLockFile   = ".live.lock"
)

// Held is a menu a launch hands its program, with the shared lock that keeps it from being
// collected while that program runs. Close releases it; the caller that execs the program calls
// KeepAcrossExec instead, so the program itself holds the lock for its life.
type Held struct {
	// Path is the menu's absolute path.
	Path string
	// Flag is the declared flag with Path in it: the argv words the caller adds.
	Flag []string
	lock *os.File
}

// Close releases the menu's lock. Safe on a nil Held and more than once.
func (h *Held) Close() error {
	if h == nil || h.lock == nil {
		return nil
	}
	err := h.lock.Close()
	h.lock = nil
	return err
}

// KeepAcrossExec clears close-on-exec on the lock's descriptor, so a program this process execs
// inherits the descriptor and with it the shared lock, which the kernel then holds until the last
// process holding the descriptor exits. Go opens every file close-on-exec, so without this the
// lock would end at the exec, and the next launch could remove the menu the program reads.
// Called only on the exec path: a program started as a child (os/exec) would inherit it too, and
// there the resident parent holds the lock itself.
func (h *Held) KeepAcrossExec() error {
	if h == nil || h.lock == nil {
		return nil
	}
	_, err := unix.FcntlInt(h.lock.Fd(), unix.F_SETFD, 0)
	return err
}

// WriteIn is the build (Request.write) into dir, the program's shared menu directory: the menu is
// <key>.json, the key file beside it, and the returned Held carries the shared lock the launch
// keeps for its program's life. nil when there is no menu to name, for any reason the build
// warns about, or when the directory or its locks cannot be had, which it warns about too: a menu
// is never a reason to refuse a launch.
func (r Request) WriteIn(dir string, stderr io.Writer) *Held {
	if len(r.List) == 0 || len(r.Program) == 0 {
		return nil
	}
	fail := func(err error) *Held {
		fmt.Fprintf(stderr, "%scould not keep %s's model menu in %s (%v), so %s shows its own model menu.\n",
			r.prefix(), r.Bin, dir, err, r.Bin)
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	decide, err := os.OpenFile(filepath.Join(dir, decideLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fail(err)
	}
	defer decide.Close()
	if err := syscall.Flock(int(decide.Fd()), syscall.LOCK_EX); err != nil {
		return fail(err)
	}
	live, err := os.OpenFile(filepath.Join(dir, LiveLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fail(err)
	}
	into := filepath.Join(dir, r.Key()+".json")
	flag, wrote := r.write(into, stderr)
	if flag == nil {
		_ = live.Close()
		return nil
	}
	if wrote {
		switch err := syscall.Flock(int(live.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
		case err == nil:
			// No program holding a menu of this directory runs: the others go.
			removeOthers(dir, into)
		case errors.Is(err, syscall.EWOULDBLOCK):
			// Another launch's program still reads its menu; every menu stays until a writer
			// finds none running.
		default:
			_ = live.Close()
			return fail(err)
		}
	}
	// Converting an exclusive lock to shared is not atomic, which is why decide is still held.
	if err := syscall.Flock(int(live.Fd()), syscall.LOCK_SH); err != nil {
		_ = live.Close()
		return fail(err)
	}
	return &Held{Path: into, Flag: flag, lock: live}
}

// removeOthers removes every entry of dir but the two lock files and keep's menu and key file:
// the menus of launches that have all exited, their key files, and any temporary file a killed
// build left, which under the decide lock no build is still writing.
func removeOthers(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	mine := map[string]bool{decideLockFile: true, LiveLockFile: true,
		filepath.Base(keep): true, filepath.Base(keep) + ".key": true}
	for _, e := range entries {
		name := e.Name()
		if mine[name] || e.IsDir() || !isMenuFile(name) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// isMenuFile reports whether name is one this store writes: a menu, a key file, or a temporary
// file of either (writeAtomic's "."+base+".*").
func isMenuFile(name string) bool {
	base := strings.TrimPrefix(name, ".")
	return strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".json.key") ||
		strings.Contains(base, ".json.")
}
