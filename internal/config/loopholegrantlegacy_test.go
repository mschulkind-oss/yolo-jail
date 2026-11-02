package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The legacy projection (the plan's PD-9; docs/design/loophole-access-approval.md §6): during the
// JSONC window a legacy `loopholes.<name>` block, and the per-workspace switch file, read as the
// ceiling, request and record lines the migration would write. Each test runs the real user and
// workspace reads and the real gate (grantGate, loopholegrantgate_test.go).

// legacyGrantFixture is a journal-shaped loophole and a host-processes-shaped one, each carrying
// the manifest's legacy keys.
func legacyGrantFixture() []GateLoophole {
	return []GateLoophole{
		{Pack: "journal", Loophole: "journal", Access: "read the host's journal", Grants: []loopholedecl.Grant{
			{Kind: "read", Text: "read your user journal", RequestableByDefault: true, LegacyEnable: true},
			{Kind: "read_all", Text: "read the whole host journal", LegacySetting: "full"},
		}},
		{Pack: "procs", Loophole: "procs", Access: "see host processes", Grants: []loopholedecl.Grant{
			{Kind: "view", Text: "see", Valued: true, ValueNoun: "process names", LegacyEnable: true, LegacyValues: "visible"},
		}},
	}
}

// writeLegacyUserConfig writes the user's legacy config.jsonc under HOME.
func writeLegacyUserConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigLegacyJSONCName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyWorkspaceConfig(t *testing.T, ws, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// legacyStandings is the standings a fresh read of the workspace holds against the record now.
func legacyStandings(t *testing.T, ws string) map[string]map[string]LoopholeGrantStanding {
	t.Helper()
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	check := WithLoopholeGrants(nil, ws, read, legacyGrantFixture())
	if check == nil {
		t.Fatal("no grant check for a selected grant-declaring loophole")
	}
	if check.Fatal != nil {
		t.Fatalf("grant check: %v", check.Fatal)
	}
	return check.LoopholeGrantStandings(ws)
}

func requireGrantLine(t *testing.T, report *ChangeReport, want string) {
	t.Helper()
	if report == nil || !containsLine(report.GrantBlock, want) {
		var got []string
		if report != nil {
			got = report.GrantBlock
		}
		t.Fatalf("grant block lacks %q:\n%s", want, strings.Join(got, "\n"))
	}
}

// asksAboutGrants: the gate showed a grant line (it may still ask about a new workspace config).
func asksAboutGrants(r *ChangeReport) bool {
	return r != nil && (r.GrantsChanged || len(r.GrantBlock) > 0)
}

// User `enabled: true` → `[allow.journal] always = ["read"]`: the always line shows at the gate.
func TestLegacyUserEnabledReadsAsAnAlwaysCeiling(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"journal": {"enabled": true}}}`)
	report := grantGate(t, ws, legacyGrantFixture())
	requireGrantLine(t, report, "  + journal: read your user journal (from your user config, for every project)")
	for _, l := range report.GrantBlock {
		if strings.Contains(l, "whole host journal") {
			t.Fatalf("read_all shown without settings.full: %q", l)
		}
	}
	if got := legacyStandings(t, ws)["journal"]["read"]; got.Standing != LoopholeGrantGranted {
		t.Fatalf("after the y, read reads %+v", got)
	}
}

// User `settings.full: true` → `read_all` joins the same always list.
func TestLegacyUserFullJoinsReadAllToTheCeiling(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"journal": {"enabled": true, "settings": {"full": true}}}}`)
	report := grantGate(t, ws, legacyGrantFixture())
	requireGrantLine(t, report, "  + journal: read your user journal (from your user config, for every project)")
	requireGrantLine(t, report, "  + journal: read the whole host journal (from your user config, for every project)")
}

