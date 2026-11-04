package config

import (
	"slices"
	"testing"
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
