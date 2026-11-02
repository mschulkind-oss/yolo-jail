package loopholedecl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// grantProbe is a grant-declaring manifest's head: a host daemon, the access line, and
// whatever the test appends (settings, grants, a brokered block).
const grantProbe = `name = "probe"
transport = "loopback-tls"
version = 1
access = "read the host's journal"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "probe"]
publishes = "socket"
`

func loadGrantProbe(t *testing.T, body string) (*loopholedecl.Manifest, error) {
	t.Helper()
	dir := writeModule(t, "probe", map[string]string{"loophole.toml": body})
	return loopholedecl.LoadDir(dir)
}

func wantGrantRefusal(t *testing.T, body string, wants ...string) {
	t.Helper()
	m, err := loadGrantProbe(t, body)
	if err == nil {
		t.Fatalf("the manifest loaded, want a refusal naming %q: %+v", wants, m)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not name %q: %v", w, err)
		}
	}
	if !strings.Contains(err.Error(), "loophole.toml") {
		t.Errorf("the refusal does not start from the manifest path: %v", err)
	}
}

// TestGrantsDeclareKindsTextAndValued: [[grants]] decodes, in written order, into the kinds the
// gate and the refusals print, and `access` is the loophole's one line of what it reaches.
func TestGrantsDeclareKindsTextAndValued(t *testing.T) {
	m, err := loadGrantProbe(t, grantProbe+`
[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true
legacy_enable = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
legacy_setting = "full"
`)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if m.Access != "read the host's journal" {
		t.Errorf("Access = %q", m.Access)
	}
	want := []loopholedecl.Grant{
		{Kind: "read", Text: "read your user journal", RequestableByDefault: true, LegacyEnable: true},
		{Kind: "read_all", Text: "read the whole host journal", LegacySetting: "full"},
	}
	if !reflect.DeepEqual(m.Grants, want) {
		t.Fatalf("Grants = %+v\nwant     %+v", m.Grants, want)
	}

	// A valued kind carries its noun and its legacy list.
	m, err = loadGrantProbe(t, grantProbe+`
[[grants]]
kind = "view"
text = "see these host processes"
valued = true
value_noun = "process names"
legacy_enable = true
legacy_values = "visible"
`)
	if err != nil {
		t.Fatalf("LoadDir (valued): %v", err)
	}
	wantValued := []loopholedecl.Grant{{Kind: "view", Text: "see these host processes", Valued: true,
		ValueNoun: "process names", LegacyEnable: true, LegacyValues: "visible"}}
	if !reflect.DeepEqual(m.Grants, wantValued) {
		t.Fatalf("Grants = %+v\nwant     %+v", m.Grants, wantValued)
	}

	// A manifest that declares none is ungated: no grants, no access.
	m, err = loadGrantProbe(t, strings.Replace(grantProbe, "access = \"read the host's journal\"\n", "", 1))
	if err != nil {
		t.Fatalf("LoadDir (ungated): %v", err)
	}
	if m.Grants != nil || m.Access != "" {
		t.Fatalf("an ungated manifest decoded grants %+v, access %q", m.Grants, m.Access)
	}
}

// TestAValuedKindRequestableByDefaultIsRefused: a valued kind's value is the project's, so
// there is no default value for the ceiling to offer (design §4.2).
func TestAValuedKindRequestableByDefaultIsRefused(t *testing.T) {
	wantGrantRefusal(t, grantProbe+`
[[grants]]
kind = "view"
text = "see these host processes"
valued = true
requestable_by_default = true
`, "requestable_by_default", "valued", "view")
}

func TestGrantsBesideBrokeredIsRefused(t *testing.T) {
	wantGrantRefusal(t, strings.Replace(grantProbe, `"probe"]`, `"probe", "{repository_scope}"]`, 1)+`
[brokered]
source = "github"
remote_host = "github.com"

[[grants]]
kind = "read"
text = "read your user journal"
`, "brokered", "grants")
}

func TestDuplicateGrantKindIsRefused(t *testing.T) {
	wantGrantRefusal(t, grantProbe+`
[[grants]]
kind = "read"
text = "read your user journal"

[[grants]]
kind = "read"
text = "read it again"
`, "read", "twice")
}

