package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholesenable_test.go drives `yolo loopholes enable|disable|list|status` through the real
// dispatch, over a temporary HOME selecting the github and journal packs, as a user runs it.
// enable and disable are RETIRED (docs/design/loophole-access-approval.md §4.5, LG-D8): both
// refuse for every loophole naming `yolo request` and `yolo allow`, `disable` first removing a
// switch an older yolo wrote; list and status show each grant kind's standing.

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

// tomlHome is enableHome with the readable config: the user config is config.toml holding
// userBody, and the workspace config yolo-jail.toml holding wsBody ("" for none).
func tomlHome(t *testing.T, userBody, wsBody string) (home, userCfg, ws string) {
	t.Helper()
	home, legacy, _, ws := enableHome(t)
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	userCfg = filepath.Join(filepath.Dir(legacy), "config.toml")
	if err := os.WriteFile(userCfg, []byte(userBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws, config.WorkspaceConfigName)); err != nil {
		t.Fatal(err)
	}
	if wsBody != "" {
		if err := os.WriteFile(filepath.Join(ws, "yolo-jail.toml"), []byte(wsBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, userCfg, ws
}

const tomlPacks = "packs = [\"github\", \"journal\"]\n"

// TestLoopholesEnableRefusesNamingRequestAndAllow: `enable` of a grant-declaring loophole refuses
// with §5's text, exactly, naming the request for this project and the allow for every project
// on the kind requestable by default (journal's `read`), exits 1, and writes nothing. It extends
// TestLoopholesEnableRefusesABroker, which holds the same for a broker.
func TestLoopholesEnableRefusesNamingRequestAndAllow(t *testing.T) {
	home, userCfg, _ := tomlHome(t, tomlPacks, "")
	t.Setenv("YOLO_NO_BANNER", "1") // the version line is not the refusal
	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "journal"})
	if rc != 1 {
		t.Fatalf("rc = %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	want := "yolo loopholes enable is retired: a loophole runs whenever its pack is selected, and a project gets access by a\n" +
		"  grant, not a switch.\n" +
		"  For this project:    yolo request journal read     (in the workspace), then `yolo config apply` here and y\n" +
		"  For every project:   yolo allow journal read\n"
	if stderr != want {
		t.Errorf("the refusal is\n%s\nwant exactly\n%s", stderr, want)
	}
	if stdout != "" {
		t.Errorf("a refusal wrote stdout: %q", stdout)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".config", "yolo-jail", "workspaces")); len(entries) != 0 {
		t.Errorf("a refusal wrote %v", entries)
	}
	if after, _ := os.ReadFile(userCfg); string(after) != tomlPacks {
		t.Errorf("config.toml changed: %q", after)
	}

	// The commands it names are ones the verbs take: `yolo request journal read` writes the request.
	rc, stdout, stderr = captureDispatchRC(t, []string{"request", "journal", "read", "--dry-run"})
	if rc != 0 {
		t.Errorf("the named request does not run: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
}

// A kind outside the user's ceiling gets the allow it needs first, and a valued kind's request a
// value placeholder, as `yolo request` spells it.
func TestLoopholesEnableNamesTheCeilingStepWhenTheKindIsNotRequestable(t *testing.T) {
	tomlHome(t, tomlPacks+"\n[allow.journal]\non_request = [\"read_all\"]\n", "")
	_, _, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "journal"})
	for _, want := range []string{"yolo request journal read ", "first:  yolo allow journal on_request read\n",
		"For every project:   yolo allow journal read\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr)
		}
	}
}

// TestLoopholesEnableRefusesABroker: a brokered loophole has no switch (docs/design/boundary-broker.md
// BB-D104, BB-D109), so `enable` refuses for it on the host, with `--global`, and in a jail, and
// `disable` with no old switch, each with the retired header, naming the conversion a legacy
// JSONC user config needs, then `yolo allow github read` and `yolo config apply`, and none
// writes a file.
func TestLoopholesEnableRefusesABroker(t *testing.T) {
	home, userCfg, userBody, ws := enableHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail", "workspaces")
	for _, c := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"on the host", []string{"loopholes", "enable", "github-broker"}, nil},
		{"--global", []string{"loopholes", "enable", "github-broker", "--global"}, nil},
		{"disable with no old switch", []string{"loopholes", "disable", "github-broker"}, nil},
		{"in a jail", []string{"loopholes", "enable", "github-broker"},
			map[string]string{"YOLO_VERSION": "9.9.9-test", "YOLO_HOST_DIR": "/home/someone/code/app", "YOLO_WORKSPACE": ws}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			rc, stdout, stderr := captureDispatchRC(t, c.args)
			if rc != 1 {
				t.Fatalf("rc %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
			}
			for _, want := range []string{" is retired: a loophole runs whenever its pack is selected",
				"`yolo config migrate`", "`yolo allow github read`", "yolo config apply"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, stderr)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("a refusal wrote %v", entries)
			}
			if after, _ := os.ReadFile(userCfg); string(after) != userBody {
				t.Errorf("config.jsonc changed: %q", after)
			}
		})
	}
}

