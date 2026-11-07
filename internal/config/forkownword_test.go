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
