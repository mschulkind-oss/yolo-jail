package config

// packresolve_test.go pins the one pack resolver (packresolve.go) directly. The call sites that
// route through it are pinned where they live: the launch in internal/cli/run
// (packresolvelaunch_test.go), the host verbs in internal/cli (hostpackresolve_test.go).

import (
	"os"
	"path/filepath"
	"slices"
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

// DECLARATION MODE GIVES THE LAUNCH'S ANSWER for an unfiltered pack it reads in place
// (packstage.Check), so "does this pack resolve" is the same in both modes: for a LOCAL pack, an
// escaping link is followed in both (OQ-NC9, ruled A). The refusal half, for a fetched pack, is
// pinned where a fetched fixture exists (TestStagePacksRefusesAFetchedPacksEscapingSymlink, and
// TestApplyHostRefusesAFetchedPackWithAnEscapingSymlink at the host); packstage's own tests pin
// the rule a non-following walk applies.
func TestResolvePackDeclarationModeAnswersAsTheLaunchDoes(t *testing.T) {
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

	declared, err := ResolvePack(entry, ResolvePackSpec{})
	if err != nil || declared.Pack == nil {
		t.Fatalf("declaration mode over a local pack's escaping symlink = %v, want it followed", err)
	}
	staged, err := ResolvePack(entry, ResolvePackSpec{Dest: filepath.Join(t.TempDir(), "esc")})
	if err != nil || staged.Pack == nil {
		t.Fatalf("stage mode over a local pack's escaping symlink = %v, want it followed", err)
	}
	if !slices.Equal(declared.Staged.Staged, staged.Staged.Staged) {
		t.Errorf("the two modes disagree about what the pack holds: %v vs %v",
			declared.Staged.Staged, staged.Staged.Staged)
	}
	if body, err := os.ReadFile(filepath.Join(staged.Pack.Root, "skills", "leak.md")); err != nil || string(body) != "s" {
		t.Errorf("the staged copy must hold the link's target as a file: %q, %v", body, err)
	}
}

// AN EMBEDDED ENTRY RESOLVES OFFICIAL, IN EVERY MODE, and nothing else does (packload.Pack.Official,
// the mark that admits a service's host half, docs/design/host-notch-services.md OQ-HS4). A local
// pack that takes an embedded pack's name is not yolo's, whatever it declares.
func TestResolvePackMarksOnlyAnEmbeddedEntryOfficial(t *testing.T) {
	for _, spec := range []ResolvePackSpec{{}, {Dest: filepath.Join(t.TempDir(), "wire-bridge")}} {
		res, err := ResolvePack(EmbeddedPackEntry("wire-bridge"), spec)
		if err != nil || res.Pack == nil || !res.Pack.Official {
			t.Errorf("an embedded entry (dest %q) must resolve official: %+v, %v", spec.Dest, res.Pack, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "wire-bridge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	local := PackEntry{Source: "file://" + dir, Name: "wire-bridge"}
	res, err := ResolvePack(local, ResolvePackSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pack.Official {
		t.Error("a local pack named like an embedded one resolved official")
	}
}

// A FILE:// ENTRY, THE CONVENTIONAL LOCAL PACK INCLUDED, RESOLVES LOCAL, in both modes and through
// its filters, and a FETCHED one does not, though its repository is a path on this machine too:
// packload.Pack.Local is what admits a pack's host half at the host and on macos-user
// (docs/design/host-notch-services.md HS-D27), and only the user's own file:// line, or the
// directory beside their config, is that word. An embedded entry is official and not local.
// Deleting the Local line in ResolvePack fails every local case.
func TestResolvePackMarksAFileEntryLocalAndAFetchedOneNot(t *testing.T) {
	home := useProfileKeysHome(t)
	for _, spec := range []ResolvePackSpec{{}, {Dest: filepath.Join(t.TempDir(), "wire-bridge")}} {
		res, err := ResolvePack(EmbeddedPackEntry("wire-bridge"), spec)
		if err != nil || res.Pack == nil || res.Pack.Local || !res.Pack.MayRunHostHalf() {
			t.Errorf("an embedded entry (dest %q) resolved %+v, %v; want official, not local", spec.Dest, res.Pack, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := PackEntry{Source: "file://" + dir, Name: "mine"}
	filtered := local
	filtered.Exclude = []string{"README.md"}
	for name, tc := range map[string]struct {
		entry PackEntry
		spec  ResolvePackSpec
	}{
		"declaration": {local, ResolvePackSpec{}},
		"staged":      {local, ResolvePackSpec{Dest: filepath.Join(t.TempDir(), "mine")}},
		"filtered":    {filtered, ResolvePackSpec{}},
	} {
		res, err := ResolvePack(tc.entry, tc.spec)
		if err != nil || res.Pack == nil || !res.Pack.Local || res.Pack.Official || !res.Pack.MayRunHostHalf() {
			t.Errorf("%s: a file:// entry resolved %+v, %v; want local, not official", name, res.Pack, err)
		}
	}

	if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail", "local"), 0o755); err != nil {
		t.Fatal(err)
	}
	conventional, ok := localPackEntry()
	if !ok {
		t.Fatal("fixture: the conventional local pack's directory was not found")
	}
	if res, err := ResolvePack(conventional, ResolvePackSpec{}); err != nil || res.Pack == nil || !res.Pack.Local {
		t.Errorf("the conventional local pack resolved %+v, %v; want local", res.Pack, err)
	}

	repo := gitPackRepo(t, map[string]string{"pack.json": `{"name":"gp"}`}, nil)
	source := "git+file://" + repo + "?ref=main"
	syncFetchedPack(t, source)
	res, err := ResolvePack(PackEntry{Source: source, Name: "gp"}, ResolvePackSpec{Getenv: noPackRoot})
	if err != nil || res.Pack == nil {
		t.Fatalf("the fetched entry did not resolve: %v", err)
	}
	if res.Pack.Local || res.Pack.Official || res.Pack.MayRunHostHalf() {
		t.Errorf("a git+file:// entry resolved local %v, official %v: a fetched pack's host half must not run",
			res.Pack.Local, res.Pack.Official)
	}
}