// A TOML user's refusal names the readable grant, `yolo allow github read` (the github-broker
// manifest's read_capability), never a `brokered.github.grants` entry config.toml cannot hold.
func TestLoopholesEnableRefusesABrokerNamingYoloAllowToATOMLUser(t *testing.T) {
	tomlHome(t, tomlPacks, "")
	for _, args := range [][]string{{"loopholes", "enable", "github-broker"}, {"loopholes", "disable", "github-broker"}} {
		rc, stdout, stderr := captureDispatchRC(t, args)
		if rc != 1 {
			t.Fatalf("%v rc %d, want 1\nstdout:\n%s\nstderr:\n%s", args, rc, stdout, stderr)
		}
		if !strings.Contains(stderr, "is retired") || !strings.Contains(stderr, "`yolo allow github read`") ||
			strings.Contains(stderr, "brokered.github.grants") || strings.Contains(stderr, "yolo config migrate") {
			t.Errorf("%v does not name `yolo allow github read` to a TOML user:\n%s", args, stderr)
		}
	}
}

// TestLoopholesEnableRefusesWhatItCannotDo: an unknown name is refused naming `yolo loopholes
// list`, `--global` and a jail get the retired refusal, and none of them writes anything.
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
		{"in a jail", []string{"loopholes", "enable", "journal"},
			map[string]string{"YOLO_VERSION": "9.9.9-test", "YOLO_HOST_DIR": "/home/someone/code/app",
				"YOLO_WORKSPACE": ws}, "",
			[]string{"is retired", "yolo request journal read", "yolo allow journal read"}},
		{"--global", []string{"loopholes", "enable", "journal", "--global"}, nil, "",
			[]string{"is retired", "yolo request journal read", "yolo allow journal read"}},
		{"--global for an unknown name", []string{"loopholes", "enable", "no-such", "--global"}, nil, "",
			[]string{`no loophole named "no-such"`, "`yolo loopholes list`"}},
		{"the home", []string{"loopholes", "disable", "journal"}, nil, home,
			[]string{"yolo loopholes disable is retired", "yolo pack remove journal"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.cwd != "" {
				t.Chdir(c.cwd)
			}
			rc, stdout, stderr := captureDispatchRC(t, c.args)
			if rc != 1 {
				t.Fatalf("rc %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, stderr)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("a refusal wrote %v", entries)
			}
			if after, _ := os.ReadFile(userCfg); string(after) != userBody {
				t.Errorf("config.jsonc changed: %q", after)
			}
		})
	}
}

// TestLoopholesDisableRemovesAnOldSwitchThenRefuses: `disable` takes out a switch an older yolo
// wrote into the working directory's per-workspace file, exit 0, naming the file; run again,
// with nothing left to remove, it refuses as `enable` does, exit 1. The switch of another
// workspace, and of a parent folder, is left alone.
func TestLoopholesDisableRemovesAnOldSwitchThenRefuses(t *testing.T) {
	home, _, _, ws := enableHome(t)
	sub := filepath.Join(ws, "sub")
	other := filepath.Join(home, "code", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{ws, sub, other} {
		if _, err := config.SetWorkspaceLoophole(w, "journal", true); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(sub)
	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "disable", "journal"})
	if rc != 0 {
		t.Fatalf("disable rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	for _, want := range []string{"Removed the switch an older yolo wrote for journal", config.WorkspaceFilePath(sub)} {
		if !strings.Contains(stdout, want) {
			t.Errorf("disable's line does not say %q:\n%s", want, stdout)
		}
	}
	if wf := config.ReadWorkspaceFile(sub); wf != nil {
		if _, set := wf.LoopholeSwitch("journal"); set {
			t.Errorf("the switch is still in %s: %q", wf.Path, wf.Switches())
		}
	}
	for _, w := range []string{ws, other} {
		if wf := config.ReadWorkspaceFile(w); wf.Switches() != "journal on" {
			t.Errorf("disable in %s changed %s's switch: %q", sub, w, wf.Switches())
		}
	}

	rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "disable", "journal"})
	if rc != 1 || !strings.Contains(stderr, "yolo loopholes disable is retired") ||
		!strings.Contains(stderr, "To stop it running:  yolo pack remove journal") {
		t.Errorf("with no switch left, disable rc = %d, want the refusal:\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}

	// `--workspace`, and a `~` the shell left in `--workspace=~/x`, name the workspace instead.
	rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "disable", "journal", "--workspace=~/code/other"})
	if rc != 0 || !strings.Contains(stdout, config.WorkspaceFilePath(other)) {
		t.Fatalf("disable --workspace rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if wf := config.ReadWorkspaceFile(ws); wf.Switches() != "journal on" {
		t.Errorf("--workspace changed this workspace's file too: %q", wf.Switches())
	}

	// `enable` never takes a switch out, nor writes one.
	rc, _, _ = captureDispatchRC(t, []string{"loopholes", "enable", "journal", "--workspace", ws})
	if wf := config.ReadWorkspaceFile(ws); rc != 1 || wf.Switches() != "journal on" {
		t.Errorf("enable rc = %d, and the switch reads %q", rc, wf.Switches())
	}
}

