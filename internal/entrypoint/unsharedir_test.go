package entrypoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// unsharedir_test.go covers the unshare_directory hook: undoing the link a shared_directory
// hook left behind, once a pack stops sharing that directory
// (docs/design/pi-git-extension-caching.md §3.5 — c402dd43's `.pi/agent/git` ->
// `.pi-shared-git` link). It must remove exactly that link and nothing else: never follow it,
// never touch its target, a real directory, or a link to anywhere else.

// unshareHome returns a fresh Env and the hook under test, acting on the pi layout the
// retired hook used, so the link target below is the one linkIntoSharedDir really wrote.
func unshareHome(t *testing.T) (*Env, packdecl.Hook) {
	t.Helper()
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	return e, packdecl.Hook{Name: HookUnshareDirectory, File: ".pi/agent/git", SharedDir: ".pi-shared-git"}
}

// plantSharedLink writes the link exactly as linkIntoSharedDir wrote it: relative, from the
// link's parent to the shared dir.
func plantSharedLink(t *testing.T, e *Env, h packdecl.Hook) string {
	t.Helper()
	link := filepath.Join(e.Home, filepath.FromSlash(h.File))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.Rel(filepath.Dir(link), filepath.Join(e.Home, h.SharedDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func requireEmptyRealDir(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v, want an empty real directory", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("%s is %v, want a real directory", path, fi.Mode())
	}
	if ents, _ := os.ReadDir(path); len(ents) != 0 {
		t.Fatalf("%s holds %d entries, want none", path, len(ents))
	}
}

// TestUnshareDirectoryReplacesTheLinkASharedHookLeft: the store behind the link keeps every
// byte (it is another workspace's view too, and the redesign's migration owns it), and the
// workspace gets an empty real directory in the link's place — pi re-clones into it, the
// pre-c402dd43 behavior.
func TestUnshareDirectoryReplacesTheLinkASharedHookLeft(t *testing.T) {
	e, h := unshareHome(t)
	store := filepath.Join(e.Home, h.SharedDir, "github.com", "x", "y")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "index.ts"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := plantSharedLink(t, e, h)

	if err := runPackHook(e, &packload.Pack{Name: "pi"}, h); err != nil {
		t.Fatalf("unshare_directory: %v", err)
	}
	requireEmptyRealDir(t, link)
	if b, err := os.ReadFile(filepath.Join(store, "index.ts")); err != nil || string(b) != "keep" {
		t.Fatalf("the shared store behind the link was touched: %q, %v", b, err)
	}
}

// TestUnshareDirectoryReplacesADanglingLink is the case that motivates the hook: once the
// machine dir is no longer mounted, the link dangles, and pi's next clone into it fails.
func TestUnshareDirectoryReplacesADanglingLink(t *testing.T) {
	e, h := unshareHome(t)
	link := plantSharedLink(t, e, h)
	if err := runPackHook(e, &packload.Pack{Name: "pi"}, h); err != nil {
		t.Fatalf("unshare_directory: %v", err)
	}
	requireEmptyRealDir(t, link)
}

// TestUnshareDirectoryLeavesARealDirectoryAlone: a workspace that never had the link keeps
// its own checkouts.
func TestUnshareDirectoryLeavesARealDirectoryAlone(t *testing.T) {
	e, h := unshareHome(t)
	dir := filepath.Join(e.Home, filepath.FromSlash(h.File), "github.com", "x", "y")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runPackHook(e, &packload.Pack{Name: "pi"}, h); err != nil {
		t.Fatalf("unshare_directory: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a real directory's contents were removed: %v", err)
	}
}

// TestUnshareDirectoryLeavesALinkToAnythingElseAlone: only the exact target the retired hook
// wrote is undone. A user's own link, or an absolute spelling yolo never wrote, stays.
func TestUnshareDirectoryLeavesALinkToAnythingElseAlone(t *testing.T) {
	for _, target := range []string{"/elsewhere/git", "../../.pi-shared-git/sub", "../.pi-shared-git"} {
		e, h := unshareHome(t)
		link := filepath.Join(e.Home, filepath.FromSlash(h.File))
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := runPackHook(e, &packload.Pack{Name: "pi"}, h); err != nil {
			t.Fatalf("unshare_directory: %v", err)
		}
		if got, err := os.Readlink(link); err != nil || got != target {
			t.Errorf("a link to %q became %q (%v); only the retired hook's own link may change", target, got, err)
		}
	}
}

// TestUnshareDirectoryCreatesNothingWhereNothingWas: an absent path stays absent; the tool
// creates its own directory when it needs one.
func TestUnshareDirectoryCreatesNothingWhereNothingWas(t *testing.T) {
	e, h := unshareHome(t)
	if err := runPackHook(e, &packload.Pack{Name: "pi"}, h); err != nil {
		t.Fatalf("unshare_directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(e.Home, filepath.FromSlash(h.File))); !os.IsNotExist(err) {
		t.Fatalf("an absent path now exists (%v)", err)
	}
}

// TestShippedPiPackUnsharesItsGitDirectory pins the call site end to end: the shipped pi
// pack's manifest, through RunPackHooks, turns the c402dd43 link into an empty directory. It
// fails if the pack drops the declaration or RunPackHooks stops dispatching the hook.
func TestShippedPiPackUnsharesItsGitDirectory(t *testing.T) {
	p, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	var hook packdecl.Hook
	for _, h := range p.Decl.HookContributions() {
		if h.Name == HookUnshareDirectory && h.File == ".pi/agent/git" {
			hook = h
		}
		if h.Name == HookSharedDirectory && h.File == ".pi/agent/git" {
			t.Fatalf("packs/pi still shares .pi/agent/git (%+v); the redesign rules that out", h)
		}
	}
	if hook.Name == "" {
		t.Fatal("packs/pi no longer unshares .pi/agent/git; homes that booted with c402dd43 keep a dangling link")
	}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	link := plantSharedLink(t, e, hook)
	RunPackHooks(e, []*packload.Pack{p})
	requireEmptyRealDir(t, link)
}
