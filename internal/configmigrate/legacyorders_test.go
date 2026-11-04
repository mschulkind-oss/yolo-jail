package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// legacyorders_test.go pins that a conversion holds whichever workspace it runs from and in
// whichever order the files are converted (docs/design/legacy-config-compat.md LC-D14, LC-D16,
// LC-D18): the user config's conversion writes the same ceiling from any workspace, and a
// project's `enabled: false` converts to its withdrawal.

// targetBytes is the converted bytes the plan writes in place of old.
func targetBytes(t *testing.T, p *MigrationPlan, old string) string {
	t.Helper()
	for _, tg := range p.Targets {
		if tg.Old == old {
			return string(tg.Bytes)
		}
	}
	t.Fatalf("the plan converts no %s: %+v", old, p.Targets)
	return ""
}

// A legacy user config admits a TOML project's request for a legacy_enable kind (LC-D14). The
// user's conversion writes that admission as an on_request line whether it runs alone, from
// another workspace, or from the project's own, so the project keeps what it holds in every order.
func TestTheUserConversionWritesTheLegacyAdmissionFromAnyWorkspace(t *testing.T) {
	for _, mode := range []string{"user-only", "from-another-workspace", "from-the-project"} {
		t.Run(mode, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			project, other := realDir(t), realDir(t)
			write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["host-processes"]}`)
			write(t, filepath.Join(project, "yolo-jail.toml"), "[requests.host-processes]\nview = [\"postgres\"]\n")
			req := Request{UserConfigDir: dir}
			switch mode {
			case "from-another-workspace":
				req.Workspace = other
			case "from-the-project":
				req.Workspace = project
			}
			p := plan(t, req)
			if !p.Equal || len(p.NotConverted) != 0 || p.Refused != nil {
				t.Fatalf("equal=%v notconverted=%+v refused=%v", p.Equal, p.NotConverted, p.Refused)
			}
			got := targetBytes(t, p, filepath.Join(dir, "config.jsonc"))
			if !strings.Contains(got, "[allow.host-processes]\non_request = [\"view\"]") {
				t.Fatalf("the user conversion dropped the legacy admission of view:\n%s", got)
			}
		})
	}
}

// A legacy user's values and a legacy workspace's union (LC-D16). The user's conversion carries
// the user's values into the TOML ceiling, and the unconverted workspace's values still join them
// at the launch (pinned in internal/config's TestALegacyWorkspacesValuesUnionWithATOMLUsers); the
// workspace's own conversion carries its values as its request.
func TestAUserConversionKeepsTheUsersValuesForALegacyWorkspaceToUnionWith(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["serial"], "loopholes": {"serial": {"enabled": true, "settings": {"allowed_devices": ["/dev/ttyACM0"]}}}}`)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"serial": {"settings": {"allowed_devices": ["/dev/ttyUSB1"]}}}}`)
	for _, req := range []Request{{UserConfigDir: dir}, {UserConfigDir: dir, Workspace: ws}} {
		p := plan(t, req)
		if !p.Equal || len(p.NotConverted) != 0 || p.Refused != nil {
			t.Fatalf("workspace=%q: equal=%v notconverted=%+v refused=%v diffs=%+v",
				req.Workspace, p.Equal, p.NotConverted, p.Refused, p.Differences)
		}
		user := targetBytes(t, p, filepath.Join(dir, "config.jsonc"))
		if !strings.Contains(user, "/dev/ttyACM0") || !strings.Contains(user, `on_request = ["devices"]`) {
			t.Fatalf("workspace=%q: the user's devices were not carried:\n%s", req.Workspace, user)
		}
		if req.Workspace != "" {
			project := targetBytes(t, p, filepath.Join(ws, "yolo-jail.jsonc"))
			if !strings.Contains(project, "[requests.serial]") || !strings.Contains(project, "/dev/ttyUSB1") {
				t.Fatalf("the workspace's devices were not carried as its request:\n%s", project)
			}
		}
	}
}

// A legacy workspace's `enabled: false` under a user's `enabled: true` turned the loophole off in
// that workspace (LC-D18): its conversion writes `requests.<pack> = "off"`, and the standing it
// reads is not_allowed, as before. The user's conversion, alone or from another workspace,
// converts equal too, since the launch reads the unconverted workspace's false as the same
// withdrawal.
func TestAProjectsEnabledFalseConvertsToItsWithdrawalInEveryOrder(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws, other := realDir(t), realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["serial"], "loopholes": {"serial": {"enabled": true, "settings": {"allowed_devices": ["/dev/ttyACM0"]}}}}`)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"serial": {"enabled": false}}}`)

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) != 0 || p.Refused != nil {
		t.Fatalf("equal=%v notconverted=%+v refused=%v diffs=%+v", p.Equal, p.NotConverted, p.Refused, p.Differences)
	}
	if got := targetBytes(t, p, filepath.Join(ws, "yolo-jail.jsonc")); !strings.Contains(got, "[requests]\nserial = \"off\"") {
		t.Fatalf("the workspace's enabled: false did not convert to its withdrawal:\n%s", got)
	}
	if len(p.GrantStandings) == 0 {
		t.Fatalf("the plan compared no grant standings")
	}
	for w, packs := range p.GrantStandings {
		if got := packs["serial"]["devices"]; got != "not_allowed" {
			t.Fatalf("serial devices reads %q in %s after the conversion; want not_allowed", got, w)
		}
	}

	for _, req := range []Request{{UserConfigDir: dir}, {UserConfigDir: dir, Workspace: other}} {
		p := plan(t, req)
		if !p.Equal || p.Refused != nil {
			t.Fatalf("workspace=%q: equal=%v refused=%v diffs=%+v", req.Workspace, p.Equal, p.Refused, p.Differences)
		}
	}
}

// A user-level broker switch names the pack that ships the broker, found through the manifests
// (github-broker is the github pack's, git-broker the git pack's), and with that pack selected it
// converts to the broker's read grant (L5, LC-D2).
func TestAUserBrokerSwitchNamesItsPackAndConvertsToTheReadGrant(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"loopholes": {"github-broker": {"enabled": true}}}`, "add the pack with `yolo pack add github`"},
		{`{"loopholes": {"git-broker": {"enabled": true}}}`, "add the pack with `yolo pack add git`"},
	} {
		home := env(t)
		dir := filepath.Join(home, ".config", "yolo-jail")
		write(t, filepath.Join(dir, "config.jsonc"), tc.body)
		p := plan(t, Request{UserConfigDir: dir})
		if got := p.Transcript(); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: the transcript does not name the shipping pack (%q):\n%s", tc.body, tc.want, got)
		}
	}

	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["github"], "loopholes": {"github-broker": {"enabled": true}}}`)
	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal || len(p.NotConverted) != 0 || p.Refused != nil {
		t.Fatalf("equal=%v notconverted=%+v refused=%v", p.Equal, p.NotConverted, p.Refused)
	}
	if got := targetBytes(t, p, filepath.Join(dir, "config.jsonc")); !strings.Contains(got, "[allow.github]\nalways = [\"read\"]") {
		t.Fatalf("a selected github pack's broker switch did not convert to the read grant:\n%s", got)
	}
}