// User `enabled: true` on a valued kind → `on_request`: no value to carry, so nothing is granted
// until a project asks.
func TestLegacyUserEnabledOnAValuedKindReadsAsOnRequest(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"procs": {"enabled": true}}}`)
	if got := grantGate(t, ws, legacyGrantFixture()); got != nil {
		t.Fatalf("an on_request ceiling with no request asked the gate: %+v", got)
	}
	if got := legacyStandings(t, ws)["procs"]["view"]; got.Standing != LoopholeGrantRequestable {
		t.Fatalf("view reads %+v, want requestable", got)
	}
}

// Workspace `enabled: true` → `[requests.journal] read = true`: a request, named by its file.
func TestLegacyWorkspaceEnabledReadsAsARequest(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"journal": {"enabled": true}}}`)
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	check := WithLoopholeGrants(nil, ws, read, legacyGrantFixture())
	p := &reportPrompter{accept: false}
	if ok, err := CheckConfigAndScopeChanges(ws, read.Config, check, true, false, p); ok || err != nil {
		t.Fatalf("declined gate ok=%v err=%v", ok, err)
	}
	found := false
	for _, l := range p.got.GrantBlock {
		found = found || strings.HasPrefix(l, "  + journal: read your user journal (requested by ") && strings.Contains(l, WorkspaceConfigName)
	}
	if !found {
		t.Fatalf("the workspace switch did not read as a request naming %s:\n%s", WorkspaceConfigName, strings.Join(p.got.GrantBlock, "\n"))
	}
	if got := check.LoopholeGrantStandings(ws)["journal"]["read"]; got.Standing != LoopholeGrantPending {
		t.Fatalf("a declined request reads %+v, want pending", got)
	}
}

