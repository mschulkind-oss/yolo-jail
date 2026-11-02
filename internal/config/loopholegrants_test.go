package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// loopholegrants_test.go pins the ceiling and request readers for a grant-declaring loophole
// (docs/design/loophole-access-approval.md §4.2; plan T3). No shipped pack declares [[grants]]
// until the flips, so every case runs a fixture pack.

// grantFixtureJournal is a journal-shaped manifest: `read` requestable by default, `read_all`
// not.
const grantFixtureJournal = `
[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
`

// grantFixtureSerial is a serial-shaped manifest: one valued kind.
const grantFixtureSerial = `
[[grants]]
kind = "devices"
text = "open these host serial devices"
valued = true
value_noun = "device globs"
`

// writeGrantFixturePack writes the pack name under root, shipping one loophole of the same name
// whose manifest declares grants. It returns the pack's directory.
func writeGrantFixturePack(t *testing.T, root, name, grants string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	write(t, filepath.Join(dir, "pack.toml"), "name = \""+name+"\"\n\n[[loophole]]\nfrom = \"loopholes/"+name+"\"\n")
	write(t, filepath.Join(dir, "loopholes", name, "loophole.toml"), grantFixtureManifest(name, grants))
	return dir
}

// grantFixtureManifest is the loophole manifest writeGrantFixturePack writes.
func grantFixtureManifest(name, grants string) string {
	return `name = "` + name + `"
description = "a grant-declaring fixture"
version = 1
transport = "loopback-tls"
access = "reach the fixture"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "` + name + `", "--socket", "{socket}"]
publishes = "socket"
` + grants
}

// useGrantFixturePacks makes the fixture packs (name → its [[grants]]) the packs yolo ships, so
// `packs = ['<name>']` selects one by its bare name, as a shipped pack is selected and as
// `allow.<pack>` names it. Call it after HOME is set: the embedded tree is materialized there.
func useGrantFixturePacks(t *testing.T, packs map[string]string) {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, grants := range packs {
		fsys[name+"/pack.toml"] = &fstest.MapFile{Data: []byte("name = \"" + name + "\"\n\n[[loophole]]\nfrom = \"loopholes/" + name + "\"\n")}
		fsys[name+"/loopholes/"+name+"/loophole.toml"] = &fstest.MapFile{Data: []byte(grantFixtureManifest(name, grants))}
	}
	resetEmbeddedForExplicitEditTest(t)
	packload.SetEmbeddedFS(fsys)
}