func TestTwoLegacyEnableKindsAreRefused(t *testing.T) {
	wantGrantRefusal(t, grantProbe+`
[[grants]]
kind = "read"
text = "read your user journal"
legacy_enable = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
legacy_enable = true
`, "legacy_enable", "read", "read_all")
}

// TestASettingNamedGrantsIsRefused: `grants` is the reserved key of the grant file, which is the
// settings file, so a setting of that name would be overwritten by the grants or overwrite them.
func TestASettingNamedGrantsIsRefused(t *testing.T) {
	// Refused with and without a grant declaration: the key is reserved in the file either way.
	for name, head := range map[string]string{
		"with grants": grantProbe,
		"no grants":   strings.Replace(grantProbe, "access = \"read the host's journal\"\n", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			body := head + `
[settings.grants]
type = "bool"
scope = "user"
default = false
`
			if name == "with grants" {
				body += `
[[grants]]
kind = "read"
text = "read your user journal"
`
			}
			wantGrantRefusal(t, body, "grants", "reserved")
		})
	}
}

// TestSettingsTokenIsLegalWithGrantsAndNoSettings: the grant file is the file {settings} names
// (PD-2), so a manifest declaring grants and no settings still has a file to hand its daemon.
func TestSettingsTokenIsLegalWithGrantsAndNoSettings(t *testing.T) {
	body := strings.Replace(grantProbe, `"probe"]`, `"probe", "--settings", "{settings}"]`, 1) + `
[[grants]]
kind = "read"
text = "read your user journal"
`
	m, err := loadGrantProbe(t, body)
	if err != nil {
		t.Fatalf("{settings} with [[grants]] and no [settings] was refused: %v", err)
	}
	if len(m.Grants) != 1 || len(m.Settings) != 0 {
		t.Fatalf("decoded grants %+v settings %+v", m.Grants, m.Settings)
	}
	// Without the grants, the token still names nothing and is still refused.
	noGrants := strings.Replace(strings.Replace(grantProbe, `"probe"]`, `"probe", "--settings", "{settings}"]`, 1),
		"access = \"read the host's journal\"\n", "", 1)
	wantGrantRefusal(t, noGrants, "{settings}")
}

// TestGrantDeclarationShapeIsRefused covers the rest of the block's schema: each entry is
// checked whole, and a key it does not know is refused in both decoders, as `brokered`'s are.
func TestGrantDeclarationShapeIsRefused(t *testing.T) {
	grant := func(lines string) string { return grantProbe + "\n[[grants]]\n" + lines }
	for name, c := range map[string]struct{ body, want string }{
		"no kind":                 {grant(`text = "t"`), "kind"},
		"bad kind":                {grant("kind = \"Read All\"\ntext = \"t\""), "kind"},
		"no text":                 {grant(`kind = "read"`), "text"},
		"unknown key":             {grant("kind = \"read\"\ntext = \"t\"\nvalue = true"), "grants[0].value"},
		"noun on unvalued":        {grant("kind = \"read\"\ntext = \"t\"\nvalue_noun = \"x\""), "value_noun"},
		"legacy list on unvalued": {grant("kind = \"read\"\ntext = \"t\"\nlegacy_values = \"x\""), "legacy_values"},
		"valued not a bool":       {grant("kind = \"read\"\ntext = \"t\"\nvalued = \"true\""), "valued"},
		"grants without access": {strings.Replace(grantProbe, "access = \"read the host's journal\"\n", "", 1) +
			"\n[[grants]]\nkind = \"read\"\ntext = \"t\"\n", "access"},
		"access without grants": {grantProbe, "access"},
	} {
		t.Run(name, func(t *testing.T) {
			wantGrantRefusal(t, c.body, c.want)
			if _, _, err := loopholedecl.LoadDirTolerant(writeModule(t, "probe",
				map[string]string{"loophole.toml": c.body})); err == nil {
				t.Errorf("the tolerant decoder loaded it")
			}
		})
	}
}
