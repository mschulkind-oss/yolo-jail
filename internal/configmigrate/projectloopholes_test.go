package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// projectloopholes_test.go pins the lowerings docs/design/legacy-config-compat.md LC-D13 to
// LC-D17 rule: each converts a legacy loophole line to the TOML spelling a launch reads the same
// way, and the plan passing is the proof's verdict that it does (the proof reads each through the
// launch's own readers: loopholes.LaunchBlock, loopholes.WithProjectRequestSettings and the
// grant gate's projection).

// planTargets plans req and returns each planned file's bytes by its old path.
func planTargets(t *testing.T, req Request) (*MigrationPlan, map[string]string) {
	t.Helper()
	p := plan(t, req)
	if !p.Equal || len(p.NotConverted) != 0 || p.Refused != nil {
		t.Fatalf("the plan did not pass: equal=%v not converted=%+v refused=%v differences=%+v\n%s",
			p.Equal, p.NotConverted, p.Refused, p.Differences, p.Transcript())
	}
	out := map[string]string{}
	for _, tg := range p.Targets {
		out[tg.Old] = string(tg.Bytes)
	}
	return p, out
}

func requireHolds(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, got)
		}
	}
}

// LC-D15: a workspace `enabled: false` on an off-switch loophole kept it off for that workspace in
// v0.15.1, so it converts to the project's `[requests.<pack>] off = true`, never to the tombstone
// `requests.<pack> = "off"` (which withdraws the project's requests and turns nothing off).
func TestAProjectEnabledFalseOnAnOffSwitchConvertsToRequestsOff(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.toml"), "packs = [\"aws-auth\"]\n\n[allow.aws-auth]\nprofile = \"p\"\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"aws-auth": {"enabled": false}}}`)
	_, files := planTargets(t, Request{UserConfigDir: dir, Workspace: ws})
	project := files[filepath.Join(ws, "yolo-jail.jsonc")]
	requireHolds(t, "the project", project, "[requests.aws-auth]\noff = true")
	if strings.Contains(project, `"off"`) {
		t.Errorf("the project names the tombstone, not the off switch:\n%s", project)
	}
}

// LG-D15 and LC-D17: a request-scope setting a legacy workspace wrote (serial's `default_baud`,
// host-processes's `fields`) is that project's request, so it converts under `[requests.<pack>]`,
// where the launch reads it over the user's value.
func TestMigrateWritesAProjectsRequestScopeSettingsUnderRequests(t *testing.T) {
	for _, c := range []struct{ pack, settings, want string }{
		{"serial", `{"default_baud": 9600}`, "[requests.serial]\ndefault_baud = 9600"},
		{"host-processes", `{"fields": ["pid", "comm"]}`, "[requests.host-processes]\nfields = [\"pid\", \"comm\"]"},
	} {
		t.Run(c.pack, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			ws := realDir(t)
			write(t, filepath.Join(dir, "config.toml"), "packs = [\""+c.pack+"\"]\n")
			write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"`+c.pack+`": {"settings": `+c.settings+`}}}`)
			_, files := planTargets(t, Request{UserConfigDir: dir, Workspace: ws})
			requireHolds(t, "the project", files[filepath.Join(ws, "yolo-jail.jsonc")], c.want)
		})
	}
}

// LC-D16: a user switch on a valued kind with its values (serial's `allowed_devices`) asked every
// project for those values, so it converts to the ceiling line beside the user-sourced request
// `[allow.serial] devices = [...]`, which each project's gate shows as its own request.
func TestAUserSwitchWithValuesConvertsToAUserSourcedRequest(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["serial"], "loopholes": {"serial": {"enabled": true, "settings": {"allowed_devices": ["/dev/ttyACM0"]}}}}`)
	_, files := planTargets(t, Request{UserConfigDir: dir, Workspace: ws})
	user := files[filepath.Join(dir, "config.jsonc")]
	requireHolds(t, "the user config", user, "[allow.serial]", "on_request = [\"devices\"]", "devices = [\"/dev/ttyACM0\"]")
	if strings.Contains(user, "allowed_devices") {
		t.Errorf("the user config still names the legacy list:\n%s", user)
	}
}

// LC-D14: under a legacy user config a TOML project's request for a `legacy_enable` kind was
// admitted as the old switch admitted it, so converting the user config writes the on_request
// line that request needs; without it the converted launch would skip the project's request.
func TestConvertingALegacyUserWritesTheCeilingATOMLProjectNeeds(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["host-processes"]}`)
	write(t, filepath.Join(ws, "yolo-jail.toml"), "[requests.host-processes]\nview = [\"postgres\"]\n")
	_, files := planTargets(t, Request{UserConfigDir: dir, Workspace: ws})
	requireHolds(t, "the user config", files[filepath.Join(dir, "config.jsonc")], "[allow.host-processes]", "on_request = [\"view\"]")
}

// LC-D16 (with LC-D13): on the host, a workspace converted alone over a user config already in
// TOML writes the on_request line its converted requests need into that user file, in place, as
// a migration covering both does; the user file is read, never converted.
func TestAWorkspaceOnlyMigrationWritesTheTOMLUsersCeilingLine(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	userPath := filepath.Join(dir, "config.toml")
	write(t, userPath, "packs = [\"serial\"]\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"serial": {"enabled": true}}}`)
	p, files := planTargets(t, Request{Workspace: ws})
	requireHolds(t, "the user config", files[userPath], "[allow.serial]\non_request = [\"devices\"]")
	for _, tg := range p.Targets {
		if tg.Old == userPath && tg.New != userPath {
			t.Errorf("the TOML user config is converted rather than edited in place: %+v", tg)
		}
	}
	requireHolds(t, "the project", files[filepath.Join(ws, "yolo-jail.jsonc")], "[requests.serial]")
}

// A pack every launch selects with no `packs` line (cgroup-delegate) is selected for the
// lowering too, so its `enabled: false` converts to its off switch rather than being dropped as
// a switch on an unselected pack, which would turn the delegate back on.
func TestAnOffSwitchOnASelectedByDefaultPackConvertsToItsOff(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"loopholes": {"cgroup-delegate": {"enabled": false}}}`)
	_, files := planTargets(t, Request{UserConfigDir: dir, Workspace: ws})
	requireHolds(t, "the user config", files[filepath.Join(dir, "config.jsonc")], "[allow.cgroup-delegate]\noff = true")
}
