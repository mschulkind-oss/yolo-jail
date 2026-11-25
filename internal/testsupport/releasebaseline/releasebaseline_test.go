package releasebaseline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLatestSelectsDisconnectedReleaseInsteadOfAncestralTag(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init", "-q")
	gitTest(t, repo, "config", "user.name", "Release Baseline Test")
	gitTest(t, repo, "config", "user.email", "release-baseline@example.invalid")

	write(t, repo, "legacy.txt", "old tree\n")
	gitTest(t, repo, "add", "legacy.txt")
	gitTest(t, repo, "commit", "-qm", "legacy ancestry")
	legacy := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	gitTest(t, repo, "tag", "v0.2.0")

	gitTest(t, repo, "checkout", "--orphan", "disconnected-release")
	gitTest(t, repo, "rm", "-rf", ".")
	write(t, repo, "go.mod", "module release.example\n")
	write(t, repo, "packs/example/pack.json", "{}\n")
	gitTest(t, repo, "add", "go.mod", "packs/example/pack.json")
	gitTest(t, repo, "commit", "-qm", "disconnected release")
	gitTest(t, repo, "tag", "v0.15.0")

	gitTest(t, repo, "checkout", "-qb", "candidate", legacy)
	if got := strings.TrimSpace(gitOutput(t, repo, "describe", "--tags", "--abbrev=0", "HEAD")); got != "v0.2.0" {
		t.Fatalf("ancestry witness = %s, want v0.2.0", got)
	}
	got, err := Latest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.15.0" {
		t.Fatalf("Latest() = %s, want disconnected v0.15.0", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); !os.IsNotExist(err) {
		t.Fatalf("candidate fixture unexpectedly contains go.mod: %v", err)
	}
}

func TestLatestBeforeExcludesCandidateTagButLatestIncludesIt(t *testing.T) {
	repo := releaseRepo(t)
	initial := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	gitTest(t, repo, "tag", "v0.15.0", initial)
	write(t, repo, "candidate", "release candidate\n")
	gitTest(t, repo, "add", "candidate")
	gitTest(t, repo, "commit", "-qm", "candidate")
	gitTest(t, repo, "tag", "v0.16.0")
	latest, err := Latest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if latest != "v0.16.0" {
		t.Fatalf("Latest() = %s, want v0.16.0", latest)
	}
	previous, err := LatestBefore(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if previous != "v0.15.0" {
		t.Fatalf("LatestBefore(HEAD) = %s, want v0.15.0", previous)
	}
}

func TestLatestOrdersVersionsAndIgnoresMalformedTags(t *testing.T) {
	repo := releaseRepo(t)
	for _, tag := range []string{
		"v1.9.0", "v1.10.0", "v2.0.0-rc.2", "v2.0.0-rc.10", "v2.0.0",
		"v99.0", "v3.0.0-", "release-9.0.0",
	} {
		gitTest(t, repo, "tag", tag)
	}
	got, err := Latest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2.0.0" {
		t.Fatalf("Latest() = %s, want stable v2.0.0 above prereleases", got)
	}
	gitTest(t, repo, "tag", "-d", "v2.0.0")
	got, err = Latest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2.0.0-rc.10" {
		t.Fatalf("Latest() without stable tag = %s, want rc.10 above rc.2", got)
	}
}

func TestLatestIgnoresLeadingZeroSemVerComponents(t *testing.T) {
	repo := releaseRepo(t)
	for _, tag := range []string{
		"v2.0.0-rc.1", "v2.0.0",
		"v099.0.0", "v99.0.0-rc.01",
	} {
		gitTest(t, repo, "tag", tag)
	}
	got, err := Latest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2.0.0" {
		t.Fatalf("Latest() = %s, want valid stable v2.0.0; invalid leading-zero tags must not win", got)
	}
}

func TestParseVersionAcceptsZeroAndTextPrereleaseIdentifiers(t *testing.T) {
	for _, tag := range []string{"v0.0.0", "v1.0.0-rc.0", "v1.0.0-rc.foo", "v1.0.0-rc01"} {
		if _, ok := parseVersion(tag); !ok {
			t.Errorf("parseVersion(%q) rejected a valid SemVer tag", tag)
		}
	}
}

func TestLatestRequiresAValidLocalReleaseTag(t *testing.T) {
	repo := releaseRepo(t)
	for _, tag := range []string{"v1.0", "v1.0.0-", "not-a-release"} {
		gitTest(t, repo, "tag", tag)
	}
	if got, err := Latest(repo); err == nil {
		t.Fatalf("Latest() = %q, want missing-valid-tag error", got)
	}
}

func releaseRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTest(t, repo, "init", "-q")
	gitTest(t, repo, "config", "user.name", "Release Baseline Test")
	gitTest(t, repo, "config", "user.email", "release-baseline@example.invalid")
	write(t, repo, "file", "fixture\n")
	gitTest(t, repo, "add", "file")
	gitTest(t, repo, "commit", "-qm", "fixture")
	return repo
}

func gitTest(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompareOrdersReleaseTags(t *testing.T) {
	if c, ok := Compare("v0.19.0", "v0.20.0"); !ok || c != -1 {
		t.Fatalf("Compare(v0.19.0, v0.20.0) = %d, %v", c, ok)
	}
	if c, ok := Compare("v0.20.0", "v0.20.0"); !ok || c != 0 {
		t.Fatalf("equal tags = %d, %v", c, ok)
	}
	if _, ok := Compare("v0.20.0", "nightly"); ok {
		t.Fatal("an invalid tag must report !ok")
	}
}
