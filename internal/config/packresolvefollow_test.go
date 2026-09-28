package config

// packresolvefollow_test.go pins that every resolution follows a local pack's escaping symlinks
// (docs/plans/notch-convergence.md OQ-NC9, ruled A, 2026-09-28), and where a process resolution
// reads a pack from.
//
// Before the ruling, a local pack was followed by the callers that had READ it in place before
// there was one resolver (the host verbs, config validation, UseProfileCLINames and the lazy
// loophole resolver) and refused by the ones that STAGED it (the launch, `yolo check`, `yolo pack
// explain`, and every reader of a filtered entry), through an input each caller set. A dotfile
// manager's pack therefore worked at the host and refused every jail launch. The input is gone:
// a local pack is a directory the user named in their own user config, and every caller follows
// its links. A fetched pack's escaping link is refused everywhere (packstage's no-escape rule).

import (
	"os"
	"path/filepath"
	"slices"
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

// A LOCAL PACK'S LINKS ARE FOLLOWED IN EVERY MODE, FILTERED OR NOT (OQ-NC9, ruled A). Every caller
// asks the resolver the same way, so there is no input left to set: the launch stages it, a host
// verb reads it for its process, and a declaration reader loads it in place, and all three follow.
// The entry's filters still apply to what the links deliver.
func TestResolvePackFollowsALocalPacksLinksInEveryMode(t *testing.T) {
	root := escapingLocalPack(t, "stow")
	for _, exclude := range [][]string{nil, {"README.md"}} {
		entry := PackEntry{Source: "file://" + root, Name: "stow", Exclude: exclude}
		for _, spec := range []ResolvePackSpec{{}, {Dest: filepath.Join(t.TempDir(), "stow")}} {
			res, err := ResolvePack(entry, spec)
			if err != nil || res.Pack == nil {
				t.Errorf("exclude %v, dest %q: a local pack's link = %v, want it followed", exclude, spec.Dest, err)
				continue
			}
			if !slices.Contains(res.Staged.Staged, "skills/leak/SKILL.md") {
				t.Errorf("exclude %v, dest %q: the followed file is not in the result: %v", exclude, spec.Dest,
					res.Staged.Staged)
			}
			if len(exclude) > 0 && slices.Contains(res.Staged.Staged, "README.md") {
				t.Errorf("dest %q: the filter must still apply to a followed pack: %v", spec.Dest, res.Staged.Staged)
			}
		}
		if _, err := ResolvePackForProcess(entry, ResolvePackSpec{}); err != nil {
			t.Errorf("exclude %v: a process resolution refused a local pack's link: %v", exclude, err)
		}
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
