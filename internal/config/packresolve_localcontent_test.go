package config

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ResolvePack's public caller must use the origin-aware reader for the conventional local alias.
// The host and materialized reads then discover the same linked contributions and overlays,
// retain source-facing paths, and compose the same selected-owner/profile result.
func TestResolvePackReadsTrustedLocalAliasContentLikeItsMaterializedTree(t *testing.T) {
	useProfileKeysHome(t)
	localDir := paths.LocalPackDir()
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := localDir
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"manifest-name-must-not-replace-local"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The home root, a nested home directory, and home files are all user-authored aliases.
	// The executable pins mode and source-path preservation.
	linkedHome := t.TempDir()
	linkedDocs := t.TempDir()
	if err := os.Symlink(linkedHome, filepath.Join(root, "home")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedDocs, filepath.Join(linkedHome, "docs")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedDocs, "README.md"), []byte("linked home content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkedFile := filepath.Join(t.TempDir(), "profile")
	if err := os.WriteFile(linkedFile, []byte("linked file content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedFile, filepath.Join(linkedHome, ".profile")); err != nil {
		t.Fatal(err)
	}

	linkedExecutable := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(linkedExecutable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedExecutable, filepath.Join(root, "skills", "tool", "run.sh")); err != nil {
		t.Fatal(err)
	}

	// A conventional overlay is also linked to a file outside the pack root. Its profile gate
	// makes the composed result prove that the actual collector saw the selected layer.
	overlayDir := filepath.Join(root, "overlays")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overlaySource := filepath.Join(t.TempDir(), "claude-settings@linked.json")
	if err := os.WriteFile(overlaySource, []byte(`{"env":{"LOCAL_LINKED_OVERLAY":"present"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(overlaySource, filepath.Join(overlayDir, "claude-settings@linked.json")); err != nil {
		t.Fatal(err)
	}

	entry, ok := localPackEntry()
	if !ok || entry.Name != LocalPackName || !entry.Implicit {
		t.Fatalf("conventional local entry = %+v, %v", entry, ok)
	}
	owner, err := ResolvePack(EmbeddedPackEntry("claude"), ResolvePackSpec{})
	if err != nil || owner.Pack == nil || !owner.Pack.Official {
		t.Fatalf("embedded owner: pack=%+v err=%v", owner.Pack, err)
	}
	surfaces, problems := owner.Pack.SurfacesFor(false)
	if len(problems) != 0 {
		t.Fatalf("claude surfaces: %v", problems)
	}
	var surface *manifest.Surface
	for i := range surfaces {
		if surfaces[i].Agent == "claude" && surfaces[i].Name == "settings" {
			surface = &surfaces[i]
			break
		}
	}
	if surface == nil {
		t.Fatal("fixture: embedded claude does not own claude/settings")
	}

	declaration, err := ResolvePack(entry, ResolvePackSpec{})
	if err != nil {
		t.Fatalf("ResolvePack(local alias): %v", err)
	}
	if declaration.Pack == nil || len(declaration.Problems) != 0 {
		t.Fatalf("local alias did not use its origin-aware content reader: pack=%+v problems=%v", declaration.Pack, declaration.Problems)
	}
	if declaration.Pack.Name != LocalPackName || !declaration.Pack.Local || declaration.Pack.Official {
		t.Errorf("local provenance/name changed: name=%q local=%v official=%v", declaration.Pack.Name,
			declaration.Pack.Local, declaration.Pack.Official)
	}
	wantContributions := map[string]string{
		"home/docs/README.md": "docs/README.md",
		"home/.profile":       ".profile",
	}
	for _, contribution := range declaration.Pack.Decl.Contributions() {
		if contribution.Kind == "files" {
			if want, ok := wantContributions[contribution.From]; ok && want != contribution.Into {
				t.Errorf("contribution %q lands at %q, want alias-relative %q", contribution.From, contribution.Into, want)
			}
			delete(wantContributions, contribution.From)
		}
	}
	if len(wantContributions) != 0 {
		t.Errorf("local linked home contributions missing: %v", wantContributions)
	}

	active := map[string][]string{"claude": {"linked"}}
	compose := func(contributor *packload.Pack, profiles map[string][]string) []byte {
		t.Helper()
		set := packoverlay.Collect([]*packload.Pack{owner.Pack, contributor}, false, profiles)
		if len(set.Problems) != 0 || len(set.Orphans) != 0 {
			t.Fatalf("packoverlay.Collect: problems=%v orphans=%+v", set.Problems, set.Orphans)
		}
		result, err := agentcfg.Compose(agentcfg.Inputs{Surface: *surface, Overlays: set.For("claude", "settings")})
		if err != nil {
			t.Fatalf("agentcfg.Compose: %v", err)
		}
		return result.Encoded
	}
	declarationOutput := compose(declaration.Pack, active)
	if !strings.Contains(string(declarationOutput), `"LOCAL_LINKED_OVERLAY"`) {
		t.Errorf("selected local overlay is missing from composed output: %s", declarationOutput)
	}
	if inactive := compose(declaration.Pack, nil); strings.Contains(string(inactive), `"LOCAL_LINKED_OVERLAY"`) {
		t.Errorf("profile-inactive local overlay reached composed output: %s", inactive)
	}

	dest := filepath.Join(t.TempDir(), "staged-local")
	staged, err := ResolvePack(entry, ResolvePackSpec{Dest: dest})
	if err != nil || staged.Pack == nil || len(staged.Problems) != 0 {
		t.Fatalf("ResolvePack(local alias, Dest): pack=%+v problems=%v err=%v", staged.Pack, staged.Problems, err)
	}
	if staged.Pack.Name != LocalPackName || !staged.Pack.Local || staged.Pack.Official {
		t.Errorf("staged local provenance/name changed: name=%q local=%v official=%v", staged.Pack.Name,
			staged.Pack.Local, staged.Pack.Official)
	}
	for rel, want := range map[string]string{
		"home/docs/README.md": "linked home content\n",
		"home/.profile":       "linked file content\n",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("staged alias-relative contribution %s = %q, %v; want %q", rel, got, err, want)
		}
	}
	executable := filepath.Join(dest, "skills", "tool", "run.sh")
	if info, err := os.Stat(executable); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("staged linked executable mode = %v, %v; want executable", info, err)
	}
	if got, want := staged.Pack.SourcePath(executable), filepath.Join(localDir, "skills", "tool", "run.sh"); got != want {
		t.Errorf("staged executable source path = %q, want alias-relative %q", got, want)
	}
	if got, want := compose(staged.Pack, active), declarationOutput; !bytes.Equal(got, want) {
		t.Errorf("materialized local content composed differently:\n got %s\nwant %s", got, want)
	}

	// A pack whose manifest merely looks local is still fetched: its linked overlay is refused
	// by staging, and origin metadata stays nonlocal/nonofficial.
	fetchedSourceFile := filepath.Join(t.TempDir(), "fetched-overlay.json")
	if err := os.WriteFile(fetchedSourceFile, []byte(`{"env":{"SHOULD_NOT_LOAD":"yes"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := gitPackRepo(t, map[string]string{"pack.json": `{"name":"local"}`},
		map[string]string{"overlays/claude-settings.json": fetchedSourceFile})
	fetchedAddress := "git+file://" + repo + "?ref=main"
	syncFetchedPack(t, fetchedAddress)
	fetched, err := ResolvePack(PackEntry{Source: fetchedAddress, Name: "local"},
		ResolvePackSpec{Getenv: noPackRoot})
	if err == nil || !strings.Contains(err.Error(), "symlink pointing outside the pack") {
		t.Errorf("fetched local-looking pack = %+v, %v; want its escaping linked overlay refused", fetched, err)
	}
	if fetched.Pack != nil && (fetched.Pack.Local || fetched.Pack.Official) {
		t.Errorf("fetched local-looking manifest gained local/official provenance: %+v", fetched.Pack)
	}
}

// An explicit yolo/<name> handle keeps the authored staging slug but resolves against the
// embedded content's canonical name, source and official provenance in every resolver mode.
func TestResolvePackPreservesExplicitEmbeddedHandleAcrossStagingAndFiltering(t *testing.T) {
	home := useProfileKeysHome(t)
	configDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, paths.UserConfigTOMLName), []byte("packs = ['yolo/hello-daemon']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("LoadPackEntries: entries=%+v problems=%v err=%v", entries, problems, err)
	}
	entry := entries[0]
	if entry.Name != "yolo/hello-daemon" || entry.EmbeddedName != "hello-daemon" || !entry.Embedded() || entry.Source != "embedded:hello-daemon" {
		t.Fatalf("explicit embedded entry lost its handle/source mapping: %+v", entry)
	}
	if got, want := entry.Slug(), (HostFileEntry{Path: "yolo/hello-daemon"}).Slug(); got != want {
		t.Fatalf("explicit embedded slug = %q, want %q", got, want)
	}

	var embedded *packload.Pack
	for _, candidate := range packload.Embedded() {
		if candidate.Name == "hello-daemon" {
			embedded = candidate
			break
		}
	}
	if embedded == nil {
		t.Fatalf("embedded materialization has no %q pack", entry.EmbeddedName)
	}
	manifestPath := filepath.Join(embedded.Root, "pack.toml")
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read immutable embedded manifest: %v", err)
	}
	assertPack := func(where string, resolved ResolvedPack, wantRoot, wantSourceRoot string) {
		t.Helper()
		if resolved.Pack == nil || len(resolved.Problems) != 0 {
			t.Fatalf("%s: pack=%+v problems=%v", where, resolved.Pack, resolved.Problems)
		}
		if resolved.Pack.Name != "hello-daemon" || resolved.Pack.Root != wantRoot || resolved.Pack.SourceRoot != wantSourceRoot ||
			resolved.Pack.Local || !resolved.Pack.Official {
			t.Errorf("%s identity = name:%q root:%q sourceRoot:%q local:%v official:%v", where,
				resolved.Pack.Name, resolved.Pack.Root, resolved.Pack.SourceRoot,
				resolved.Pack.Local, resolved.Pack.Official)
		}
	}

	inPlace, err := ResolvePack(entry, ResolvePackSpec{})
	if err != nil {
		t.Fatalf("unfiltered embedded resolution: %v", err)
	}
	assertPack("in place", inPlace, embedded.Root, "")
	if inPlace.Pack != embedded || inPlace.Pack.StagedSlug() != filepath.Base(embedded.Root) || inPlace.Staged != nil {
		t.Errorf("unfiltered embedded resolution did not retain its immutable materialization: %+v", inPlace)
	}

	stagedDest := filepath.Join(t.TempDir(), entry.Slug())
	staged, err := ResolvePack(entry, ResolvePackSpec{Dest: stagedDest})
	if err != nil {
		t.Fatalf("staged embedded resolution: %v", err)
	}
	assertPack("staged", staged, stagedDest, embedded.Root)
	if staged.Pack.StagedSlug() != entry.Slug() || staged.Pack.SourcePath(filepath.Join(stagedDest, "pack.toml")) != manifestPath {
		t.Errorf("staged embedded slug/source mapping changed: slug=%q source=%q", staged.Pack.StagedSlug(),
			staged.Pack.SourcePath(filepath.Join(stagedDest, "pack.toml")))
	}

	filteredEntry := entry
	filteredEntry.Exclude = []string{"README.md"}
	filteredDest := filepath.Join(t.TempDir(), entry.Slug())
	filtered, err := ResolvePack(filteredEntry, ResolvePackSpec{Dest: filteredDest})
	if err != nil {
		t.Fatalf("filtered staged embedded resolution: %v", err)
	}
	assertPack("filtered staged", filtered, filteredDest, embedded.Root)
	if filtered.Pack.StagedSlug() != entry.Slug() || slices.Contains(filtered.Staged.Staged, "README.md") ||
		!slices.Contains(filtered.Staged.Excluded, "README.md") ||
		filtered.Pack.SourcePath(filepath.Join(filteredDest, "pack.toml")) != manifestPath {
		t.Errorf("filtered staged embedded slug/filter/source changed: slug=%q staged=%v excluded=%v source=%q",
			filtered.Pack.StagedSlug(), filtered.Staged.Staged, filtered.Staged.Excluded,
			filtered.Pack.SourcePath(filepath.Join(filteredDest, "pack.toml")))
	}

	filteredDeclaration, err := ResolvePack(filteredEntry, ResolvePackSpec{})
	if err != nil {
		t.Fatalf("filtered declaration embedded resolution: %v", err)
	}
	if filteredDeclaration.Pack == nil {
		t.Fatalf("filtered declaration returned no loaded pack: %+v", filteredDeclaration)
	}
	assertPack("filtered declaration", filteredDeclaration, filteredDeclaration.Pack.Root, embedded.Root)
	if filteredDeclaration.Pack.StagedSlug() != entry.Slug() ||
		filteredDeclaration.Pack.SourcePath(filepath.Join(filteredDeclaration.Pack.Root, "pack.toml")) != manifestPath {
		t.Errorf("filtered declaration embedded slug/source changed: slug=%q source=%q",
			filteredDeclaration.Pack.StagedSlug(),
			filteredDeclaration.Pack.SourcePath(filepath.Join(filteredDeclaration.Pack.Root, "pack.toml")))
	}
	if _, err := os.Stat(filteredDeclaration.Pack.Root); !os.IsNotExist(err) {
		t.Errorf("filtered declaration tree lifetime: temp root %q still exists or has unexpected stat error %v",
			filteredDeclaration.Pack.Root, err)
	}
	if manifestAfter, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(manifestAfter, manifestBefore) {
		t.Errorf("resolver mutated the immutable embedded manifest: read error=%v", err)
	}
}
