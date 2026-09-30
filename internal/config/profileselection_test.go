package config

// profileselection_test.go pins the `profile` key (docs/design/providers-and-profiles-redesign.md
// PP-D10): its three forms, the one fold both it and -p go through, the precedence that fold
// keeps, the refusal of the key it renamed, and its place in the inherited snapshots. The flag
// half of "the key and -p say the same thing" is internal/cli's profilekeyflag_test.go, which
// can parse an argv; this file owns what one ProfileSelection means.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// tableOf renders a folded table as a plain map, nulls kept, so a test can state the whole
// table: a CLI with a nil entry is named and selects nothing, which is not the same fact as a
// CLI that is absent.
func tableOf(m *jsonx.OrderedMap) map[string]any {
	out := map[string]any{}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = v
	}
	return out
}

// The key's three forms, and null, lower to the two fields -p sets: the string and "*" are
// Default, every other key is Named.
func TestProfileSelectionOfLowersEveryForm(t *testing.T) {
	for _, tc := range []struct {
		name        string
		value       string
		wantDefault string
		wantNamed   map[string]string
	}{
		{"a name for every agent", `"bedrock"`, "bedrock", nil},
		{"per agent", `{"pi": "codex", "claude": "bedrock"}`, "",
			map[string]string{"pi": "codex", "claude": "bedrock"}},
		{"every agent not named", `{"*": "bedrock", "pi": "codex"}`, "bedrock",
			map[string]string{"pi": "codex"}},
		{"a null entry names its agent and selects none", `{"*": "bedrock", "codex": null}`,
			"bedrock", map[string]string{"codex": ""}},
		{"null selects nothing", `null`, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := jsonx.Decode([]byte(tc.value))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := ProfileSelectionOf(v)
			if !ok {
				t.Fatalf("%s did not lower", tc.value)
			}
			if got.Default != tc.wantDefault || !reflect.DeepEqual(got.Named, tc.wantNamed) {
				t.Errorf("%s lowered to {Default: %q, Named: %v}, want {%q, %v}",
					tc.value, got.Default, got.Named, tc.wantDefault, tc.wantNamed)
			}
		})
	}
}

// THE PRECEDENCE, whole: within one selection a named CLI keeps its entry and the default
// reaches every other INSTALLED one; across selections the later wins for every CLI it
// reaches, whatever form either uses. A launch hands the key first and -p second, so the rows
// below are the rule "every -p beats the key", which is what the persistent table always had
// under the flag.
func TestProfileTableForPrecedence(t *testing.T) {
	bins := []string{"claude", "pi", "codex"}
	key := func(v string) ProfileSelection {
		t.Helper()
		d, err := jsonx.Decode([]byte(v))
		if err != nil {
			t.Fatal(err)
		}
		s, ok := ProfileSelectionOf(d)
		if !ok {
			t.Fatalf("%s did not lower", v)
		}
		return s
	}
	flag := func(values ...string) ProfileSelection {
		var s ProfileSelection
		for _, v := range values {
			s.ApplyFlag(v)
		}
		return s
	}
	for _, tc := range []struct {
		name string
		sels []ProfileSelection
		want map[string]any
	}{
		{"\"*\" reaches every installed CLI the key does not name",
			[]ProfileSelection{key(`{"*": "bedrock", "pi": "codex"}`)},
			map[string]any{"claude": "bedrock", "pi": "codex", "codex": "bedrock"}},
		{"the string form reaches every installed CLI",
			[]ProfileSelection{key(`"bedrock"`)},
			map[string]any{"claude": "bedrock", "pi": "bedrock", "codex": "bedrock"}},
		{"a null entry keeps \"*\" off its CLI",
			[]ProfileSelection{key(`{"*": "bedrock", "codex": null}`)},
			map[string]any{"claude": "bedrock", "pi": "bedrock", "codex": nil}},
		{"within -p a pair keeps its CLI beside a bare name, in either order",
			[]ProfileSelection{flag("pi=codex", "bedrock")},
			map[string]any{"claude": "bedrock", "pi": "codex", "codex": "bedrock"}},
		{"a bare -p beats the key's named entry",
			[]ProfileSelection{key(`{"pi": "codex"}`), flag("zai")},
			map[string]any{"claude": "zai", "pi": "zai", "codex": "zai"}},
		{"a -p pair beats the key's \"*\" for its CLI alone",
			[]ProfileSelection{key(`{"*": "bedrock"}`), flag("pi=zai")},
			map[string]any{"claude": "bedrock", "pi": "zai", "codex": "bedrock"}},
		{"a -p pair beats the key's named entry",
			[]ProfileSelection{key(`{"pi": "codex", "claude": "bedrock"}`), flag("pi=zai")},
			map[string]any{"claude": "bedrock", "pi": "zai"}},
		{"a named CLI nothing installs is kept for the refusals to find",
			[]ProfileSelection{key(`{"*": "bedrock", "cloude": "zai"}`)},
			map[string]any{"claude": "bedrock", "pi": "bedrock", "codex": "bedrock", "cloude": "zai"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tableOf(ProfileTableFor(bins, tc.sels...)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("table = %v, want %v", got, tc.want)
			}
		})
	}
}

