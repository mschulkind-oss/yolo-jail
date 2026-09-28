package cli

// packprecedence_test.go pins the HOST's call site of the one pack order
// (config.PackSelection.Packs, docs/plans/notch-convergence.md OQ-NC4, ruled A): config order as
// written, then the closure's additions, then the conventional local pack last. The launch's and
// the boot's call sites are pinned in internal/cli/run over the same fixture.
//
// The host followed config order already, but put the local pack before the closure's additions,
// because the conventional local pack is the last ENTRY and the additions came after every entry.

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestHostVerbsOrderPacksAsTheConfigListsThem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	zeta := filepath.Join(t.TempDir(), "zeta")
	writeFile(t, filepath.Join(zeta, "pack.json"), `{"name":"zeta","needs":[{"pack":"hello-daemon"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"), `{"name":"local"}`)
	selectPacks(t, home, `"file://`+zeta+`","guardrails"`)

	set := selectConfiguredHostPacks()
	var got []string
	for _, p := range set.packs {
		got = append(got, p.Name)
	}
	want := []string{"zeta", "guardrails", "hello-daemon", "local"}
	if !slices.Equal(got, want) {
		t.Errorf("the host selects %v, want %v (config order, the closure, then the local pack)", got, want)
	}
}
