package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationRefusesDeclaredCallRootsBeforeTouchingOriginals(t *testing.T) {
	home := env(t)
	ws := realDir(t)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":["aws"]}`)
	file := filepath.Join(ws, "yolo-jail.jsonc")
	old := `{"brokered":{"aws":{"roots":["s3://bkt/team/","ce://host-login"]}}}`
	write(t, file, old)
	graph, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(graph)
	if err == nil || !strings.Contains(err.Error(), "call_roots.aws") {
		t.Fatalf("plan=%+v error=%v", p, err)
	}
	if p != nil {
		if _, err := Apply(p, ApplyOptions{}); err == nil {
			t.Fatal("unsafe plan applied")
		}
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != old {
		t.Fatalf("original changed: %q %v", data, err)
	}
	for _, name := range []string{"yolo-jail.toml", "yolo-jail.jsonc.pre-migrate"} {
		if exists(filepath.Join(ws, name)) {
			t.Fatalf("refusal created %s", name)
		}
	}
}
