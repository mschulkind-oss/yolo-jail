package cli

// modelmenu_dispatch_test.go pins the call site the generated launchers reach: `yolo internal
// model-menu` (modelmenu.Verb) dispatches to modelmenu.Run with the process's HOME, which the
// launcher's shell relies on for every path in the pack's declaration
// (docs/design/model-lists-and-pickers.md MM-D22). An unknown verb also exits 2, so the case
// runs a whole, valid invocation and reads its effect on HOME.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
)

func TestYoloInternalModelMenuRunsTheMenuAgainstHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stale := filepath.Join(home, ".app", "menu.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := `{"catalog":["x"],"list":".app/list.json","into":".app/menu.json",` +
		`"flag":["-c","k={into}"],"entries":"models","id":"slug"}`
	if rc := runInternal([]string{modelmenu.Verb, "--bin=app", "--spec=" + spec, "--", "/bin/true"}); rc != 0 {
		t.Fatalf("yolo internal %s = %d, want 0 for a launch whose list names no model", modelmenu.Verb, rc)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the menu under HOME survived a launch with no list (%v): the verb did not reach "+
			"modelmenu.Run with the process's HOME", err)
	}
}
