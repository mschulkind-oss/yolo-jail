package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/skillimports"
)

func TestRepairSkillsRefusedAddUsesRealResolverAndWritesNothing(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	store, err := skillimports.Open(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	before := readFile(t, fx.project)
	rc, _, _ := captureStd(t, func() int {
		return registry["skills"]([]string{"skills", "add", "git@unapproved.test:o/r.git", "--as", "acme"})
	})
	if rc == 0 {
		t.Fatal("unapproved SSH add succeeded")
	}
	if readFile(t, fx.project) != before {
		t.Fatal("refused add edited config")
	}
	if _, err := os.Stat(store.Directory()); !os.IsNotExist(err) {
		t.Fatalf("production refusal left durable import state: %v", err)
	}
}
func TestRepairSkillsListShowsImportOwnership(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.a]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n[skill_imports.b]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
	cfg, _ := loadSkillCommandConfig(fx.workspace)
	for _, e := range config.SkillImports(cfg) {
		if err := store.WriteResolution(cliResolution(e, c, "main")); err != nil {
			t.Fatal(err)
		}
	}
	old := readSkillInventorySourcesForCommand
	readSkillInventorySourcesForCommand = func(string) (run.SkillInventorySources, error) {
		return run.SkillInventorySources{Targets: []jailcontent.SkillTarget{{Dest: ".claude/skills", Staging: "claude"}}}, nil
	}
	t.Cleanup(func() { readSkillInventorySourcesForCommand = old })
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || errOut != "" || !strings.Contains(out, "review at ~/.claude/skills by import a") {
		t.Fatalf("losing import is undisclosed: rc=%d out=%s err=%s", rc, out, errOut)
	}
}
func TestRepairConcurrentAddCannotPublishLosingRecord(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	store, c := seedSkillsCLIStore(t, fx.workspace)
	old := resolveSkillImportForCommand
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan int, 1)
	resolveSkillImportForCommand = func(_ context.Context, s *skillimports.Store, e config.SkillImport, _ skillimports.Authority) (skillimports.Resolution, error) {
		if e.Git == "https://example.test/org/winner" {
			close(entered)
			<-release
		}
		r := cliResolution(e, c, "main")
		r.Repo = strings.TrimPrefix(e.Git, "https://")
		if err := s.WriteResolution(r); err != nil {
			return r, err
		}
		return r, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = old })
	go func() {
		done <- registry["skills"]([]string{"skills", "add", "https://example.test/org/winner", "--as", "acme", "--track"})
	}()
	<-entered
	// This callback must still own the transaction lock, not just the fetch call.
	locked, err := store.TryWithLock(func() error { return nil })
	loserStarted := make(chan struct{})
	loserDone := make(chan int, 1)
	go func() {
		close(loserStarted)
		loserDone <- registry["skills"]([]string{"skills", "add", "https://example.test/org/loser", "--as", "acme", "--track"})
	}()
	<-loserStarted
	close(release)
	rc := <-done
	loserRC := <-loserDone
	if err != nil || locked {
		t.Fatalf("command fetch/config transaction lock not held: locked=%t err=%v", locked, err)
	}
	if rc != 0 {
		t.Fatalf("winner rc=%d", rc)
	}
	rc = loserRC

	cfg, _ := loadSkillCommandConfig(fx.workspace)
	e := config.SkillImports(cfg)[0]
	r, ok := store.Resolution("acme", e)
	if rc == 0 || !ok || r.Git != "https://example.test/org/winner" {
		t.Fatalf("loser published: rc=%d record=%+v ok=%t", rc, r, ok)
	}
}

func TestRepairSkillsFailedFetchUsesRealResolverAndWritesNothing(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	store, err := skillimports.Open(fx.workspace)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "injected fetch failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	before := readFile(t, fx.project)
	rc, _, errOut := captureStd(t, func() int {
		return registry["skills"]([]string{"skills", "add", server.URL + "/org/repo", "--as", "acme"})
	})
	if rc == 0 || !strings.Contains(errOut, "did not resolve") {
		t.Fatalf("failed fetch rc=%d err=%s", rc, errOut)
	}
	if readFile(t, fx.project) != before {
		t.Fatal("failed fetch edited config")
	}
	if _, err := os.Stat(store.Directory()); !os.IsNotExist(err) {
		t.Fatalf("failed production fetch left durable owned state: %v", err)
	}
}
func TestRepairSkillsPinKeepsListAndUpdateResolution(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
	cfg, _ := loadSkillCommandConfig(fx.workspace)
	e := config.SkillImports(cfg)[0]
	if err := store.WriteResolution(cliResolution(e, c, "main")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.RecordPath("acme"))
	rc, _, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "pin", "acme"}) })
	if rc != 0 || errOut != "" {
		t.Fatalf("pin rc=%d err=%s", rc, errOut)
	}
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || !strings.Contains(out, "stored=true") || !strings.Contains(out, "review") {
		t.Fatalf("list after pin: %d %s %s", rc, out, errOut)
	}
	after, _ := os.ReadFile(store.RecordPath("acme"))
	if string(before) != string(after) {
		t.Fatal("pin/list wrote record")
	}
	old := resolveSkillImportForCommand
	calls := 0
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport, skillimports.Authority) (skillimports.Resolution, error) {
		calls++
		return cliResolution(e, c, "main"), nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = old })
	rc, _, errOut = captureStd(t, func() int { return registry["skills"]([]string{"skills", "update", "acme"}) })
	if rc != 0 || calls != 1 {
		t.Fatalf("pin lost update resolution: %d calls=%d %s", rc, calls, errOut)
	}
}
func TestRepairSkillsListAndPinRefuseAgentWritableGit(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, c := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{"review"})
	cfg, _ := loadSkillCommandConfig(fx.workspace)
	e := config.SkillImports(cfg)[0]
	if err := store.WriteResolution(cliResolution(e, c, "main")); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(fx.workspace, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho executed > "+marker+"\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	captureStd(t, func() int { return registry["skills"]([]string{"skills", "pin", "acme"}) })
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("CLI cached readers ran agent-writable git")
	}
}
