package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// olderYoloRepo builds a repo whose HEAD has moved past the commit this binary is stamped
// with, through internal/ (a path version.SourceSkew compares), and returns the root and both
// full commits. The stamp is restored when the test ends.
func olderYoloRepo(t *testing.T) (root, installed, head string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{"-c", "user.email=test@example.com", "-c", "user.name=test",
			"-c", "commit.gpgsign=false"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = root
		// Never the committer's repository: a hook exports GIT_DIR and friends
		// (packsrc.CleanGitEnv's doc has the measurement).
		cmd.Env = packsrc.CleanGitEnv(os.Environ())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(body string) {
		t.Helper()
		p := filepath.Join(root, "internal", "x", "x.go")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("package x // installed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "installed")
	installed = git("rev-parse", "HEAD")
	write("package x // moved on\n")
	git("add", "-A")
	git("commit", "-q", "-m", "moved on")
	head = git("rev-parse", "HEAD")

	orig := version.GitCommit
	t.Cleanup(func() { version.GitCommit = orig })
	version.GitCommit = installed
	return root, installed, head
}

// TestCheckLoopholesUnmatchedSupersessionNamesProvenSkew: when the yolo running `check` is
// older than the source tree the launch would build from, the [FAIL] row carries the skew
// clause the launch's refusal carries (RM-D2): both commits and `just install`. A preflight
// that sends the reader looking for a typo they did not make, when the cause is an old yolo
// whose shipped packs lack the capability, is the refusal §4.6 calls worse than the warning
// it replaced.
//
// It reads the repo root through Options.RepoRoot, the resolver the launch uses, so deleting
// the lookup in checkLoopholes (or the clause in UnmatchedSupersessionFix) turns this red.
func TestCheckLoopholesUnmatchedSupersessionNamesProvenSkew(t *testing.T) {
	isolatedModuleDir(t)
	recordSupersessions(t, loopholes.PackSupersession{Pack: "acme-bedrock",
		Capability: "acme-oauth-refresh", Because: "Bedrock overrides the OAuth path"})
	root, installed, head := olderYoloRepo(t)

	var out strings.Builder
	r := newReporter(&out, false)
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	fillDefaults(o)
	o.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: root, Source: reporoot.FromEnv}, true
	}
	o.checkLoopholes(r)

	if r.failed != 1 {
		t.Fatalf("failed=%d, want the one unmatched claim:\n%s", r.failed, out.String())
	}
	for _, want := range []string{
		"older than the source tree",
		installed[:8],
		head[:8],
		"just install",
		root,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the [FAIL] row's skew clause is missing %q:\n%s", want, out.String())
		}
	}
}
