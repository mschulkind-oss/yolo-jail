package configmigrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// gitIn runs git in dir with no user or system config, so the test sees only its own repository.
// Automatic maintenance is off: on macOS a commit forks a detached `git maintenance run --auto`
// whose lock file (.git/objects/maintenance.lock) appears and vanishes while treeState walks the
// repository, which failed the walk with "no such file or directory" (CI, 2026-11-02).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	pre := []string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}
	cmd := exec.Command("git", append(pre, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// dotfilesRepo makes dir a git repository with everything in it committed: a stand-in for a
// dotfiles tree the conversion must leave exactly as it found it.
func dotfilesRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "dotfiles")
}

// The owner's ruling of 2026-11-01 (RC-D41): a config file reached through a symlink is never
// written through. The link itself moves to <name>.pre-migrate, the converted file is a regular
// file at the new name beside the link, and the link's target and its directory are untouched:
// here a dotfiles git repository stays byte-identical and clean, for a linked top-level file and
// a linked include alike.
func TestALinkedConfigIsMovedAndItsTargetIsNeverTouched(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	repo := filepath.Join(home, "dotfiles")
	dots := filepath.Join(repo, "yolo")
	write(t, filepath.Join(dots, "config.jsonc"), `{"packs": ["claude"], "include_if_found": ["work.jsonc"]}`+"\n")
	write(t, filepath.Join(dots, "work.jsonc"), `{"packages": ["ripgrep"]}`+"\n")
	dotfilesRepo(t, repo)
	dotsBefore := treeState(t, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{"config.jsonc": "../../dotfiles/yolo/config.jsonc", "work.jsonc": "../../dotfiles/yolo/work.jsonc"}
	for name, text := range links {
		if err := os.Symlink(text, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := config.ReadMigrationGraph(filepath.Join(dir, "config.jsonc"), "")
	if err != nil {
		t.Fatal(err)
	}

	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal {
		t.Fatalf("unequal: %+v %+v", p.Differences, p.NotConverted)
	}
	top := target(t, p, filepath.Join(dir, "config.jsonc"))
	if top.New != filepath.Join(dir, "config.toml") || top.Backup != filepath.Join(dir, "config.jsonc.pre-migrate") || top.LinkText != links["config.jsonc"] {
		t.Fatalf("top target = %+v", top)
	}
	if !strings.Contains(string(top.Bytes), `include_if_found = ["work.toml"]`) {
		t.Fatalf("include not respelled:\n%s", top.Bytes)
	}
	if text := p.Transcript(); !strings.Contains(text, filepath.Join(dir, "work.jsonc")+" is a link to "+filepath.Join(dots, "work.jsonc")) {
		t.Fatalf("the transcript does not name the link:\n%s", text)
	}

	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := treeState(t, repo); !reflect.DeepEqual(got, dotsBefore) {
		t.Fatalf("the dotfiles tree changed:\ngot  %v\nwant %v", got, dotsBefore)
	}
	if s := gitIn(t, repo, "status", "--porcelain", "--ignored"); s != "" {
		t.Fatalf("git status in the dotfiles repository is not clean:\n%s", s)
	}
	for name, text := range links {
		if got, err := os.Readlink(filepath.Join(dir, name+".pre-migrate")); err != nil || got != text {
			t.Fatalf("%s.pre-migrate = %q (%v), want the original link to %s", name, got, err, text)
		}
		if exists(filepath.Join(dir, name)) {
			t.Fatalf("the old link %s is still there", name)
		}
	}
	for _, name := range []string{"config.toml", "work.toml"} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s is not a regular file: %v", name, err)
		}
	}
	want := LinkedFile{Link: filepath.Join(dir, "config.jsonc"), Backup: filepath.Join(dir, "config.jsonc.pre-migrate"),
		Target: filepath.Join(dots, "config.jsonc"), New: filepath.Join(dir, "config.toml")}
	if len(res.Links) != 2 || res.Links[0] != want {
		t.Fatalf("Links = %+v, want each moved link with its target and new file, first %+v", res.Links, want)
	}
	after, err := config.ReadMigrationGraph(filepath.Join(dir, "config.toml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if describe(after.User) != describe(before.User) {
		t.Fatalf("the effective user config changed:\nbefore %s\nafter  %s", describe(before.User), describe(after.User))
	}
	if v, _ := after.User.Get("install"); describe(v) != `{"nix": ["ripgrep"]}` {
		t.Fatalf("migrated user graph install = %s", describe(v))
	}
}

// A link to a link is moved like any other link: only the user's own link moves, its text kept
// verbatim, and neither hop changes.
func TestALinkToALinkIsMovedAsItIs(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	dots := filepath.Join(home, "dotfiles")
	write(t, filepath.Join(dots, "real.jsonc"), `{"packages": ["jq"]}`+"\n")
	if err := os.Symlink("real.jsonc", filepath.Join(dots, "hop.jsonc")); err != nil {
		t.Fatal(err)
	}
	dotsBefore := treeState(t, dots)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dots, "hop.jsonc"), filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan(t, Request{UserConfigDir: dir}), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := treeState(t, dots); !reflect.DeepEqual(got, dotsBefore) {
		t.Fatalf("the dotfiles tree changed:\ngot  %v\nwant %v", got, dotsBefore)
	}
	if got, err := os.Readlink(filepath.Join(dir, "config.jsonc.pre-migrate")); err != nil || got != filepath.Join(dots, "hop.jsonc") {
		t.Fatalf("backup link = %q (%v)", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "config.toml")); err != nil || !strings.Contains(string(b), "jq") {
		t.Fatalf("config.toml = %q (%v)", b, err)
	}
}

// A linked include whose own include entries are read beside its target cannot become a regular
// file beside the link without those entries reading somewhere else, so the plan refuses it,
// naming the way out, before anything is written.
func TestALinkedIncludeWithIncludesBesideItsTargetIsRefused(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	dots := filepath.Join(home, "dotfiles")
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": ["work.jsonc"]}`+"\n")
	write(t, filepath.Join(dots, "work.jsonc"), `{"include_if_found": ["more.jsonc"]}`+"\n")
	write(t, filepath.Join(dots, "more.jsonc"), `{"packages": ["jq"]}`+"\n")
	if err := os.Symlink(filepath.Join(dots, "work.jsonc"), filepath.Join(dir, "work.jsonc")); err != nil {
		t.Fatal(err)
	}
	dotsBefore := treeState(t, dots)
	g, err := Discover(Request{UserConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Plan(g)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "work.jsonc")+" is a link to "+filepath.Join(dots, "work.jsonc")) ||
		!strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Plan = %v, want the linked include refused", err)
	}
	if got := treeState(t, dots); !reflect.DeepEqual(got, dotsBefore) {
		t.Fatalf("the dotfiles tree changed:\ngot  %v\nwant %v", got, dotsBefore)
	}
}