// The default never reaches a CLI no pack installs — the 2026-09-03 ruling for a bare -p, and
// so for "*": `yolo -- sleep 60` is not a profile target.
func TestProfileTableForDefaultReachesInstalledCLIsOnly(t *testing.T) {
	got := tableOf(ProfileTableFor([]string{"claude"}, ProfileSelection{Default: "bedrock"}))
	if !reflect.DeepEqual(got, map[string]any{"claude": "bedrock"}) {
		t.Errorf("table = %v, want claude alone", got)
	}
}

// The table is the same BYTES whichever spelling selected it: named entries emit sorted, so a
// key written in any order and its -p twin put one line in the jail, on every run.
func TestProfileTableForIsTheSameBytesForEitherSpelling(t *testing.T) {
	d, _ := jsonx.Decode([]byte(`{"pi": "codex", "claude": "bedrock", "codex": "zai"}`))
	key, _ := ProfileSelectionOf(d)
	var flag ProfileSelection
	flag.ApplyFlag("codex=zai,pi=codex")
	flag.ApplyFlag("claude=bedrock")
	fromKey, _ := jsonx.DumpsCompact(ProfileTableFor(nil, key))
	fromFlag, _ := jsonx.DumpsCompact(ProfileTableFor(nil, flag))
	if want := `{"claude": "bedrock", "codex": "zai", "pi": "codex"}`; fromKey != want || fromFlag != want {
		t.Errorf("key emits %s and -p emits %s, want both %s", fromKey, fromFlag, want)
	}
}

// ConfigProfileTable reads the key by its name, over the packs' bins: the reader of every
// verb with no launch in hand. The embedded claude pack installs `claude`, so "*" reaches it.
func TestConfigProfileTableFoldsTheKeyOverThePacks(t *testing.T) {
	var claude []*packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "claude" {
			claude = append(claude, p)
		}
	}
	if len(claude) != 1 {
		t.Fatal("fixture: the embedded claude pack is missing")
	}
	cfg := decode(t, `{"profile": {"*": "bedrock"}}`)
	if got := ConfigProfileTable(cfg, claude); !reflect.DeepEqual(got, map[string]string{"claude": "bedrock"}) {
		t.Errorf("table = %v, want claude on bedrock", got)
	}
	if got := ConfigProfileTable(decode(t, `{"use_profiles": {"claude": "bedrock"}}`), claude); len(got) != 0 {
		t.Errorf("the retired key must select nothing, got %v", got)
	}
}

// Every form the key takes validates clean, "*" included, and "*" is never held to the
// CLI-name namespace: no program is called `*`.
func TestValidateProfileAcceptsEveryForm(t *testing.T) {
	useProfileKeysHome(t)
	for _, v := range []string{
		`"bedrock"`,
		`{"pi": "codex", "claude": "bedrock"}`,
		`{"*": "bedrock", "pi": "codex", "codex": null}`,
		`{"*": null}`,
		`null`,
	} {
		errs, _ := ValidateConfig(decode(t, `{"profile": `+v+`}`), t.TempDir(), nil)
		if len(errs) != 0 {
			t.Errorf("profile %s must validate clean, got %v", v, errs)
		}
	}
}

// What the key refuses, each by its path. A list is an ordinary shape error today (the
// provider-list build gives it a meaning), so these assert only the path and the type words.
func TestValidateProfileRefusesMalformedValues(t *testing.T) {
	useProfileKeysHome(t)
	for _, tc := range []struct {
		value, wantPrefix, wantText string
	}{
		{`""`, "config.profile:", "null selects none"},
		{`{"pi": ""}`, "config.profile.pi:", "null selects none"},
		{`{"pi": 4}`, "config.profile.pi:", "expected a string profile name"},
		{`{"*": 4}`, "config.profile.*:", "expected a string profile name"},
		{`4`, "config.profile:", "expected a string"},
		{`["zai"]`, "config.profile:", "expected a string"},
		{`{"pi": ["zai"]}`, "config.profile.pi:", "expected a string profile name"},
		{`{"cloude": "zai"}`, "config.profile.cloude:", `no pack installs a CLI named "cloude"`},
		// The flag's pair grammar written into the string form: refused, and respelled as the
		// object it meant.
		{`"pi=codex,claude=bedrock"`, "config.profile:", `"profile": {"claude": "bedrock", "pi": "codex"}`},
	} {
		errs, _ := ValidateConfig(decode(t, `{"profile": `+tc.value+`}`), t.TempDir(), nil)
		found := ""
		for _, e := range errs {
			if strings.HasPrefix(e, tc.wantPrefix) {
				found = e
			}
		}
		if found == "" || !strings.Contains(found, tc.wantText) {
			t.Errorf("profile %s: want an error at %s saying %q, got %v", tc.value,
				tc.wantPrefix, tc.wantText, errs)
		}
	}
}

