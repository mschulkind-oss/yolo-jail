package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packbuild_test.go is the container-level cell for `yolo pack build`
// (docs/design/pack-buildable-programs.md §8, BG-D11): the explicit act builds a pack-tree program
// in the launch's own sealed build jail, a second run is up to date, --force rebuilds, and the next
// launch finds the build and builds nothing — the proof that the verb and a launch key one build.
// In a jail the verb refuses, naming the host command.
//
// HERMETIC, as packtreeprogram_test.go: a `file://` pack whose build writes a script printing its
// VERSION, and a private fixture capture store.

func TestPackBuildPrebuildsWhatALaunchThenHits(t *testing.T) {
	requireJail(t)
	const pack, bin = "packbuildfx", "packbuildfx-x"
	root := t.TempDir()
	src := filepath.Join(root, "tools", "x")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"`+pack+`","contributes":[`+
		`{"kind":"program","bin":"`+bin+`","via":"pack","from":"tools/x","build":"`+buildJSON+`","run":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+root+`", "name": "`+pack+`"}]}`)
	home := withPrivateFixtureYoloStore(t)
	if privateSuite.armed {
		usePrivateNativeFixture(t, packBuildNativeFixture(home, root))
	}
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, bin) })

	packBuild := func(what string, args ...string) result {
		t.Helper()
		r := runCommand(t, t.TempDir(), append([]string{"pack", "build"}, args...), withHostSemantics())
		t.Logf("%s: rc %d\nstdout:\n%s\nstderr:\n%s", what, r.rc, r.stdout, r.stderr)
		return r
	}
	label := "program " + pack + "/" + bin

	// 1. THE EXPLICIT ACT BUILDS IT: one built line, one entry.
	r := packBuild("the first yolo pack build")
	if r.rc != 0 || !strings.Contains(r.stdout, "built") || !strings.Contains(r.stdout, label) {
		t.Fatalf("the first yolo pack build did not build %s (rc %d)", bin, r.rc)
	}
	if added := newCaptureEntries(t, store, before, bin); len(added) != 1 {
		t.Fatalf("the first yolo pack build added %d entries, want 1: %v", len(added), added)
	}

	// 2. AGAIN: up to date, nothing built.
	r = packBuild("the second yolo pack build", pack)
	if r.rc != 0 || !strings.Contains(r.stdout, "up to date") {
		t.Fatalf("the second yolo pack build was not up to date (rc %d)", r.rc)
	}
	if added := newCaptureEntries(t, store, before, bin); len(added) != 1 {
		t.Errorf("the second yolo pack build added an entry: %v", added)
	}

	// 3. --force: rebuilt. The bytes are the same, so the store may answer with the same entry.
	r = packBuild("yolo pack build --force", "--force", pack)
	if r.rc != 0 || !strings.Contains(r.stdout, "built") || strings.Contains(r.stdout, "up to date") {
		t.Fatalf("yolo pack build --force did not rebuild (rc %d)", r.rc)
	}

	// 4. THE NEXT LAUNCH BUILDS NOTHING and runs what the verb built.
	l := runCommand(t, t.TempDir(), append(jailRunArgs(), "--", bin), withHostSemantics())
	out := l.combined()
	t.Logf("the launch after yolo pack build:\n%s", out)
	if l.rc != 0 || !strings.Contains(out, "x-1") {
		t.Fatalf("the launch did not run the built program: rc %d", l.rc)
	}
	if strings.Contains(out, "built "+label) || strings.Contains(out, "program builds") {
		t.Errorf("the launch rebuilt what yolo pack build had built")
	}

	// 5. IN A JAIL it refuses, naming the host command.
	j := runCommand(t, t.TempDir(), []string{"pack", "build", pack}, withEnv("YOLO_VERSION=1.0.0"))
	if j.rc != 2 || !strings.Contains(j.stderr, "on the host") || !strings.Contains(j.stderr, "yolo pack build "+pack) {
		t.Errorf("yolo pack build in a jail: rc %d, want 2 naming the host command\n%s", j.rc, j.stderr)
	}
}
