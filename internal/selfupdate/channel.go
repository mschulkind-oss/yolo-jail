// Package selfupdate tells a host `yolo` that a newer yolo exists and, when
// asked, installs it through the same channel that installed this one.
//
// # The three pieces
//
//   - DETECT (this file): which channel installed the running binary — a
//     from-source `just install`, Homebrew, `go install`, pipx, `uv tool`, or a
//     release archive. Every channel has its own notion of "newer" and its own
//     upgrade command, so nothing below can be decided before this.
//   - CHECK (check.go, state.go): whether something newer exists. Release
//     channels check in a detached `yolo internal update-check` process and
//     cache the answer for Interval, so no ordinary command waits on the
//     network. Source channels check only on an explicit `yolo update` request,
//     because their checkout may be writable from a jail.
//   - APPLY (apply.go): the complete channel's own upgrade command, run by
//     `yolo update` or by the launch prompt, after which the caller re-execs the
//     new binary. Binary-only channels are checked but refused with coordinated
//     binary-and-bundle instructions.
//
// # What it deliberately does not do
//
// It never runs inside a jail (the in-jail yolo is the host's, mounted
// read-only), never updates without an explicit yes, and never replaces a
// binary itself: a self-contained channel's package manager, or `just deploy`,
// stays the only writer of its own install.
package selfupdate

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

const (
	// Module is the Go module path, which `go install …@latest` names.
	Module = "github.com/mschulkind-oss/yolo-jail"
	// ReleasesPage is where a person downloads a release by hand.
	ReleasesPage = "https://github.com/mschulkind-oss/yolo-jail/releases/latest"
	// LatestReleaseAPI answers the newest published release's tag.
	LatestReleaseAPI = "https://api.github.com/repos/mschulkind-oss/yolo-jail/releases/latest"

	// DisableEnv turns every update check, notice and prompt off when set to any
	// non-empty value. CI is honored the same way (see Enabled).
	DisableEnv = "YOLO_NO_UPDATE_CHECK"
	// ReexecEnv marks the process the launch prompt re-exec'd into after an
	// update, so that process neither prompts again nor loops.
	ReexecEnv = "YOLO_UPDATE_REEXEC"
)

// Kind names an install channel.
type Kind string

const (
	KindSource    Kind = "source"     // `just install` / `just deploy` from a checkout
	KindHomebrew  Kind = "homebrew"   // brew install mschulkind-oss/tap/yolo-jail
	KindGoInstall Kind = "go-install" // go install …/cmd/yolo@<version>
	KindPipx      Kind = "pipx"       // pipx install yolo-jail
	KindUV        Kind = "uv"         // uv tool install yolo-jail
	KindArchive   Kind = "archive"    // a release archive unpacked by hand
	KindUnknown   Kind = "unknown"    // nothing identifies it: no checks at all
)

// envRootDepth bounds Detect's walk from the binary up to a pipx/uv
// environment root: <env>/lib/pythonX.Y/site-packages/yolo_jail/bin/yolo is six
// directories below <env>.
const envRootDepth = 6

// Channel is the running binary's install channel and what it was built as.
type Channel struct {
	Kind Kind
	// Exe is the running binary, symlinks resolved.
	Exe string
	// SourceDir is the checkout a KindSource binary was built from.
	SourceDir string
	// Branch is the branch a KindSource binary was built from, when the build
	// recorded one. An update refuses a checkout that has since switched
	// branches: pulling THAT branch would deploy something the binary never was.
	Branch string
	// MissingSourceDir is set on a KindUnknown channel whose binary WAS built
	// from source, from a path that no longer contains the yolo-jail checkout.
	// It lets `yolo update` say so and name --from, instead of reporting an
	// unidentifiable install.
	MissingSourceDir string
	// Version is what a check compares against: the stamped commit for
	// KindSource (its updates are commits, not releases), the release version
	// for every other kind.
	Version string
}

// Identity is the value a cached check is keyed to. A source check depends on
// the checkout and branch as well as the binary's commit, so two builds from
// the same commit but different clones must not share an answer.
func (c Channel) Identity() string {
	if c.Kind == KindSource {
		return strings.Join([]string{string(c.Kind), c.Version, c.SourceDir, c.Branch}, "\x00")
	}
	return string(c.Kind) + ":" + c.Version
}

// CanApply reports whether `yolo update` can replace the complete installation
// for this channel. The Go and Python package channels carry only the host
// binary, while an archive has no package manager to ask.
func (c Channel) CanApply() bool {
	switch c.Kind {
	case KindSource, KindHomebrew:
		return true
	}
	return false
}

// DetectInput is everything Detect decides from, so every channel is testable
// without an installed binary.
type DetectInput struct {
	Exe           string // the running binary, symlinks resolved
	SourceDir     string // version.SourceDir
	SourceBranch  string // version.SourceBranch
	GitCommit     string // version.GitCommit
	Baked         string // version.Baked()
	ModuleVersion string // debug.BuildInfo.Main.Version
	// BuiltFromVCS reports a vcs.revision build setting: the binary was built
	// inside a checkout, not fetched from the module proxy.
	BuiltFromVCS bool
	// IsSourceCheckout reports whether dir is this module's git work tree.
	IsSourceCheckout func(dir string) bool
	// HasBundleBeside reports whether a release bundle sits beside exeDir.
	HasBundleBeside func(exeDir string) bool
	// FileExists reports whether path exists: the pipx and uv markers.
	FileExists func(path string) bool
}