// Switch file `enabled: true` → a grant the record holds: the human ran `yolo loopholes enable` on
// the host, which was the approval, so the gate asks nothing and the grant stands. For a valued
// kind the value is the one the old settings gave; the ceiling gains on_request for it.
func TestLegacySwitchFileEnabledReadsAsARecordedGrant(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"procs": {"settings": {"visible": ["postgres"]}}}}`)
	for _, name := range []string{"journal", "procs"} {
		if _, err := SetWorkspaceLoophole(ws, name, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := grantGate(t, ws, legacyGrantFixture()); asksAboutGrants(got) {
		t.Fatalf("a switch-file grant asked the gate: %+v", got)
	}
	standings := legacyStandings(t, ws)
	if got := standings["journal"]["read"]; got.Standing != LoopholeGrantGranted {
		t.Fatalf("journal read reads %+v, want granted", got)
	}
	if got := standings["procs"]["view"]; got.Standing != LoopholeGrantGranted || strings.Join(got.Values, ",") != "postgres" {
		t.Fatalf("procs view reads %+v, want granted postgres", got)
	}
}

// `visible` → request values, one gate line per value.
func TestLegacyVisibleListReadsAsRequestValues(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"procs": {"enabled": true}}}`)
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"procs": {"settings": {"visible": ["postgres", "redis-server"]}}}}`)
	report := grantGate(t, ws, legacyGrantFixture())
	for _, v := range []string{"postgres", "redis-server"} {
		found := false
		for _, l := range report.GrantBlock {
			found = found || strings.HasPrefix(l, "  + procs: see "+v+" (requested by ")
		}
		if !found {
			t.Fatalf("no request line for %s:\n%s", v, strings.Join(report.GrantBlock, "\n"))
		}
	}
	if got := legacyStandings(t, ws)["procs"]["view"]; got.Standing != LoopholeGrantGranted || len(got.Values) != 2 {
		t.Fatalf("view reads %+v", got)
	}
}

// A legacy switch turned off in this workspace projects nothing here: no access gained.
func TestLegacyWorkspaceOffProjectsNothing(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"journal": {"enabled": true, "settings": {"full": true}}}}`)
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"journal": {"enabled": false}}}`)
	if got := grantGate(t, ws, legacyGrantFixture()); asksAboutGrants(got) {
		t.Fatalf("a workspace that switched the loophole off was asked about its grants: %+v", got)
	}
}

// The projection is the function the migration rows call (T12): its lines, read directly.
func TestLegacyProjectionLines(t *testing.T) {
	user := decode(t, `{"loopholes": {"journal": {"enabled": true, "settings": {"full": true}}, "procs": {"enabled": true, "settings": {"visible": ["sshd"]}}}}`)
	ws := decode(t, `{"loopholes": {"procs": {"settings": {"visible": ["postgres"]}}}}`)
	var decls []LoopholeGrantDeclaration
	for _, lp := range legacyGrantFixture() {
		decls = append(decls, LoopholeGrantDeclaration{Pack: lp.Pack, Loophole: lp.Loophole, Grants: lp.Grants})
	}
	got := ProjectLegacyLoopholeGrants(LegacyLoopholeGrantInput{User: user, Workspace: ws,
		Switches: map[string]bool{"procs": true}, Declarations: decls})
	if len(got) != 2 {
		t.Fatalf("projection %+v", got)
	}
	j, p := got[0], got[1]
	if strings.Join(j.Always, ",") != "read,read_all" || len(j.OnRequest) != 0 || len(j.Requests) != 0 || len(j.Recorded) != 0 {
		t.Fatalf("journal %+v", j)
	}
	if strings.Join(p.OnRequest, ",") != "view" || strings.Join(p.Requests["view"], ",") != "sshd,postgres" ||
		strings.Join(p.Recorded, ",") != "view=sshd,view=postgres" {
		t.Fatalf("procs %+v", p)
	}
}

// The legacy validator accepts the settings the projection reads, though the manifest no longer
// declares them, and still refuses a typo or a wrong type.
func TestLegacySettingsTheProjectionReadsAreNotRefused(t *testing.T) {
	info := &LoopholeInfo{Name: "journal", Grants: legacyGrantFixture()[0].Grants}
	procs := &LoopholeInfo{Name: "procs", Grants: legacyGrantFixture()[1].Grants}
	for _, tc := range []struct {
		info    *LoopholeInfo
		spec    string
		wantErr string
	}{
		{info, `{"settings": {"full": true}}`, ""},
		{procs, `{"settings": {"visible": ["postgres"]}}`, ""},
		{info, `{"settings": {"fulll": true}}`, "fulll"},
		{info, `{"settings": {"full": "yes"}}`, "full"},
		{procs, `{"settings": {"visible": "postgres"}}`, "visible"},
	} {
		var errs []string
		validateLoopholeSettings(decode(t, tc.spec), "config.loopholes."+tc.info.Name, tc.info, &errs)
		if tc.wantErr == "" && len(errs) != 0 {
			t.Fatalf("%s refused: %v", tc.spec, errs)
		}
		if tc.wantErr != "" && (len(errs) == 0 || !strings.Contains(errs[0], tc.wantErr)) {
			t.Fatalf("%s: errs %v, want one naming %q", tc.spec, errs, tc.wantErr)
		}
	}
	// `full` was user-scope: a workspace file supplying it is still refused there.
	spec := decode(t, `{"settings": {"full": true}}`)
	if v := loopholeSettingsScopeViolations("journal", spec, WorkspaceConfigName, info); len(v) != 1 || !strings.Contains(v[0], "user-scope only") {
		t.Fatalf("workspace full: %v", v)
	}
}

// grantFixtureLegacyProcs is a host-processes-shaped manifest carrying the legacy keys and no
// settings, as F3 leaves it.
const grantFixtureLegacyProcs = `
[[grants]]
kind = "view"
text = "see these host processes"
valued = true
value_noun = "process names"
legacy_enable = true
legacy_values = "visible"
`

// Through the real load and `yolo check`'s validator, with the pack's real manifest: a legacy
// workspace file's `enabled` and `visible` read as a request (no "switched it on" line, no
// "no such setting" refusal), and a request the ceiling leaves out is named with its command.
func TestLegacyWorkspaceSwitchReadsAsARequestThroughTheValidator(t *testing.T) {
	_, cfgDir := sourcesHome(t)
	useGrantFixturePacks(t, map[string]string{"procs": grantFixtureLegacyProcs})
	write(t, filepath.Join(cfgDir, paths.UserConfigLegacyJSONCName), `{"packs": ["procs"]}`)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"loopholes": {"procs": {"enabled": true, "settings": {"visible": ["postgres"]}}}}`)
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	packs, _ := resolveSelectedPacks()
	errs, warns := ValidateConfig(merged, ws, explicitPackLoopholeResolver{packs: packs})
	if len(errs) != 0 {
		t.Fatalf("a legacy request is not an error: %q", errs)
	}
	if hits := containing(warns, "switched it on"); len(hits) != 0 {
		t.Fatalf("the workspace switch was disclosed as switching the loophole on: %q", hits)
	}
	if hits := containing(warns, "this project requests procs view postgres", "yolo allow procs on_request view"); len(hits) != 1 {
		t.Fatalf("warnings = %q, want the skipped request named with its command", warns)
	}
}

