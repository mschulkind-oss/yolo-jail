package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/skillimports"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestSkillsCommandDispatchAndHelp(t *testing.T) {
	if got, _, _ := routeArgv([]string{"skills", "list"}); got != "skills" {
		t.Fatalf("skills list routes to %q", got)
	}
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "--help"}) })
	if rc != 0 || !strings.Contains(out, "Usage: yolo skills") || errOut != "" {
		t.Fatalf("skills help rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
}

func TestSkillsJailedCommandsKeepHostStateOnHost(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/acme/skills\"\nref = \"main\"\n")
	t.Setenv("YOLO_VERSION", "0.17.1-test")
	t.Setenv("YOLO_WORKSPACE", fx.workspace)
	t.Setenv("YOLO_HOST_DIR", fx.workspace)
	before := readFile(t, fx.project)
	if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
		t.Fatalf("fixture storage unexpectedly exists: %v", err)
	}
	for _, args := range [][]string{
		{"skills", "add", "https://example.invalid/acme/skills", "--as", "acme", "--track"},
		{"skills", "update", "acme"},
		{"skills", "pin", "acme"},
	} {
		command := strings.Join(append([]string{"yolo", "skills"}, args[1:]...), " ")
		rc, out, errOut := captureStd(t, func() int { return registry["skills"](args) })
		if rc == 0 || out != "" || !strings.Contains(errOut, "nothing ran here") || !strings.Contains(errOut, "yolo host-run -C "+fx.workspace+" -- "+command) {
			t.Fatalf("jailed %s rc=%d stdout=%q stderr=%q", args[1], rc, out, errOut)
		}
	}
	if after := readFile(t, fx.project); after != before {
		t.Fatalf("jailed add changed workspace config:\n%s", after)
	}
	if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
		t.Fatalf("jailed add created host import state: %v", err)
	}
}

func TestSkillsJailedListAndRemoveUseWorkspaceOnly(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/acme/skills\"\nref = \"main\"\npath = \"skills\"\n")
	t.Setenv("YOLO_VERSION", "0.17.1-test")
	t.Setenv("YOLO_WORKSPACE", fx.workspace)
	t.Setenv("YOLO_HOST_DIR", fx.workspace)
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || !strings.Contains(out, "acme: moving ref") || !strings.Contains(out, "host-side") || !strings.Contains(errOut, "yolo host-run -C "+fx.workspace+" -- yolo skills list") {
		t.Fatalf("jailed list rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
		t.Fatalf("jailed list touched host storage: %v", err)
	}
	rc, out, errOut = captureStd(t, func() int { return registry["skills"]([]string{"skills", "remove", "acme"}) })
	if rc != 0 || !strings.Contains(out, "Removed \"acme\"") || errOut != "" {
		t.Fatalf("jailed remove rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	cfg, err := config.LoadReadableConfig(fx.workspace, true, nil)
	if err != nil || len(config.SkillImports(cfg)) != 0 {
		t.Fatalf("jailed remove config = %+v, err=%v", config.SkillImports(cfg), err)
	}
	if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
		t.Fatalf("jailed remove created host state: %v", err)
	}
}

func TestSkillsDerivesDefaultHandleFromValidatedRepositoryURL(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://example.test/org/repo", "repo"},
		{"https://example.test/org/repo.git", "repo"},
		{"https://example.test/org/repo.git/", "repo"},
		{"git@example.test:org/repo.git/", "repo"},
		{"ssh://git@example.test/org/repo.git", "repo"},
		{"https://example.test/org/repo.git.git", "repo.git"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			got, err := deriveSkillImportHandle(tc.url)
			if err != nil || got != tc.want {
				t.Fatalf("deriveSkillImportHandle(%q) = %q, %v; want %q", tc.url, got, err, tc.want)
			}
		})
	}
	for _, url := range []string{"https://example.test/", "https://example.test/org/.git", "file:///tmp/repo"} {
		if got, err := deriveSkillImportHandle(url); err == nil || got != "" {
			t.Errorf("deriveSkillImportHandle(%q) = %q, %v; want refusal", url, got, err)
		}
	}
}