// Detect names the channel that installed the binary described by in. Order
// matters: a source stamp is the most specific evidence there is, and the
// package-manager paths are checked before the bundle test because Homebrew
// ALSO ships a bundle beside its binary.
//
// pipx and uv are recognised by the metadata file each writes at the root of
// the environment it installs into, not by a path spelling: both tools let a
// user relocate that root (PIPX_HOME, UV_TOOL_DIR), and the marker moves with it.
// The running binary is NOT at <env>/bin/yolo — that is the wheel's Python
// console script, which execs the real binary from inside the package
// (tools/build-wheels): <env>/lib/pythonX.Y/site-packages/yolo_jail/bin/yolo. So
// the root is found by walking up to envRootDepth parents, to the first one
// named yolo-jail (the name both tools give the environment) that holds the
// marker.
func Detect(in DetectInput) Channel {
	ch := Channel{Kind: KindUnknown, Exe: in.Exe}
	exe := filepath.ToSlash(in.Exe)
	hasMarker := func(name string) bool {
		if in.FileExists == nil {
			return false
		}
		dir := filepath.Dir(in.Exe)
		for i := 0; i < envRootDepth; i++ {
			if filepath.Base(dir) == "yolo-jail" && in.FileExists(filepath.Join(dir, name)) {
				return true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return false
	}
	stamped := in.SourceDir != "" && in.GitCommit != "" && in.GitCommit != "unknown"
	switch {
	case stamped && in.IsSourceCheckout != nil && in.IsSourceCheckout(in.SourceDir):
		ch.Kind, ch.SourceDir, ch.Branch, ch.Version = KindSource, in.SourceDir, in.SourceBranch, in.GitCommit
	case stamped:
		// Built from source, but the path no longer names this checkout.
		// Nothing below can identify a from-source binary, so say what happened
		// instead.
		return Channel{Kind: KindUnknown, Exe: in.Exe, MissingSourceDir: in.SourceDir}
	case strings.Contains(exe, "/Cellar/yolo-jail/"):
		ch.Kind, ch.Version = KindHomebrew, in.Baked
	case hasMarker("pipx_metadata.json"):
		ch.Kind, ch.Version = KindPipx, in.Baked
	case hasMarker("uv-receipt.toml"):
		ch.Kind, ch.Version = KindUV, in.Baked
	case in.Baked == "" && strings.HasPrefix(in.ModuleVersion, "v") && !in.BuiltFromVCS:
		// `go install …@v0.10.0` stamps the module version into the build info
		// and nothing into -ldflags. A local `go build` in a checkout ALSO gets a
		// "v…" version (a pseudo-version, since Go 1.24), but it carries the
		// checkout's vcs.revision, which a module-proxy build never has.
		ch.Kind, ch.Version = KindGoInstall, version.Normalize(in.ModuleVersion)
	case in.Baked != "" && in.HasBundleBeside != nil && in.HasBundleBeside(filepath.Dir(in.Exe)):
		ch.Kind, ch.Version = KindArchive, in.Baked
	}
	// A release channel with no version has nothing to compare, so it is
	// treated as unidentified rather than as permanently out of date.
	if ch.Kind != KindUnknown && ch.Version == "" {
		return Channel{Kind: KindUnknown, Exe: in.Exe}
	}
	return ch
}

// Current detects the running binary's channel.
func Current() Channel {
	exe, err := os.Executable()
	if err != nil {
		return Channel{Kind: KindUnknown}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	var modVersion string
	var fromVCS bool
	if bi, ok := debug.ReadBuildInfo(); ok {
		modVersion = bi.Main.Version
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				fromVCS = true
			}
		}
	}
	return Detect(DetectInput{
		Exe:              exe,
		SourceDir:        version.SourceDir,
		SourceBranch:     version.SourceBranch,
		GitCommit:        version.GitCommit,
		Baked:            version.Baked(),
		ModuleVersion:    modVersion,
		BuiltFromVCS:     fromVCS,
		IsSourceCheckout: IsSourceCheckout,
		HasBundleBeside: func(exeDir string) bool {
			_, ok := reporoot.BundledSourceDirFrom(exeDir)
			return ok
		},
		FileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
	})
}

// IsSourceCheckout reports whether dir is a yolo-jail git checkout. A stamped
// path can outlive the checkout that occupied it; checking only for `.git`
// would let an unrelated replacement repository become the source of a later
// `git pull && just deploy`.
func IsSourceCheckout(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "//") {
			continue
		}
		return len(fields) >= 2 &&
			fields[0] == "module" &&
			strings.Trim(fields[1], "\"`") == Module
	}
	return false
}

// Enabled reports whether update checks may run at all in this process: never
// inside a jail (YOLO_VERSION is set there, see internal/banner), never in CI,
// and never when DisableEnv is set.
func Enabled(getenv func(string) string) bool {
	return getenv("YOLO_VERSION") == "" && getenv("CI") == "" && getenv(DisableEnv) == ""
}
