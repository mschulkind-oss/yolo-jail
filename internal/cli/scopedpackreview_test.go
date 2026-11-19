package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestPackAddAndIndexedEditValidateWholeSubdirInsteadOfParent(t *testing.T) {
	project := testconfig.Workspace(t, "")
	original := "# preserve this comment\npacks=[]\n"
	user := testconfig.User(t, original)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(project.Root)
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(root, "pack.toml"), "broken parent TOML [\n")
	scopedCLIWrite(t, filepath.Join(root, "sibling/pack.toml"), "needs=[{pack='must-not-resolve-sibling'}]\n")
	for _, dir := range []string{"first", "second"} {
		scopedCLIWrite(t, filepath.Join(root, dir, "pack.toml"), "needs=[{pack='codex-auth'}]\n[[skills]]\nagent='fixture'\ninto='.fixture/skills'\n")
		scopedCLIWrite(t, filepath.Join(root, dir, "skills/chosen/SKILL.md"), dir+"\n")
	}
	before := scopedCLISnapshot(t, root, paths.PacksDir())
	rc, out, errw := dispatchScopedPackCapture(t, []string{"add", "local/corpus", "--subdir", "first"})
	if rc != 0 || !strings.Contains(out, "Added") {
		t.Fatalf("actual whole-subdir add: %d %s %s", rc, out, errw)
	}
	rc, out, errw = dispatchConfigCapture(t, []string{"set", "packs[0].subdir", `"second"`, "--user"})
	if rc != 0 || !strings.Contains(out, "Written.") {
		t.Fatalf("actual indexed whole-subdir edit: %d %s %s", rc, out, errw)
	}
	if after := scopedCLISnapshot(t, root, paths.PacksDir()); !reflect.DeepEqual(before, after) {
		t.Fatal("validation wrote source/store")
	}
	accepted, err := os.ReadFile(user.Path)
	if err != nil || !strings.Contains(string(accepted), "# preserve this comment") || !strings.Contains(string(accepted), "second") {
		t.Fatalf("transaction bytes: %s %v", accepted, err)
	}
	for _, dir := range []string{"sibling", "missing"} {
		rc, out, errw = dispatchConfigCapture(t, []string{"set", "packs[0].subdir", fmt.Sprintf("%q", dir), "--user"})
		if rc == 0 {
			t.Fatalf("invalid selected root accepted: %s %s", out, errw)
		}
		if data, _ := os.ReadFile(user.Path); string(data) != string(accepted) {
			t.Fatal("refused indexed edit changed bytes")
		}
	}
	// The selected root, not its parent or sibling, supplies provider declarations.
	scopedCLIWrite(t, filepath.Join(root, "second/pack.toml"), "[providers.selected]\npinned_endpoint=true\nendpoints.openai.base_url='https://example.invalid'\nendpoints.openai.wire_api='openai-chat-completions'\n")
	rc, out, errw = dispatchConfigCapture(t, []string{"set", "providers.selected.endpoints.openai.base_url", `"https://other.invalid"`, "--user"})
	t.Logf("selected provider refusal: %s", errw)
	if rc == 0 || !strings.Contains(errw, "selected") || !strings.Contains(errw, "pinned") {
		t.Fatalf("selected provider conflict not validated: %d %s %s", rc, out, errw)
	}
	if data, _ := os.ReadFile(user.Path); string(data) != string(accepted) {
		t.Fatal("provider refusal changed bytes")
	}
}

func TestPackAddWholeNamedGitSubdirValidatesSelectedCheckout(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
	}
	git("init", "-q", "--initial-branch=main")
	scopedCLIWrite(t, filepath.Join(repo, "corpus/pack.toml"), "poison parent [\n")
	scopedCLIWrite(t, filepath.Join(repo, "corpus/nested/skills/chosen/SKILL.md"), "nested\n")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	user := testconfig.User(t, fmt.Sprintf("# preserve\npacks=[{pack='demo/corpus',subdir='nested'}]\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", filepath.ToSlash(repo)))
	project := testconfig.Workspace(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(project.Root)
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) != 0 {
		t.Fatalf("fixture entries: %v %v", err, problems)
	}
	var entry config.PackEntry
	for _, candidate := range entries {
		if candidate.Name == "demo/corpus" {
			entry = candidate
		}
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	refreshed, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
	if err != nil || len(refreshed) != 1 || refreshed[0].Err != nil {
		t.Fatalf("fixture refresh: %+v %v", refreshed, err)
	}
	scopedCLIWrite(t, user.Path, fmt.Sprintf("# preserve\npacks=[]\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", filepath.ToSlash(repo)))
	before := scopedCLISnapshot(t, repo, paths.PacksDir())
	rc, out, errw := dispatchScopedPackCapture(t, []string{"add", "demo/corpus", "--subdir", "nested"})
	if rc != 0 || !strings.Contains(out, "Added") {
		t.Fatalf("actual fetched whole-subdir add: %d %s %s", rc, out, errw)
	}
	if after := scopedCLISnapshot(t, repo, paths.PacksDir()); !reflect.DeepEqual(before, after) {
		t.Fatal("whole Git validation wrote source/store")
	}
	if data, _ := os.ReadFile(user.Path); !strings.Contains(string(data), "# preserve") || !strings.Contains(string(data), "nested") {
		t.Fatalf("Git transaction bytes: %s", data)
	}
}

