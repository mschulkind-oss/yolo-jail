package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholesenable_test.go drives `yolo loopholes enable|disable` through the real dispatch, over a
// temporary HOME selecting the github pack, as a user runs it (docs/design/boundary-broker.md
// OQ-BB12, OQ-BB13, BB-D57).

// enableHome is a resolved HOME whose user config selects the github and journal packs, written as a person
// writes it (comments included), and a resolved workspace with a workspace config and a folder
// `sub` inside it. The working directory is the workspace, as a launch there has it.
func enableHome(t *testing.T) (home, userCfg, userBody, ws string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	userCfg = filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatal(err)
	}
	userBody = "// mine, by hand\n{\n  \"packs\": [\"github\", \"journal\"], // the forwarder, and a loophole that is not brokered\n}\n"
	if err := os.WriteFile(userCfg, []byte(userBody), 0o644); err != nil {
		t.Fatal(err)
	}
	ws = filepath.Join(home, "code", "app")
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, config.WorkspaceConfigName), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(ws)
	return home, userCfg, userBody, ws
}

// TestLoopholesEnableWritesThePerWorkspaceFileAndNothingElse: `enable` writes the workspace's
// per-workspace file (the working directory's, as a launch there uses), says which file and that it
// applies at the next fresh launch, and the launch's config then has the switch; `disable` turns
// it off in the same file; `--workspace` names another workspace; and config.jsonc's bytes never
// change.
func TestLoopholesEnableWritesThePerWorkspaceFileAndNothingElse(t *testing.T) {
	home, userCfg, userBody, ws := enableHome(t)

	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "github-broker"})
	if rc != 0 {
		t.Fatalf("enable rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	path := config.WorkspaceFilePath(ws)
	if !strings.HasPrefix(path, filepath.Join(home, ".config", "yolo-jail", "workspaces")+string(filepath.Separator)) {
		t.Fatalf("the per-workspace file %s is not in the folder beside the user config", path)
	}
	for _, want := range []string{"github-broker is on for " + ws, "next fresh launch", path} {
		if !strings.Contains(stdout, want) {
			t.Errorf("enable's line does not say %q:\n%s", want, stdout)
		}
	}
	if n := strings.Count(strings.TrimSpace(stdout), "\n"); n != 0 {
		t.Errorf("enable printed %d lines, want one:\n%s", n+1, stdout)
	}
	cfg, err := config.LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := cfg.Get("loopholes")
	if v, set := loopholes.ConfigEnabledOverride(asMapForTest(block), "github-broker"); !set || !v {
		t.Fatalf("after enable the launch's config has github-broker enabled=%v set=%v", v, set)
	}

	rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "disable", "github-broker"})
	if rc != 0 || !strings.Contains(stdout, "github-broker is off for "+ws) {
		t.Fatalf("disable rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if wf := config.ReadWorkspaceFile(ws); wf.Switches() != "github-broker off" {
		t.Errorf("after disable the file says %q", wf.Switches())
	}

	other := filepath.Join(home, "code", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "enable", "github-broker", "--workspace", other})
	if rc != 0 || !strings.Contains(stdout, "github-broker is on for "+other) {
		t.Fatalf("enable --workspace rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if wf := config.ReadWorkspaceFile(other); wf.Switches() != "github-broker on" {
		t.Errorf("--workspace wrote %q for the other workspace", wf.Switches())
	}
	if wf := config.ReadWorkspaceFile(ws); wf.Switches() != "github-broker off" {
		t.Errorf("--workspace changed this workspace's file too: %q", wf.Switches())
	}

	if after, _ := os.ReadFile(userCfg); string(after) != userBody {
		t.Errorf("config.jsonc's bytes changed:\n before: %q\n  after: %q", userBody, after)
	}
}

