// Package helperpin pins the host yolo binary a launch starts its helpers from, and formats the
// line each helper logs when it starts.
//
// A launch resolves the bundle root once (symlinks followed) and records the identity of
// <root>/bin/<GOOS>-<GOARCH>/yolo. A later `just install` swaps the bundle generation and replaces
// that file; Unchanged is how a long-lived caller notices, by a stat and never a hash.
package helperpin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/logstamp"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// EnvJail names the jail (the container name) a helper serves, for the start line.
const EnvJail = "YOLO_HELPER_JAIL"

// Pin is the host yolo a launch pinned and the identity it had when pinned. The fields are
// exported and tagged so a pin round-trips through a plan file.
type Pin struct {
	Path       string `json:"path"`
	Dev        uint64 `json:"dev"`
	Ino        uint64 `json:"ino"`
	Size       int64  `json:"size"`
	MtimeNanos int64  `json:"mtime_ns"`
}

// Resolve pins the host yolo of the bundle at bundleRoot for the running platform.
func Resolve(bundleRoot string) (Pin, error) {
	return ResolveFor(bundleRoot, runtime.GOOS, runtime.GOARCH)
}

// ResolveFor is Resolve for an explicit platform.
func ResolveFor(bundleRoot, goos, goarch string) (Pin, error) {
	root, err := filepath.EvalSymlinks(bundleRoot)
	if err != nil {
		return Pin{}, fmt.Errorf("cannot resolve the bundle root %s: %w. %s", bundleRoot, err, reporoot.ReinstallSteps)
	}
	path := filepath.Join(root, "bin", goos+"-"+goarch, "yolo")
	fi, err := os.Stat(path)
	if err != nil {
		return Pin{}, fmt.Errorf("the bundle has no host yolo at %s: %w. %s", path, err, reporoot.ReinstallSteps)
	}
	pin := Pin{Path: path, Size: fi.Size(), MtimeNanos: fi.ModTime().UnixNano()}
	pin.Dev, pin.Ino = devIno(fi)
	return pin, nil
}

// Unchanged reports whether the pinned file is still the one recorded: a stat, never a hash.
// A missing file, or a differing device, inode, size or mtime, reads as changed.
func (p Pin) Unchanged() bool {
	fi, err := os.Stat(p.Path)
	if err != nil {
		return false
	}
	dev, ino := devIno(fi)
	return dev == p.Dev && ino == p.Ino && fi.Size() == p.Size && fi.ModTime().UnixNano() == p.MtimeNanos
}

// StartLine is the line a helper logs when it starts, built from this process.
func StartLine(now time.Time, name string) string {
	exe, err := version.ExecutablePath()
	build := version.Baked()
	if build == "" {
		build = "unstamped"
	}
	return formatStartLine(now, name, build, version.CommitLabel(), os.Getpid(), os.Getenv(EnvJail), exe, err)
}

func formatStartLine(now time.Time, name, build, commit string, pid int, jail, exe string, exeErr error) string {
	if jail == "" {
		jail = "-"
	}
	if exeErr != nil || exe == "" {
		exe = "unknown"
	}
	return logstamp.Format(now) + " helper start: name=" + quote(name) + " build=" + quote(build) +
		" commit=" + quote(commit) + " pid=" + strconv.Itoa(pid) + " jail=" + quote(jail) + " exe=" + quote(exe)
}

// quote Go-quotes a value that holds whitespace or a quote, so a line splits on spaces.
func quote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\r\n\"") {
		return strconv.Quote(s)
	}
	return s
}
