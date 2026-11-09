package config

// forkownword_test.go: a fork's `fork_of` names a CONFIGURED base by the base's own word, through
// the real config path (FP-D5). `fork_of` must be a bare name, and a configured local pack's Name
// is always its `<source>/<pack>` handle, so a selection lowered from a TOML user config never
// carries a bare name for it; TestSelectPacksAppliesAForkToAConfiguredBase hand-builds one and so
// could not see that.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const forkOwnWordBase = `[[program]]
bin = "tool"
via = "npm"
package = "tool"
`

const forkOwnWordFork = `[[program]]
bin = "tool"
via = "source"
fork_of = "board"
source = "git+https://example.invalid/tool-fork?ref=main"
build = "make install"
produces = [".local/bin/tool"]
`

// forkOwnWordHome writes a TOML user config selecting packs from a `src` pack source holding the
// given pack directories (name → pack.toml).
func forkOwnWordHome(t *testing.T, selected []string, packs map[string]string) {
	t.Helper()
	home := readablePackTestHome(t)
	root := filepath.Join(home, "src", "packs")
	for name, manifest := range packs {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "pack.toml"), manifest)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = ['"+strings.Join(selected, "', '")+"']\n[pack_sources]\nsrc = '"+root+"'\n")
}

// forkOwnWordResolve is the production resolver SelectedPackDeclarations hands SelectPacks.
func forkOwnWordResolve(entry PackEntry) (*packload.Pack, error) {
	res, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true})
	if err != nil {
		return nil, err
	}
	if res.Pack == nil || len(res.Problems) > 0 {
		return nil, fmt.Errorf("%s: %v", entry.Name, res.Problems)
	}
	return res.Pack, nil
}

func TestAForkNamesAConfiguredBaseByItsOwnWord(t *testing.T) {
	forkOwnWordHome(t, []string{"src/board", "src/matt"},
		map[string]string{"board": forkOwnWordBase, "matt": forkOwnWordFork})
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	sel, err := SelectPacks(entries, PackSelectSpec{Resolve: forkOwnWordResolve, FailFast: true})
	if err != nil {
		t.Fatalf("a fork of src/board written as fork_of = \"board\" was refused: %v", err)
	}
	if got := installKindOf(sel.Packs(), "src/board", "tool"); got != packdecl.InstallKindSource {
		t.Errorf("src/board's program is %q, want the fork's %q", got, packdecl.InstallKindSource)
	}
}

// Two selected packs with the same own word: the fork's base is ambiguous, and the refusal names
// both handles.
func TestAForkRefusesAnOwnWordTwoSelectedPacksShare(t *testing.T) {
	forkOwnWordHome(t, []string{"src/board", "src/matt", "local/board"},
		map[string]string{"board": forkOwnWordBase, "matt": forkOwnWordFork})
	local := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "packs", "board")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(local, "pack.toml"), forkOwnWordBase)
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	_, err = SelectPacks(entries, PackSelectSpec{Resolve: forkOwnWordResolve, FailFast: true})
	if err == nil || !strings.Contains(err.Error(), "src/board") || !strings.Contains(err.Error(), "local/board") {
		t.Fatalf("err = %v, want a refusal naming both packs called board", err)
	}
}

// forkOwnWordEditHome is the owner's shape (2026-11-09): a bare `path` pack source holding a local
// base pack and a pack whose program forks it from source, with an embedded set that ships no pack
// of the base's name. It selects the named packs and returns the workspace and the user config.
func forkOwnWordEditHome(t *testing.T, packs map[string]string, selected ...string) (workspace, physical string) {
	t.Helper()
	home := readablePackTestHome(t)
	t.Setenv("YOLO_VERSION", "")
	resetEmbeddedForExplicitEditTest(t)
	packload.SetEmbeddedFS(fstest.MapFS{"shipped/pack.json": {Data: []byte(`{"name":"shipped"}`)}})
	root := filepath.Join(home, "dotfiles", "yolo-packs")
	for name, manifest := range packs {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "pack.toml"), manifest)
	}
	physical = filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	if err := os.MkdirAll(filepath.Dir(physical), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, physical, "packs = ['"+strings.Join(selected, "', '")+"']\n\n"+
		"[pack_sources.yolo-packs]\npath = '"+root+"'\nbare = true\n")
	return explicitEditTempDir(t), physical
}

// validateForkOwnWordEdit runs the explicit edit validation `yolo allow` and `yolo config set`
// run (configedit's validateTarget calls ValidateExplicitEdit) over the user config with one
// unrelated key added.
func validateForkOwnWordEdit(t *testing.T, workspace, physical string) error {
	t.Helper()
	before, err := os.ReadFile(physical)
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewConfigReadInput(physical, append(before, []byte("\n[install]\nnix = ['hello']\n")...))
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewExplicitEditTarget(workspace, physical, ExplicitEditUser, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidateExplicitEdit(workspace, target, input)
	return err
}

// The explicit edit validator resolves a fork's configured base as the launch does: a base the
// user selected is not an embedded pack the closure failed to find, so an edit is not refused
// with "embedded pack board is not present in the immutable source" (the owner's
// `yolo allow github on_request create_pr`, 2026-11-09).
func TestExplicitEditAcceptsAForkOfASelectedConfiguredBase(t *testing.T) {
	workspace, physical := forkOwnWordEditHome(t,
		map[string]string{"board": forkOwnWordBase, "matt": forkOwnWordFork}, "yolo-packs/board", "yolo-packs/matt")
	if err := validateForkOwnWordEdit(t, workspace, physical); err != nil {
		t.Fatalf("an edit to a config selecting yolo-packs/board and its fork was refused: %v", err)
	}
}

// A fork whose base is truly absent is still refused, and the refusal names the fork, the base it
// names, and the fix (select the base in packs), never the shipped pack source.
func TestExplicitEditRefusesAForkWhoseBaseIsNotSelectedNamingTheFix(t *testing.T) {
	workspace, physical := forkOwnWordEditHome(t,
		map[string]string{"board": forkOwnWordBase, "matt": forkOwnWordFork}, "yolo-packs/matt")
	err := validateForkOwnWordEdit(t, workspace, physical)
	if err == nil {
		t.Fatal("an edit to a config selecting a fork without its base was accepted")
	}
	for _, want := range []string{"yolo-packs/matt", "fork", "board", "packs list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "immutable source") {
		t.Errorf("refusal blames the shipped pack source for a pack yolo does not ship: %v", err)
	}
}