func TestSkillsHostAddPinsByDefaultAndHonorsExplicitHandle(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	store, commit := seedSkillsCLIStore(t, fx.workspace)
	calls := 0
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(_ context.Context, gotStore *skillimports.Store, entry config.SkillImport,
		authority skillimports.Authority) (skillimports.Resolution, error) {
		calls++
		if gotStore.Directory() != store.Directory() || entry.Handle != "manual" || entry.Ref != "" || entry.Git != "https://example.test/org/repo.git" {
			t.Fatalf("resolver entry/store = %+v / %s", entry, gotStore.Directory())
		}
		if len(authority.ApprovedRemotes) != 0 {
			t.Fatalf("unapproved command gained approved remotes: %+v", authority.ApprovedRemotes)
		}
		record := cliResolution(entry, commit, "main")
		if err := gotStore.WriteResolution(record); err != nil {
			t.Fatal(err)
		}
		return record, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int {
		return registry["skills"]([]string{"skills", "add", "https://example.test/org/repo.git", "--as", "manual", "--only", "review,triage"})
	})
	if rc != 0 || errOut != "" || !strings.Contains(out, "Added \"manual\"") || !strings.Contains(out, "pinned") || calls != 1 {
		t.Fatalf("add rc=%d stdout=%q stderr=%q calls=%d", rc, out, errOut, calls)
	}
	cfg, err := config.LoadReadableConfig(fx.workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := config.SkillImports(cfg)
	if len(entries) != 1 || entries[0].Handle != "manual" || entries[0].Ref != "main" || entries[0].Rev != commit || !entries[0].OnlySet || strings.Join(entries[0].Only, ",") != "review,triage" {
		t.Fatalf("added entry = %+v", entries)
	}
	if record, ok := store.Resolution("manual", entries[0]); !ok || record.Commit != commit || record.Rev != commit || record.ResolvedRef != "" {
		t.Fatalf("pinned record = %+v valid=%t", record, ok)
	}
}

func TestSkillsHostAddRefusesInvalidOrDuplicateHandleWithoutWrites(t *testing.T) {
	for _, tc := range []struct{ name, config, url string }{
		{"missing repository component", "", "https://example.test/"},
		{"invalid derived handle", "", "https://example.test/org/bad:name"},
		{"duplicate derived handle", "[skill_imports.repo]\ngit = \"https://example.test/org/old\"\nref = \"main\"\n", "https://example.test/org/repo.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSkillsCLIWorkspace(t, tc.config)
			before := readFile(t, fx.project)
			calls := 0
			oldResolver := resolveSkillImportForCommand
			resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport,
				skillimports.Authority) (skillimports.Resolution, error) {
				calls++
				return skillimports.Resolution{}, nil
			}
			t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
			rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "add", tc.url}) })
			if rc == 0 || out != "" || calls != 0 {
				t.Fatalf("add rc=%d stdout=%q stderr=%q calls=%d", rc, out, errOut, calls)
			}
			if tc.name == "duplicate derived handle" {
				if !strings.Contains(errOut, "distinct handle") {
					t.Fatalf("duplicate refusal lacks override step: %q", errOut)
				}
			} else if !strings.Contains(errOut, "--as <handle>") {
				t.Fatalf("invalid derived handle lacks override suggestion: %q", errOut)
			}
			if readFile(t, fx.project) != before {
				t.Fatal("refused add changed workspace config")
			}
			if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
				t.Fatalf("refused add created host state: %v", err)
			}
		})
	}
}

