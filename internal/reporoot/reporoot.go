// Package reporoot discovers the flake and helper bundle paired with the running
// yolo executable. Launches use the executable-relative bundle first; only an
// explicitly installed launcher may fall back to its matching installed bundle.
package reporoot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Source names which candidate produced the root, for launch/check diagnostics.
type Source string

const (
	// FromEnv remains for old diagnostic consumers. Runtime resolution now refuses
	// the retired variable rather than using it to select a source.
	FromEnv             Source = "env"
	FromBesideBinary    Source = "beside-binary"
	FromInstalledBundle Source = "installed-bundle"
)

func (s Source) Describe() string {
	switch s {
	case FromEnv:
		return "YOLO_REPO_ROOT"
	case FromBesideBinary:
		return "flake bundle beside the binary"
	case FromInstalledBundle:
		return "flake bundle staged by `just install`"
	default:
		return string(s)
	}
}

// Resolution is a canonical flake root together with its provenance. Error is
// populated on failure so callers can preserve the resolver's actionable remedy.
type Resolution struct {
	Root             string
	Source           Source
	SnapshotIdentity string
	BuildKind        string
	Error            error
}

type bundleStamp struct {
	Version          string `json:"version"`
	Commit           string `json:"commit"`
	SnapshotIdentity string `json:"snapshotIdentity"`
	BuildKind        string `json:"buildKind"`
}

var linuxBinaries = []string{"yolo", "yolo-entrypoint", "yolo-jaild", "yolo-ps", "yolo-cglimit", "yolo-journalctl", "yolo-serial", "yolo-log"}

// darwinBinaries is what a darwin bundle dir holds: the host yolo, the same build as the launcher
// (docs/design/pinned-host-helpers.md, PH-D2), then the macos-user guest set. It is pinned to
// DARWIN_HOST_BINARIES and GUEST_BINARIES in scripts/stage-source-bundle.sh.
var darwinBinaries = []string{"yolo", "yolo-jaild", "yolo-serial", "yolo-ps", "yolo-log"}

// bundleGOOS is the OS the bundle is validated for; a variable so a test can check the darwin
// requirements on Linux.
var bundleGOOS = runtime.GOOS

// ReinstallSteps is the next step when yolo cannot find the files a launch builds each jail from.
// Most users have no checkout, so `just` comes last, for the ones who do.
const ReinstallSteps = "Reinstall yolo from Homebrew or a release archive, which include the build files each jail is made from " +
	"(see \"Other ways to install\" at https://docs.yolo-jail.mschulkind.dev); a `go install` or pipx install does not include them. " +
	"In a yolo-jail checkout, run `just install`, or run `just build-dev-bundle` and then the launcher it prints."

var resolveExecutable = os.Executable
var resolveInstalledBundle = paths.FlakeBundleDir

