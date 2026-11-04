package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// TestTheOffDisclosuresCommandTurnsTheLoopholeBackOn: for every place an off switch can live
// (docs/design/legacy-config-compat.md LC-D3), the command the launch's disclosure names
// (run.LoopholesOff) runs, removes that switch, and the next launch's config keeps the loophole
// off no longer. A disclosure naming a command that is refused is a stop with no next step.
func TestTheOffDisclosuresCommandTurnsTheLoopholeBackOn(t *testing.T) {
	for _, c := range []struct {
		name         string
		format       configdoc.Format
		user, ws     string
		wsFile       bool
		wantWhere    string
		wantCommands []string
	}{
		{name: "TOML user", format: configdoc.TOML,
			user: "packs = [\"aws-auth\"]\n\n[allow.aws-auth]\noff = true\nprofile = \"p\"\n", ws: "",
			wantWhere: "your user config", wantCommands: []string{"yolo config unset allow.aws-auth.off"}},
		{name: "legacy user", format: configdoc.JSONC,
			user: `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": false, "settings": {"profile": "p"}}}}`, ws: `{}`,
			wantWhere: "your user config", wantCommands: []string{"yolo config unset loopholes.aws-auth.enabled --user"}},
		{name: "workspace config", format: configdoc.JSONC,
			user: `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"settings": {"profile": "p"}}}}`, ws: `{"loopholes": {"aws-auth": {"enabled": false}}}`,
			wantWhere: "the workspace config", wantCommands: []string{"yolo config unset loopholes.aws-auth.enabled --project"}},
		{name: "per-workspace file", format: configdoc.JSONC,
			user: `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"settings": {"profile": "p"}}}}`, ws: `{}`, wsFile: true,
			wantWhere: "per-workspace file", wantCommands: []string{"yolo loopholes disable aws-auth"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws := testconfig.WorkspaceFormat(t, c.format, c.ws)
			user := testconfig.UserFormat(t, c.format, c.user)
			t.Setenv("HOME", user.Root)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_WORKSPACE", "")
			t.Setenv("YOLO_HOST_DIR", "")
			withConfigEditCWD(t, ws.Root)
			if c.wsFile {
				path := config.WorkspaceFilePath(ws.Root)
				if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
					t.Fatal(err)
				}
				body := `{"workspace": "` + ws.Root + `", "loopholes": {"aws-auth": {"enabled": false}}}`
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			off := offOf(t, ws.Root, "aws-auth")
			if off == nil {
				t.Fatal("control: the config does not keep aws-auth off")
			}
			var commands []string
			for _, sw := range off.Switches {
				commands = append(commands, sw.Command)
				if !strings.Contains(sw.Where, c.wantWhere) {
					t.Errorf("the switch is said to be in %q, want %q", sw.Where, c.wantWhere)
				}
			}
			if strings.Join(commands, "\n") != strings.Join(c.wantCommands, "\n") {
				t.Fatalf("the disclosure names %q, want %q", commands, c.wantCommands)
			}
			for _, cmd := range commands {
				args := strings.Fields(cmd)[1:]
				rc, out, errs := captureStd(t, func() int { return registry[args[0]](args) })
				if !strings.Contains(out, "Written") && rc != 0 {
					t.Fatalf("`%s` was refused: rc=%d\n%s\n%s", cmd, rc, out, errs)
				}
			}
			if off := offOf(t, ws.Root, "aws-auth"); off != nil {
				t.Errorf("after %q the config still keeps aws-auth off: %+v", commands, off.Switches)
			}
		})
	}
}

func offOf(t *testing.T, ws, name string) *run.LoopholeOff {
	t.Helper()
	cfg, err := config.LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range run.LoopholesOff(cfg, ws) {
		if off.Loophole == name {
			return &off
		}
	}
	return nil
}

func dirOf(path string) string { return path[:strings.LastIndex(path, "/")] }

// TestTheNonBooleanOffRefusalNamesNoRefusedCommand: a user config whose `off` is not a boolean is
// refused where it is read. `yolo config unset` reads the file first and refuses it for the same
// value, so the refusal names the hand fix, never that command; and a `yolo config` edit names the
// same refusal.
func TestTheNonBooleanOffRefusalNamesNoRefusedCommand(t *testing.T) {
	ws := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.TOML, "packs = [\"aws-auth\"]\n\n[allow.aws-auth]\noff = \"yes\"\nprofile = \"p\"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	withConfigEditCWD(t, ws.Root)
	_, err := config.LoadConfig(ws.Root, true, nil)
	const fix = "write `off = true` to keep the pack's loophole off, or delete the line to let it run"
	if err == nil || !strings.Contains(err.Error(), fix) || strings.Contains(err.Error(), "yolo config unset") {
		t.Fatalf("err = %v, want the hand fix %q and no `yolo config unset`", err, fix)
	}
	args := []string{"config", "unset", "allow.aws-auth.off"}
	rc, out, errs := captureStd(t, func() int { return registry[args[0]](args) })
	if rc == 0 || !strings.Contains(out+errs, fix) {
		t.Errorf("control: `yolo config unset` on the refused file: rc=%d, want a refusal naming the hand fix\n%s\n%s", rc, out, errs)
	}
}

// TestTheWrongTableOffRefusalsFixMovesIt: an `off` under a pack with no off-switch loophole is
// refused at launch naming the table that has one; done as it says, the launch refuses nothing
// and the loophole is off from the right table.
func TestTheWrongTableOffRefusalsFixMovesIt(t *testing.T) {
	ws := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.TOML, "packs = [\"claude\"]\n\n[allow.claude]\noff = true\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	const step = "move the line under [allow.claude-auth], and delete [allow.claude] if it is then empty"
	if errs := strings.Join(config.AllowPackSwitchErrors(), "\n"); !strings.Contains(errs, step) {
		t.Fatalf("the refusal lacks %q:\n%s", step, errs)
	}
	if err := os.WriteFile(user.Path, []byte("packs = [\"claude\"]\n\n[allow.claude-auth]\noff = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if errs := config.AllowPackSwitchErrors(); len(errs) != 0 {
		t.Errorf("after the named step the launch still refuses: %q", errs)
	}
	if offOf(t, ws.Root, "claude-oauth-broker") == nil {
		t.Error("after the named step claude-oauth-broker is not off")
	}
}
