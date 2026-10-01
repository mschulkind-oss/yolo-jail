// Package logcap bounds a diagnostics log that ANOTHER PROCESS writes: the stdout and stderr
// yolo hands a host daemon or a socat forward at spawn (host-service-<name>.log,
// <cname>-socat.log, under GLOBAL_STORAGE/logs). Each was opened O_APPEND with no bound, so it
// grew for as long as the machine kept launching (docs/research/central-yolo-watcher.md §2.6,
// item 3).
//
// # ONE ARCHIVED GENERATION, AS crossings.log HAS
//
// The bound is the one internal/crossaudit gives crossings.log: past MaxBytes the log moves to
// exactly one archived generation, <log>.1, replacing whatever was there, and starts again
// empty. So the archive is never more than MaxBytes, with no reaper to write and nothing for
// `yolo prune` to learn.
//
// # COPY AND TRUNCATE, NOT RENAME, because the writer is not this process
//
// crossaudit rotates by rename, at write time, because it is the writer. Here the writer is a
// child that holds its own descriptor for its whole life, and several can hold one log at once:
// a host-wide daemon outlives the launch that spawned it, and a per-jail daemon's log is keyed
// on the loophole's name, so every jail running that loophole appends to the same file. A
// rename would leave each of them writing into the archive, and the next rename would unlink
// that inode while they still write to it, so the bytes would go on growing in a file nobody
// can see. Trim instead copies the newest MaxBytes into the archive and truncates the log IN
// PLACE: the inode every holder writes to stays the log, and because every holder opened it
// O_APPEND, its next write lands at the new end rather than leaving a hole. The cost is that a
// line written between the copy and the truncate is in neither file.
//
// # WHEN IT RUNS
//
// At every open (Open), which is every spawn, and wherever a launch reuses a live writer
// (broker.EnsureSingleton calls Trim for a host-wide daemon it does not respawn). So the log
// holds at most MaxBytes plus what its writers wrote since the last launch that touched it.
// Nothing trims it while no yolo command runs, so a daemon that writes heavily between two
// launches still grows its log past the cap until the next one; the archive never passes it.
//
// # NEVER A DEPENDENCY
//
// A log is diagnostics. A failed trim leaves the file as it was, and Open opens it anyway: the
// daemon still starts, with the log it would have had before this package existed.
package logcap

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

const (
	// MaxBytes is the most a log holds before Trim moves it to the archive, and the most the
	// archive ever holds. crossings.log's cap (crossaudit.MaxBytes), for the same reason: this
	// machine's jail store and jail home share one block device, so an unbounded log is a
	// disk-exhaustion bug rather than a tidiness question.
	MaxBytes = 4 << 20

	// ArchiveSuffix names the one archived generation, beside the log.
	ArchiveSuffix = ".1"
)

// Open opens path for a child's output — O_APPEND, created at perm if absent — after trimming
// it. The trim's own failure is not returned: it leaves the log as it was, and the open below
// still reports whether there is a log at all.
func Open(path string, perm os.FileMode) (*os.File, error) {
	_ = Trim(path)
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
}

// Trim bounds the log at path: when it holds more than MaxBytes, its newest MaxBytes, from the
// first whole line, replace path+ArchiveSuffix and path is truncated to empty in place. A log
// at or under the cap, a missing one and anything not a regular file are left alone.
//
// An exclusive flock on the log serializes trimmers: two launches that find one log over the
// cap would otherwise both copy it, and the second copy, made after the first truncate, would
// replace the archive with a near-empty one. The writers take no lock and are never blocked.
func Trim(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	if over, err := overCap(f); err != nil || !over {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	// Asked again under the lock: another trimmer may have emptied it while this one waited.
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() <= MaxBytes {
		return err
	}
	tail, err := newestLines(f, st.Size())
	if err != nil {
		return err
	}
	if err := writeArchive(path+ArchiveSuffix, tail, st.Mode().Perm()); err != nil {
		return err
	}
	return f.Truncate(0)
}

// overCap reports whether f is a regular file holding more than MaxBytes.
func overCap(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	return st.Mode().IsRegular() && st.Size() > MaxBytes, nil
}

// newestLines is the last MaxBytes of f's first size bytes, starting at a line: the byte before
// the window is read too, so a window that already starts a line keeps it whole, and one that
// starts mid-line drops the partial line rather than archiving half of it.
func newestLines(f *os.File, size int64) ([]byte, error) {
	start := size - MaxBytes - 1
	buf := make([]byte, MaxBytes+1)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		return buf[i+1:], nil
	}
	return buf[1:], nil // one line longer than the cap: keep its newest bytes
}

// writeArchive replaces path with data, atomically: a reader of the archive sees the old
// generation or the new one, never a half-written file.
func writeArchive(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
