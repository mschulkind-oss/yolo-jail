package check

import (
	"bytes"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSectionPacksSourcedDeliveredRecordDoesNotReadAbsentHostStore(t *testing.T) {
	repo := checkGitRepo(t)
	source := "git+file://" + repo + "//sub/skills/s?ref=main"
	owner := t.TempDir()
	manifest := []byte(fmt.Sprintf("[[skills]]\nsource = %q\nas = \"registered\"\n", source))
	if err := os.WriteFile(filepath.Join(owner, "pack.toml"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, fmt.Sprintf(`{"packs":[{"name":"selected","source":%q}]}`, "file://"+owner))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local/share"))
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	var selected config.PackEntry
	for _, e := range entries {
		if e.Name == "selected" {
			selected = e
		}
	}
	root := t.TempDir()
	decl := filepath.Join(root, selected.Slug())
	if err := os.MkdirAll(decl, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decl, "pack.toml"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(decl, "selected")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	packs := []*packload.Pack{p}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	result := packload.ResolveSelectedSkillPayloads(packs, store, packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath())}, true)[0]
	if result.State != packsrc.SkillReady {
		t.Fatalf("writer %+v", result)
	}
	if err := packload.StageSkillPayloads(packs, root, root); err != nil {
		t.Fatal(err)
	}
	if err := packload.WritePackTreeRecordForWorkspace(root, packs, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = packsrc.RemoveSealedSkillTree(filepath.Dir(root), root) })
	if err := os.RemoveAll(owner); err != nil {
		t.Fatal(err)
	}
	if err := packsrc.RemoveSealedSkillTree(filepath.Dir(store.Dir), store.Dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_PACK_ROOT", root)
	var output bytes.Buffer
	getEnv := func(k string) string {
		if k == "YOLO_PACK_ROOT" {
			return root
		}
		return hostEnv(k)
	}
	(&Options{Getenv: getEnv}).sectionPacks(&reporter{w: &output}, jsonx.NewOrderedMap())
	text := output.String()
	if strings.Contains(text, "not fetched") || !strings.Contains(text, "selected Git registration validated at "+result.Commit) {
		t.Fatalf("recorded delivery re-read absent host store:\n%s", text)
	}
	if _, err := os.Lstat(store.Dir); !os.IsNotExist(err) {
		t.Fatalf("reader created host store: %v", err)
	}
}