// Resolve discovers the matched bundle for the running executable. It retains
// the historical (Resolution, bool) API; the false result includes Resolution.Error.
func Resolve(getenv func(string) string) (Resolution, bool) {
	exe, err := resolveExecutable()
	if err != nil {
		res := Resolution{Error: fmt.Errorf("cannot locate the running yolo executable: %w. %s", err, ReinstallSteps)}
		return res, false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	res := resolveWith(exe, resolveInstalledBundle(), getenv, version.SnapshotIdentity, version.BuildKind)
	return res, res.Error == nil
}

// resolveWith is the deterministic resolver core used by Resolve and its tests.
func resolveWith(executable, installedPath string, getenv func(string) string, identity, buildKind string) Resolution {
	if getenv == nil {
		getenv = os.Getenv
	}
	if inherited := getenv("YOLO_REPO_ROOT"); inherited != "" {
		return failed(fmt.Errorf("YOLO_REPO_ROOT=%s no longer selects a launch source. Build a matched development bundle with `just build-dev-bundle` and invoke its printed launcher path, or unset YOLO_REPO_ROOT and use the installed bundle (`just install`)", inherited))
	}

	if executable == "" {
		return failed(fmt.Errorf("cannot locate the running yolo executable. %s", ReinstallSteps))
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	exeDir := filepath.Dir(executable)
	for _, candidate := range adjacentCandidates(exeDir) {
		if isInstalledStateDirectory(candidate.path, installedPath) {
			continue
		}
		if !isBundleCandidate(candidate) {
			continue
		}
		root := canonicalRoot(candidate.path)
		stamp, err := validateBundle(root)
		if err != nil {
			return failed(fmt.Errorf("adjacent yolo-jail bundle at %s is incomplete or mismatched: %v. Rebuild it with `just build-dev-bundle` and invoke the printed launcher path, or reinstall with `just install`", root, err))
		}
		if err := validateLauncherIdentity(identity, buildKind, stamp, true); err != nil {
			return failed(fmt.Errorf("adjacent yolo-jail bundle at %s does not match this launcher: %v. Rebuild it with `just build-dev-bundle` and invoke the printed launcher path, or reinstall with `just install`", root, err))
		}
		return Resolution{Root: root, Source: FromBesideBinary, SnapshotIdentity: stamp.SnapshotIdentity, BuildKind: stamp.BuildKind}
	}

	if buildKind != "installed" || identity == "" {
		return failed(errors.New("no complete adjacent yolo-jail bundle was found, and this launcher is not marked installed with a snapshot identity. Build a matched development bundle with `just build-dev-bundle` and invoke its printed launcher path, or install yolo with `just install`"))
	}
	root := canonicalRoot(installedPath)
	stamp, err := validateBundle(root)
	if err != nil {
		return failed(fmt.Errorf("installed yolo-jail bundle at %s is incomplete: %v. Run `just install` to publish a matching bundle", root, err))
	}
	if err := validateLauncherIdentity(identity, buildKind, stamp, false); err != nil {
		return failed(fmt.Errorf("installed yolo-jail bundle at %s does not match this launcher: %v. Run `just install` to publish a matching bundle", root, err))
	}
	return Resolution{Root: root, Source: FromInstalledBundle, SnapshotIdentity: stamp.SnapshotIdentity, BuildKind: stamp.BuildKind}
}

func failed(err error) Resolution { return Resolution{Error: err} }

type adjacentCandidate struct {
	path             string
	existenceIsProof bool
}

func adjacentCandidates(exeDir string) []adjacentCandidate {
	return []adjacentCandidate{
		{path: filepath.Join(exeDir, "..", "share", "yolo-jail"), existenceIsProof: true},
		{path: filepath.Join(exeDir, "share", "yolo-jail"), existenceIsProof: true},
		{path: exeDir},
	}
}

func isInstalledStateDirectory(candidate, installedPath string) bool {
	if installedPath == "" {
		return false
	}
	stateDir := canonicalRoot(filepath.Dir(installedPath))
	return stateDir != "" && canonicalRoot(candidate) == stateDir
}

// A share/yolo-jail path is recognized by its existence, even when empty; the
// direct exeDir layout needs bundle contents so an ordinary bin dir is not one.
func isBundleCandidate(candidate adjacentCandidate) bool {
	if candidate.existenceIsProof {
		if _, err := os.Lstat(candidate.path); err == nil {
			return true
		}
	}
	for _, name := range []string{"flake.nix", "flake.lock", "yolo-build-stamp.json", "bin"} {
		if fileExists(filepath.Join(candidate.path, name)) {
			return true
		}
	}
	return false
}

func validateBundle(root string) (bundleStamp, error) {
	var stamp bundleStamp
	if root == "" {
		return stamp, errors.New("bundle root is unavailable")
	}
	if !isRegularFile(filepath.Join(root, "flake.nix")) || !isRegularFile(filepath.Join(root, "flake.lock")) {
		return stamp, errors.New("both flake.nix and flake.lock are required")
	}
	stampPath := filepath.Join(root, "yolo-build-stamp.json")
	if isRegularFile(stampPath) {
		data, err := os.ReadFile(stampPath)
		if err != nil {
			return stamp, fmt.Errorf("read yolo-build-stamp.json: %w", err)
		}
		if err := json.Unmarshal(data, &stamp); err != nil {
			return stamp, fmt.Errorf("parse yolo-build-stamp.json: %w", err)
		}
	} else if fileExists(stampPath) {
		return stamp, errors.New("yolo-build-stamp.json is not a regular file")
	}
	if stamp.SnapshotIdentity != "" && !validSnapshotIdentity(stamp.SnapshotIdentity) {
		return stamp, errors.New("snapshot identity in yolo-build-stamp.json is not a lowercase SHA-256")
	}
	if stamp.BuildKind != "" && stamp.BuildKind != "development" && stamp.BuildKind != "installed" {
		return stamp, fmt.Errorf("unsupported build kind %q in yolo-build-stamp.json", stamp.BuildKind)
	}
	for _, name := range linuxBinaries {
		path := filepath.Join(root, "bin", "linux-"+runtime.GOARCH, name)
		if !isExecutableRegularFile(path) {
			return stamp, fmt.Errorf("required executable Linux helper bin/linux-%s/%s is missing or not executable", runtime.GOARCH, name)
		}
	}
	if bundleGOOS == "darwin" {
		for _, name := range darwinBinaries {
			if !isRegularFile(filepath.Join(root, "bin", "darwin-"+runtime.GOARCH, name)) {
				return stamp, fmt.Errorf("required macOS helper bin/darwin-%s/%s is missing", runtime.GOARCH, name)
			}
		}
	}
	return stamp, nil
}

func validateLauncherIdentity(identity, buildKind string, stamp bundleStamp, adjacent bool) error {
	if buildKind != "" && buildKind != "development" && buildKind != "installed" {
		return fmt.Errorf("launcher has unsupported build kind %q", buildKind)
	}
	if buildKind == "development" && identity == "" {
		return errors.New("development launcher has no snapshot identity")
	}
	if identity != "" {
		if !validSnapshotIdentity(identity) {
			return errors.New("launcher snapshot identity is not a lowercase SHA-256")
		}
		if stamp.SnapshotIdentity == "" {
			return errors.New("launcher carries a snapshot identity but the bundle stamp does not")
		}
		if stamp.SnapshotIdentity != identity {
			return fmt.Errorf("snapshot identity %q differs from launcher identity %q", stamp.SnapshotIdentity, identity)
		}
	}
	if buildKind != "" && stamp.BuildKind != "" && stamp.BuildKind != buildKind {
		return fmt.Errorf("bundle build kind %q differs from launcher build kind %q", stamp.BuildKind, buildKind)
	}
	if !adjacent && identity == "" {
		return errors.New("installed fallback requires a nonempty launcher snapshot identity")
	}
	return nil
}

// BundledSourceDir discovers a complete executable-relative helper bundle.
func BundledSourceDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return BundledSourceDirFrom(filepath.Dir(exe))
}

// BundledSourceDirFrom checks the supported executable-relative layouts. A
// recognized incomplete candidate is not returned; Resolve reports its detailed
// refusal instead of falling through to another source.
func BundledSourceDirFrom(exeDir string) (string, bool) {
	for _, candidate := range adjacentCandidates(exeDir) {
		if !isBundleCandidate(candidate) {
			continue
		}
		root := canonicalRoot(candidate.path)
		if _, err := validateBundle(root); err == nil {
			return root, true
		}
		return "", false
	}
	return "", false
}

func validSnapshotIdentity(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func canonicalRoot(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func isExecutableRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
