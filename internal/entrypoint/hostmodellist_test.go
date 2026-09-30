package entrypoint

// hostmodellist_test.go pins the list half of codex's menu at `yolo host --`
// (docs/design/model-lists-and-pickers.md MM-D24): the shipped codex pack's own list derive, run
// over one launch's wire tables, gives the list a jail's boot would render into the file its
// `model_menu.list` names, and writes nothing. The call site, hostExec, is pinned in internal/cli.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostListFor is HostModelList for the shipped codex pack over tables selecting use for codex.
func hostListFor(t *testing.T, home, use string) ([]modelmenu.ListEntry, error) {
	t.Helper()
	packs := testPacksForAgent(t, "codex", "openrouter")
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	var codex *packload.Pack
	for _, p := range packs {
		if p.Name == "codex" {
			codex = p
		}
	}
	in := &HostInputs{Packs: packs, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, table),
		"YOLO_USE_PROFILES": use,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	return HostModelList(codex, *shippedCodexInstall(t).ModelMenu, home, in, func(line string) {
		t.Errorf("the derive warned: %s", line)
	})
}

func TestHostModelListIsTheLaunchsListAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	list, err := hostListFor(t, home, `{"codex":"codex"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []modelmenu.ListEntry{{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol"}, {ID: "gpt-6-astra", Name: "GPT-6 Astra"},
		{ID: "gpt-6-luna", Name: "GPT-6 Luna"}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("the host list on -p codex = %v, want the list the jail's boot renders, %v", list, want)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("composing the list wrote into the home: %v", entries)
	}
	other, err := hostListFor(t, home, `{"codex":"openrouter"}`)
	if err != nil || len(other) != 0 {
		t.Errorf("the host list on openrouter = %v (%v), want none", other, err)
	}
	none, err := hostListFor(t, home, `{}`)
	if err != nil || len(none) != 0 {
		t.Errorf("the host list with no profile = %v (%v), want none", none, err)
	}
}

// A declaration whose list no surface of the pack writes is an error, never an empty list read as
// "no menu": the path equality is the only thing tying the two together.
func TestHostModelListNamesADeclarationNoSurfaceWrites(t *testing.T) {
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatal(err)
	}
	spec := *shippedCodexInstall(t).ModelMenu
	spec.List = ".codex/elsewhere.json"
	_, err = HostModelList(codex, spec, t.TempDir(), &HostInputs{}, nil)
	if err == nil || !strings.Contains(err.Error(), filepath.ToSlash("~/.codex/elsewhere.json")) {
		t.Errorf("err = %v, want it to name the path no surface writes", err)
	}
}