func TestIndexedConfigEditValidatesWholeSubdirWithPoisonParent(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "# keep\npacks=[{pack='local/corpus',subdir='first'}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(project.Root)
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(root, "pack.toml"), "poison parent [\n")
	for _, dir := range []string{"first", "second"} {
		scopedCLIWrite(t, filepath.Join(root, dir, "skills/chosen/SKILL.md"), dir+"\n")
	}
	before := scopedCLISnapshot(t, root, paths.PacksDir())
	rc, out, errw := dispatchConfigCapture(t, []string{"set", "packs[0].subdir", `"second"`, "--user"})
	if rc != 0 || !strings.Contains(out, "Written.") {
		t.Fatalf("independent actual indexed edit: %d %s %s", rc, out, errw)
	}
	if data, _ := os.ReadFile(user.Path); !strings.Contains(string(data), "# keep") || !strings.Contains(string(data), "second") {
		t.Fatalf("indexed transaction bytes: %s", data)
	}
	if after := scopedCLISnapshot(t, root, paths.PacksDir()); !reflect.DeepEqual(before, after) {
		t.Fatal("indexed validation wrote source/store")
	}
}

func TestConfigProjectEditWholeSubdirDistinguishesSourceAndDeliveredRoots(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("delivered=%v", delivered), func(t *testing.T) {
			project := testconfig.Workspace(t, "[machine.resources]\ncpus=1\n")
			user := testconfig.User(t, "packs=[{pack='local/corpus',subdir='nested'}]\n")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_PACK_ROOT", "")
			root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
			scopedCLIWrite(t, filepath.Join(root, "pack.toml"), "poison parent [\n")
			scopedCLIWrite(t, filepath.Join(root, "nested/skills/chosen/SKILL.md"), "nested\n")
			entries, problems, err := config.LoadPackEntries()
			if err != nil || len(problems) != 0 {
				t.Fatalf("fixture entries: %v %v", err, problems)
			}
			var entry config.PackEntry
			for _, candidate := range entries {
				if candidate.Name == "local/corpus" {
					entry = candidate
				}
			}
			tree := t.TempDir()
			if !delivered {
				scopedCLIWrite(t, filepath.Join(tree, entry.Slug(), "pack.toml"), "poison delivered decoy [\n")
			}
			if delivered {
				if _, err := config.ResolvePack(entry, config.ResolvePackSpec{Dest: filepath.Join(tree, entry.Slug())}); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("YOLO_VERSION", "test-jail")
			t.Setenv("YOLO_WORKSPACE", project.Root)
			t.Setenv("YOLO_PACK_ROOT", tree)
			t.Chdir(project.Root)
			before := scopedCLISnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), tree)
			rc, out, errw := dispatchConfigCapture(t, []string{"set", "machine.resources.cpus", "2", "--project"})
			if rc != 0 || strings.Contains(out+errw, "was not checked") {
				t.Fatalf("source/delivered whole root: %d %s %s", rc, out, errw)
			}
			if after := scopedCLISnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), tree); !reflect.DeepEqual(before, after) {
				t.Fatal("whole-root jail edit changed source/store/delivered bytes")
			}
			if !delivered {
				if err := os.RemoveAll(filepath.Join(root, "nested")); err != nil {
					t.Fatal(err)
				}
				accepted, _ := os.ReadFile(project.Path)
				rc, out, errw = dispatchConfigCapture(t, []string{"set", "machine.resources.cpus", "3", "--project"})
				if rc == 0 || strings.Contains(out+errw, "was not checked") {
					t.Fatalf("invalid readable whole root was skipped: %d %s %s", rc, out, errw)
				}
				if data, _ := os.ReadFile(project.Path); string(data) != string(accepted) {
					t.Fatal("invalid root refusal changed project bytes")
				}
			}
		})
	}
}

