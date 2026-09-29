package main

// release_test.go holds the judge to the `yolo check` of the version the TAP carries,
// not only to this tree's.
//
// WHY. tap-install.yml runs this checker from the default branch against whatever
// release the tap carries, so the judge meets that release's report, never this tree's.
// judge_test.go's pin runs this tree's check.Check alone, which cannot see the gap:
// reword or downgrade one of bareMacFailures' FAILs here, drop its entry as the old
// reverse assertion demanded, and every weekly and dispatched run goes red against the
// published binary until the next release reaches the tap. So this test compiles
// package baremac inside the newest release tag's tree and runs that release's
// check.Check under the same conditions, and the judge must accept both reports — the
// old wording and the new, for as long as the tap can still carry the old.
//
// It is also where the allowlist is held to the code in the other direction: an entry
// of bareMacFailures that NEITHER report prints excuses a failure nothing expects, and
// fails here. An entry only the release prints is kept, and logged as removable once the
// tap carries a release that no longer prints it.
//
// WHICH RELEASE. The newest v* tag reachable from HEAD: the version the tap carries once
// that tag's Release run has pushed its formula (release.yml). On a release commit that is
// the commit itself, so the pin compares this tree with itself, which is what the tap is
// about to carry.
//
// WHERE IT RUNS. In the short suite, so `just check-ci` (CI's check-go) and check-macos
// run it; both check out full history and tags. Outside CI, a clone without tags or git
// skips it, saying so. Under GitHub Actions a missing tag is a failure: a silent skip there
// would retire the check with every run green. The same shape, and the same reasons, as
// packs/releasedecode_test.go's TestShippedPacksDecodeUnderTheLastRelease.

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bareMacProbeDir is where the probe's main package is written inside the release's tree:
// a directory no tree has, so it never lands on top of that release's own code.
const bareMacProbeDir = "cmd/tap-install-bare-mac-probe"

// bareMacProbe runs package baremac once and exits with the report's exit code. It exists
// only inside the extracted release tree; exit 64 is its own usage error, so it cannot be
// mistaken for a report's 0 or 1.
const bareMacProbe = `// Command tap-install-bare-mac-probe is written into a release's tree by
// tools/tap-install-check/release_test.go. It is no tree's source.
package main

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/tools/tap-install-check/baremac"
)

func main() {
	if len(os.Args) != 4 {
		os.Stderr.WriteString("usage: tap-install-bare-mac-probe <version> <bundle> <workspace>\n")
		os.Exit(64)
	}
	os.Exit(baremac.Check(baremac.Inputs{Version: os.Args[1], Bundle: os.Args[2], Workspace: os.Args[3]}, os.Stdout))
}
`

// TestTheJudgeAcceptsWhatTheLastReleaseReportsOnABareMac is the check.
func TestTheJudgeAcceptsWhatTheLastReleaseReportsOnABareMac(t *testing.T) {
	root, tag := lastReleaseTag(t)
	version := strings.TrimPrefix(tag, "v")
	keg, bundle := fakeKeg(t)

	relOut, relRC := releaseBareMacCheck(t, root, tag, version, bundle)
	requireTheJudgeAccepts(t, tag+"'s", relOut, relRC, version, keg, bundle)

	headOut, _ := bareMacCheck(t, version, bundle)
	reports := []struct {
		whose string
		rep   checkReport
	}{{"this tree", decodeReport(t, headOut)}, {tag, decodeReport(t, relOut)}}
	for _, e := range bareMacFailures {
		var printedBy []string
		for _, r := range reports {
			for _, f := range r.rep.Findings {
				if f.Status == "fail" && f.Section == e.section && strings.HasPrefix(f.Message, e.messagePrefix) {
					printedBy = append(printedBy, r.whose)
					break
				}
			}
		}
		switch {
		case len(printedBy) == 0:
			t.Errorf("bareMacFailures excuses %+v, but neither this tree's report nor %s's has such a FAIL; "+
				"drop the entry, which would excuse a failure nothing expects", e, tag)
		case len(printedBy) == 1 && printedBy[0] == tag:
			t.Logf("bareMacFailures keeps %+v for %s alone: this tree no longer prints it. "+
				"Removable once the tap carries a release cut after the change.", e, tag)
		}
	}
}