// TestLoopholesDisableClearsASwitchForALoopholeNoLongerInstalled: once the pack that ships a
// loophole is deselected, the per-workspace file's switch for it names nothing, and `yolo check`
// warns about it. `disable` is the command that clears it, so it removes the switch rather than
// refusing an uninstalled name; with no switch to clear, the name is refused.
func TestLoopholesDisableClearsASwitchForALoopholeNoLongerInstalled(t *testing.T) {
	_, userCfg, _, ws := enableHome(t)
	path, err := config.SetWorkspaceLoophole(ws, "journal", true)
	if err != nil {
		t.Fatal(err)
	}
	deselected := "{}\n"
	if err := os.WriteFile(userCfg, []byte(deselected), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.ResetPackModules()

	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "disable", "journal"})
	if rc != 0 {
		t.Fatalf("disable of a stale switch rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	for _, want := range []string{"journal", "Removed", path} {
		if !strings.Contains(stdout, want) {
			t.Errorf("disable's line does not say %q:\n%s", want, stdout)
		}
	}
	if wf := config.ReadWorkspaceFile(ws); wf != nil {
		if _, set := wf.LoopholeSwitch("journal"); set {
			t.Errorf("the stale switch is still in %s: %q", wf.Path, wf.Switches())
		}
	}
	rc, _, stderr = captureDispatchRC(t, []string{"loopholes", "disable", "journal"})
	if rc == 0 || !strings.Contains(stderr, `no loophole named "journal"`) {
		t.Errorf("with no switch left, disable rc = %d, want the uninstalled-name refusal:\n%s", rc, stderr)
	}
}

// TestLoopholesDisableRemovesABrokersOldSwitch: a switch a previous yolo wrote for a broker into
// the per-workspace file is ignored now (BB-D109); `disable` is the command the launch's note
// names to take it out, and it does, exit 0.
func TestLoopholesDisableRemovesABrokersOldSwitch(t *testing.T) {
	_, _, _, ws := enableHome(t)
	path, err := config.SetWorkspaceLoophole(ws, "github-broker", true)
	if err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "disable", "github-broker"})
	if rc != 0 {
		t.Fatalf("disable rc = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if !strings.Contains(stdout, "Removed the switch") || !strings.Contains(stdout, path) {
		t.Errorf("disable's line does not name the removal and %s:\n%s", path, stdout)
	}
	if wf := config.ReadWorkspaceFile(ws); wf != nil {
		if _, set := wf.LoopholeSwitch("github-broker"); set {
			t.Errorf("the old switch is still in %s: %q", wf.Path, wf.Switches())
		}
	}
}

