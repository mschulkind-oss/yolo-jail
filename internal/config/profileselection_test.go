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

// The key's forms, and null, lower to the two fields -p sets: the string, the list and "*" are
// Default, every other key is Named. A list is an active set, in order
// (docs/design/active-provider-sets.md OQ-AP1).
func TestProfileSelectionOfLowersEveryForm(t *testing.T) {
	for _, tc := range []struct {
		name        string
		value       string
		wantDefault []string
		wantNamed   map[string][]string
	}{
		{"a name for every agent", `"bedrock"`, []string{"bedrock"}, nil},
		{"per agent", `{"pi": "codex", "claude": "bedrock"}`, nil,
			map[string][]string{"pi": {"codex"}, "claude": {"bedrock"}}},
		{"every agent not named", `{"*": "bedrock", "pi": "codex"}`, []string{"bedrock"},
			map[string][]string{"pi": {"codex"}}},
		{"a null entry names its agent and selects none", `{"*": "bedrock", "codex": null}`,
			[]string{"bedrock"}, map[string][]string{"codex": nil}},
		{"null selects nothing", `null`, nil, nil},
		{"a list for every agent", `["zai", "openrouter"]`, []string{"zai", "openrouter"}, nil},
		{"a list per agent", `{"pi": ["zai", "openrouter"], "claude": "bedrock"}`, nil,
			map[string][]string{"pi": {"zai", "openrouter"}, "claude": {"bedrock"}}},
		{"a list for every agent not named", `{"*": ["zai", "openrouter"], "codex": "bedrock"}`,
			[]string{"zai", "openrouter"}, map[string][]string{"codex": {"bedrock"}}},
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
			if !reflect.DeepEqual(got.Default, tc.wantDefault) || !reflect.DeepEqual(got.Named, tc.wantNamed) {
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
	// pi declares provider_sets, so a bare list reaches it whole (OQ-AP3).
	recv := ProfileReceivers{Bins: []string{"claude", "pi", "codex"}, SetCapable: map[string]bool{"pi": true}}
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
			if err := s.ApplyFlag(v); err != nil {
				t.Fatalf("-p %s: %v", v, err)
			}
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
		// THE LIST (OQ-AP1 to OQ-AP3): a named list is kept whole, for OQ-AP2's refusal to find
		// at a CLI that cannot hold it; a bare list reaches a set-capable CLI whole and every
		// other as its first entry; a later pair replaces a list whole (AP-D4).
		{"a named list is kept whole",
			[]ProfileSelection{key(`{"pi": ["zai", "openrouter"], "claude": ["zai", "openrouter"]}`)},
			map[string]any{"pi": []any{"zai", "openrouter"}, "claude": []any{"zai", "openrouter"}}},
		{"the key's list reaches pi whole and the others as its first entry",
			[]ProfileSelection{key(`["zai", "openrouter"]`)},
			map[string]any{"claude": "zai", "pi": []any{"zai", "openrouter"}, "codex": "zai"}},
		{"a bare -p list is the key's list",
			[]ProfileSelection{flag("zai,openrouter")},
			map[string]any{"claude": "zai", "pi": []any{"zai", "openrouter"}, "codex": "zai"}},
		{"a -p pair replaces the key's list whole",
			[]ProfileSelection{key(`{"pi": ["zai", "openrouter"]}`), flag("pi=kilo")},
			map[string]any{"pi": "kilo"}},
		{"a list of one is the plain name",
			[]ProfileSelection{key(`{"pi": ["zai"]}`)},
			map[string]any{"pi": "zai"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tableOf(ProfileTableFor(recv, tc.sels...)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("table = %v, want %v", got, tc.want)
			}
		})
	}
}

// WHAT A BARE LIST DID (OQ-AP3), which the launch line and the declaration check read off the
// fold: the list, the selection it came from, and which receivers took it whole or narrowed. A
// receiver a named entry took is in neither, and a later bare name clears the record.
func TestFoldProfilesRecordsWhatABareListNarrowed(t *testing.T) {
	recv := ProfileReceivers{Bins: []string{"claude", "pi", "codex"}, SetCapable: map[string]bool{"pi": true}}
	list := []string{"zai", "openrouter"}
	key := ProfileSelection{Default: list}
	pair := ProfileSelection{Named: map[string][]string{"codex": {"bedrock"}}}
	fold := FoldProfiles(recv, key, pair)
	if !reflect.DeepEqual(fold.BareList, list) || fold.BareFrom != 0 {
		t.Errorf("fold = %v from %d, want the key's list from selection 0", fold.BareList, fold.BareFrom)
	}
	if !reflect.DeepEqual(fold.Whole, []string{"pi"}) || !reflect.DeepEqual(fold.Narrowed, []string{"claude"}) {
		t.Errorf("whole %v, narrowed %v; want pi whole and claude narrowed, codex taken by its pair",
			fold.Whole, fold.Narrowed)
	}
	for _, want := range []string{"the profile key's list", "claude takes one profile (its pack does not declare provider_sets)",
		`"profile": {"<agent>": ["zai", "openrouter"]}`} {
		if note := fold.BareListNote(true); !strings.Contains(note, want) {
			t.Errorf("the key's line must say %q:\n%s", want, note)
		}
	}
	if fold := FoldProfiles(recv, key, ProfileSelection{Default: []string{"kilo"}}); fold.BareList != nil ||
		fold.BareListNote(false) != "" {
		t.Errorf("a later bare name replaces the list everywhere, so nothing is narrowed: %+v", fold)
	}
	if fold := FoldProfiles(ProfileReceivers{Bins: []string{"pi"}, SetCapable: map[string]bool{"pi": true}}, key); fold.BareListNote(false) != "" {
		t.Errorf("a list every receiver holds narrows nothing and says nothing: %q", fold.BareListNote(false))
	}
}

// The default never reaches a CLI no pack installs — the 2026-09-03 ruling for a bare -p, and
// so for "*": `yolo -- sleep 60` is not a profile target.
func TestProfileTableForDefaultReachesInstalledCLIsOnly(t *testing.T) {
	got := tableOf(ProfileTableFor(ProfileReceivers{Bins: []string{"claude"}},
		ProfileSelection{Default: []string{"bedrock"}}))
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
	for _, v := range []string{"codex=zai,pi=codex", "claude=bedrock"} {
		if err := flag.ApplyFlag(v); err != nil {
			t.Fatal(err)
		}
	}
	fromKey, _ := jsonx.DumpsCompact(ProfileTableFor(ProfileReceivers{}, key))
	fromFlag, _ := jsonx.DumpsCompact(ProfileTableFor(ProfileReceivers{}, flag))
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
		// Lists (OQ-AP1): pi's pack declares provider_sets, and a bare list is narrowed rather
		// than refused wherever it lands (OQ-AP3).
		`{"pi": ["zai", "openrouter"], "claude": ["bedrock"]}`,
		`["zai", "openrouter"]`,
		`{"*": ["zai", "openrouter"], "pi": ["openrouter", "zai"]}`,
	} {
		errs, _ := ValidateConfig(decode(t, `{"profile": `+v+`}`), t.TempDir(), nil)
		if len(errs) != 0 {
			t.Errorf("profile %s must validate clean, got %v", v, errs)
		}
	}
}

