package run

// macosfiles_test.go pins plan item 21 (docs/plans/notch-convergence.md row D8): a pack's
// `files` trees reach a macos-user home, through the same home overlay that carries skills and
// briefings, and are write-protected there as the container's `:ro` bind protects them.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE DONE-WHEN: pi's two extensions exist in a macos-user home. End to end on Linux: run.Run
// composes the overlay for `packs: ["pi"]` on the macos-user arm, and the bootstrap's two
// content steps (the home layout, then the overlay install) lay it into a sandbox home. Fails if
// the arm stops passing `files` to the overlay builder, or the builder stops copying them.
func TestPiExtensionsReachAMacosUserHome(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["pi"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	var got macosuser.HomeOverlay
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
		overlay macosuser.HomeOverlay, _ macosuser.HostContext,
		_ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		got = overlay
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	extensions := []string{
		".pi/agent/extensions/yolo-openai-auth.js",
		".pi/agent/extensions/yolo-footer.js",
	}
	for _, want := range extensions {
		if !containsStr(got.Dests, want) {
			t.Errorf("the overlay does not deliver %s (Dests %v) — the profile would not "+
				"protect it and the bootstrap would not install it", want, got.Dests)
		}
	}

	pi := officialPack(t, "pi")
	sandboxHome := t.TempDir()
	e := entrypoint.DarwinEnvFrom(map[string]string{
		"HOME":                          sandboxHome,
		entrypoint.DarwinHomeSidecarEnv: filepath.Join(ws, ".yolo", "home"),
		"YOLO_DARWIN_HOME_OVERLAY":      got.Tree,
	}, sandboxHome)
	if err := entrypoint.InstallDarwinHomeLayout(e, []*packload.Pack{pi}); err != nil {
		t.Fatalf("home layout: %v", err)
	}
	if err := entrypoint.InstallHomeOverlay(e, []*packload.Pack{pi}); err != nil {
		t.Fatalf("overlay install: %v", err)
	}
	for _, rel := range extensions {
		body, err := os.ReadFile(filepath.Join(sandboxHome, rel))
		if err != nil {
			t.Errorf("~/%s is not in the macos-user home: %v", rel, err)
			continue
		}
		want, err := os.ReadFile(filepath.Join(pi.Root, "extensions", filepath.Base(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, want) {
			t.Errorf("~/%s does not hold the pack's file", rel)
		}
	}
}

// A `files` tree whose source is missing is skipped with the jail's own warning, not copied
// and not listed: the overlay says what the container arm says.
func TestMacosOverlaySkipsAMissingFilesSourceWithTheJailsWarning(t *testing.T) {
	staging := t.TempDir()
	root := t.TempDir()
	present := filepath.Join(root, "bin")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(present, "tool.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := packFilesTarget{Pack: "acme", Src: filepath.Join(root, "gone"), Dest: ".acme/gone",
		Root: root, From: "gone"}
	var warnings []string
	tree, dests, err := buildMacosHomeOverlayFor(staging, nil, nil, []packFilesTarget{
		{Pack: "acme", Src: present, Dest: ".acme/bin", Root: root, From: "bin"},
		missing,
	}, func(line string) { warnings = append(warnings, line) })
	if err != nil {
		t.Fatal(err)
	}
	if len(dests) != 1 || dests[0] != ".acme/bin" {
		t.Errorf("Dests = %v, want only the tree that exists", dests)
	}
	fi, err := os.Stat(filepath.Join(tree, ".acme", "bin", "tool.sh"))
	if err != nil {
		t.Fatalf("the present tree was not copied: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("tool.sh lost its exec bit in the overlay: %v", fi.Mode())
	}
	if len(warnings) != 1 || warnings[0] != packFilesSkipWarning(missing) ||
		!strings.Contains(warnings[0], `"gone"`) {
		t.Errorf("warnings = %q, want the jail's one skip warning naming the `from`", warnings)
	}
}
