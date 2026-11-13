// Package helperpin pins the host yolo binary a launch starts its helpers from, and formats the
// line each helper logs when it starts.
//
// A launch resolves the bundle root once (symlinks followed) and records the identity of
// <root>/bin/<GOOS>-<GOARCH>/yolo. A later `just install` swaps the bundle generation and replaces
// that file; Unchanged is how a long-lived caller notices, by a stat and never a hash.
package helperpin

import (
	"encoding/json"
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
	return ResolveForBuild(bundleRoot, goos, goarch, version.Baked(), version.GitCommit)
}

// bundleStamp is the part of the bundle's yolo-build-stamp.json a pin is checked against: the raw
// version and commit its binaries were linked with (scripts/stage-source-bundle.sh).
type bundleStamp struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// ResolveForBuild is ResolveFor for an explicit launcher build: wantVersion is the launcher's
// normalized stamped version (version.Baked) and wantCommit its stamped commit (PH-D1, docs/reference/pinned-host-helpers.md).
// The pin is accepted only from a bundle whose stamp names that version and commit. A field either
// side does not carry (empty or "unknown": an unstamped launcher, an older bundle) is not compared,
// so it never refuses on that field alone.
func ResolveForBuild(bundleRoot, goos, goarch, wantVersion, wantCommit string) (Pin, error) {
	root, err := filepath.EvalSymlinks(bundleRoot)
	if err != nil {
		return Pin{}, fmt.Errorf("cannot resolve the bundle root %s: %w. %s", bundleRoot, err, reporoot.ReinstallSteps)
	}
	if err := checkBuild(root, wantVersion, wantCommit); err != nil {
		return Pin{}, err
	}
	path := filepath.Join(root, "bin", goos+"-"+goarch, "yolo")
	// Every symlink resolved, the file itself too: Path and Unchanged name the real file.
	if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil {
		path = resolved
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Pin{}, fmt.Errorf("the bundle has no host yolo at %s: %w. %s", path, err, reporoot.ReinstallSteps)
	}
	pin := Pin{Path: path, Size: fi.Size(), MtimeNanos: fi.ModTime().UnixNano()}
	pin.Dev, pin.Ino = devIno(fi)
	return pin, nil
}

// checkBuild refuses a bundle whose stamp names another build than the launcher's.
func checkBuild(root, wantVersion, wantCommit string) error {
	data, err := os.ReadFile(filepath.Join(root, "yolo-build-stamp.json"))
	if err != nil {
		return nil // an older bundle: reporoot already validated what it requires
	}
	var st bundleStamp
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	haveVersion := ""
	if known(st.Version) {
		haveVersion = version.Normalize(st.Version)
	}
	versionDiffers := known(wantVersion) && haveVersion != "" && haveVersion != wantVersion
	commitDiffers := known(wantCommit) && known(st.Commit) && st.Commit != wantCommit
	if !versionDiffers && !commitDiffers {
		return nil
	}
	return fmt.Errorf("the bundle at %s is yolo %s (commit %s) and this launcher is yolo %s (commit %s), "+
		"so its yolo would not be this launcher's build. %s",
		root, orUnknown(haveVersion), orUnknown(st.Commit), orUnknown(wantVersion), orUnknown(wantCommit),
		reporoot.ReinstallSteps)
}

func known(s string) bool { return s != "" && s != "unknown" }

func orUnknown(s string) string {
	if !known(s) {
		return "unknown"
	}
	return s
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