func TestSkillsHostAddAndUpdateRespectUserVetoWithoutFetching(t *testing.T) {
	fx := newSkillsCLIWorkspaceWithUser(t, "", "packs = [\"git\", \"github\"]\nworkspace_skill_imports = false\n")
	before := readFile(t, fx.project)
	calls := 0
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport,
		skillimports.Authority) (skillimports.Resolution, error) {
		calls++
		return skillimports.Resolution{}, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int {
		return registry["skills"]([]string{"skills", "add", "https://example.test/org/repo", "--as", "repo"})
	})
	if rc == 0 || out != "" || calls != 0 || !strings.Contains(errOut, "workspace_skill_imports = false") || !strings.Contains(errOut, "Nothing was fetched or written") {
		t.Fatalf("vetoed add rc=%d stdout=%q stderr=%q calls=%d", rc, out, errOut, calls)
	}
	if readFile(t, fx.project) != before {
		t.Fatal("vetoed add changed config")
	}
	if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
		t.Fatalf("vetoed add created host state: %v", err)
	}
	rc, out, errOut = captureStd(t, func() int { return registry["skills"]([]string{"skills", "update"}) })
	if rc == 0 || out != "" || calls != 0 || !strings.Contains(errOut, "workspace_skill_imports = false") {
		t.Fatalf("vetoed update rc=%d stdout=%q stderr=%q calls=%d", rc, out, errOut, calls)
	}
}

func TestSkillsHostAddFetchFailureLeavesConfigAndStateUntouched(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	before := readFile(t, fx.project)
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport,
		skillimports.Authority) (skillimports.Resolution, error) {
		return skillimports.Resolution{}, os.ErrPermission
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int {
		return registry["skills"]([]string{"skills", "add", "https://example.test/org/repo", "--as", "repo"})
	})
	if rc == 0 || out != "" || !strings.Contains(errOut, "approved Git access") {
		t.Fatalf("failed add rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	if readFile(t, fx.project) != before {
		t.Fatal("failed add changed workspace config")
	}
	if _, err := os.Stat(paths.GlobalStorage()); !os.IsNotExist(err) {
		t.Fatalf("failed add created host state: %v", err)
	}
}

func TestSkillsUpdateAndPinUseRecordedCommitWithoutJailRPC(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, oldCommit := seedSkillsCLIStore(t, fx.workspace)
	newCommit := seedSkillsCLICommit(t, store, "second")
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/org/repo", Ref: "main", Path: "skills"}
	if err := store.WriteResolution(cliResolution(entry, oldCommit, "main")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(_ context.Context, gotStore *skillimports.Store, requested config.SkillImport,
		_ skillimports.Authority) (skillimports.Resolution, error) {
		calls++
		if requested.Rev != "" || requested.Ref != "main" || gotStore.Directory() != store.Directory() {
			t.Fatalf("update request = %+v", requested)
		}
		fresh := cliResolution(requested, newCommit, "main")
		if err := gotStore.WriteResolution(fresh); err != nil {
			t.Fatal(err)
		}
		return fresh, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "update", "acme"}) })
	if rc != 0 || errOut != "" || !strings.Contains(out, oldCommit) || !strings.Contains(out, newCommit) {
		t.Fatalf("update rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	cfg, err := config.LoadReadableConfig(fx.workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record, ok := store.Resolution("acme", config.SkillImports(cfg)[0]); !ok || record.Commit != newCommit {
		t.Fatalf("updated resolution = %+v valid=%t", record, ok)
	}
	before, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errOut = captureStd(t, func() int { return registry["skills"]([]string{"skills", "pin", "acme"}) })
	if rc != 0 || errOut != "" || !strings.Contains(out, newCommit) || calls != 1 {
		t.Fatalf("pin rc=%d stdout=%q stderr=%q resolver calls=%d", rc, out, errOut, calls)
	}
	cfg, err = config.LoadReadableConfig(fx.workspace, true, nil)
	if err != nil || config.SkillImports(cfg)[0].Rev != newCommit {
		t.Fatalf("pinned config=%+v err=%v", config.SkillImports(cfg), err)
	}
	after, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("pin changed host resolution state: %v", err)
	}
}