// TestLoopholesEnableRefusesWhatItCannotDo: each refusal exits non-zero, writes nothing, and names
// the next step: an unknown name (`yolo loopholes list`), a jail (the host command, with this
// jail's workspace as the host names it), `--global` for a brokered loophole (the per-project
// command), and a home that cannot be a workspace. `--global` for any other loophole still
// prints the block to paste, and writes nothing.
func TestLoopholesEnableRefusesWhatItCannotDo(t *testing.T) {
	home, userCfg, userBody, ws := enableHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail", "workspaces")
	for _, c := range []struct {
		name string
		args []string
		env  map[string]string
		cwd  string
		want []string
	}{
		{"unknown name", []string{"loopholes", "enable", "no-such"}, nil, "",
			[]string{`no loophole named "no-such"`, "`yolo loopholes list`"}},
		{"in a jail", []string{"loopholes", "enable", "github-broker"},
			map[string]string{"YOLO_VERSION": "9.9.9-test", "YOLO_HOST_DIR": "/home/someone/code/app",
				"YOLO_WORKSPACE": ws}, "",
			[]string{"this is a jail", "yolo loopholes enable github-broker --workspace /home/someone/code/app",
				"fresh jail"}},
		{"--global for a brokered loophole", []string{"loopholes", "enable", "github-broker", "--global"}, nil, "",
			[]string{"no every-project switch", "`yolo loopholes enable github-broker`"}},
		{"--global for another loophole", []string{"loopholes", "enable", "journal", "--global"}, nil, "",
			[]string{"writes nothing", `"journal": { "enabled": true }`, "config.jsonc"}},
		// A block naming nothing installed is one the launch then warns about, or, for a pack
		// selected later, refuses: --global checks the name first, as the per-project form does.
		{"--global for an unknown name", []string{"loopholes", "enable", "no-such", "--global"}, nil, "",
			[]string{`no loophole named "no-such"`, "`yolo loopholes list`"}},
		{"the home", []string{"loopholes", "enable", "github-broker"}, nil, home,
			[]string{"no launch may use it as a workspace", "--workspace <project>"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.cwd != "" {
				t.Chdir(c.cwd)
			}
			rc, stdout, stderr := captureDispatchRC(t, c.args)
			if rc == 0 {
				t.Fatalf("rc 0, want a refusal\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, stderr)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("a refusal wrote %v", entries)
			}
			if strings.Contains(c.name, "unknown") && strings.Contains(stderr, "add this to") {
				t.Errorf("an unknown name got a block to paste:\n%s", stderr)
			}
			if after, _ := os.ReadFile(userCfg); string(after) != userBody {
				t.Errorf("config.jsonc changed: %q", after)
			}
		})
	}
}

// asMapForTest is v as the config block a loophole switch is read from.
func asMapForTest(v any) *jsonx.OrderedMap {
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// TestLoopholesEnableSwitchesTheFolderALaunchHereUses: with no --workspace, the switch is for the
// working directory, which is the workspace a `yolo` launched in the same place uses, and not the
// nearest folder above it holding a workspace config or a launch's .yolo: the per-workspace file
// matches its exact folder, so a switch written for a parent would leave the launch here without
// it, and a `disable` here would miss the switch the launch here reads.
func TestLoopholesEnableSwitchesTheFolderALaunchHereUses(t *testing.T) {
	home, _, _, ws := enableHome(t)
	code := filepath.Join(home, "code")
	if err := os.MkdirAll(filepath.Join(code, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(code, ".yolo", "config-boot.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newproj := filepath.Join(code, "newproj")
	if err := os.MkdirAll(newproj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, cwd, parent string }{
		{"a folder inside a workspace config's", filepath.Join(ws, "sub"), ws},
		{"a project below a launched folder", newproj, code},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(c.cwd)
			rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "github-broker"})
			if rc != 0 || !strings.Contains(stdout, "github-broker is on for "+c.cwd+" ") {
				t.Fatalf("enable rc = %d, want it on for %s\nstdout:\n%s\nstderr:\n%s", rc, c.cwd, stdout, stderr)
			}
			if wf := config.ReadWorkspaceFile(c.cwd); wf.Switches() != "github-broker on" {
				t.Errorf("the launch here reads %q, want the switch", wf.Switches())
			}
			if wf := config.ReadWorkspaceFile(c.parent); wf != nil {
				t.Errorf("enable wrote the parent's file %s: %q", wf.Path, wf.Switches())
			}
			rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "disable", "github-broker"})
			if rc != 0 {
				t.Fatalf("disable rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
			}
			if wf := config.ReadWorkspaceFile(c.cwd); wf.Switches() != "github-broker off" {
				t.Errorf("after disable here the launch here reads %q", wf.Switches())
			}
		})
	}
}