// A switch-file grant the gate would not hold (here the project's own file declines it) is no
// grant, and is not shown removed at every launch: two gates in a row ask nothing about it.
func TestLegacySwitchFileGrantTheGateWouldNotHoldIsNotShownRemoved(t *testing.T) {
	ws := approvalWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, WorkspaceTOMLConfigName), []byte("[requests.journal]\nread = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetWorkspaceLoophole(ws, "journal", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if got := grantGate(t, ws, legacyGrantFixture()); asksAboutGrants(got) {
			t.Fatalf("gate %d asked about a grant it would not hold: %+v", i, got)
		}
	}
	if got := legacyStandings(t, ws)["journal"]["read"]; got.Standing == LoopholeGrantGranted {
		t.Fatalf("read reads %+v when the project declines it", got)
	}
}

// shippedSerialGateLoophole is the shipped serial pack's grant declaration, read from the
// embedded tree as a launch reads it, so the legacy rows run on the real manifest's keys.
func shippedSerialGateLoophole(t *testing.T) []GateLoophole {
	t.Helper()
	return shippedGateLoophole(t, "serial")
}

// shippedGateLoophole is one shipped pack's single grant-declaring loophole, as the gate reads it.
func shippedGateLoophole(t *testing.T, pack string) []GateLoophole {
	t.Helper()
	decls := LoopholeGrantDeclarationsForPacks([]*packload.Pack{selectedEmbeddedPackForReadableDeclarations(t, pack)})
	if len(decls) != 1 {
		t.Fatalf("the shipped %s pack declares %d grant-declaring loopholes, want 1: %+v", pack, len(decls), decls)
	}
	d := decls[0]
	return []GateLoophole{{Pack: d.Pack, Loophole: d.Loophole, Access: d.Access, Grants: d.Grants}}
}

