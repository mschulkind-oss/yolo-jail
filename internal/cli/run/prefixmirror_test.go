package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/flakebundle"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fakeStorePrefix lays out a built prefix the way flake.nix's installPrefix does — read-only, as
// the store is — at a temp dir, and points readStoreFrom at it for the store path returned.
func fakeStorePrefix(t *testing.T) (storePath string) {
	t.Helper()
	storePath = filepath.Join(hostNixStore, "abc-yolo-jail-install-prefix")
	real := resolveSymlinks(t.TempDir())
	bundle := filepath.Join(real, image.JailPrefixSubdir, "share", "yolo-jail")
	bin := prebuiltBinDir(bundle)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join(bundle, "flake.nix"):    "{}",
		filepath.Join(bin, "yolo-entrypoint"): "#!/bin/sh\n",
	} {
		if err := os.WriteFile(name, []byte(body), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{bin, filepath.Dir(bin), bundle} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range []string{bundle, filepath.Dir(bin), bin} {
			_ = os.Chmod(d, 0o755)
		}
	})
	saved := readStoreFrom
	readStoreFrom = func(p string) string {
		if rest, ok := strings.CutPrefix(p, storePath); ok {
			return real + rest
		}
		return p
	}
	t.Cleanup(func() { readStoreFrom = saved })
	return storePath
}

// THE CALL SITE. A Mac whose podman VM cannot see /nix, launching from a live checkout: the
// prefix the build produced is in the store, and the launch mounts a copy of it under $HOME
// instead of refusing. Delete the mirror call in resolveJailPrefix and this fails at the first
// check, because the refusal comes back.
func TestDarwinMirrorsABuiltStorePrefixInsteadOfRefusing(t *testing.T) {
	home := resolveSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	root := stageBundle(t, false)
	store := fakeStorePrefix(t)
	o, _ := prefixOptions(t, func(string) (string, []string) { return store, nil })
	o.IsMacOS = true
	fakePodmanMachine(t, o, append(append([]string{}, defaultMacShares...), home), new(int))
	var stderr bytes.Buffer
	o.Stderr = &stderr

	p, ok := o.resolveJailPrefix(root, "podman")
	if !ok {
		t.Fatalf("a Mac launch from a checkout was refused instead of copying its build out "+
			"of the store:\n%s", stderr.String())
	}
	gens := flakebundle.GenerationsDir(paths.FlakeBundleDir())
	for _, dir := range []string{p.binDir, p.shareDir} {
		if underDir(dir, hostNixStore) || !underDir(dir, gens) {
			t.Errorf("mount source %s is not a generation under %s", dir, gens)
		}
	}
	if b, err := os.ReadFile(filepath.Join(p.binDir, "yolo-entrypoint")); err != nil || string(b) != "#!/bin/sh\n" {
		t.Errorf("the copied entrypoint = %q, %v", b, err)
	}
	if !strings.Contains(stderr.String(), "Copied yolo's own binaries out of /nix/store") ||
		!strings.Contains(describeJailPrefix(p), "copied from "+filepath.Join(store, image.JailPrefixSubdir)) {
		t.Errorf("the launch does not say its binaries were copied:\n%s\n%s", stderr.String(),
			describeJailPrefix(p))
	}
	// NEVER ACTIVATED: `yolo` without YOLO_REPO_ROOT keeps running the installed bundle.
	if cur := flakebundle.Current(paths.FlakeBundleDir()); cur != "" {
		t.Errorf("the stable bundle path now resolves to %s; a mirror must not become the default", cur)
	}

	// The same store path again: the same copy, renewed, not a second one.
	stderr.Reset()
	again, ok := o.resolveJailPrefix(root, "podman")
	if !ok || again.shareDir != p.shareDir {
		t.Fatalf("a second launch of the same build copied again (%s, then %s)", p.shareDir, again.shareDir)
	}
	if !strings.Contains(stderr.String(), "Reusing a copy of") {
		t.Errorf("the reuse is not reported:\n%s", stderr.String())
	}

	// REAPABLE once no jail mounts it and its grace is over — which also proves the copy dropped
	// the store's read-only modes, or RemoveAll could not delete it.
	removed := flakebundle.Reap(paths.FlakeBundleDir(), nil, true, true, time.Now().Add(2*flakebundle.GenerationGrace))
	if len(removed) != 1 || removed[0] != p.shareDir {
		t.Fatalf("reaped %v, want exactly the mirror %s", removed, p.shareDir)
	}
	if _, err := os.Stat(p.shareDir); !os.IsNotExist(err) {
		t.Errorf("the reaped mirror is still on disk: %v", err)
	}
}

// A copy interrupted before its marker was written is never reused: the next launch makes its own.
func TestAnInterruptedMirrorIsNotReused(t *testing.T) {
	stable := filepath.Join(resolveSymlinks(t.TempDir()), "flake-bundle")
	store := fakeStorePrefix(t)
	p := jailPrefix{shareDir: filepath.Join(store, image.JailPrefixSubdir, "share", "yolo-jail"), built: true}
	now := time.Now()

	first, _, err := mirrorBuiltPrefix(stable, p, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(first.shareDir, prefixMirrorMarker)); err != nil {
		t.Fatal(err)
	}
	second, reused, err := mirrorBuiltPrefix(stable, p, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if reused || second.shareDir == first.shareDir {
		t.Errorf("a copy with no marker was reused: %s", second.shareDir)
	}
}

// A store bundle with no entrypoint is not a prefix, and the copy says so rather than mounting it.
func TestAMirrorWithoutAnEntrypointIsRefused(t *testing.T) {
	stable := filepath.Join(resolveSymlinks(t.TempDir()), "flake-bundle")
	store := fakeStorePrefix(t)
	share := filepath.Join(store, image.JailPrefixSubdir, "share", "yolo-jail")
	bin := prebuiltBinDir(readStoreFrom(share))
	_ = os.Chmod(bin, 0o755)
	if err := os.Remove(filepath.Join(bin, "yolo-entrypoint")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mirrorBuiltPrefix(stable, jailPrefix{shareDir: share, built: true}, time.Now(), 1); err == nil {
		t.Fatal("a bundle without yolo-entrypoint was mirrored")
	}
	if gens := flakebundle.Generations(stable); len(gens) != 0 {
		t.Errorf("the failed copy left %v behind", gens)
	}
}