// lastReleaseTag finds the module root and the newest release tag reachable from HEAD.
func lastReleaseTag(t *testing.T) (root, tag string) {
	t.Helper()
	inCI := os.Getenv("GITHUB_ACTIONS") == "true"
	unavailable := func(format string, args ...any) {
		t.Helper()
		if inCI {
			t.Fatalf(format+" — CI must fetch full history and tags (actions/checkout fetch-depth: 0) "+
				"for this check to run", args...)
		}
		t.Skipf(format, args...)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		unavailable("no git on PATH to read the last release from")
	}
	// The MODULE root, from this package's directory, which is where `go test` runs it —
	// not `git rev-parse --show-toplevel`, which a git hook's exported GIT_DIR misleads
	// (packs/releasedecode_test.go's lastRelease records the case).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("this test runs from tools/tap-install-check, so %s should be the module root, and it holds no go.mod: %v", root, err)
	}
	if err := exec.Command(git, "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		unavailable("not a git checkout, so there is no last release to read")
	}
	out, err := exec.Command(git, "-C", root, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "HEAD").Output()
	if err != nil {
		unavailable("no release tag is reachable from HEAD (a shallow clone, or no tags fetched)")
	}
	tag = strings.TrimSpace(string(out))
	if _, err := normalizeExpect(tag); err != nil {
		t.Fatalf("the newest release tag %q is not a release version: %v", tag, err)
	}
	return root, tag
}

// releaseBareMacCheck builds package baremac, THIS tree's copy, inside the release's tree
// and runs it there: the release's check.Check under this tree's definition of a bare Mac.
func releaseBareMacCheck(t *testing.T, root, tag, version, bundle string) ([]byte, int) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH to build %s's check with", tag)
	}
	old := resolvedTempDir(t)
	extractTree(t, root, tag, old)

	// This tree's baremac replaces whatever the release has at that path, so both reports
	// are taken under one definition of the machine.
	dst := filepath.Join(old, "tools", "tap-install-check", "baremac")
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	copied := 0
	entries, err := os.ReadDir("baremac")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("baremac", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dst, e.Name()), string(b), 0o644)
		copied++
	}
	if copied == 0 {
		t.Fatal("package baremac has no source file to copy into the release's tree")
	}
	writeFile(t, filepath.Join(old, filepath.FromSlash(bareMacProbeDir), "main.go"), bareMacProbe, 0o644)

	// Hermetic: the release's own vendor tree, this machine's toolchain, no network, and no
	// workspace file steering the build. -trimpath keeps the build cache keyed on content
	// rather than on this run's temp directory, so a rerun compiles only what changed.
	bin := filepath.Join(resolvedTempDir(t), "tap-install-bare-mac-probe")
	build := exec.Command(goBin, "build", "-trimpath", "-o", bin, "./"+bareMacProbeDir)
	build.Dir = old
	build.Env = append(os.Environ(), "GOFLAGS=-mod=vendor", "GOTOOLCHAIN=local", "GOWORK=off",
		"GOPROXY=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building %s's check with this tree's package baremac failed: %v\n%s\n"+
			"baremac may use only what %s exports (its package comment lists it); if this tree "+
			"changed one of those on purpose, baremac has to keep compiling against both trees.",
			tag, err, out, tag)
	}

	// A fresh HOME and TMPDIR, and no YOLO_* from a developer's shell or a jail: the
	// runner's, as far as the process environment reaches.
	home, tmp := resolvedTempDir(t), resolvedTempDir(t)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "YOLO_") && !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	run := exec.Command(bin, version, bundle, resolvedTempDir(t))
	run.Env = append(env, "HOME="+home, "TMPDIR="+tmp)
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	rc := 0
	if err := run.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running %s's check: %v", tag, err)
		}
		rc = exit.ExitCode()
	}
	if rc != 0 && rc != 1 {
		t.Fatalf("%s's check exited %d, which no report does:\n%s", tag, rc, stderr.String())
	}
	return stdout.Bytes(), rc
}

// extractTree writes the tree at tag into dest, streaming `git archive` through
// archive/tar so no tar binary is needed. The whole tree, not a package list: the check
// package of a later release may import, or embed, anything in its module.
func extractTree(t *testing.T, root, tag, dest string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "archive", "--format=tar", tag)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(stdout)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading git archive %s: %v", tag, err)
		}
		name := filepath.Clean(h.Name)
		if name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
			continue
		}
		path := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			_ = os.Symlink(h.Linkname, path)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("git archive %s: %v\n%s", tag, err, stderr.String())
	}
}
