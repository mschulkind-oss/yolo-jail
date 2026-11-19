package check

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestSectionPacksRefusesCachedScopedPayloadLicenseConflictWithoutWrites(t *testing.T) {
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
	put := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "--initial-branch=main")
	for name, data := range map[string]string{"safe/SKILL.md": "safe\n", "chosen/notes.md": "folder\n", "chosen/LICENSE": "one\n", "chosen/LICENSE.txt": "two\n"} {
		put(filepath.Join(repo, "corpus/skills", name), data)
	}
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	body := func(name string) string {
		return fmt.Sprintf("packs=[{pack='demo/corpus',skills={only=[%q]}}]\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", name, filepath.ToSlash(repo))
	}
	user := testconfig.User(t, body("safe"))
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
		before := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), repo)
		var out bytes.Buffer
		r := newReporter(&out, false)
		(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
		if r.failed != 0 || !strings.Contains(out.String(), state) || !strings.Contains(out.String(), resolved.Pack.Projection.Commit) {
			t.Fatalf("cached scoped payload state %s: %s", state, out.String())
		}
		if after := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), repo); !reflect.DeepEqual(before, after) {
			t.Fatal("read-only scoped payload state inspection wrote")
		}
	}
	inspectState("NOT FETCHED")
	results := packload.ResolveSelectedSkillPayloads([]*packload.Pack{resolved.Pack}, store, packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath())}, true)
	if len(results) != 1 || results[0].State != packsrc.SkillReady {
		t.Fatalf("existing shared repository/ref record: %+v", results)
	}
	put(user.Path, body("chosen"))
	before := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), repo)
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed == 0 || !strings.Contains(out.String(), "conflicting selected licenses") || !strings.Contains(out.String(), "selected skill payload refused for") || !strings.Contains(out.String(), "demo/corpus") || !strings.Contains(out.String(), "skills[0]") || !strings.Contains(out.String(), "chosen") {
		t.Fatalf("cached scoped Check discarded payload refusal: %s", out.String())
	}
	if strings.Contains(out.String(), "inspected: cached commit") {
		t.Fatalf("refused selection claimed inspected: %s", out.String())
	}
	if after := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), repo); !reflect.DeepEqual(before, after) {
		t.Fatal("scoped Check refusal wrote config/source/shared store")
	}
	put(user.Path, body("safe"))
	if err := packsrc.RemoveSealedSkillTree(user.Root, results[0].Payload); err != nil {
		t.Fatal(err)
	}
	inspectState("NOT MATERIALIZED")
}