// TestLoopholesDisableAdviceIsACommandThatRuns: the `yolo pack remove <pack>` a refused
// `disable` names must not be refused by the verb it names; run through the dispatch registry it
// succeeds and takes the pack out of the user config.
func TestLoopholesDisableAdviceIsACommandThatRuns(t *testing.T) {
	_, userCfg, _, _ := enableHome(t)
	rc, _, stderr := captureDispatchRC(t, []string{"loopholes", "disable", "journal", "--global"})
	if rc != 1 {
		t.Fatalf("disable --global rc = %d, want 1 (nothing changed)\n%s", rc, stderr)
	}
	var cmd string
	for _, l := range strings.Split(stderr, "\n") {
		if i := strings.Index(l, "yolo pack remove "); i >= 0 {
			cmd = strings.TrimSpace(l[i:])
		}
	}
	if cmd != "yolo pack remove journal" {
		t.Fatalf("the advice names %q, want `yolo pack remove journal`:\n%s", cmd, stderr)
	}
	rc, stdout, stderr2 := captureDispatchRC(t, strings.Fields(cmd)[1:])
	if rc != 0 {
		t.Fatalf("the printed command was refused: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr2)
	}
	after, _ := os.ReadFile(userCfg)
	if strings.Contains(string(after), `"journal"`) {
		t.Errorf("the printed command left journal in the user config:\n%s", after)
	}
}

// TestLoopholesListAndStatusShowTheStandingPerKind: `list` and `status` show each grant kind's
// standing for the workspace, through the gate's own resolver: with nothing requested, journal's
// `read` is requestable by default and `read_all` not allowed; with both allowed on request and
// requested, and the approval record holding `read`, `read` is granted and `read_all` pending.
// `list --format json` carries the same as `grants`. An ungated or brokered loophole shows none.
func TestLoopholesListAndStatusShowTheStandingPerKind(t *testing.T) {
	t.Run("nothing requested", func(t *testing.T) {
		tomlHome(t, tomlPacks, "")
		for _, verb := range []string{"list", "status"} {
			rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", verb})
			if rc != 0 {
				t.Fatalf("%s rc = %d\nstdout:\n%s\nstderr:\n%s", verb, rc, stdout, stderr)
			}
			if !strings.Contains(stdout, "grants here: read requestable, read_all not_allowed") {
				t.Errorf("%s does not show journal's standings:\n%s", verb, stdout)
			}
			if n := strings.Count(stdout, "grants here:"); n != 1 {
				t.Errorf("%s shows %d standing lines, want journal's alone:\n%s", verb, n, stdout)
			}
		}
	})
	t.Run("requested and partly approved", func(t *testing.T) {
		_, _, ws := tomlHome(t, tomlPacks+"\n[allow.journal]\non_request = [\"read\", \"read_all\"]\n",
			"[requests.journal]\nread = true\nread_all = true\n")
		record := config.ApprovalLoopholeGrantsPath(ws)
		if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record, []byte(`{"journal": ["read"]}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "list"})
		if rc != 0 || !strings.Contains(stdout, "grants here: read granted, read_all pending") {
			t.Fatalf("list rc = %d, want read granted and read_all pending\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
		}
		rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "list", "--format", "json"})
		if rc != 0 {
			t.Fatalf("list --format json rc = %d\n%s", rc, stderr)
		}
		var entries []loopholes.ListEntry
		if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
			t.Fatalf("list --format json: %v\n%s", err, stdout)
		}
		got := map[string]string{}
		for _, e := range entries {
			if e.Name != "journal" {
				if len(e.Grants) != 0 {
					t.Errorf("%s carries grants %v", e.Name, e.Grants)
				}
				continue
			}
			for _, k := range e.Grants {
				got[k.Kind] = k.Standing
			}
		}
		if got["read"] != "granted" || got["read_all"] != "pending" || len(got) != 2 {
			t.Errorf("journal's json grants = %v, want read granted, read_all pending", got)
		}

		// In a jail the approval record is the host's, so a kind the gate would hold is a
		// candidate, not granted or pending.
		t.Setenv("YOLO_VERSION", "9.9.9-test")
		rc, stdout, stderr = captureDispatchRC(t, []string{"loopholes", "list"})
		if rc != 0 || !strings.Contains(stdout, "grants here: read candidate, read_all candidate") {
			t.Errorf("in a jail, list rc = %d, want both candidates\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
		}
	})
}

// A valued kind's request carries a value placeholder in `yolo request`'s spelling, and its
// every-project line allows it on request, since `always` carries no value (§4.2); a loophole
// that declares no grant names its pack.
func TestLoopholesEnableNamesAValuedKindAndAnUngatedPack(t *testing.T) {
	tomlHome(t, "packs = [\"host-processes\", \"cgroup-delegate\"]\n", "")
	_, _, stderr := captureDispatchRC(t, []string{"loopholes", "enable", "host-processes"})
	for _, want := range []string{"yolo request host-processes view <process names>     (in the workspace)",
		"For every project:   yolo allow host-processes on_request view     (each project then requests its own process names)"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr)
		}
	}
	rc, _, stderr := captureDispatchRC(t, []string{"loopholes", "disable", "cgroup-delegate"})
	// cgroup-delegate's pack is selected by default (PD-13), so removing a `packs` line would not
	// stop it, and there is no per-workspace `off` yet (LG-D9): the refusal says so instead of
	// naming `yolo pack remove`.
	if rc != 1 || !strings.Contains(stderr, "cgroup-delegate declares no grant") ||
		!strings.Contains(stderr, "is selected by default") ||
		!strings.Contains(stderr, "To stop it running:  not possible yet") ||
		strings.Contains(stderr, "yolo pack remove cgroup-delegate") {
		t.Errorf("disable of an ungated loophole rc = %d:\n%s", rc, stderr)
	}
}
