package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/skillimports"
)

func TestFinalSkillsSameCommitPinnedUpdatePublishesUnwrittenResolution(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
	cfg, err := loadSkillCommandConfig(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	e := config.SkillImports(cfg)[0]
	prior := cliResolution(e, c, "main")
	prior.AccessClass = skillimports.AccessAuthenticated
	prior.Rev = c
	prior.ResolvedRef = ""
	if err := store.WriteResolution(prior); err != nil {
		t.Fatal(err)
	}
	if err := setSkillImportRev(fx.workspace, "acme", c, nil); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, fx.project)
	fresh := cliResolution(e, c, "main")
	fresh.ResolvedAt = prior.ResolvedAt.Add(time.Hour)
	old := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport, skillimports.Authority) (skillimports.Resolution, error) {
		return fresh, nil
	} // deliberately unpublished, as the production fetcher returns it
	t.Cleanup(func() { resolveSkillImportForCommand = old })
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "update", "acme"}) })
	if rc != 0 || errOut != "" {
		t.Fatalf("update rc=%d out=%s err=%s", rc, out, errOut)
	}
	cfg, err = loadSkillCommandConfig(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	e = config.SkillImports(cfg)[0]
	got, ok := store.Resolution("acme", e)
	if !ok || got.AccessClass != skillimports.AccessAnonymous || !got.ResolvedAt.Equal(fresh.ResolvedAt) {
		t.Fatalf("successful no-op update did not publish: %+v valid=%t", got, ok)
	}
	if readFile(t, fx.project) != before {
		t.Fatal("same-commit update rewrote config")
	}
	rc, out, errOut = captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || !strings.Contains(out, "obtained anonymous") || !strings.Contains(out, "skills that would be projected: review") {
		t.Fatalf("list after update: %d %s %s", rc, out, errOut)
	}
	p, err := store.Project(e, skillimports.Authority{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Skills) != 1 || p.Skills[0].Name != "review" {
		t.Fatalf("attach projection still withholds refreshed anonymous commit: %+v", p)
	}
}

func TestFinalSkillsSymlinkedConfigReusesTransactionParent(t *testing.T) {
	for _, verb := range []string{"pin", "remove", "add", "update"} {
		t.Run(verb, func(t *testing.T) {
			fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
			store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
			cfg, err := loadSkillCommandConfig(fx.workspace)
			if err != nil {
				t.Fatal(err)
			}
			e := config.SkillImports(cfg)[0]
			record := cliResolution(e, c, "main")
			if verb == "update" {
				record.Rev = c
				record.ResolvedRef = ""
				if err := setSkillImportRev(fx.workspace, "acme", c, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.WriteResolution(record); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(filepath.Dir(fx.workspace), "shared.toml")
			if err := os.Rename(fx.project, shared); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../shared.toml", fx.project); err != nil {
				t.Fatal(err)
			}
			old := resolveSkillImportForCommand
			resolveSkillImportForCommand = func(_ context.Context, _ *skillimports.Store, entry config.SkillImport, _ skillimports.Authority) (skillimports.Resolution, error) {
				return cliResolution(entry, c, "main"), nil
			}
			t.Cleanup(func() { resolveSkillImportForCommand = old })
			args := []string{"skills", verb, "acme"}
			if verb == "add" {
				args = []string{"skills", "add", e.Git, "--as", "new"}
			}
			started := make(chan struct{})
			done := make(chan int, 1)
			go func() { close(started); done <- registry["skills"](args) }()
			<-started
			select {
			case rc := <-done:
				if rc != 0 {
					t.Fatalf("%s symlink command rc=%d", verb, rc)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s deadlocked reacquiring its held parent inode", verb)
			}
			if info, err := os.Lstat(fx.project); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("supported config link replaced: %v %v", info, err)
			}
			locked, err := store.TryWithLock(func() error { return nil })
			if err != nil || !locked {
				t.Fatalf("command retained transaction lock: %t %v", locked, err)
			}
		})
	}
}

func TestFinalSkillsListProjectionBudgetOrderAndLosingOwnership(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.first]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\nonly = [\"b\", \"a\"]\n[skill_imports.later]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, smallCommit := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"a"})
	work := t.TempDir()
	cliGit(t, "", "init", "--template=", work)
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(work, "skills", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("first "+name), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload"), []byte(strings.Repeat(name, 20<<20)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cliGit(t, work, "add", ".")
	cliGit(t, work, "-c", "user.name=skills", "-c", "user.email=skills@example.test", "-c", "commit.gpgsign=false", "commit", "-m", "bounded skills")
	bigCommit := strings.TrimSpace(cliGit(t, work, "rev-parse", "HEAD"))
	cliGit(t, "", "--git-dir", store.ObjectDirectory(), "fetch", work, bigCommit)
	cfg, err := loadSkillCommandConfig(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	entries := config.SkillImports(cfg)
	if err := store.WriteResolution(cliResolution(entries[0], bigCommit, "main")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteResolution(cliResolution(entries[1], smallCommit, "main")); err != nil {
		t.Fatal(err)
	}
	var inspectBudget, projectBudget skillimports.ProjectionBudget
	owners := map[string]string{}
	for _, e := range entries {
		i, err := store.InspectWithBudget(e, skillimports.Authority{}, &inspectBudget)
		if err != nil {
			t.Fatal(err)
		}
		p, err := store.ProjectWithBudget(e, skillimports.Authority{}, &projectBudget)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		names := make([]string, 0, len(p.Skills))
		for _, s := range p.Skills {
			names = append(names, s.Name)
		}
		if !reflect.DeepEqual(i.Skills, names) {
			t.Fatalf("%s authored only order disagrees: inspection=%v projection=%v", e.Handle, i.Skills, names)
		}
		for _, s := range p.Skills {
			if owner := owners[s.Name]; owner != "" {
				if s.Handle != "later" || owner != "first" {
					t.Fatalf("unexpected losing ownership: %s by %s", s.Handle, owner)
				}
			} else {
				owners[s.Name] = s.Handle
			}
		}
	}
	if owners["a"] != "first" || owners["b"] != "" {
		t.Fatalf("shared budget winners=%v", owners)
	}
	old := readSkillInventorySourcesForCommand
	readSkillInventorySourcesForCommand = func(string) (run.SkillInventorySources, error) {
		return run.SkillInventorySources{Targets: []jailcontent.SkillTarget{{Dest: ".claude/skills", Staging: "claude"}}}, nil
	}
	t.Cleanup(func() { readSkillInventorySourcesForCommand = old })
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || !strings.Contains(out, "a at ~/.claude/skills by import first") {
		t.Fatalf("list losing owner disagrees: %d %s %s", rc, out, errOut)
	}
	if !strings.Contains(out, "projection byte or link bound") {
		t.Fatalf("missing budget refusal: %s", out)
	}
}

func TestFinalSkillsSameCommitPublicationFailureKeepsConfigAndRecord(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
	cfg, err := loadSkillCommandConfig(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	e := config.SkillImports(cfg)[0]
	prior := cliResolution(e, c, "main")
	prior.Rev = c
	prior.ResolvedRef = ""
	if err := store.WriteResolution(prior); err != nil {
		t.Fatal(err)
	}
	if err := setSkillImportRev(fx.workspace, "acme", c, nil); err != nil {
		t.Fatal(err)
	}
	beforeConfig := readFile(t, fx.project)
	beforeRecord, err := os.ReadFile(store.RecordPath(e.Handle))
	if err != nil {
		t.Fatal(err)
	}
	old := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport, skillimports.Authority) (skillimports.Resolution, error) {
		fresh := cliResolution(e, c, "main")
		fresh.AccessClass = "invalid"
		return fresh, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = old })
	rc, _, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "update", "acme"}) })
	if rc == 0 || !strings.Contains(errOut, "invalid skill import resolution record") {
		t.Fatalf("publication failure ignored: rc=%d err=%s", rc, errOut)
	}
	afterRecord, err := os.ReadFile(store.RecordPath(e.Handle))
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, fx.project) != beforeConfig || string(afterRecord) != string(beforeRecord) {
		t.Fatal("failed no-op publication changed config or prior record")
	}
	locked, err := store.TryWithLock(func() error { return nil })
	if err != nil || !locked {
		t.Fatalf("failed update retained lock: %t %v", locked, err)
	}
}