// grantFixtureDeclarations loads the fixture pack as the launch's readers do and returns its
// readable declarations.
func grantFixtureDeclarations(t *testing.T, dir, name string) []ReadablePackDeclaration {
	t.Helper()
	pack, problems := packload.LoadDir(dir, name)
	if pack == nil {
		t.Fatalf("fixture pack: %v", problems)
	}
	_, settings, err := ReadableDeclarationsForPacks([]*packload.Pack{pack})
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func journalStanding(t *testing.T, plan ReadableAllowancePlan, kind string) LoopholeKindState {
	t.Helper()
	for _, p := range plan.LoopholeGrants.Packs {
		if p.Pack != "journal" {
			continue
		}
		for _, k := range p.Kinds {
			if k.Grant.Kind == kind {
				return k
			}
		}
	}
	t.Fatalf("no standing for journal %s in %+v", kind, plan.LoopholeGrants)
	return LoopholeKindState{}
}

// A kind the manifest marks requestable by default sits in the ceiling's on_request with no
// `[allow.journal]` at all, so a project's request reaches the gate; the user's own on_request
// list replaces the default whole, so `on_request = []` withdraws it and the request is skipped
// and named.
func TestARequestableByDefaultKindNeedsNoCeilingLine(t *testing.T) {
	dir := writeGrantFixturePack(t, t.TempDir(), "journal", grantFixtureJournal)
	decls := grantFixtureDeclarations(t, dir, "journal")
	workspace := editReadableMap(t, `{"requests":{"journal":{"read":true}}}`)

	plan, err := ResolveReadableAllowance(ReadableAllowanceInput{User: editReadableMap(t, `{}`), Workspace: workspace,
		SelectedPacks: []string{"journal"}, PackSettings: decls})
	if err != nil {
		t.Fatal(err)
	}
	read := journalStanding(t, plan, "read")
	if read.Standing != LoopholeKindCandidate || read.Ceiling != LoopholeCeilingOnRequest || !read.CeilingByDefault || !read.Requested {
		t.Errorf("no allow.journal: read = %+v, want a gate candidate on the default on_request", read)
	}
	if all := journalStanding(t, plan, "read_all"); all.Standing != LoopholeKindNotAllowed {
		t.Errorf("no allow.journal: read_all = %+v, want not_allowed (never requestable by default)", all)
	}
	if len(plan.LoopholeGrants.Packs[0].Skipped) != 0 {
		t.Errorf("no request is outside the default ceiling, got skipped %+v", plan.LoopholeGrants.Packs[0].Skipped)
	}

	// Without the request it is requestable, and nothing is skipped.
	plan, err = ResolveReadableAllowance(ReadableAllowanceInput{User: editReadableMap(t, `{}`), Workspace: editReadableMap(t, `{}`),
		SelectedPacks: []string{"journal"}, PackSettings: decls})
	if err != nil {
		t.Fatal(err)
	}
	if read := journalStanding(t, plan, "read"); read.Standing != LoopholeKindRequestable {
		t.Errorf("no request: read = %+v, want requestable", read)
	}

	plan, err = ResolveReadableAllowance(ReadableAllowanceInput{User: editReadableMap(t, `{"allow":{"journal":{"on_request":[]}}}`),
		Workspace: workspace, SelectedPacks: []string{"journal"}, PackSettings: decls})
	if err != nil {
		t.Fatal(err)
	}
	if read := journalStanding(t, plan, "read"); read.Standing != LoopholeKindNotAllowed || read.Ceiling != LoopholeCeilingNone {
		t.Errorf("on_request = []: read = %+v, want not_allowed outside every ceiling", read)
	}
	skipped := plan.LoopholeGrants.Packs[0].Skipped
	if len(skipped) != 1 || skipped[0].Kind != "read" || skipped[0].Path != "requests.journal.read" {
		t.Errorf("on_request = []: skipped = %+v, want the read request named", skipped)
	}

	// `always` holds it for every project, with or without a request.
	plan, err = ResolveReadableAllowance(ReadableAllowanceInput{User: editReadableMap(t, `{"allow":{"journal":{"always":["read_all"]}}}`),
		Workspace: editReadableMap(t, `{}`), SelectedPacks: []string{"journal"}, PackSettings: decls})
	if err != nil {
		t.Fatal(err)
	}
	if all := journalStanding(t, plan, "read_all"); all.Standing != LoopholeKindCandidate || all.Ceiling != LoopholeCeilingAlways {
		t.Errorf("always = [read_all]: read_all = %+v, want a gate candidate under always", all)
	}
	if read := journalStanding(t, plan, "read"); read.Standing != LoopholeKindRequestable {
		t.Errorf("always without on_request keeps the default: read = %+v, want requestable", read)
	}
}

// The readers refuse what no gate could show: a kind the manifest does not declare, in the
// ceiling or a request, and a request whose shape does not match the kind.
func TestTheGrantReadersRefuseUndeclaredKindsAndWrongShapes(t *testing.T) {
	journalDir := writeGrantFixturePack(t, t.TempDir(), "journal", grantFixtureJournal)
	serialDir := writeGrantFixturePack(t, t.TempDir(), "serial", grantFixtureSerial)
	decls := append(grantFixtureDeclarations(t, journalDir, "journal"), grantFixtureDeclarations(t, serialDir, "serial")...)
	for _, tc := range []struct{ user, workspace, want string }{
		{`{"allow":{"journal":{"on_request":["write"]}}}`, `{}`, "allow.journal.on_request lists write, which the journal pack does not declare"},
		{`{}`, `{"requests":{"journal":{"write":true}}}`, "requests.journal.write is not declared"},
		{`{}`, `{"requests":{"journal":{"read":["x"]}}}`, "requests.journal.read: read carries no value; write read = true"},
		{`{}`, `{"requests":{"serial":{"devices":true}}}`, "requests.serial.devices: devices carries device globs; write devices = ["},
		{`{"allow":{"serial":{"always":["devices"]}}}`, `{}`, "allow.serial.always lists devices, which carries a value only a project can give."},
	} {
		_, err := ResolveReadableAllowance(ReadableAllowanceInput{User: editReadableMap(t, tc.user), Workspace: editReadableMap(t, tc.workspace),
			SelectedPacks: []string{"journal", "serial"}, PackSettings: decls})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("user %s, workspace %s: err = %v, want it to say %q", tc.user, tc.workspace, err, tc.want)
		}
	}

	// A valued request carries its values through to the standing.
	plan, err := ResolveReadableAllowance(ReadableAllowanceInput{User: editReadableMap(t, `{"allow":{"serial":{"on_request":["devices"]}}}`),
		Workspace: editReadableMap(t, `{"requests":{"serial":{"devices":["/dev/ttyUSB*"]}}}`), SelectedPacks: []string{"journal", "serial"}, PackSettings: decls})
	if err != nil {
		t.Fatal(err)
	}
	var devices LoopholeKindState
	for _, p := range plan.LoopholeGrants.Packs {
		if p.Pack == "serial" {
			devices = p.Kinds[0]
		}
	}
	if devices.Standing != LoopholeKindCandidate || !slices.Equal(devices.Values, []string{"/dev/ttyUSB*"}) {
		t.Errorf("serial devices = %+v, want a candidate carrying its glob", devices)
	}
}