func TestSkillsUpdatePinnedEntryChangesConfigAndRecordTogether(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, oldCommit := seedSkillsCLIStore(t, fx.workspace)
	newCommit := seedSkillsCLICommit(t, store, "second")
	if err := setSkillImportRev(fx.workspace, "acme", oldCommit); err != nil {
		t.Fatal(err)
	}
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/org/repo", Ref: "main", Rev: oldCommit, Path: "skills"}
	old := cliResolution(entry, oldCommit, "main")
	old.Rev, old.ResolvedRef = oldCommit, ""
	if err := store.WriteResolution(old); err != nil {
		t.Fatal(err)
	}
	calls := 0
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(_ context.Context, gotStore *skillimports.Store, request config.SkillImport,
		_ skillimports.Authority) (skillimports.Resolution, error) {
		calls++
		if request.Rev != "" {
			t.Fatalf("pinned update fetched using immutable rev: %+v", request)
		}
		fresh := cliResolution(request, newCommit, "main")
		if err := gotStore.WriteResolution(fresh); err != nil {
			t.Fatal(err)
		}
		return fresh, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "update", "acme"}) })
	if rc != 0 || errOut != "" || calls != 1 || !strings.Contains(out, oldCommit) || !strings.Contains(out, newCommit) {
		t.Fatalf("pinned update rc=%d stdout=%q stderr=%q calls=%d", rc, out, errOut, calls)
	}
	cfg, err := config.LoadReadableConfig(fx.workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated := config.SkillImports(cfg)[0]
	if updated.Rev != newCommit {
		t.Fatalf("pinned update config = %+v", updated)
	}
	record, ok := store.Resolution("acme", updated)
	if !ok || record.Commit != newCommit || record.Rev != newCommit || record.ResolvedRef != "" {
		t.Fatalf("pinned update record = %+v valid=%t", record, ok)
	}
}