// What the key refuses, each by its path. The list's own refusals (an empty list, a repeated
// name, a list named at an agent that cannot hold one) are profilesets_test.go's.
func TestValidateProfileRefusesMalformedValues(t *testing.T) {
	useProfileKeysHome(t)
	for _, tc := range []struct {
		value, wantPrefix, wantText string
	}{
		{`""`, "config.profile:", "null selects none"},
		{`{"pi": ""}`, "config.profile.pi:", "null selects none"},
		{`{"pi": 4}`, "config.profile.pi:", "expected a profile name, a list of them"},
		{`{"*": 4}`, "config.profile.*:", "expected a profile name, a list of them"},
		{`4`, "config.profile:", "expected a string"},
		{`[4]`, "config.profile:", "entry 1 of the list is not a profile name"},
		{`{"pi": ["zai", ""]}`, "config.profile.pi:", "entry 2 of the list is not a profile name"},
		{`{"cloude": "zai"}`, "config.profile.cloude:", `no pack installs a CLI named "cloude"`},
		// The flag's pair grammar written into the string form: refused, and respelled as the
		// object it meant.
		{`"pi=codex,claude=bedrock"`, "config.profile:", `"profile": {"claude": "bedrock", "pi": "codex"}`},
		// And the flag's comma list: refused, and respelled as the JSON list it meant.
		{`"zai,openrouter"`, "config.profile:", `write it in its place: ["zai", "openrouter"]`},
		{`{"pi": "zai,openrouter"}`, "config.profile.pi:", `write it in its place: ["zai", "openrouter"]`},
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

// THE DESELECTION NAMES WHERE THE SELECTION CAME FROM AND THE SPELLING THAT UNDOES IT THERE. A
// selection from the `profile` key is answered with the key's file and line and the key's own
// spelling with a null for the agent — which, parsed and folded back, selects nothing for that
// agent while every other agent keeps the selection. One from -p is answered with the -p pair
// that selects none, since nothing persistent wrote it.
func TestProfileDeselectionNamesTheSourceAndTheSpelling(t *testing.T) {
	h := newMountsHost(t)
	h.user(t, "{\n  \"profile\": \"bedrock\"\n}\n")
	key := ProfileSelection{Default: []string{"bedrock"}}

	got := ProfileDeselection(key, ProfileSelection{}, "agy")
	const spelling = `"profile": {"*": "bedrock", "agy": null}`
	for _, want := range []string{
		"your config's `profile` key at ~/.config/yolo-jail/config.jsonc:2:14",
		"write `" + spelling + "` there",
		"or add `-p agy=` for one launch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ProfileDeselection() = %q, want it to contain %q", got, want)
		}
	}
	doc, err := jsonx.Decode([]byte("{" + spelling + "}"))
	if err != nil {
		t.Fatalf("the printed spelling does not parse: %v", err)
	}
	v, _ := doc.(*jsonx.OrderedMap).Get(ProfileKey)
	sel, ok := ProfileSelectionOf(v)
	if !ok {
		t.Fatalf("the printed spelling is not a valid `profile` value: %v", v)
	}
	table := ProfileTableFor(ProfileReceivers{Bins: []string{"claude", "agy"}}, sel)
	if agy, _ := table.Get("agy"); agy != nil {
		t.Errorf("the printed spelling still selects %v for agy", agy)
	}
	if claude, _ := table.Get("claude"); claude != "bedrock" {
		t.Errorf("the printed spelling took bedrock from claude too: %v", claude)
	}

	flag := ProfileSelection{Default: []string{"bedrock"}}
	if got := ProfileDeselection(key, flag, "agy"); got != "the selection is this launch's `-p`, so add `-p agy=` to it" {
		t.Errorf("a -p selection was answered with %q", got)
	}
}