// A legacy audio switch (plan F5) reads as the one kind `use` on the shipped manifest, row for row
// of design §6: the workspace's `enabled: true` is a request the gate shows naming its file, the
// user's is an always line, and the host-written switch file is a grant the record already holds.
// None of them gains anything a legacy config did not already turn on.
func TestLegacyAudioSwitchReadsAsTheUseGrant(t *testing.T) {
	lps := shippedGateLoophole(t, "audio")
	if len(lps[0].Grants) != 1 || lps[0].Grants[0].Kind != "use" || !lps[0].Grants[0].LegacyEnable {
		t.Fatalf("the shipped audio grants are %+v, want one `use` kind with legacy_enable", lps[0].Grants)
	}
	const line = "  + audio: use your microphone and speakers"

	// `use` is not requestable by default, so a workspace switch alone is a request the user's
	// config does not allow: the gate shows the step, and nothing is granted.
	ws := approvalWorkspace(t)
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"audio": {"enabled": true}}}`)
	requireGrantLine(t, grantGate(t, ws, lps),
		"  – audio: use (not allowed on request by your config: yolo allow audio on_request use)")
	useStanding := func(ws string) LoopholeGrantStanding {
		t.Helper()
		read, err := ReadWorkspaceForGate(ws)
		if err != nil {
			t.Fatal(err)
		}
		return WithLoopholeGrants(nil, ws, read, lps).LoopholeGrantStandings(ws)["audio"]["use"]
	}
	if got := useStanding(ws); got.Standing != LoopholeGrantNotAllowed {
		t.Fatalf("a workspace switch alone reads %+v; want not_allowed", got)
	}

	// With the user's ceiling allowing it on request, the same switch is the request, named by
	// its file.
	ws = approvalWorkspace(t)
	writeGrantUserConfig(t, "[allow.audio]\non_request = [\"use\"]\n")
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"audio": {"enabled": true}}}`)
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	check := WithLoopholeGrants(nil, ws, read, lps)
	p := &reportPrompter{accept: false}
	if ok, err := CheckConfigAndScopeChanges(ws, read.Config, check, true, false, p); ok || err != nil {
		t.Fatalf("declined gate ok=%v err=%v", ok, err)
	}
	found := false
	for _, l := range p.got.GrantBlock {
		found = found || strings.HasPrefix(l, line+" (requested by ") && strings.Contains(l, WorkspaceConfigName)
	}
	if !found {
		t.Fatalf("the workspace audio switch did not read as a `use` request naming %s:\n%s", WorkspaceConfigName, strings.Join(p.got.GrantBlock, "\n"))
	}
	if got := check.LoopholeGrantStandings(ws)["audio"]["use"]; got.Standing != LoopholeGrantPending {
		t.Fatalf("a declined audio request reads %+v, want pending", got)
	}

	ws = approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"audio": {"enabled": true}}}`)
	requireGrantLine(t, grantGate(t, ws, lps), line+" (from your user config, for every project)")
	if got := useStanding(ws); got.Standing != LoopholeGrantGranted {
		t.Fatalf("after the y, use reads %+v; want granted", got)
	}

	ws = approvalWorkspace(t)
	if _, err := SetWorkspaceLoophole(ws, "audio", true); err != nil {
		t.Fatal(err)
	}
	if got := grantGate(t, ws, lps); asksAboutGrants(got) {
		t.Fatalf("a switch-file audio grant asked the gate: %+v", got)
	}
	if got := useStanding(ws); got.Standing != LoopholeGrantGranted {
		t.Fatalf("the switch file's audio grant reads %+v; want granted", got)
	}
}

// A legacy config that switched serial on and wrote no `allowed_devices` ran on the setting's five
// default globs; after the flip (plan F4) it reads as a request for exactly those, shown at the
// gate one line per glob, so the y keeps the access it had and nothing more. A list the config did
// write stands alone: the defaults never join it.
func TestLegacySerialWithNoListReadsAsTheDefaultGlobs(t *testing.T) {
	defaults := []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/cu.usbserial*", "/dev/cu.usbmodem*", "/dev/serial/by-id/*"}

	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"serial": {"enabled": true, "settings": {"default_baud": 9600}}}}`)
	lps := shippedSerialGateLoophole(t)
	report := grantGate(t, ws, lps)
	for _, glob := range defaults {
		requireGrantLinePrefix(t, report, "  + serial: open "+glob+" (requested by ")
	}
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	got := WithLoopholeGrants(nil, ws, read, lps).LoopholeGrantStandings(ws)["serial"]["devices"]
	if got.Standing != LoopholeGrantGranted || strings.Join(got.Values, ",") != strings.Join(defaults, ",") {
		t.Fatalf("after the y, devices reads %+v; want granted on the five default globs", got)
	}

	ws = approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"loopholes": {"serial": {"enabled": true}}}`)
	writeLegacyWorkspaceConfig(t, ws, `{"loopholes": {"serial": {"settings": {"allowed_devices": ["/dev/ttyACM0"]}}}}`)
	report = grantGate(t, ws, lps)
	for _, l := range report.GrantBlock {
		if strings.Contains(l, "/dev/ttyUSB*") {
			t.Fatalf("a written allowed_devices list gained a default glob: %q", l)
		}
	}
	requireGrantLinePrefix(t, report, "  + serial: open /dev/ttyACM0 (requested by ")
}

func requireGrantLinePrefix(t *testing.T, report *ChangeReport, prefix string) {
	t.Helper()
	if report == nil {
		t.Fatalf("the gate asked nothing; want a line starting %q", prefix)
	}
	for _, l := range report.GrantBlock {
		if strings.HasPrefix(l, prefix) {
			return
		}
	}
	t.Fatalf("grant block has no line starting %q:\n%s", prefix, strings.Join(report.GrantBlock, "\n"))
}
