package config

// embeddedlazy_test.go pins that LINKING this package reads no pack.
//
// Until 2026-09-23 hostFileWritableRoots was a package-level map whose initializer called
// packload.EmbeddedWritableDirs, so the embedded pack tree was written to disk at INIT by
// every process that imported internal/config — every test binary (the pre-commit gate runs
// all of them), `yolo --version`, every in-jail daemon — and each exit path that skipped a
// defer leaked it (measured 2026-09-23: 281 leaked trees in one jail's /tmp, +1 per test
// package run).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// loadedAtInit is evaluated during this test binary's package initialization, AFTER every
// non-test file's package-level vars (hostfiles.go's included, which has no dependency on
// this one). A package-level initializer anywhere in internal/config reaching the embedded
// packs makes it true.
var loadedAtInit = packload.EmbeddedLoaded()

func TestLinkingConfigMaterializesNoPacks(t *testing.T) {
	if loadedAtInit {
		t.Fatal("the embedded packs were loaded during package init: some package-level var " +
			"in internal/config (or a package it imports) reaches packload.Embedded, so every " +
			"process linking this package writes a pack tree before main runs")
	}
}

// TestConfigCodeThatReadsNoPackCreatesNothing drives ordinary config code that has no
// reason to read a pack, with both places a tree could land redirected at empty temp dirs,
// then proves the probe is live by running code that DOES read one.
func TestConfigCodeThatReadsNoPackCreatesNothing(t *testing.T) {
	// Resolved where minted: the loader resolves its base through symlinks, and darwin's
	// t.TempDir() is under the /var -> /private/var symlink.
	resolved := func() string {
		d, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cache, tmp := resolved(), resolved()
	t.Setenv("TMPDIR", tmp)
	restore := packload.OverrideEmbeddedCacheDir(cache)
	t.Cleanup(restore)

	_ = WorkspaceConfigBootPath(t.TempDir())
	_ = AgentUpdatesWire()

	if packload.EmbeddedLoaded() {
		t.Fatal("config code that reads no pack loaded the embedded packs")
	}
	for _, dir := range []string{cache, tmp} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s gained %d entries from code that reads no pack", dir, len(entries))
		}
	}

	// Positive control: the first real reader loads, into the cache base and nowhere else.
	// builtinSurfacePaths, the host_files surface reservation, rather than StagingFor, which
	// read the packs here until OQ-BH14 made it take the selection as an argument.
	_ = builtinSurfacePaths()
	if !packload.EmbeddedLoaded() {
		t.Fatal("builtinSurfacePaths did not load the embedded packs; the negative assertions " +
			"above cannot tell lazy from never")
	}
	root, fallback := packload.EmbeddedLocation()
	if fallback || filepath.Dir(root) != cache {
		t.Errorf("tree at %s (fallback=%v), want one under the cache base %s", root, fallback, cache)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("TMPDIR gained %d entries although the cache base was usable", len(entries))
	}
}
