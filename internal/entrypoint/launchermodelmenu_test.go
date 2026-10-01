package entrypoint

// launchermodelmenu_test.go pins the launcher half of a program's model menu (packdecl.ModelMenu;
// docs/design/model-lists-and-pickers.md MM-D9, MM-D22) on a template of its own: the npm
// launcher runs the same step the native one does (codexmodelmenu_test.go pins that one through
// the shipped codex pack), through `yolo internal model-menu`, which is this test binary
// re-executed as TestModelMenuHelper. The program is a shell stub, so no agent runs.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const modelMenuHelperEnv = "YOLO_TEST_MODEL_MENU_HELPER"

// TestModelMenuHelper is not a test: it is `yolo internal model-menu` for the launcher runs
// below, when this binary is re-executed with the helper variable set.
func TestModelMenuHelper(t *testing.T) {
	if os.Getenv(modelMenuHelperEnv) != "1" {
		t.Skip("helper process for the codex model-menu launcher tests")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) < 2 || args[0] != "internal" || args[1] != modelmenu.Verb {
		fmt.Fprintf(os.Stderr, "helper: unexpected argv %q\n", args)
		os.Exit(2)
	}
	os.Exit(modelmenu.Run(args[2:], os.Getenv("HOME"), os.Stdout, os.Stderr))
}

// THE NPM TEMPLATE RUNS THE SAME STEP: model_menu is a program's declaration whatever delivered
// the program, so an npm program declaring one is exec'd with its flag too.
func TestTheNpmLauncherHandsItsProgramAModelMenuToo(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	realBin := filepath.Join(home, ".npm-global", "bin", "tool")
	for _, d := range []string{fakeBin, filepath.Dir(realBin), filepath.Join(home, ".tool")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argvLog := filepath.Join(home, "tool.argv")
	stub := "#!/bin/bash\n" +
		"if [ \"$*\" = 'catalog' ]; then echo '{\"entries\":[{\"key\":\"m-1\"}]}'; exit 0; fi\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shellSingleQuote(argvLog) + "; done\n"
	if err := os.WriteFile(realBin, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".tool", "list.json"),
		[]byte(`{"models":[{"id":"m-1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	yolo := "#!/bin/sh\n" + modelMenuHelperEnv + "=1 exec " + shellSingleQuote(self) +
		" -test.run='^TestModelMenuHelper$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "yolo"), []byte(yolo), 0o755); err != nil {
		t.Fatal(err)
	}
	menu := &packdecl.ModelMenu{Catalog: []string{"catalog"}, List: ".tool/list.json",
		Into: ".tool/menu.json", Flag: []string{"--menu={into}"}, Entries: "entries", ID: "key"}
	body := npmAgentLauncher("probe", &packdecl.Install{Kind: "npm", Bin: "tool", Package: "tool", ModelMenu: menu},
		filepath.Join(home, "stamps"), filepath.Join(home, "receipts.jsonl"), false, launcherServers{}, nil)
	out, rc := runLauncher(t, home, "tool-launcher", body, fakeBin)
	if rc != 0 {
		t.Fatalf("the npm launcher failed (rc=%d):\n%s", rc, out)
	}
	want := "--menu=" + filepath.Join(home, ".tool", "menu.json")
	if got := logLines(t, argvLog); len(got) != 1 || got[0] != want {
		t.Errorf("the npm program was exec'd with %q, want the menu flag %q alone", got, want)
	}
}
