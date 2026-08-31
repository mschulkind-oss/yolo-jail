package run

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// The source-skew gate: a host yolo older than the tree it would build the image
// from REFUSES, before the build. These tests drive Run() rather than
// refuseOnSourceSkew, deliberately — a test that exercises the predicate and
// survives the deletion of its call site is the shape this repo has shipped five
// times (AGENTS.md, "Testing"). Delete the call in run.go and
// TestRunRefusesWhenTheHostBinaryIsOlderThanTheTree fails.

// skewRepo builds a repo whose HEAD has moved past `installed` through internal/,
// and stamps this binary as `installed` for the duration of the test.
func skewRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{
			"-c", "user.email=test@example.com",
			"-c", "user.name=test",
			"-c", "commit.gpgsign=false",
		}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(content string) {
		t.Helper()
		p := filepath.Join(root, "internal", "entrypoint", "env.go")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("package entrypoint // installed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "installed")
	installed := git("rev-parse", "HEAD")
	write("package entrypoint // moved on\n")
	git("add", "-A")
	git("commit", "-q", "-m", "moved on")

	orig := version.GitCommit
	t.Cleanup(func() { version.GitCommit = orig })
	version.GitCommit = installed
	return root
}

// skewOptions is runFatalOptions' twin for the skew gate: every seam is the same
// except that the repo root RESOLVES, so the launch reaches the gate under test.
func skewOptions(t *testing.T, repoRoot string, env map[string]string, stdout, stderr *bytes.Buffer) *Options {
	t.Helper()
	getenv := func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return env[k]
	}
	o := &Options{Workspace: t.TempDir(), Network: "bridge", IsLinux: true}
	fillDefaults(o)
	o.Stdout = stdout
	o.Stderr = stderr
	o.Getenv = getenv
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repoRoot, Source: reporoot.FromEnv}, true
	}
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[0] == "podman" && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		}
		return ExecResult{Ran: false}
	}
	return o
}

// TestRunRefusesWhenTheHostBinaryIsOlderThanTheTree is the regression for the boot
// that died at `mkdir /home/agent/.yolo: read-only file system`: an installed yolo
// from before the ~/.yolo/bin rename, launching against a tree that has it. The
// refusal must land BEFORE the image build, and must name the fix.
func TestRunRefusesWhenTheHostBinaryIsOlderThanTheTree(t *testing.T) {
	repoRoot := skewRepo(t)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := skewOptions(t, repoRoot, nil, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 {
		t.Fatalf("Run() = %d, want 1 (host binary older than the tree)\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"older than the source tree",
		"just install",
		"they differ in  internal",
		AllowSourceSkewEnv,
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("refusal is missing %q:\n%s", want, stderr.String())
		}
	}
}

// TestRunSkewGateIsSilentWhenTheBinaryMatches: the same tree, stamped at HEAD. The
// gate must not fire — otherwise it fires on every launch and gets deleted.
func TestRunSkewGateIsSilentWhenTheBinaryMatches(t *testing.T) {
	repoRoot := skewRepo(t)
	head := headCommit(t, repoRoot)
	version.GitCommit = head // skewRepo's Cleanup still restores the original

	var stdout, stderr bytes.Buffer
	o := skewOptions(t, repoRoot, nil, &stdout, &stderr)

	if o.refuseOnSourceSkew(repoRoot) {
		t.Errorf("refused a binary stamped at HEAD:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("gate printed on a matching binary:\n%s", stderr.String())
	}
}

// TestRunSkewGateHonorsTheEscapeHatch: the refusal names an env var, so the env var
// has to work. A fatal witness whose escape hatch is only documented is not one.
func TestRunSkewGateHonorsTheEscapeHatch(t *testing.T) {
	repoRoot := skewRepo(t)

	var stdout, stderr bytes.Buffer
	o := skewOptions(t, repoRoot, map[string]string{AllowSourceSkewEnv: "1"}, &stdout, &stderr)

	if o.refuseOnSourceSkew(repoRoot) {
		t.Errorf("%s=1 did not lift the refusal:\n%s", AllowSourceSkewEnv, stderr.String())
	}
}

func headCommit(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