func TestSkillsUpdateWithoutRecordDoesNotContactRemoteAndRemoveKeepsStore(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	calls := 0
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport,
		skillimports.Authority) (skillimports.Resolution, error) {
		calls++
		return skillimports.Resolution{}, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "update", "acme"}) })
	if rc != 0 || errOut != "" || !strings.Contains(out, "no valid stored resolution") || calls != 0 {
		t.Fatalf("update without record rc=%d stdout=%q stderr=%q calls=%d", rc, out, errOut, calls)
	}
	store, commit := seedSkillsCLIStore(t, fx.workspace)
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/org/repo", Ref: "main", Path: "skills"}
	if err := store.WriteResolution(cliResolution(entry, commit, "main")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errOut = captureStd(t, func() int { return registry["skills"]([]string{"skills", "remove", "acme"}) })
	if rc != 0 || errOut != "" || !strings.Contains(out, "store and resolution record were left alone") {
		t.Fatalf("remove rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	after, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil || string(after) != string(before) || !store.HasCommit(commit) {
		t.Fatalf("remove changed host state: read=%v stored=%t", err, store.HasCommit(commit))
	}
}

func TestSkillsHostListIsReadOnlyAndShowsResolution(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\n")
	store, commit := seedSkillsCLIStore(t, fx.workspace)
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/org/repo", Ref: "main", Path: "skills"}
	if err := store.WriteResolution(cliResolution(entry, commit, "main")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || errOut != "" || !strings.Contains(out, commit) || !strings.Contains(out, "current Git access=anonymous") || !strings.Contains(out, "skills that would be projected") {
		t.Fatalf("host list rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	after, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("list changed resolution: %v", err)
	}
}

func TestSkillsHostAddTrackStoresMovingRef(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "")
	store, commit := seedSkillsCLIStore(t, fx.workspace)
	oldResolver := resolveSkillImportForCommand
	resolveSkillImportForCommand = func(_ context.Context, gotStore *skillimports.Store, entry config.SkillImport,
		_ skillimports.Authority) (skillimports.Resolution, error) {
		if entry.Handle != "repo" || entry.Ref != "" || entry.Rev != "" {
			t.Fatalf("track request = %+v", entry)
		}
		record := cliResolution(entry, commit, "main")
		if err := gotStore.WriteResolution(record); err != nil {
			t.Fatal(err)
		}
		return record, nil
	}
	t.Cleanup(func() { resolveSkillImportForCommand = oldResolver })
	rc, out, errOut := captureStd(t, func() int {
		return registry["skills"]([]string{"skills", "add", "https://example.test/org/repo.git", "--track"})
	})
	if rc != 0 || errOut != "" || !strings.Contains(out, "moving ref") {
		t.Fatalf("track add rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	cfg, err := config.LoadReadableConfig(fx.workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := config.SkillImports(cfg)
	if len(entries) != 1 || entries[0].Handle != "repo" || entries[0].Ref != "main" || entries[0].Rev != "" {
		t.Fatalf("track config entry = %+v", entries)
	}
	if record, ok := store.Resolution("repo", entries[0]); !ok || record.Commit != commit || record.Ref != "main" {
		t.Fatalf("track record = %+v valid=%t", record, ok)
	}
}

func TestSkillsHostListShowsPerDestinationShadowsWithoutWriting(t *testing.T) {
	fx := newSkillsCLIWorkspace(t, "[skill_imports.acme]\ngit = \"https://example.test/org/repo\"\nref = \"main\"\npath = \"skills\"\n")
	store, commit := seedSkillsCLIStoreWithSkills(t, fx.workspace, []string{
		"pack-collision", "local-collision", "configuring-the-jail", "diagnosing-the-jail", "workspace-collision", "yolo-lsp",
	})
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/org/repo", Ref: "main", Path: "skills"}
	if err := store.WriteResolution(cliResolution(entry, commit, "main")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.AgentsDir()); !os.IsNotExist(err) {
		t.Fatalf("fixture staging already exists at %s: %v", paths.AgentsDir(), err)
	}
	workspacePack, localPack := t.TempDir(), t.TempDir()
	writeInventorySkill(t, fx.workspace, ".agents/skills", "workspace-collision")
	writeInventorySkill(t, workspacePack, "skills", "pack-collision")
	writeInventorySkill(t, workspacePack, "skills", "configuring-the-jail")
	writeInventorySkill(t, localPack, "skills", "local-collision")
	oldInventory := readSkillInventorySourcesForCommand
	readSkillInventorySourcesForCommand = func(string) (run.SkillInventorySources, error) {
		return run.SkillInventorySources{
			WorkspaceDirs: []string{".agents/skills"},
			PackSources: []jailcontent.PackSkillSource{
				{Pack: "copilot-pack", Dir: filepath.Join(workspacePack, "skills"), Agents: []string{"copilot"}},
				{Pack: "local", Dir: filepath.Join(localPack, "skills")},
			},
			Targets: []jailcontent.SkillTarget{
				{Dest: ".copilot/skills", Agent: "copilot"},
				{Dest: ".codex/skills", Agent: "codex"},
			},
		}, nil
	}
	oldResolver := resolveSkillImportForCommand
	resolverCalls := 0
	resolveSkillImportForCommand = func(context.Context, *skillimports.Store, config.SkillImport,
		skillimports.Authority) (skillimports.Resolution, error) {
		resolverCalls++
		return skillimports.Resolution{}, os.ErrPermission
	}
	t.Cleanup(func() {
		readSkillInventorySourcesForCommand = oldInventory
		resolveSkillImportForCommand = oldResolver
	})
	beforeConfig := readFile(t, fx.project)
	beforeRecord, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errOut := captureStd(t, func() int { return registry["skills"]([]string{"skills", "list"}) })
	if rc != 0 || errOut != "" || resolverCalls != 0 {
		t.Fatalf("list rc=%d stdout=%q stderr=%q resolver calls=%d", rc, out, errOut, resolverCalls)
	}
	for _, want := range []string{
		"pack-collision at ~/.copilot/skills by pack copilot-pack",
		"configuring-the-jail at ~/.copilot/skills by pack copilot-pack",
		"local-collision at ~/.copilot/skills by pack local",
		"diagnosing-the-jail at ~/.copilot/skills by yolo's built-in skills",
		"workspace-collision at ~/.copilot/skills by workspace skill source .agents/skills",
		"local-collision at ~/.codex/skills by pack local",
		"configuring-the-jail at ~/.codex/skills by yolo's built-in skills",
		"yolo-lsp at ~/.codex/skills by yolo's LSP plugin",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks exact shadow %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pack-collision at ~/.codex/skills") || strings.Contains(out, "unique at ~/.") {
		t.Fatalf("list reported a name outside that destination's audience or one not shadowed:\n%s", out)
	}
	if readFile(t, fx.project) != beforeConfig {
		t.Fatal("list changed workspace config")
	}
	afterRecord, err := os.ReadFile(store.RecordPath("acme"))
	if err != nil || string(afterRecord) != string(beforeRecord) {
		t.Fatalf("list changed the resolution record: %v", err)
	}
	if _, err := os.Stat(paths.AgentsDir()); !os.IsNotExist(err) {
		t.Fatalf("list created staging at %s: %v", paths.AgentsDir(), err)
	}
}

func newSkillsCLIWorkspace(t *testing.T, project string) skillsCLIWorkspace {
	return newSkillsCLIWorkspaceWithUser(t, project, "packs = [\"git\", \"github\"]\n")
}

type skillsCLIWorkspace struct{ workspace, project, userConfig string }

func newSkillsCLIWorkspaceWithUser(t *testing.T, project, userConfig string) skillsCLIWorkspace {
	t.Helper()
	ws := testconfig.Workspace(t, project)
	user := testconfig.User(t, userConfig)
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	withConfigEditCWD(t, ws.Root)
	return skillsCLIWorkspace{workspace: ws.Root, project: ws.Path, userConfig: user.Path}
}

func seedSkillsCLIStoreWithSkills(t *testing.T, workspace string, names []string) (*skillimports.Store, string) {
	t.Helper()
	store, err := skillimports.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source, work := filepath.Join(root, "source.git"), filepath.Join(root, "work")
	cliGit(t, "", "init", "--bare", "--template=", source)
	cliGit(t, "", "clone", source, work)
	cliGit(t, work, "config", "user.name", "skills test")
	cliGit(t, work, "config", "user.email", "skills@example.test")
	for _, name := range names {
		dir := filepath.Join(work, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cliGit(t, work, "add", "skills")
	cliGit(t, work, "-c", "commit.gpgsign=false", "commit", "-m", "skills")
	commit := strings.TrimSpace(cliGit(t, work, "rev-parse", "HEAD"))
	cliGit(t, work, "push", "origin", "HEAD:refs/heads/main")
	cliGit(t, "", "--git-dir", store.ObjectDirectory(), "fetch", source, commit)
	return store, commit
}

func writeInventorySkill(t *testing.T, root, rel, name string) {
	t.Helper()
	dir := filepath.Join(root, rel, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedSkillsCLIStore(t *testing.T, workspace string) (*skillimports.Store, string) {
	t.Helper()
	store, err := skillimports.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	return store, seedSkillsCLICommit(t, store, "first")
}

func seedSkillsCLICommit(t *testing.T, store *skillimports.Store, text string) string {
	t.Helper()
	root := t.TempDir()
	source, work := filepath.Join(root, "source.git"), filepath.Join(root, "work")
	cliGit(t, "", "init", "--bare", "--template=", source)
	cliGit(t, "", "clone", source, work)
	cliGit(t, work, "config", "user.name", "skills test")
	cliGit(t, work, "config", "user.email", "skills@example.test")
	if err := os.WriteFile(filepath.Join(work, "SKILL.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cliGit(t, work, "add", "SKILL.md")
	cliGit(t, work, "-c", "commit.gpgsign=false", "commit", "-m", text)
	commit := strings.TrimSpace(cliGit(t, work, "rev-parse", "HEAD"))
	cliGit(t, work, "push", "origin", "HEAD:refs/heads/main")
	cliGit(t, "", "--git-dir", store.ObjectDirectory(), "fetch", source, commit)
	return commit
}

func cliResolution(entry config.SkillImport, commit, ref string) skillimports.Resolution {
	return skillimports.Resolution{Version: 1, Handle: entry.Handle, Git: entry.Git, Ref: ref,
		ResolvedRef: "refs/heads/" + ref, Repo: "example.test/org/repo", Commit: commit,
		AccessClass: skillimports.AccessAnonymous, ResolvedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
}

func cliGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
