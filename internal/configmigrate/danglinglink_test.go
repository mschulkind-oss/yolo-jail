package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RC-D59: `yolo config migrate` refuses a config name that is a link to nothing before planning
// anything, so a dangling name is never counted as "no old file here": not at the top level of
// either scope, not a config.d fragment. An include_if_found entry in that state is the one
// difference: it is not found, and kept as written (RC-D67).
func TestConfigMigrateRefusesADanglingConfigLink(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)

	// The owner's workspace: a dangling yolo-jail.jsonc link beside a TOML file that reads.
	write(t, filepath.Join(ws, "yolo-jail.toml"), "[install]\nnix = ['just']\n")
	link := filepath.Join(ws, "yolo-jail.jsonc")
	if err := os.Symlink("swarf/.links/yolo-jail.jsonc", link); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(Request{Workspace: ws})
	if err == nil || !strings.HasPrefix(err.Error(), link+" is a symbolic link to swarf/.links/yolo-jail.jsonc, which does not exist.") {
		t.Fatalf("workspace discovery over a dangling link: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	// A user config whose include is a link to nothing.
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["claude"], "include_if_found": ["work.jsonc"]}`+"\n")
	inc := filepath.Join(dir, "work.jsonc")
	if err := os.Symlink("../../gone/work.jsonc", inc); err != nil {
		t.Fatal(err)
	}
	// An include is "if found", best effort: a link to nothing is not found, so the migration
	// keeps the entry as it keeps one naming no file (RC-D67).
	g, err := Discover(Request{UserConfigDir: dir})
	if err != nil {
		t.Fatalf("user discovery over a dangling include: %v", err)
	}
	if len(g.Unreachable) != 1 || g.Unreachable[0].Path != inc {
		t.Fatalf("the dangling include is not recorded as not found: %+v", g.Unreachable)
	}
	if err := os.Remove(inc); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(Request{UserConfigDir: dir}); err != nil {
		t.Fatalf("an absent include refused: %v", err)
	}

	// A TOML user config whose config.d fragment is a link to nothing.
	if err := os.Remove(filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config.toml"), "packs = ['claude']\n")
	frag := filepath.Join(dir, "config.d", "work.toml")
	if err := os.MkdirAll(filepath.Dir(frag), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../gone.toml", frag); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(Request{UserConfigDir: dir}); err == nil ||
		!strings.HasPrefix(err.Error(), "config.d/work.toml: "+frag+" is a symbolic link to ../gone.toml") {
		t.Fatalf("user discovery over a dangling config.d fragment: %v", err)
	}
}
