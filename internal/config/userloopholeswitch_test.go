package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// A user-scope `loopholes.<name>.enabled: true` for a pack's loophole switches nothing: the loophole
// runs whenever its pack is selected. The validator warns, located in the file that wrote it (a
// `--user-layer` among them), and for a LOGIN loophole (aws-auth: its pack's env contribution
// names the `profile` setting) it names the step that does keep the service from serving: the
// profile set to "", in both config spellings. A grant-declaring loophole's user-scope switch is
// read as a legacy grant (loopholegrantlegacy.go), so it draws no such line, and a config-defined
// loophole's switch is a real switch.
func TestAUserScopeSwitchOfAPackLoopholeSwitchesNothing(t *testing.T) {
	resolver := fakeResolver{
		"aws-auth": {Name: "aws-auth", HasHostDaemon: true},
		"journal":  {Name: "journal", HasHostDaemon: true, Grants: []loopholedecl.Grant{{Kind: "read"}}},
	}
	_, userCfg, ws := workspaceFileHome(t, "app")
	write(t, userCfg, `{"packs": ["aws-auth", "journal"]}`)
	layer := filepath.Join(t.TempDir(), "bedrock-key.jsonc")
	write(t, layer, `{"loopholes": {"aws-auth": {"enabled": true}, "journal": {"enabled": true}, `+
		`"mine": {"command": ["/bin/true"], "enabled": false}}}`)
	t.Setenv(UserLayerEnv, layer)

	cfg, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := ValidateConfig(cfg, ws, resolver)
	all := strings.Join(warns, "\n")
	for _, want := range []string{"config.loopholes.aws-auth.enabled", layer, "switches nothing",
		"runs whenever its pack is selected", `yolo config set allow.aws-auth.profile "" --user`} {
		if !strings.Contains(all, want) {
			t.Errorf("the warning does not say %q:\n%s", want, all)
		}
	}
	for _, unwanted := range []string{"config.loopholes.journal.enabled", "config.loopholes.mine.enabled"} {
		if strings.Contains(all, unwanted+" ") || strings.Contains(all, unwanted+" in") {
			t.Errorf("a switch that is not a no-op drew the line (%s):\n%s", unwanted, all)
		}
	}
	if len(errs) != 0 {
		t.Errorf("a no-op switch must warn, not refuse: %v", errs)
	}
}

// TestAUserScopeOffOfAnOffSwitchLoopholeSaysItIsOff: for LC-D3's loopholes, whose running hands the
// jail a login, credentials or the cgroup delegate, a user-scope `enabled: false` is honored
// (loopholes.Discover), so the validator no longer calls it a no-op: it says the loophole is off
// where the file is in force, and names the TOML spelling a conversion writes.
func TestAUserScopeOffOfAnOffSwitchLoopholeSaysItIsOff(t *testing.T) {
	resolver := fakeResolver{
		"aws-auth":            {Name: "aws-auth", HasHostDaemon: true},
		"claude-oauth-broker": {Name: "claude-oauth-broker", HasHostDaemon: true},
	}
	_, userCfg, ws := workspaceFileHome(t, "app")
	write(t, userCfg, `{"packs": ["aws-auth", "claude"], "loopholes": {"aws-auth": {"enabled": false}, `+
		`"claude-oauth-broker": {"enabled": false}}}`)
	cfg, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := ValidateConfig(cfg, ws, resolver)
	all := strings.Join(warns, "\n")
	if strings.Contains(all, "switches nothing") {
		t.Errorf("an honored off switch is called a no-op:\n%s", all)
	}
	for _, want := range []string{
		"config.loopholes.aws-auth.enabled: false keeps the loophole 'aws-auth' off",
		"off = true under [allow.aws-auth]",
		"config.loopholes.claude-oauth-broker.enabled: false keeps the loophole 'claude-oauth-broker' off",
		"off = true under [allow.claude-auth]",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the warning does not say %q:\n%s", want, all)
		}
	}
	if len(errs) != 0 {
		t.Errorf("an off switch must not refuse: %v", errs)
	}
}
