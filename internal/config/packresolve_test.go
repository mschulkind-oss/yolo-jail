package config

// packresolve_test.go pins the one pack resolver (packresolve.go) directly. The call sites that
// route through it are pinned where they live: the launch in internal/cli/run
// (packresolvelaunch_test.go), the host verbs in internal/cli (hostpackresolve_test.go).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// withEmbeddedFS swaps the embedded pack filesystem for this test, releasing the loaded packs on
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

// AN EMBEDDED ENTRY'S FILTERS APPLY, in both modes. `{"source": "hello-daemon", "exclude": [...]}`
// used to be accepted and then ignored at every notch, because an embedded pack was copied (or read)
// whole.
func TestResolvePackAppliesAnEmbeddedEntrysFilters(t *testing.T) {
	entry := EmbeddedPackEntry("hello-daemon")
	entry.Exclude = []string{"README.md"}

	dest := filepath.Join(t.TempDir(), "hello-daemon")
	res, err := ResolvePack(entry, ResolvePackSpec{Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pack == nil || res.Pack.Root != dest {
		t.Fatalf("stage mode must load the staged copy, got %+v", res.Pack)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Errorf("an excluded file of an embedded pack was staged (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Errorf("control: the unexcluded manifest must stage: %v", err)
	}
	if !strings.Contains(strings.Join(res.Staged.Excluded, ","), "README.md") {
		t.Errorf("the exclusion must be reported: %v", res.Staged.Excluded)
	}
	if res.Pack.SourceRoot == "" || res.Pack.SourcePath(filepath.Join(dest, "pack.json")) ==
		filepath.Join(dest, "pack.json") {
		t.Errorf("a staged pack must map its paths back to where it was staged from")
	}

	// Declaration mode reads the filtered tree too: a filter that drops the manifest drops
	// every declaration with it.
	entry.Exclude = []string{"pack.json"}
	decl, err := ResolvePack(entry, ResolvePackSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if decl.Pack == nil || len(decl.Pack.Decl.Contributions()) != 0 {
		t.Errorf("a manifest the entry excludes must declare nothing, got %+v", decl.Pack)
	}
}

// A BROKEN EMBEDDED MATERIALIZATION IS NAMED AS ONE — a yolo bug — and never as "this build ships
// no pack by that name", which is what the host read in Embedded()'s empty answer (row B7).
func TestResolvePackNamesABrokenEmbeddedMaterialization(t *testing.T) {
	withEmbeddedFS(t, fstest.MapFS{"broken/pack.json": {Data: []byte("{not json")}})
	if len(packload.EmbeddedProblems()) == 0 {
		t.Fatal("fixture: the swapped FS must fail to materialize")
	}
	_, err := ResolvePack(EmbeddedPackEntry("broken"), ResolvePackSpec{})
	if err == nil {
		t.Fatal("a broken embedded materialization must not resolve")
	}
	if !strings.Contains(err.Error(), "yolo bug") || strings.Contains(err.Error(), "ships no pack") {
		t.Errorf("the error must name a yolo bug, not a missing pack: %v", err)
	}
}

// DECLARATION MODE STILL APPLIES THE NO-ESCAPE RULE to an unfiltered pack it reads in place
// (packstage.Check), so the answer "does this pack resolve" is the launch's in both modes — and
// FollowLocalSymlinks, the host's standing input pending OQ-NC9, is the one thing that changes it.
func TestResolvePackDeclarationModeRefusesWhatTheLaunchRefuses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "esc")
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "skills", "leak.md")); err != nil {
		t.Fatal(err)
	}
	entry := PackEntry{Source: "file://" + root, Name: "esc"}

	if _, err := ResolvePack(entry, ResolvePackSpec{}); err == nil ||
		!strings.Contains(err.Error(), "outside the pack") {
		t.Errorf("declaration mode over an escaping symlink = %v, want the launch's refusal", err)
	}
	if _, err := ResolvePack(entry, ResolvePackSpec{Dest: filepath.Join(t.TempDir(), "esc")}); err == nil {
		t.Error("stage mode must refuse it too")
	}
	res, err := ResolvePack(entry, ResolvePackSpec{FollowLocalSymlinks: true})
	if err != nil || res.Pack == nil {
		t.Errorf("FollowLocalSymlinks must follow a LOCAL pack's link: %v", err)
	}
}