func TestFinalSkillsConcurrentSymlinkAddCannotOverwriteWinner(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
	shared := filepath.Join(filepath.Dir(fx.workspace), "shared.toml")
	if err := os.Rename(fx.project, shared); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../shared.toml", fx.project); err != nil {
		t.Fatal(err)
	}
	old := resolveSkillImportForCommand
	entered := make(chan struct{})
	release := make(chan struct{})
	winnerDone := make(chan int, 1)
	loserDone := make(chan int, 1)
	resolveSkillImportForCommand = func(_ context.Context, _ *skillimports.Store, e config.SkillImport, _ skillimports.Authority) (skillimports.Resolution, error) {
		if strings.HasSuffix(e.Git, "/winner") {
			close(entered)
			<-release
		}
		r := cliResolution(e, c, "main")
		r.Repo = strings.TrimPrefix(e.Git, "https://")
		return r, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = old })
	go func() {
		winnerDone <- registry["skills"]([]string{"skills", "add", "https://example.test/org/winner", "--as", "acme", "--track"})
	}()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("winner did not enter its transaction")
	}
	locked, err := store.TryWithLock(func() error { return nil })
	loserStarted := make(chan struct{})
	go func() {
		close(loserStarted)
		loserDone <- registry["skills"]([]string{"skills", "add", "https://example.test/org/loser", "--as", "acme", "--track"})
	}()
	<-loserStarted
	close(release)
	for _, done := range []<-chan int{winnerDone, loserDone} {
		select {
		case rc := <-done:
			if done == winnerDone && rc != 0 || done == loserDone && rc == 0 {
				t.Fatalf("unexpected concurrent command result=%d", rc)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("symlink writer failed to finish after release")
		}
	}
	if locked || err != nil {
		t.Fatalf("second descriptor acquired winner's transaction: %t %v", locked, err)
	}
	cfg, err := loadSkillCommandConfig(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	e := config.SkillImports(cfg)[0]
	r, ok := store.Resolution(e.Handle, e)
	if !ok || !strings.HasSuffix(r.Git, "/winner") {
		t.Fatalf("loser overwrote winning resolution: %+v %t", r, ok)
	}
	if info, err := os.Lstat(fx.project); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config link was replaced: %v %v", info, err)
	}
}
