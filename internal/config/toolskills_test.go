package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func skillsCfg(t *testing.T, doc string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.OrderedMap)
}

// MS-D1: the allowlist is the user scope's install.mise names in declaration order, off excluded.
func TestMiseSkillToolsAreTheUserScopesTablesNamesInOrderWithoutOff(t *testing.T) {
	user := skillsCfg(t, `{"install": {"mise": {"zeta": "1", "alpha": "off", "mid": "2", "beta": "latest"}}}`)
	got := MiseSkillTools(user, false, false, false)
	if want := []string{"zeta", "mid", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowlist = %v, want %v (declaration order, off excluded)", got, want)
	}
}

func TestMiseSkillToolsHonorTheLegacySpelling(t *testing.T) {
	user := skillsCfg(t, `{"mise_tools": {"jq": "1.8.0", "gone": "off", "ripgrep": "14"}}`)
	got := MiseSkillTools(user, false, false, false)
	if want := []string{"jq", "ripgrep"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowlist = %v, want %v", got, want)
	}
}

func TestMiseSkillToolsAreEmptyWhenSealedCapturedOrNested(t *testing.T) {
	user := skillsCfg(t, `{"install": {"mise": {"jq": "1"}}}`)
	for name, args := range map[string][3]bool{
		"sealed": {true, false, false}, "capture": {false, true, false}, "nested": {false, false, true},
	} {
		got := MiseSkillTools(user, args[0], args[1], args[2])
		if got == nil || len(got) != 0 {
			t.Errorf("%s: allowlist = %#v, want empty non-nil", name, got)
		}
	}
	if got := MiseSkillTools(nil, false, false, false); len(got) != 0 {
		t.Errorf("no user scope: allowlist = %v, want empty", got)
	}
}

func TestToolSkillsTokenIsStableAndStoredOutsideTheWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := ToolSkillsToken(ws)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ToolSkillsToken(ws)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 64 {
		t.Fatalf("token not stable across launches or not 32 bytes hex: %q then %q", a, b)
	}
	other, _ := filepath.EvalSymlinks(t.TempDir())
	if c, _ := ToolSkillsToken(other); c == a {
		t.Fatalf("two workspaces share a token")
	}
	if entries, _ := os.ReadDir(ws); len(entries) != 0 {
		t.Fatalf("the workspace gained files: %v", entries)
	}
	var found string
	_ = filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.TrimSpace(string(b)) == a {
				found = p
			}
		}
		return nil
	})
	if found == "" || !strings.HasPrefix(found, home) {
		t.Fatalf("token not stored under the host home; found %q", found)
	}
	if fi, _ := os.Stat(found); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(found)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("token folder mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestToolSkillsTokenReplacesAGarbageFileNeverFollowingALink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	first, err := ToolSkillsToken(ws)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	_ = filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".token") {
			path = p
		}
		return nil
	})
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	next, err := ToolSkillsToken(ws)
	if err != nil || next == first || len(next) != 64 {
		t.Fatalf("a linked token file was trusted: %q %v", next, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("the link target was written: %q", b)
	}
}
