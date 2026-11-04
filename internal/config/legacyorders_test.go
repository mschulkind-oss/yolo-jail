package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// legacyorders_test.go pins the launch's reading of a still-legacy workspace beside a user config
// in either format, so a user conversion changes nothing another workspace means
// (docs/design/legacy-config-compat.md LC-D16, LC-D18). The user TOML is the one the user's
// conversion writes from the legacy user config each case also runs under.

const (
	legacySerialUser = `{"packs": ["serial"], "loopholes": {"serial": {"enabled": true, "settings": {"allowed_devices": ["/dev/ttyACM0"]}}}}`
	tomlSerialUser   = "packs = [\"serial\"]\n\n[allow.serial]\non_request = [\"devices\"]\ndevices = [\"/dev/ttyACM0\"]\n"
)

func serialDevices(t *testing.T, ws string, lps []GateLoophole) LoopholeGrantStanding {
	t.Helper()
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	return WithLoopholeGrants(nil, ws, read, lps).LoopholeGrantStandings(ws)["serial"]["devices"]
}

// A legacy workspace's allowed_devices union with the user's, under a legacy user config and under
// the TOML one its conversion writes alike (LC-D16): v0.15.1 unioned them, so the user's
// conversion must not narrow a workspace that has not converted yet.
func TestALegacyWorkspacesValuesUnionWithATOMLUsers(t *testing.T) {
	lps := shippedSerialGateLoophole(t)
	for _, tc := range []struct {
		name string
		user func(t *testing.T)
	}{
		{"legacy user", func(t *testing.T) { writeLegacyUserConfig(t, legacySerialUser) }},
		{"TOML user", func(t *testing.T) { writeGrantUserConfig(t, tomlSerialUser) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := approvalWorkspace(t)
			tc.user(t)
			writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"serial": {"settings": {"allowed_devices": ["/dev/ttyUSB1"]}}}}`)
			grantGate(t, ws, lps)
			got := serialDevices(t, ws, lps)
			values := slices.Clone(got.Values)
			slices.Sort(values)
			if got.Standing != LoopholeGrantGranted || !slices.Equal(values, []string{"/dev/ttyACM0", "/dev/ttyUSB1"}) {
				t.Fatalf("devices reads %+v; want granted with both the user's and the workspace's", got)
			}
		})
	}
}

// A legacy workspace's `enabled: false` turns the loophole off in that workspace under a user's
// `enabled: true`, and under the TOML user config that conversion writes alike (LC-D18): it reads
// as the project's withdrawal, `requests.<pack> = "off"`. Without the false, the user's grant holds.
func TestALegacyWorkspacesEnabledFalseWithdrawsUnderEitherUser(t *testing.T) {
	lps := shippedSerialGateLoophole(t)
	for _, tc := range []struct {
		name string
		user func(t *testing.T)
	}{
		{"legacy user", func(t *testing.T) { writeLegacyUserConfig(t, legacySerialUser) }},
		{"TOML user", func(t *testing.T) { writeGrantUserConfig(t, tomlSerialUser) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := approvalWorkspace(t)
			tc.user(t)
			writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"serial": {"enabled": false}}}`)
			grantGate(t, ws, lps)
			if got := serialDevices(t, ws, lps); got.Standing != LoopholeGrantNotAllowed || !got.Withheld {
				t.Fatalf("under the workspace's enabled: false, devices reads %+v; want not_allowed, withheld", got)
			}

			ws = approvalWorkspace(t)
			tc.user(t)
			writeLegacyWorkspaceConfig(t, ws, `{}`)
			grantGate(t, ws, lps)
			if got := serialDevices(t, ws, lps); got.Standing == LoopholeGrantNotAllowed {
				t.Fatalf("control: with no enabled: false, devices reads %+v", got)
			}
		})
	}
}

// writeSwitchFile writes the workspace's per-workspace switch file, as `yolo loopholes disable`
// wrote it before the switch was retired.
func writeSwitchFile(t *testing.T, ws, name string, on bool) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	v := "false"
	if on {
		v = "true"
	}
	p := WorkspaceFilePath(resolved)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, p, `{"workspace": "`+resolved+`", "loopholes": {"`+name+`": {"enabled": `+v+`}}}`)
}

// The per-workspace switch file is the user's, in the user's config directory, so its false
// withholds the pack in that workspace whatever the project's own files request: a TOML project's
// `[requests.<pack>]` does not reopen it, under a legacy user config or a TOML one (LC-D18).
func TestASwitchFileFalseHoldsOverATOMLProjectsRequest(t *testing.T) {
	lps := shippedSerialGateLoophole(t)
	for _, tc := range []struct {
		name string
		user func(t *testing.T)
	}{
		{"legacy user", func(t *testing.T) { writeLegacyUserConfig(t, legacySerialUser) }},
		{"TOML user", func(t *testing.T) { writeGrantUserConfig(t, tomlSerialUser) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := approvalWorkspace(t)
			tc.user(t)
			write(t, filepath.Join(ws, paths.WorkspaceConfigTOMLName), "[requests.serial]\ndevices = [\"/dev/ttyUSB1\"]\n")
			writeSwitchFile(t, ws, "serial", false)
			grantGate(t, ws, lps)
			if got := serialDevices(t, ws, lps); got.Standing != LoopholeGrantNotAllowed || !got.Withheld {
				t.Fatalf("under the switch file's false, devices reads %+v; want not_allowed, withheld", got)
			}

			ws = approvalWorkspace(t)
			tc.user(t)
			write(t, filepath.Join(ws, paths.WorkspaceConfigTOMLName), "[requests.serial]\ndevices = [\"/dev/ttyUSB1\"]\n")
			grantGate(t, ws, lps)
			if got := serialDevices(t, ws, lps); got.Standing != LoopholeGrantGranted {
				t.Fatalf("control: with no switch file, the project's request reads %+v; want granted after the y", got)
			}
		})
	}
}