// A valued kind may sit only in on_request: its value is the project's to give, so `always`
// has none to carry. Through the TOML validator, as `yolo check` and a launch reach it, the
// refusal is the design's §5 text exactly, located at the line of the user's `always`.
func TestAValuedKindUnderAlwaysIsRefused(t *testing.T) {
	_, cfgDir := sourcesHome(t)
	useGrantFixturePacks(t, map[string]string{"serial": grantFixtureSerial})
	write(t, filepath.Join(cfgDir, paths.UserConfigTOMLName), `packs = ['serial']

# The serial pack's one grant kind, devices, names a value
# (the device globs), so only a project can give it. These
# lines put the always list on line 9, the line the design's
# example names.

[allow.serial]
always = ["devices"]
`)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(merged, ws, nil)
	want := "~/.config/yolo-jail/config.toml:9: allow.serial.always lists devices, which carries a value only a project can give.\n" +
		"  Move it:  yolo config unset allow.serial.always devices   then  yolo allow serial on_request devices"
	if !slices.Contains(errs, want) {
		t.Fatalf("errors = %q\nwant one to be exactly:\n%s", errs, want)
	}

	// The same table with the kind under on_request is accepted.
	write(t, filepath.Join(cfgDir, paths.UserConfigTOMLName), `packs = ['serial']

[allow.serial]
on_request = ["devices"]
`)
	merged, err = LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	if errs, _ := ValidateConfig(merged, ws, nil); len(errs) != 0 {
		t.Errorf("on_request = [devices] is the right spelling, got errors %q", errs)
	}
}

// A project's request outside the user's ceiling is a warning, not a refusal: the project runs
// without it, and the warning names the request at its line and the one command that allows it.
func TestARequestOutsideTheCeilingWarnsThroughTheValidator(t *testing.T) {
	_, cfgDir := sourcesHome(t)
	useGrantFixturePacks(t, map[string]string{"journal": grantFixtureJournal})
	write(t, filepath.Join(cfgDir, paths.UserConfigTOMLName), "packs = ['journal']\n")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws, paths.WorkspaceConfigTOMLName), "[requests.journal]\nread = true\nread_all = true\n")
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := ValidateConfig(merged, ws, nil)
	if len(errs) != 0 {
		t.Fatalf("a skipped request is not an error: %q", errs)
	}
	want := paths.WorkspaceConfigTOMLName + ":3: this project requests journal read_all.\n" +
		"  Your config does not allow read_all on request, so this project runs without it.\n" +
		"  To allow it for every project you work on (in your config, not this file):\n" +
		"    yolo allow journal on_request read_all"
	if !slices.Contains(warns, want) {
		t.Fatalf("warnings = %q\nwant one to be exactly:\n%s", warns, want)
	}
	for _, w := range warns {
		if strings.Contains(w, "requests journal read.") {
			t.Errorf("read is requestable by default, so its request is not skipped: %q", w)
		}
	}
}
