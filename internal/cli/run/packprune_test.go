package run

// packprune_test.go pins the prune half of "the MOUNT is the filter" for CONFIGURED
// packs: a slug the config no longer names must lose its staged tree.
//
// This is not a tidiness property. The in-jail entrypoint renders every pack it finds
// under YOLO_PACK_ROOT and cannot read the config to learn which ones were selected, so a
// leftover staging dir is a fully ACTIVE pack — its surfaces render, its hooks run, its
// shims generate. That is how the bug was found: a deleted test pack kept regenerating a
// broken `fzf` shim across launches, after the user had removed both the pack and its
// config entry.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// localPackDir writes a minimal local pack (manifest + one marker file) and returns its
// root, for use as a `file://` source.
func localPackDir(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// stagingOptions returns Options whose console output is captured, so a test can assert
// the prune REPORTS what it removed (no silent caps) as well as that it removed it.
func stagingOptions(t *testing.T) (*Options, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &Options{Workspace: t.TempDir(), Stdout: &out}, &out
}

// TestStagePacksPrunesDroppedConfiguredPack is the defect itself: `stagePacks` cleared
// _official and only _official, so removing a USER's pack from `packs` left its staged
// copy behind and it kept rendering forever.
func TestStagePacksPrunesDroppedConfiguredPack(t *testing.T) {
	home := packHome(t)
	keep := localPackDir(t, "keeper")
	drop := localPackDir(t, "dropped")
	writeUserPacks(t, home, `["file://`+keep+`", "file://`+drop+`"]`)

	o, out := stagingOptions(t)
	stagingRoot, loaded, _, err := o.stagePacks("yolo-test-prune")
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("first pass: want 2 packs staged, got %d", len(loaded))
	}
	for _, slug := range []string{"keeper", "dropped"} {
		if !isDir(filepath.Join(stagingRoot, slug)) {
			t.Fatalf("first pass did not stage %s", slug)
		}
	}

	// Drop one. The tree of the pack that is gone must go with it; the other must be
	// untouched and still carry its content.
	writeUserPacks(t, home, `["file://`+keep+`"]`)
	out.Reset()
	stagingRoot, loaded, _, err = o.stagePacks("yolo-test-prune")
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "keeper" {
		t.Fatalf("second pass: want [keeper], got %d packs", len(loaded))
	}
	if _, statErr := os.Stat(filepath.Join(stagingRoot, "dropped")); !os.IsNotExist(statErr) {
		t.Errorf("the dropped pack's staged tree survived (%v) — the entrypoint renders every "+
			"pack under YOLO_PACK_ROOT, so it would still generate shims and run hooks", statErr)
	}
	if body, rerr := os.ReadFile(filepath.Join(stagingRoot, "keeper", "marker.txt")); rerr != nil ||
		strings.TrimSpace(string(body)) != "keeper" {
		t.Errorf("the KEPT pack's content did not survive the prune: %v / %q", rerr, body)
	}
	// NO SILENT CAPS: the user has to be able to see that the deactivation took.
	if !strings.Contains(out.String(), "dropped") {
		t.Errorf("prune did not report the removed pack; a user who dropped one would be left "+
			"wondering whether it is still active:\n%s", out.String())
	}
}

// TestStagePacksKeepsUnresolvableFetchedPack is the constraint that rules out the simpler
// clear-everything-and-restage: a fetched pack that could not be reached THIS launch is
// still configured, so its staged tree must survive. Wiping it would silently discard
// content the user still wants on every offline launch.
//
// Launch is strictly offline (C5), so an unfetched git pack is a fatal error naming
// `yolo pack install` — and the prune must already have spared it by then.
func TestStagePacksKeepsUnresolvableFetchedPack(t *testing.T) {
	home := packHome(t)
	// A previous launch staged it; nothing has been fetched into the store since.
	o, _ := stagingOptions(t)
	writeUserPacks(t, home, `[]`)
	stagingRoot, _, _, err := o.stagePacks("yolo-test-unresolvable")
	if err != nil {
		t.Fatalf("bootstrap pass: %v", err)
	}
	staged := filepath.Join(stagingRoot, "acme")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "marker.txt"), []byte("acme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeUserPacks(t, home, `["git+ssh://example.invalid/org/repo//acme?ref=v1"]`)
	if _, _, _, err := o.stagePacks("yolo-test-unresolvable"); err == nil {
		t.Fatal("an unfetched git pack must fail the launch (C5: launch never fetches)")
	}
	if _, statErr := os.Stat(filepath.Join(staged, "marker.txt")); statErr != nil {
		t.Errorf("a CONFIGURED but unresolvable pack was pruned (%v) — an unreachable git "+
			"remote is not a deactivation signal, and discarding the tree would lose the "+
			"pack's content on every offline launch", statErr)
	}
}

