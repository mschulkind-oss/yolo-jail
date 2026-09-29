// Package baremac is the one definition of "a stock macOS runner right after `brew
// install`" that tap-install-check's tests grade its judge against: the REAL `yolo check
// --no-build --format json` body, check.Check, run under that machine's conditions — macOS,
// nothing on PATH (no podman, no Apple Container, no nix), no /nix, no config anywhere, and the
// flake resolved from the bundle beside the binary, which is reporoot's Homebrew answer.
//
// ⚠ IT IS COMPILED AGAINST TWO TREES. The tests in the parent directory call it in-process,
// against this tree's check package. release_test.go copies this directory into the newest
// release tag's tree and builds it there, so the same conditions are graded against the check
// package of the version the tap carries — the version the workflow's judge actually meets.
// So this file may use only what that release exports with these shapes: check.Check,
// check.Options with the fields set below, check.ExecResult.Ran, reporoot.Resolution and
// reporoot.FromBesideBinary. v0.11.0 exports all of them. Building it here, in the tree that
// becomes the next release, is what notices a change to any of them before that release is
// cut; if one is changed on purpose, release_test.go fails to build this file against the
// release and says so.
//
// It imports nothing outside the standard library, check and reporoot, and is a single file,
// so copying the directory is the whole of moving it.
package baremac

import (
	"io"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/check"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// Inputs are the facts about the install that the report names.
type Inputs struct {
	// Version is the version the report names: the formula's.
	Version string
	// Bundle is the flake bundle beside the binary, <keg>/share/yolo-jail.
	Bundle string
	// Workspace is the directory `yolo check` runs in: an empty one, as the checker's is.
	Workspace string
}

// Check writes the report to stdout and returns its exit code. HOME must already name an
// empty directory: it is process state, so setting it is the caller's job (t.Setenv in
// process, the child's environment in release_test.go).
func Check(in Inputs, stdout io.Writer) int {
	return check.Check(check.Options{
		Build:             false,
		SkipEnsureStorage: true,
		Version:           in.Version,
		Now:               func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		Getenv:            func(string) string { return "" },
		LookPath:          func(string) (string, bool) { return "", false },
		Exec: func([]string, string, []string, time.Duration) check.ExecResult {
			return check.ExecResult{Ran: false}
		},
		Stdout:      stdout,
		IsTTYStdout: func() bool { return false },
		IsMacOS:     true,
		Machine:     "arm64",
		Workspace:   in.Workspace,
		RepoRoot: func() (reporoot.Resolution, bool) {
			return reporoot.Resolution{Root: in.Bundle, Source: reporoot.FromBesideBinary}, true
		},
		PathExists: func(p string) bool {
			if p == "/nix" {
				return false // the runner has no Nix volume
			}
			_, err := os.Stat(p)
			return err == nil
		},
		Format: "json",
	})
}