func TestPackExplainRefusesCachedScopedPayloadLicenseConflictWithoutWrites(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
	}
	git("init", "-q", "--initial-branch=main")
	for name, data := range map[string]string{"safe/SKILL.md": "safe\n", "chosen/notes.md": "folder\n", "chosen/LICENSE": "one\n", "chosen/LICENSE.txt": "two\n"} {
		scopedCLIWrite(t, filepath.Join(repo, "corpus/skills", name), data)
	}
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	body := func(name string) string {
		return fmt.Sprintf("packs=[{pack='demo/corpus',skills={only=[%q]}}]\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", name, filepath.ToSlash(repo))
	}
	user := testconfig.User(t, body("safe"))
	project := testconfig.Workspace(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) != 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	var entry config.PackEntry
	for _, candidate := range entries {
		if candidate.Name == "demo/corpus" {
			entry = candidate
		}
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	refreshed, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
	if err != nil || len(refreshed) != 1 || refreshed[0].Err != nil {
		t.Fatalf("fixture refresh: %+v %v", refreshed, err)
	}
	resolved, err := config.ResolvePack(entry, config.ResolvePackSpec{ReadOnlyStore: true})
	if err != nil {
		t.Fatal(err)
	}
	inspectState := func(state string) {
		t.Helper()
		before := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir())
		rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "demo/corpus"})
		if rc != 0 || !strings.Contains(out, state) || !strings.Contains(out, resolved.Pack.Projection.Commit) {
			t.Fatalf("cached payload state %s: %d %s %s", state, rc, out, errw)
		}
		if after := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
			t.Fatal("read-only payload state inspection wrote")
		}
	}
	inspectState("NOT FETCHED")
	results := packload.ResolveSelectedSkillPayloads([]*packload.Pack{resolved.Pack}, store, packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath())}, true)
	if len(results) != 1 || results[0].State != packsrc.SkillReady {
		t.Fatalf("existing shared repository/ref record: %+v", results)
	}
	scopedCLIWrite(t, user.Path, body("chosen"))
	before := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir())
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "demo/corpus"})
	if rc == 0 || !strings.Contains(errw, "conflicting selected licenses") || !strings.Contains(errw, "selected skill payload refused for") || !strings.Contains(errw, "demo/corpus") || !strings.Contains(errw, "skills[0]") || !strings.Contains(errw, "chosen") {
		t.Fatalf("cached payload refusal discarded: %d %s %s", rc, out, errw)
	}
	if strings.Contains(out, "inspected: cached commit") {
		t.Fatalf("refused selection claimed inspected: %s", out)
	}
	if after := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
		t.Fatal("Explain refusal wrote config/source/shared store")
	}
	scopedCLIWrite(t, user.Path, body("safe"))
	if err := packsrc.RemoveSealedSkillTree(user.Root, results[0].Payload); err != nil {
		t.Fatal(err)
	}
	inspectState("NOT MATERIALIZED")
}

func TestConfigProjectEditSkipsUnavailableScopedLocalSourceWithoutRevalidatingDeliveredCopy(t *testing.T) {
	project := testconfig.Workspace(t, "[machine.resources]\ncpus=1\n")
	user := testconfig.User(t, "packs=[{pack='local/corpus',skills={only=['chosen']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(root, "skills/chosen/SKILL.md"), "chosen\n")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) != 0 {
		t.Fatalf("fixture entries: %v %v", err, problems)
	}
	var entry config.PackEntry
	for _, candidate := range entries {
		if candidate.Name == "local/corpus" {
			entry = candidate
		}
	}
	tree := t.TempDir()
	resolved, err := config.ResolvePack(entry, config.ResolvePackSpec{Dest: filepath.Join(tree, "local-corpus")})
	if err != nil {
		t.Fatal(err)
	}
	if err := packload.WritePackTreeRecord(tree, []*packload.Pack{resolved.Pack}); err != nil {
		t.Fatal(err)
	}
	records, recorded, err := packload.ReadPackTreeRecord(tree)
	if err != nil || !recorded || len(records) != 1 {
		t.Fatalf("recorded fixture: %v %v %v", records, recorded, err)
	}
	if _, err := records[0].LoadRecordedScopedPack(tree); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_VERSION", "test-jail")
	t.Setenv("YOLO_WORKSPACE", project.Root)
	t.Setenv("YOLO_PACK_ROOT", tree)
	t.Chdir(project.Root)
	before := scopedCLISnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), tree)
	rc, out, errw := dispatchConfigCapture(t, []string{"set", "machine.resources.cpus", "2", "--project"})
	if rc != 0 || !strings.Contains(out+errw, "was not checked") || !strings.Contains(out+errw, "host") {
		t.Fatalf("jailed actual project edit: %d %s %s", rc, out, errw)
	}
	if data, _ := os.ReadFile(project.Path); !strings.Contains(string(data), "2") {
		t.Fatalf("project not written: %s", data)
	}
	if after := scopedCLISnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), tree); !reflect.DeepEqual(before, after) {
		t.Fatal("jailed edit changed user/source/store/delivered record")
	}
	// A readable source remains authoritative and invalid selectors still refuse.
	scopedCLIWrite(t, filepath.Join(root, "skills/other/SKILL.md"), "other\n")
	for _, jail := range []string{"", "test-jail"} {
		t.Setenv("YOLO_VERSION", jail)
		accepted, _ := os.ReadFile(project.Path)
		rc, out, errw = dispatchConfigCapture(t, []string{"set", "machine.resources.cpus", "3", "--project"})
		if rc == 0 || !strings.Contains(errw, "available names") {
			t.Fatalf("readable invalid source swallowed: %d %s %s", rc, out, errw)
		}
		if data, _ := os.ReadFile(project.Path); string(data) != string(accepted) {
			t.Fatal("invalid source refusal wrote project")
		}
	}
}
