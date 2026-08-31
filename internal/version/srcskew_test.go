package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// gitRepo builds a throwaway repo and returns a commit helper. Identity is passed
// per-command so the test never depends on the developer's ~/.gitconfig (and works
// in a jail, where there is none).
func gitRepo(t *testing.T) (root string, commit func(relPath, content string) string) {
	t.Helper()
	root = t.TempDir()
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
	git("init", "-q", "-b", "main")
	return root, func(relPath, content string) string {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", "add "+relPath)
		return git("rev-parse", "HEAD")
	}
}

// withStamp sets the build-time commit stamp for one test and restores it.
func withStamp(t *testing.T, sha string) {
	t.Helper()
	orig := GitCommit
	t.Cleanup(func() { GitCommit = orig })
	GitCommit = sha
}

// TestSourceSkewReportsImageSourceMovement is the case that shipped: the binary was
// installed at one commit, the tree moved on through internal/, and every launch
// after that builds an image the launcher does not match.
func TestSourceSkewReportsImageSourceMovement(t *testing.T) {
	root, commit := gitRepo(t)
	installed := commit("internal/entrypoint/env.go", "package entrypoint // v1\n")
	head := commit("internal/entrypoint/env.go", "package entrypoint // v2\n")

	withStamp(t, installed)
	skew := SourceSkew(root)
	if skew == nil {
		t.Fatal("SourceSkew = nil, want a skew: the tree moved through internal/")
	}
	if skew.BinaryCommit != installed || skew.TreeCommit != head {
		t.Errorf("SourceSkew = {%s, %s}, want {%s, %s}",
			skew.BinaryCommit, skew.TreeCommit, installed, head)
	}
	if got := strings.Join(skew.Changed, ","); got != "internal" {
		t.Errorf("Changed = %q, want %q", got, "internal")
	}
}

// TestSourceSkewIgnoresDocsOnlyMovement is what keeps the refusal usable. Most
// commits in this repo touch only docs/, which flake.nix's goSrc fileset excludes —
// so the image an old binary would build is byte-identical, and refusing would be
// pure noise. A regression here does not break a launch; it breaks every launch.
func TestSourceSkewIgnoresDocsOnlyMovement(t *testing.T) {
	root, commit := gitRepo(t)
	installed := commit("internal/entrypoint/env.go", "package entrypoint\n")
	commit("docs/design/whatever.md", "# a doc\n")

	withStamp(t, installed)
	if skew := SourceSkew(root); skew != nil {
		t.Errorf("SourceSkew = %+v, want nil: docs/ is outside the image's source", skew)
	}
}

// TestSourceSkewSilentWithoutProof pins the disposition rule: everything it cannot
// PROVE is nil, never a guess. Each case is a real deployment — an unstamped
// `go build`, the baked in-jail binary (flake.nix stamps nothing), a binary carried
// from another checkout, and a caller with no repo at all.
func TestSourceSkewSilentWithoutProof(t *testing.T) {
	root, commit := gitRepo(t)
	commit("internal/a.go", "package a\n")
	commit("internal/a.go", "package a // moved\n")

	cases := []struct {
		name  string
		stamp string
		root  string
	}{
		{"unstamped go build", "", root},
		{"legacy unknown stamp", "unknown", root},
		{"commit from another checkout", "deadbee", root},
		{"no repo root", "abcdef0", ""},
		{"root is not a repo", "abcdef0", t.TempDir()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStamp(t, tc.stamp)
			if skew := SourceSkew(tc.root); skew != nil {
				t.Errorf("SourceSkew = %+v, want nil", skew)
			}
		})
	}
}

// TestSourceSkewSilentOnTheSameCommit covers the everyday state: dirty tree, HEAD
// unmoved. Uncommitted edits are invisible to nix too — both halves would build from
// the same commit — and the by-path verification loop stamps the fresh binary with
// exactly this HEAD.
func TestSourceSkewSilentOnTheSameCommit(t *testing.T) {
	root, commit := gitRepo(t)
	head := commit("internal/a.go", "package a\n")
	if err := os.WriteFile(filepath.Join(root, "internal", "a.go"),
		[]byte("package a // uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withStamp(t, head)
	if skew := SourceSkew(root); skew != nil {
		t.Errorf("SourceSkew = %+v, want nil on an unmoved HEAD", skew)
	}
}

// TestImageSourcePathsMatchTheFlake pins ImageSourcePaths against flake.nix's own
// goSrc fileset, because the list drifting OUT of the flake is silent in exactly the
// way this whole file exists to stop: a path that leaves the list stops being
// compared, and the skew it would have caught goes back to being discovered at boot.
//
// It reads the fileset's `./x` entries plus the two flake files, which are not in
// goSrc but decide the image just as much (packages, Env, installPrefix) — the same
// union `nix eval .#installPrefix.outPath` covers.
func TestImageSourcePathsMatchTheFlake(t *testing.T) {
	flake, err := os.ReadFile(filepath.Join("..", "..", "flake.nix"))
	if err != nil {
		t.Skipf("flake.nix unreadable (%v) — nothing to pin against", err)
	}
	// Both spellings goSrc uses: a bare `[ ./go.mod ]` and the guarded
	// `optionals (builtins.pathExists ./cmd) [ ./cmd ]`.
	re := regexp.MustCompile(`(?m)^\s*(?:\+\+ pkgs\.lib\.optionals \(builtins\.pathExists \./([\w.]+)\) \[ \./[\w.]+ \]|\[ \./([\w.]+) \])`)
	fileset := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(flake), -1) {
		if m[1] != "" {
			fileset[m[1]] = true
		} else if m[2] != "" {
			fileset[m[2]] = true
		}
	}
	if len(fileset) == 0 {
		t.Fatal("parsed no goSrc fileset entries from flake.nix — the pin is not " +
			"pinning anything; fix the pattern rather than deleting the test")
	}
	want := []string{"flake.lock", "flake.nix"}
	for p := range fileset {
		want = append(want, p)
	}
	sort.Strings(want)

	got := append([]string{}, ImageSourcePaths...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ImageSourcePaths = %v\nflake goSrc + flake files = %v\n"+
			"the two must agree, or SourceSkew goes silent about a path the image is built from",
			got, want)
	}
}