// THE RENAME'S REFUSAL (PP-D10): `use_profiles` is refused BY NAME on the host, naming the new
// key and respelling the user's own entries, and it is the ONLY error it produces — the key
// stays in knownTopLevelConfigKeys so no generic unknown-key error duplicates it.
func TestValidateUseProfilesRetiredOnTheHost(t *testing.T) {
	useProfileKeysHome(t)
	t.Setenv("YOLO_VERSION", "") // the host view
	if _, known := knownTopLevelConfigKeys["use_profiles"]; !known {
		t.Error("`use_profiles` must stay in knownTopLevelConfigKeys so the rename message is the only error")
	}
	errs, warns := ValidateConfig(decode(t, `{"use_profiles": {"claude": "bedrock", "pi": null}}`), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, the rename, got %d: %v", len(errs), errs)
	}
	for _, want := range []string{
		"config.use_profiles: RENAMED",
		"this key is now `profile`",
		`Your entries, respelled: "profile": {"claude": "bedrock", "pi": null}`,
	} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs[0])
		}
	}
	for _, w := range warns {
		if strings.Contains(w, "use_profiles") {
			t.Errorf("on the host the rename is an error, not a warning: %v", warns)
		}
	}
}

// In-jail the config is the snapshot a launcher generated, and one an older launcher wrote
// legitimately carries the old key, so there it is a WARNING (the agent_profiles precedent)
// that says to rename it in the HOST config.
func TestValidateUseProfilesRetiredWarnsInJail(t *testing.T) {
	useProfileKeysHome(t)
	t.Setenv("YOLO_VERSION", "test")
	errs, warns := ValidateConfig(decode(t, `{"use_profiles": {"claude": "bedrock"}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "use_profiles") {
			t.Errorf("in-jail the retired key must not refuse: %v", errs)
		}
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "config.use_profiles: RENAMED") && strings.Contains(w, "HOST config") {
			found = true
		}
	}
	if !found {
		t.Errorf("in-jail the retired key must warn, naming the host config: %v", warns)
	}
}

// A workspace that carries the old key is told both halves of the fix: the rename, and that
// the key is user-scope only.
func TestValidateUseProfilesAtWorkspaceNamesBothFixes(t *testing.T) {
	home := useProfileKeysHome(t)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	write(t, home+"/.config/yolo-jail/config.jsonc", `{}`)
	write(t, ws+"/"+WorkspaceConfigName, `{"use_profiles": {"claude": "bedrock"}}`)
	cfg, err := LoadJSONCWithIncludes(ws+"/"+WorkspaceConfigName, "workspace", false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, ws, nil)
	var renamed, scoped bool
	for _, e := range errs {
		renamed = renamed || strings.Contains(e, "config.use_profiles: RENAMED")
		scoped = scoped || strings.HasPrefix(e, "config.use_profiles: user-scope only")
	}
	if !renamed || !scoped {
		t.Errorf("want the rename and the user-scope refusal, got %v", errs)
	}
}

// The key crosses into a jail's generated user scope VERBATIM, "*" and all, for the in-jail
// readers and a nested launcher to fold over their own pack set; the retired key crosses
// nowhere, so a snapshot this build writes never re-triggers its refusal.
func TestTheProfileKeyCrossesIntoBothInheritedScopes(t *testing.T) {
	effective := decode(t, `{"profile": {"*": "bedrock", "pi": "codex"}, "use_profiles": {"pi": "zai"}}`)
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		out, unknown := FilterInherit(effective, scope)
		if len(unknown) != 0 {
			t.Errorf("%s: unclassified keys %v", scope, unknown)
		}
		if got, _ := jsonx.DumpsCompact(out); got != `{"profile": {"*": "bedrock", "pi": "codex"}}` {
			t.Errorf("%s scope = %s, want the key verbatim and no use_profiles", scope, got)
		}
	}
	if _, retired := retiredTopLevelConfigKeys["use_profiles"]; !retired {
		t.Error("use_profiles must be a retired top-level key, refused and undocumented")
	}
}

// THE CLOSURE'S CALL SITE for every host verb that launches nothing (UserScopeSelection): the
// key's string form and its "*" select the via profile for pi, so the bridge joins as it does
// at launch. Deleting the fold from UserScopeSelection (reading only named entries, or the old
// key) fails here.
func TestUserScopeSelectionFoldsTheDefaultOverTheSet(t *testing.T) {
	for name, sel := range map[string]string{
		"string form": `"pz"`,
		"\"*\"":       `{"*": "pz"}`,
	} {
		writeViaSelectionConfig(t, `{"packs": ["pi", "zai"],
		  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}},
		  "profile": `+sel+`}`)
		if names := selectedNames(t); !viaHasName(names, "wire-bridge") {
			t.Errorf("%s: the via profile \"*\" selects for pi did not join the bridge: %v", name, names)
		}
	}
}
