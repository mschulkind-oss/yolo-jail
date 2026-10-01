package nixroots

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/nixroots/nixrootstest"
)

// resolvedTempDir is t.TempDir() with its symlinks resolved, minted resolved because Root
// resolves the link's directory: on darwin t.TempDir() is under /var, a symlink to
// /private/var, and a map keyed by the unresolved spelling would match nothing (AGENTS.md,
// the darwin path-resolution class).
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// fakeStore is a store directory holding one store path.
func fakeStore(t *testing.T) (dir, storePath string) {
	t.Helper()
	dir = resolvedTempDir(t)
	storePath = filepath.Join(dir, "0123456789abcdfghijklmnpqrsvwxyz-probe")
	if err := os.WriteFile(storePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, storePath
}

// M6 of in-jail-nix-roots.md §3, as a function: the link is made in the jail, pointing at the
// store path, and the daemon is sent the HOST's spelling of it.
func TestRootMakesTheLinkAndSendsTheHostSpelling(t *testing.T) {
	store, sp := fakeStore(t)
	jailHome := resolvedTempDir(t)
	d := nixrootstest.Start(t, nixrootstest.Options{})
	r := Registrar{
		Map:      HostMap{jailHome: "/home/u/proj/.yolo/home/local"},
		Socket:   d.Socket,
		StoreDir: store,
	}
	roots := filepath.Join(jailHome, "share", "yolo-jail", "build", "roots")
	if err := os.MkdirAll(roots, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(roots, "abc")

	host, err := r.Root(link, sp)
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/u/proj/.yolo/home/local/share/yolo-jail/build/roots/abc"
	if host != want {
		t.Errorf("registered %q, want %q", host, want)
	}
	if got := d.Roots(); !slices.Equal(got, []string{want}) {
		t.Errorf("daemon recorded %q, want %q", got, want)
	}
	if target, err := os.Readlink(link); err != nil || target != sp {
		t.Errorf("link = %q, %v; want a symlink to %q", target, err, sp)
	}

	// Again, for a second store path: the link is replaced, and sent again (the auto entry
	// is named by the path's hash, so the daemon keeps one).
	sp2 := filepath.Join(store, "1123456789abcdfghijklmnpqrsvwxyz-probe")
	if err := os.WriteFile(sp2, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Root(link, sp2); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(link); target != sp2 {
		t.Errorf("the link was not replaced: it points at %q", target)
	}
	if n := len(d.Roots()); n != 2 {
		t.Errorf("daemon was sent %d roots, want 2", n)
	}
}

// The link's directory is resolved before translating: a link reached through a symlink
// in the jail is rooted where its bytes are (M7: nix's own client does not resolve, which is
// one reason a spelling is not enough).
func TestRootTranslatesWhereTheLinkReallyIs(t *testing.T) {
	store, sp := fakeStore(t)
	real := resolvedTempDir(t)
	alias := filepath.Join(resolvedTempDir(t), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	d := nixrootstest.Start(t, nixrootstest.Options{})
	r := Registrar{Map: HostMap{real: "/host/real"}, Socket: d.Socket, StoreDir: store}
	host, err := r.Root(filepath.Join(alias, "result"), sp)
	if err != nil {
		t.Fatal(err)
	}
	if host != "/host/real/result" {
		t.Errorf("registered %q, want /host/real/result", host)
	}
}

// An untranslatable link makes NOTHING: no link, no connection. That is today's in-jail
// state exactly, which the design's NR-D1 keeps for every path under no mapped bind.
func TestRootLeavesAnUntranslatableLinkAlone(t *testing.T) {
	store, sp := fakeStore(t)
	dir := resolvedTempDir(t)
	d := nixrootstest.Start(t, nixrootstest.Options{})
	for name, m := range map[string]HostMap{
		"no map":    nil,
		"a mask":    {dir: ""},
		"elsewhere": {"/workspace": "/h"},
	} {
		r := Registrar{Map: m, Socket: d.Socket, StoreDir: store}
		link := filepath.Join(dir, "result")
		if _, err := r.Root(link, sp); !errors.Is(err, ErrUntranslatable) {
			t.Errorf("%s: err = %v, want ErrUntranslatable", name, err)
		}
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Errorf("%s: an untranslatable link was created", name)
		}
	}
	if n := d.Connections(); n != 0 {
		t.Errorf("the daemon was dialled %d times for links it could not be told about", n)
	}
}

// Only a store path is rooted: a link whose target is anything else would be a second hop,
// which the daemon's root walk does not follow (§2.1).
func TestRootRefusesATargetThatIsNotAStorePath(t *testing.T) {
	store, sp := fakeStore(t)
	dir := resolvedTempDir(t)
	d := nixrootstest.Start(t, nixrootstest.Options{})
	r := Registrar{Map: HostMap{dir: "/h"}, Socket: d.Socket, StoreDir: store}
	for _, target := range []string{
		filepath.Join(sp, "bin"),                  // inside a store path, not one
		filepath.Join(dir, "elsewhere"),           // outside the store
		filepath.Join(store, "absent-store-path"), // not in this jail's view of the store
		"relative",
	} {
		if _, err := r.Root(filepath.Join(dir, "result"), target); err == nil {
			t.Errorf("rooted a link to %q", target)
		}
	}
	if n := d.Connections(); n != 0 {
		t.Errorf("the daemon was dialled %d times", n)
	}
}