// TestPackStagingRootInodeSurvivesPrune is packstage rule 3 at this call site: CLEAR
// CONTENTS, NEVER THE DIR. A running jail's /ctx/packs bind captured the staging root's
// inode, so the obvious os.RemoveAll(stagingRoot) would silently detach that mount — the
// jail would keep reading a tree nothing writes to any more. Unit-testable only as the
// inode identity, which is exactly what the mount pins.
func TestPackStagingRootInodeSurvivesPrune(t *testing.T) {
	home := packHome(t)
	drop := localPackDir(t, "ephemeral")
	writeUserPacks(t, home, `["file://`+drop+`"]`)

	o, _ := stagingOptions(t)
	stagingRoot, _, _, err := o.stagePacks("yolo-test-inode")
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	before := dirInode(t, stagingRoot)

	writeUserPacks(t, home, `[]`)
	if _, _, _, err := o.stagePacks("yolo-test-inode"); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	after := dirInode(t, stagingRoot)
	if before != after {
		t.Errorf("staging root inode changed %d -> %d: the dir was removed and recreated, "+
			"which detaches a running jail's /ctx/packs bind (packstage rule 3)", before, after)
	}
	if _, statErr := os.Stat(filepath.Join(stagingRoot, "ephemeral")); !os.IsNotExist(statErr) {
		t.Errorf("the prune under test did not actually run (%v), so the inode check above "+
			"proves nothing", statErr)
	}
}

// TestPruneDroppedPackStagingLeavesNonDirectories: the prune only removes real
// directories. Nothing yolo writes puts a file or a symlink at the top of the staging
// root, so an unrecognized entry there is somebody else's — and it cannot render as a
// pack anyway (LoadJailPacks skips every non-directory entry).
func TestPruneDroppedPackStagingLeavesNonDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, officialStagingDir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "here"), 0o755); err != nil {
		t.Fatal(err)
	}

	pruned, err := pruneDroppedPackStaging(root, map[string]bool{"here": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != "gone" {
		t.Fatalf("pruned = %v, want [gone]", pruned)
	}
	for _, keep := range []string{"stray.txt", officialStagingDir, "here"} {
		if _, statErr := os.Stat(filepath.Join(root, keep)); statErr != nil {
			t.Errorf("prune removed %s, which it must leave alone: %v", keep, statErr)
		}
	}
	// _official is rebuilt by the caller from the embed.FS, so the prune must not race it.
	if !isDir(filepath.Join(root, officialStagingDir, "claude")) {
		t.Error("prune reached into _official, which the caller owns")
	}
}

// livePackSlugs must count an EMBEDDED entry out (it lives under _official, not at
// <root>/<slug>) and a fetched entry in, regardless of whether it can be resolved.
func TestLivePackSlugsCountsConfiguredNotResolvable(t *testing.T) {
	home := packHome(t)
	local := localPackDir(t, "mine")
	writeUserPacks(t, home,
		`["claude", "file://`+local+`", "git+ssh://example.invalid/o/r//remote?ref=v1"]`)

	entries, err := config.LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	embedded := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		embedded[p.Name] = p
	}
	live := livePackSlugs(entries, embedded)
	if live["claude"] {
		t.Error("an embedded pack is staged under _official, so it must not appear as a slug")
	}
	for _, want := range []string{"mine", "remote"} {
		if !live[want] {
			t.Errorf("configured pack %q missing from the live set — it would be pruned", want)
		}
	}
}

// dirInode returns the inode number of a directory, which is the identity a bind mount
// captures at container start.
func dirInode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	return st.Ino
}
