package run

// packresolvelaunch_test.go pins the LAUNCH's call site of the one pack resolver
// (config.ResolvePack, docs/plans/notch-convergence.md item 5): every entry stagePacks stages goes
// through it, the embedded ones included, and the embedded packs come from the one
// materialization every notch reads (packload.Embedded). The resolver's own tests are in
// internal/config; these fail when stagePacks stops calling it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// withEmbeddedFS swaps the embedded pack filesystem for one test, releasing the loaded packs on
// both sides so the next Embedded() reads the swapped one.
func withEmbeddedFS(t *testing.T, f fstest.MapFS) {
	t.Helper()
	packload.ReleaseEmbedded()
	packload.SetEmbeddedFS(f)
	t.Cleanup(func() {
		packload.ReleaseEmbedded()
		packload.SetEmbeddedFS(packs.FS)
	})
}

// AN EMBEDDED ENTRY'S `exclude` REACHES THE JAIL'S TREE. It used to be accepted by config and then
// ignored: the launch copied an embedded pack whole (treesync), whatever its entry said.
func TestStagePacksAppliesAnEmbeddedEntrysFilters(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[{"source":"hello-daemon","exclude":["README.md"]}]`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	tree, loaded, _, err := o.stagePacks("yolo-test-embedded-filter")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	staged := filepath.Join(tree, officialStagingDir, "hello-daemon")
	if _, err := os.Stat(filepath.Join(staged, "README.md")); !os.IsNotExist(err) {
		t.Errorf("the entry excludes README.md and the jail's tree still has it (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(staged, "pack.json")); err != nil {
		t.Errorf("control: the rest of the pack must stage: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Root != staged {
		t.Errorf("the loaded pack must be the staged copy: %+v", loaded)
	}
}

// A LOCAL PACK'S ESCAPING SYMLINK REFUSES THE LAUNCH (packstage's no-escape rule), which is the
// jail half of OQ-NC9: the host follows such a link (TestApplyHostConvergesOverASymlinkedPack) and
// this pins that the launch does not, until the question is ruled.
func TestStagePacksRefusesALocalPacksEscapingSymlink(t *testing.T) {
	home := packHome(t)
	p := writePackManifest(t, "esc", `{"name":"esc"}`)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Root, "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(p.Root, "skills", "x", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+p.Root+`"]`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	_, _, _, err := o.stagePacks("yolo-test-escape")
	if err == nil || !strings.Contains(err.Error(), "outside the pack") || !strings.Contains(err.Error(), "esc") {
		t.Fatalf("a local pack's escaping symlink must refuse the launch, naming the pack: %v", err)
	}
}

// THE LAUNCH READS THE ONE EMBEDDED MATERIALIZATION. A pack only the registered embedded FS
// carries stages, and a broken one refuses the launch as a yolo bug. Both fail if stagePacks goes
// back to materializing packs.FS into a scratch dir of its own (row B7).
func TestStagePacksReadsTheOneEmbeddedMaterialization(t *testing.T) {
	home := packHome(t)
	withEmbeddedFS(t, fstest.MapFS{"onlyinfs/pack.json": {Data: []byte(`{"name":"onlyinfs"}`)}})
	writeUserPacks(t, home, `["onlyinfs"]`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	_, loaded, _, err := o.stagePacks("yolo-test-onlyinfs")
	if err != nil {
		t.Fatalf("a pack the registered embedded FS carries must stage: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "onlyinfs" {
		t.Fatalf("loaded = %+v, want the one embedded pack", loaded)
	}

	withEmbeddedFS(t, fstest.MapFS{
		"onlyinfs/pack.json": {Data: []byte(`{"name":"onlyinfs"}`)},
		"broken/pack.json":   {Data: []byte("{not json")},
	})
	_, _, _, err = o.stagePacks("yolo-test-broken-embedded")
	if err == nil || !strings.Contains(err.Error(), "official packs:") {
		t.Fatalf("a broken embedded materialization must refuse the launch as a yolo bug: %v", err)
	}
}
