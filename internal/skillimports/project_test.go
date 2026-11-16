package skillimports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestImportProjectReadsOnlySelectedGitSkillDirectories(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	commit := skillTreeCommit(t, store)
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/acme/skills", Ref: "main", Path: "skills",
		Only: []string{"review", "missing"}, OnlySet: true}
	if err := store.WriteResolution(Resolution{Version: resolutionVersion, Handle: entry.Handle, Git: entry.Git,
		Ref: entry.Ref, ResolvedRef: "refs/heads/main", Repo: "example.test/acme/skills", Commit: commit,
		AccessClass: AccessAnonymous, ResolvedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(entry, Authority{})
	if err != nil {
		t.Fatal(err)
	}
	defer projection.Close()
	if len(projection.Skills) != 1 || projection.Skills[0].Name != "review" || len(projection.Missing) != 1 || projection.Missing[0] != "missing" {
		t.Fatalf("Project selection = skills %+v missing %v refused %v", projection.Skills, projection.Missing, projection.Refused)
	}
	for path, want := range map[string]string{
		filepath.Join(projection.Skills[0].Root, "SKILL.md"): "review content",
		filepath.Join(projection.Skills[0].Root, "guide.md"): "review guide",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("projected %s = %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(projection.Skills[0].Root, "escape")); err != nil {
		t.Fatalf("symlink entry was not preserved for confined staging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projection.Root, "other")); !os.IsNotExist(err) {
		t.Fatalf("unselected sibling skill was projected (stat %v)", err)
	}
	if _, err := os.Stat(filepath.Join(projection.Root, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("non-skill repository content was projected (stat %v)", err)
	}
}

func TestImportProjectWithholdsAuthenticatedCacheUnderAnonymousAccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	commit := skillTreeCommit(t, store)
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/acme/skills", Ref: "main", Path: "skills"}
	if err := store.WriteResolution(Resolution{Version: resolutionVersion, Handle: entry.Handle, Git: entry.Git,
		Ref: entry.Ref, ResolvedRef: "refs/heads/main", Repo: "example.test/acme/skills", Commit: commit,
		AccessClass: AccessAuthenticated, ResolvedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Project(entry, Authority{})
	if err != nil {
		t.Fatal(err)
	}
	defer projection.Close()
	if len(projection.Skills) != 0 || len(projection.Refused) != 1 || !strings.Contains(projection.Refused[0].Reason, "higher access class") {
		t.Fatalf("anonymous attach exposed authenticated cache: %+v", projection)
	}
}

func skillTreeCommit(t *testing.T, store *Store) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source.git")
	work := filepath.Join(root, "work")
	_ = runGit(t, "", "init", "--bare", "--template=", source)
	_ = runGit(t, "", "clone", source, work)
	_ = runGit(t, work, "config", "user.name", "skill import projection test")
	_ = runGit(t, work, "config", "user.email", "skills@example.test")
	for path, body := range map[string]string{
		"skills/review/SKILL.md": "review content",
		"skills/review/guide.md": "review guide",
		"skills/other/SKILL.md":  "other content",
		"README.md":              "not a skill",
		"pack.toml":              "not a pack",
	} {
		full := filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../outside", filepath.Join(work, "skills", "review", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other/SKILL.md", filepath.Join(work, "skills", "review", "sibling")); err != nil {
		t.Fatal(err)
	}
	_ = runGit(t, work, "add", "-A")
	_ = runGit(t, work, "commit", "-m", "skill tree")
	commit := runGit(t, work, "rev-parse", "HEAD")
	_ = runGit(t, work, "push", "origin", "HEAD:refs/heads/main")
	_ = runGit(t, "", "--git-dir", store.ObjectDirectory(), "fetch", "--no-tags", source, commit)
	return commit
}
