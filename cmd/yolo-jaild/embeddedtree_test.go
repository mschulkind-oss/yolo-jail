package main

// embeddedtree_test.go pins that yolo-jaild neither writes an embedded pack tree at init nor
// keeps one past run.

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// jaildLoadedAtInit is evaluated after every package yolo-jaild links has initialized.
var jaildLoadedAtInit = packload.EmbeddedLoaded()

// TestJaildLinksNoInitTimePackTree: four yolo-jaild daemons run for the whole life of every
// jail (measured 2026-09-23: up 8h), and each used to write an embedded pack tree at init
// through internal/config.
func TestJaildLinksNoInitTimePackTree(t *testing.T) {
	if jaildLoadedAtInit {
		t.Fatal("the embedded packs were loaded during yolo-jaild's package init")
	}
}

// TestRunReleasesTheEmbeddedTree pins the release on yolo-jaild's way out: main is
// os.Exit(run(...)), so a release anywhere but inside run would be skipped. A per-process
// fallback tree stands in for a daemon that read a pack.
func TestRunReleasesTheEmbeddedTree(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Cleanup(packload.OverrideEmbeddedCacheDir(""))
	if len(packload.Embedded()) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	root, fallback := packload.EmbeddedLocation()
	if !fallback {
		t.Fatal("setup: not a fallback tree")
	}
	if rc := run(nil); rc != 2 {
		t.Fatalf("run(nil) = %d, want the usage exit 2", rc)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("yolo-jaild's run returned leaving its embedded tree %s (stat %v)", root, err)
	}
}
