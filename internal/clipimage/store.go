package clipimage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The paste store (design §7.3, PA-D6).
const (
	// DirName is the store's directory under <workspace>/.yolo.
	DirName = "paste"
	// MaxAge and MaxFiles are the retention: a file older than a week goes, and beyond 200
	// files the oldest go first.
	MaxAge   = 7 * 24 * time.Hour
	MaxFiles = 200
)

const nameTimeLayout = "20060102T150405Z"

var (
	storedName = regexp.MustCompile(`^(\d{8}T\d{6}Z)-[0-9a-f]{8}\.png$`)
	tempName   = regexp.MustCompile(`^\.(\d{8}T\d{6}Z)-[0-9a-f]{8}\.png\.tmp$`)
)

// StoreDir is the store's host path for workspace.
func StoreDir(workspace string) string {
	return filepath.Join(paths.WorkspaceStateDir(workspace), DirName)
}

// InJailPath is the path a program in the jail reads name at: jailWorkspace is the workspace's
// mount destination there (/workspace in a container; the host path on macos-user and on the
// host), which the caller knows and this package never derives.
func InJailPath(jailWorkspace, name string) string {
	return path.Join(jailWorkspace, ".yolo", DirName, name)
}

// Save writes data into workspace's store under a fresh name, by a temporary name and a rename
// so a reader never sees a partial file, and returns the name. It makes .yolo through the
// creation chokepoint, and refuses a link at .yolo or at the store (the jail can put one there).
// The file's mode is what the user's umask gives a new file, as for any file they create in
// the workspace.
func Save(workspace string, data []byte, now time.Time, rnd io.Reader) (string, error) {
	dir, err := paths.EnsureWorkspaceStateDir(workspace)
	if err != nil {
		return "", err
	}
	yr, err := paths.OpenStateDirRoot(dir)
	if err != nil {
		return "", err
	}
	defer yr.Close()
	if err := yr.Mkdir(DirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	store, err := paths.OpenStateSubdirRoot(yr, DirName, filepath.Join(dir, DirName))
	if err != nil {
		return "", err
	}
	defer store.Close()

	var suffix [4]byte
	if _, err := io.ReadFull(rnd, suffix[:]); err != nil {
		return "", fmt.Errorf("naming the file: %w", err)
	}
	name := now.UTC().Format(nameTimeLayout) + "-" + hex.EncodeToString(suffix[:]) + ".png"
	tmp := "." + name + ".tmp"
	f, err := store.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = store.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = store.Remove(tmp)
		return "", err
	}
	if err := store.Rename(tmp, name); err != nil {
		_ = store.Remove(tmp)
		return "", err
	}
	return name, nil
}

// Sweep applies the retention to workspace's store and reports how many files it removed. It
// creates nothing, and touches only names the store writes: a file older than MaxAge (by the
// time in its name) goes, an abandoned temporary of the same age goes, and beyond MaxFiles the
// oldest images go first. A missing store is not an error.
func Sweep(workspace string, now time.Time) (int, error) {
	store, err := paths.OpenWorkspaceStateSubdir(workspace, DirName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer store.Close()
	d, err := store.Open(".")
	if err != nil {
		return 0, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-MaxAge)
	removed := 0
	remove := func(n string) {
		if store.Remove(n) == nil {
			removed++
		}
	}
	var kept []string
	for _, n := range names {
		m := storedName.FindStringSubmatch(n)
		isTemp := false
		if m == nil {
			if m = tempName.FindStringSubmatch(n); m == nil {
				continue
			}
			isTemp = true
		}
		t, err := time.Parse(nameTimeLayout, m[1])
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			remove(n)
			continue
		}
		if !isTemp {
			kept = append(kept, n)
		}
	}
	if len(kept) > MaxFiles {
		sort.Strings(kept) // the name leads with its UTC time, so this is oldest first
		for _, n := range kept[:len(kept)-MaxFiles] {
			remove(n)
		}
	}
	return removed, nil
}
