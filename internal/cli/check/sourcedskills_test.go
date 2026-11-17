package check

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestSectionPacksSourcedSkillReaderStatesArePure(t *testing.T) {
	repo := checkGitRepo(t)
	source := "git+file://" + repo + "//sub/skills/s?ref=main"
	owner := t.TempDir()
	if err := os.WriteFile(filepath.Join(owner, "pack.toml"), []byte(fmt.Sprintf("[[skills]]\nsource = %q\nas = \"registered\"\n", source)), 0644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, fmt.Sprintf(`{"packs":[{"name":"selected","source":%q}]}`, "file://"+owner))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local/share"))
	check := func() string {
		t.Helper()
		var output bytes.Buffer
		r := &reporter{w: &output}
		(&Options{Getenv: hostEnv}).sectionPacks(r, jsonx.NewOrderedMap())
		return output.String()
	}
	output := check()
	if !strings.Contains(output, "[SKIP]") || !strings.Contains(output, "pack selected skills[0]: not fetched") || !strings.Contains(output, "yolo pack install") {
		t.Fatalf("unfetched check:\n%s", output)
	}
	assertNoMirrors(t)
	store := &packsrc.Store{Dir: paths.PacksDir()}
	t.Cleanup(func() { _ = packsrc.RemoveSealedSkillTree(filepath.Dir(store.Dir), store.Dir) })
	result := store.ResolveSkillPayloads([]packsrc.SkillRequest{{Label: "selected/skills[0]", Source: source}}, packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath())})[0]
	if result.State != packsrc.SkillReady {
		t.Fatalf("fixture writer: %+v", result)
	}
	digest := func() string {
		t.Helper()
		var output strings.Builder
		_ = filepath.Walk(store.Dir, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			fmt.Fprintf(&output, "%s %s %d\n", p, fi.Mode(), fi.ModTime().UnixNano())
			if fi.Mode().IsRegular() {
				data, _ := os.ReadFile(p)
				output.Write(data)
			}
			return nil
		})
		data, _ := os.ReadFile(packsrc.SkillsLockPath(paths.UserConfigPath()))
		output.Write(data)
		return output.String()
	}
	before := digest()
	output = check()
	if !strings.Contains(output, "selected Git registration validated at "+result.Commit) {
		t.Fatalf("READY check:\n%s", output)
	}
	if digest() != before {
		t.Fatal("READY check mutated sourced state")
	}
	if err := packsrc.RemoveSealedSkillTree(store.Dir, result.Payload); err != nil {
		t.Fatal(err)
	}
	before = digest()
	output = check()
	absent := result
	absent.State = packsrc.SkillNotMaterialized
	if !strings.Contains(output, "[SKIP]") || !strings.Contains(output, absent.Diagnostic("pack selected skills[0]")) || strings.Contains(output, "selected Git registration validated") {
		t.Fatalf("NOT MATERIALIZED check:\n%s", output)
	}
	if digest() != before {
		t.Fatal("NOT MATERIALIZED check wrote/rematerialized")
	}
	// Own-row validation takes precedence over absence: the cached commit's
	// selected ancestor is absent, even though the same latest pair is valid for s.
	missingSource := "git+file://" + repo + "//sub/skills/missing?ref=main"
	if err := os.WriteFile(filepath.Join(owner, "pack.toml"), []byte(fmt.Sprintf("[[skills]]\nsource = %q\nas = \"registered\"\n", missingSource)), 0644); err != nil {
		t.Fatal(err)
	}
	before = digest()
	output = check()
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "sourced skill REFUSED") || strings.Contains(output, "not materialized") {
		t.Fatalf("REFUSED check:\n%s", output)
	}
	if digest() != before {
		t.Fatal("REFUSED check mutated sourced state")
	}
}
