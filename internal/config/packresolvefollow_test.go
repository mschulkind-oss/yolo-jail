package config

// packresolvefollow_test.go pins which resolutions follow a local pack's escaping symlinks while
// docs/plans/notch-convergence.md OQ-NC9 is open, and where a process resolution reads a pack from.
//
// Before the one resolver, a local pack was READ IN PLACE by the host verbs, config validation,
// UseProfileCLINames and the lazy loophole resolver — which followed any link it held — and was
// STAGED, refusing an escaping link, by the launch, `yolo check`, `yolo pack explain` and every
// reader of a FILTERED entry. FollowLocalSymlinks keeps exactly that split and no wider one.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// escapingLocalPack is a local pack whose one skill is a symlink to a file outside the pack: the
// shape a dotfile manager (rcm, stow, chezmoi) deploys.
func escapingLocalPack(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, "skills", "leak"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte("---\nname: leak\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "skills", "leak", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	return root
}

// A FILTERED LOCAL PACK'S ESCAPING SYMLINK IS REFUSED EVEN WHEN FOLLOWING, because every notch
// staged a filtered entry through packstage.Stage, which refuses one: following only ever described
// reading an unfiltered pack in place.
func TestResolvePackFollowsOnlyAnUnfilteredLocalPacksLinks(t *testing.T) {
	root := escapingLocalPack(t, "flt")
	entry := PackEntry{Source: "file://" + root, Name: "flt", Exclude: []string{"README.md"}}
	for _, spec := range []ResolvePackSpec{
		{FollowLocalSymlinks: true},
		{FollowLocalSymlinks: true, Dest: filepath.Join(t.TempDir(), "flt")},
	} {
		if _, err := ResolvePack(entry, spec); err == nil || !strings.Contains(err.Error(), "outside the pack") {
			t.Errorf("a FILTERED local pack's escaping symlink (dest %q) = %v, want the no-escape refusal",
				spec.Dest, err)
		}
	}
	entry.Exclude = nil
	if res, err := ResolvePack(entry, ResolvePackSpec{FollowLocalSymlinks: true}); err != nil || res.Pack == nil {
		t.Errorf("control: the same pack unfiltered is followed: %v", err)
	}
}

// A PROCESS RESOLUTION COPIES ONLY WHAT A FILTER NEEDS. An unfiltered entry is read in place — an
// embedded one from the one materialization, a local one from its directory — so its Root outlives
// the process (a host-scope daemon spawned from a loophole module keeps running after the verb that
// spawned it exits) and no verb pays a copy for nothing to filter. A filtered one is staged into the
// process pack tree, where only the files its filters keep exist.
func TestResolvePackForProcessCopiesOnlyAFilteredEntry(t *testing.T) {
	var embeddedRoot string
	for _, p := range packload.Embedded() {
		if p.Name == "hello-daemon" {
			embeddedRoot = p.Root
		}
	}
	if embeddedRoot == "" {
		t.Fatal("fixture: this build must ship hello-daemon")
	}
	res, err := ResolvePackForProcess(EmbeddedPackEntry("hello-daemon"), ResolvePackSpec{})
	if err != nil || res.Pack == nil {
		t.Fatalf("embedded: %v", err)
	}
	if res.Pack.Root != embeddedRoot {
		t.Errorf("an unfiltered embedded pack was read at %s, want the one materialization %s",
			res.Pack.Root, embeddedRoot)
	}

	root := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"plain"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := PackEntry{Source: "file://" + root, Name: "plain"}
	res, err = ResolvePackForProcess(local, ResolvePackSpec{})
	if err != nil || res.Pack == nil {
		t.Fatalf("local: %v", err)
	}
	if res.Pack.Root != root {
		t.Errorf("an unfiltered local pack was read at %s, want its own directory %s", res.Pack.Root, root)
	}

	local.Exclude = []string{"README.md"}
	res, err = ResolvePackForProcess(local, ResolvePackSpec{})
	if err != nil || res.Pack == nil {
		t.Fatalf("filtered: %v", err)
	}
	tree := packload.ProcessTreeLocation()
	if tree == "" || !strings.HasPrefix(res.Pack.Root, tree+string(filepath.Separator)) {
		t.Errorf("a filtered pack was read at %s, want a staged copy under the process tree %q",
			res.Pack.Root, tree)
	}
	if _, err := os.Stat(filepath.Join(res.Pack.Root, "README.md")); !os.IsNotExist(err) {
		t.Errorf("the filtered copy holds the file its entry excludes (%v)", err)
	}
}

// CONFIG VALIDATION FOLLOWS AN UNFILTERED LOCAL PACK'S LINKS, as it did when it read such a pack in
// place, so its reservations agree with `yolo host apply` about which packs resolve. Driven through
// resolveSelectedPacks, validation's own call, so it fails if that call stops following.
func TestResolveSelectedPacksFollowsAnUnfilteredLocalPacksLinks(t *testing.T) {
	root := escapingLocalPack(t, "stow")
	selectionHome(t, `["file://`+root+`"]`)
	packs, complete := resolveSelectedPacks()
	found := false
	for _, p := range packs {
		found = found || p.Name == "stow"
	}
	if !found || !complete {
		t.Errorf("validation left out a local pack the host verbs deliver: found=%v complete=%v", found, complete)
	}
}

// USEPROFILECLINAMES FOLLOWS IT TOO, for the same reason: a pack it cannot resolve makes it step
// aside (ok=false), which silently drops the use_profiles key check for every workspace.
func TestUseProfileCLINamesFollowsAnUnfilteredLocalPacksLinks(t *testing.T) {
	root := escapingLocalPack(t, "stow")
	selectionHome(t, `["file://`+root+`"]`)
	if _, ok := UseProfileCLINames(); !ok {
		t.Error("UseProfileCLINames stepped aside over a local pack the host verbs deliver")
	}
}