// Every way a workspace's files and a user's always line meet, on the shipped audio pack, before
// any gate's y (the review's withdrawal table): only the project's withdrawal, in one of its three
// spellings (a switch file's false, a legacy workspace's enabled: false, requests.audio = "off"),
// withholds the user's grant, and the switch file outranks both the workspace's spellings and a
// TOML project's request (LC-D18).
func TestEveryWithdrawalOfAUsersAudioGrant(t *testing.T) {
	lps := shippedGateLoophole(t, "audio")
	const (
		alwaysUser = "packs = [\"audio\"]\n\n[allow.audio]\nalways = [\"use\"]\non_request = [\"use\"]\n"
		legacyUser = `{"packs": ["audio"], "loopholes": {"audio": {"enabled": true}}}`
		legacyOff  = `{"packs": ["audio"], "loopholes": {"audio": {"enabled": false}}}`
		tomlReq    = "[requests.audio]\nuse = true\n"
		wsOff      = `{"loopholes": {"audio": {"enabled": false}}}`
	)
	off, on := false, true
	for _, tc := range []struct {
		name, user       string
		legacy           bool
		sw               *bool
		legacyWS, tomlWS string
		want             string
	}{
		{"always", alwaysUser, false, nil, "", "", LoopholeGrantPending},
		{"always + switch false", alwaysUser, false, &off, "", "", LoopholeGrantNotAllowed},
		{"always + switch false + TOML request", alwaysUser, false, &off, "", tomlReq, LoopholeGrantNotAllowed},
		{"always + switch false + another pack's request", alwaysUser, false, &off, "", "[requests.serial]\ndevices = [\"/dev/x\"]\n", LoopholeGrantNotAllowed},
		{"always + ws off", alwaysUser, false, nil, wsOff, "", LoopholeGrantNotAllowed},
		{"always + ws off with settings", alwaysUser, false, nil, `{"loopholes": {"audio": {"enabled": false, "settings": {"x": 1}}}}`, "", LoopholeGrantNotAllowed},
		{"always + switch true + ws off", alwaysUser, false, &on, wsOff, "", LoopholeGrantGranted},
		{"always + switch false + ws on", alwaysUser, false, &off, `{"loopholes": {"audio": {"enabled": true}}}`, "", LoopholeGrantNotAllowed},
		{"always + requests off", alwaysUser, false, nil, "", "[requests]\naudio = \"off\"\n", LoopholeGrantNotAllowed},
		{"always + requests use = false", alwaysUser, false, nil, "", "[requests.audio]\nuse = false\n", LoopholeGrantPending},
		{"always + ws enabled null", alwaysUser, false, nil, `{"loopholes": {"audio": {"enabled": null}}}`, "", LoopholeGrantPending},
		{"legacy user + switch false", legacyUser, true, &off, "", "", LoopholeGrantNotAllowed},
		{"legacy user + switch false + TOML request", legacyUser, true, &off, "", tomlReq, LoopholeGrantNotAllowed},
		{"legacy user + ws off", legacyUser, true, nil, wsOff, "", LoopholeGrantNotAllowed},
		{"legacy user + ws enabled null", legacyUser, true, nil, `{"loopholes": {"audio": {"enabled": null}}}`, "", LoopholeGrantPending},
		{"legacy user off + ws on", legacyOff, true, nil, `{"loopholes": {"audio": {"enabled": true}}}`, "", LoopholeGrantPending},
		{"legacy user off + switch true", legacyOff, true, &on, "", "", LoopholeGrantGranted},
		{"legacy user off + TOML request", legacyOff, true, nil, "", tomlReq, LoopholeGrantPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := approvalWorkspace(t)
			if tc.legacy {
				writeLegacyUserConfig(t, tc.user)
			} else {
				writeGrantUserConfig(t, tc.user)
			}
			if tc.sw != nil {
				writeSwitchFile(t, ws, "audio", *tc.sw)
			}
			if tc.legacyWS != "" {
				writeLegacyWorkspaceConfig(t, ws, tc.legacyWS)
			}
			if tc.tomlWS != "" {
				write(t, filepath.Join(ws, paths.WorkspaceConfigTOMLName), tc.tomlWS)
			}
			read, err := ReadWorkspaceForGate(ws)
			if err != nil {
				t.Fatal(err)
			}
			check := WithLoopholeGrants(nil, ws, read, lps)
			if check == nil || check.Fatal != nil {
				t.Fatalf("grant check: %+v", check)
			}
			if got := check.LoopholeGrantStandings(ws)["audio"]["use"]; got.Standing != tc.want {
				t.Fatalf("audio use reads %+v, want %s", got, tc.want)
			}
		})
	}
}
