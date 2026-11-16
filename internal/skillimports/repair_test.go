package skillimports

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
)

func repairStore(t *testing.T) (*Store, config.SkillImport) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	e := config.SkillImport{Handle: "acme", Git: "https://example.test/acme/skills", Ref: "main", Path: "skills"}
	return s, e
}
func repairRecord(t *testing.T, s *Store, e config.SkillImport, commit string) {
	t.Helper()
	if err := s.WriteResolution(Resolution{Version: 1, Handle: e.Handle, Git: e.Git, Ref: e.Ref, ResolvedRef: "refs/heads/main", Repo: "example.test/acme/skills", Commit: commit, AccessClass: AccessAnonymous, ResolvedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}
func repairCommit(t *testing.T, s *Store, files map[string]string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", "init", "--template=", work)
	runGit(t, work, "config", "user.name", "repair")
	runGit(t, work, "config", "user.email", "repair@example.test")
	for name, body := range files {
		p := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	c := runGit(t, work, "rev-parse", "HEAD")
	runGit(t, "", "--git-dir", s.ObjectDirectory(), "fetch", work, c)
	return c
}
func TestRepairCachedReadersRefuseWorkspaceGit(t *testing.T) {
	s, e := repairStore(t)
	c := repairCommit(t, s, map[string]string{"skills/review/SKILL.md": "review"})
	repairRecord(t, s, e, c)
	bin := filepath.Join(s.workspace, "bin")
	os.Mkdir(bin, 0700)
	marker := filepath.Join(t.TempDir(), "executed")
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho executed > "+marker+"\nexit 0\n"), 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.HasCommit(c)
	s.Inspect(e, Authority{})
	p, _ := s.Project(e, Authority{})
	defer p.Close()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cached production readers executed workspace-owned git")
	}
}
func TestRepairPinReusesMovingRecordOffline(t *testing.T) {
	s, e := repairStore(t)
	c := repairCommit(t, s, map[string]string{"skills/review/SKILL.md": "review"})
	repairRecord(t, s, e, c)
	before, _ := os.ReadFile(s.RecordPath(e.Handle))
	e.Rev = c
	inspection, err := s.Inspect(e, Authority{})
	if err != nil || !inspection.HasResolution {
		t.Fatalf("pin lost cached resolution: %+v %v", inspection, err)
	}
	p, err := s.Project(e, Authority{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Skills) != 1 {
		t.Fatalf("attach pin withheld skills: %+v", p)
	}
	// An offline pin must not attempt to fetch an invalid host: cached immutable objects suffice.
	if _, _, err := s.ResolveLaunch(context.Background(), e, Authority{}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.RecordPath(e.Handle))
	if string(before) != string(after) {
		t.Fatal("pin cache reuse wrote the moving record")
	}
}
func TestRepairProjectRequiresSkillMarker(t *testing.T) {
	s, e := repairStore(t)
	c := repairCommit(t, s, map[string]string{"skills/review/SKILL.md": "review", "skills/hooks/start.sh": "not a skill"})
	repairRecord(t, s, e, c)
	p, err := s.Project(e, Authority{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Skills) != 1 || p.Skills[0].Name != "review" {
		t.Fatalf("non-skill directory admitted: %+v", p)
	}
	i, err := s.Inspect(e, Authority{})
	if err != nil || strings.Join(i.Skills, ",") != "review" {
		t.Fatalf("inspection admits non-skill: %+v %v", i, err)
	}
}
func TestRepairProjectionChargesLateFailure(t *testing.T) {
	s, e := repairStore(t)
	c := repairCommit(t, s, map[string]string{"skills/a/SKILL.md": "a", "skills/a/first": strings.Repeat("a", 20<<20), "skills/a/last": strings.Repeat("b", 20<<20), "skills/b/SKILL.md": "b", "skills/b/first": strings.Repeat("c", 20<<20), "skills/b/last": strings.Repeat("d", 20<<20)})
	repairRecord(t, s, e, c)
	budget := &ProjectionBudget{}
	p, err := s.ProjectWithBudget(e, Authority{}, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Skills) != 0 || len(p.Refused) != 2 || budget.bytes < 20<<20 || budget.entries == 0 {
		t.Fatalf("discarded work refunded: bytes=%d entries=%d projection=%+v", budget.bytes, budget.entries, p)
	}
}
func TestRepairInspectRejectsSubmoduleWithoutWrites(t *testing.T) {
	s, e := repairStore(t)
	c := repairCommit(t, s, map[string]string{"skills/review/SKILL.md": "review"})
	// Construct a gitlink directly; no submodule checkout or network is needed.
	work := t.TempDir()
	runGit(t, "", "clone", s.ObjectDirectory(), work)
	runGit(t, work, "checkout", c)
	runGit(t, work, "config", "user.name", "repair")
	runGit(t, work, "config", "user.email", "repair@example.test")
	runGit(t, work, "update-index", "--add", "--cacheinfo", "160000,"+c+",skills/review/module")
	runGit(t, work, "-c", "commit.gpgsign=false", "commit", "-m", "gitlink")
	c = runGit(t, work, "rev-parse", "HEAD")
	runGit(t, "", "--git-dir", s.ObjectDirectory(), "fetch", work, c)
	repairRecord(t, s, e, c)
	before, _ := os.ReadDir(filepath.Dir(s.directory))
	i, err := s.Inspect(e, Authority{})
	after, _ := os.ReadDir(filepath.Dir(s.directory))
	if err != nil || len(i.Skills) != 0 {
		t.Fatalf("inspection advertised refused gitlink: %+v %v", i, err)
	}
	if len(before) != len(after) {
		t.Fatal("inspection created projection scratch")
	}
}

func TestRepairLaunchResolutionEventsBelongToWriter(t *testing.T) {
	s, e := repairStore(t)
	c := repairCommit(t, s, map[string]string{"skills/review/SKILL.md": "first"})
	fetch := func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		return gitbroker.ImportFetchResult{Commit: c, ResolvedRef: "refs/heads/main", AccessClass: AccessAnonymous}, nil
	}
	first, _, err := s.resolveLaunchWithFetcher(context.Background(), e, Authority{}, time.Now(), fetch)
	if err != nil || first.Change == nil || first.Change.PreviousCommit != "" {
		t.Fatalf("creation event missing: %+v %v", first, err)
	}
	reader, _, err := s.resolveLaunchWithFetcher(context.Background(), e, Authority{}, time.Now(), fetch)
	if err != nil || reader.Change != nil {
		t.Fatalf("reader replayed writer event: %+v %v", reader, err)
	}
	previous := c
	c = repairCommit(t, s, map[string]string{"skills/review/SKILL.md": "second"})
	moved, _, err := s.resolveLaunchWithFetcher(context.Background(), e, Authority{}, time.Now().Add(48*time.Hour), fetch)
	if err != nil || moved.Change == nil || moved.Change.PreviousCommit != previous {
		t.Fatalf("move event missing: %+v %v", moved, err)
	}
}