// TestLoopholesEnableExpandsATildeTheShellLeft: bash expands `~` after `--workspace ` but not in
// `--workspace=~/x`, so the command does.
func TestLoopholesEnableExpandsATildeTheShellLeft(t *testing.T) {
	home, _, _, _ := enableHome(t)
	other := filepath.Join(home, "code", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "github-broker", "--workspace=~/code/other"})
	if rc != 0 || !strings.Contains(stdout, "github-broker is on for "+other+" ") {
		t.Fatalf("rc = %d, want it on for %s\nstdout:\n%s\nstderr:\n%s", rc, other, stdout, stderr)
	}
	if wf := config.ReadWorkspaceFile(other); wf.Switches() != "github-broker on" {
		t.Errorf("the file for %s says %q", other, wf.Switches())
	}
}

// TestLoopholesDisableClearsASwitchForALoopholeNoLongerInstalled: once the pack that ships a
// loophole is deselected, the per-workspace file's switch for it names nothing, and `yolo check`
// warns about it. `disable` is the command that clears it, so it removes the switch rather than
// refusing an uninstalled name; with no switch to clear, the name is refused as before.
func TestLoopholesDisableClearsASwitchForALoopholeNoLongerInstalled(t *testing.T) {
	_, userCfg, _, ws := enableHome(t)
	if rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "github-broker"}); rc != 0 {
		t.Fatalf("enable rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	path := config.WorkspaceFilePath(ws)
	deselected := "{}\n"
	if err := os.WriteFile(userCfg, []byte(deselected), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.ResetPackModules()

	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "disable", "github-broker"})
	if rc != 0 {
		t.Fatalf("disable of a stale switch rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	for _, want := range []string{"github-broker", "removed", path} {
		if !strings.Contains(stdout, want) {
			t.Errorf("disable's line does not say %q:\n%s", want, stdout)
		}
	}
	if wf := config.ReadWorkspaceFile(ws); wf != nil {
		if _, set := wf.LoopholeSwitch("github-broker"); set {
			t.Errorf("the stale switch is still in %s: %q", wf.Path, wf.Switches())
		}
	}
	if after, _ := os.ReadFile(userCfg); string(after) != deselected {
		t.Errorf("config.jsonc changed: %q", after)
	}

	rc, _, stderr = captureDispatchRC(t, []string{"loopholes", "disable", "github-broker"})
	if rc == 0 || !strings.Contains(stderr, `no loophole named "github-broker"`) {
		t.Errorf("with no switch left, disable rc = %d, want the uninstalled-name refusal:\n%s", rc, stderr)
	}
}

// TestLoopholesEnableGlobalRefusesALoopholeNoPackInstalls: with the github pack not selected,
// `--global` for github-broker is refused as not installed, with the step that installs it,
// rather than handing out a block the launch refuses once the pack is selected.
func TestLoopholesEnableGlobalRefusesALoopholeNoPackInstalls(t *testing.T) {
	_, userCfg, _, _ := enableHome(t)
	if err := os.WriteFile(userCfg, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.ResetPackModules()
	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "github-broker", "--global"})
	if rc == 0 {
		t.Fatalf("rc 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, `no loophole named "github-broker"`) || strings.Contains(stderr, "add this to") {
		t.Errorf("--global for an uninstalled loophole printed a block, or no refusal:\n%s", stderr)
	}
}
