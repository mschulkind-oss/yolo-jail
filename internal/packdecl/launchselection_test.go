package packdecl

// launchselection_test.go pins the `launch_selection` field (Contribution.LaunchSelection;
// docs/design/model-lists-and-pickers.md MM-D30): a program's declaration of how one `yolo host -p`
// launch hands it the selection its derive composes, projected into its Install for `yolo host --`
// to read, refused wherever no launch could hand it, and ignored by a reader that predates it.
// Through Decode, DecodeTolerant and the real validator.

import (
	"reflect"
	"testing"
)

const validLaunchSelection = `{"surface":".x/config.toml","each":["-c","{key}={value}"],
  "rows":{"table":"model_providers","named_by":["model_provider"]},
  "defaults":{"model_provider":"openai"},
  "surfaces":{".x/list.json":"X_LIST"},"subcommands":["login","mcp"]}`

func TestLaunchSelectionIsProjectedFromAProgramOfAnyVia(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"program","bin":"codex","via":"installer","url":"https://example.test/i.sh",
	   "launch_selection":` + validLaunchSelection + `},
	  {"kind":"program","bin":"tool","via":"npm","package":"tool","launch_selection":
	   {"surface":".t/settings.json","flags":[
	    {"key":"defaultProvider","argv":["--provider","{value}"],"requires":["defaultModel"]},
	    {"key":"defaultModel","argv":["--model","{value}"]}]}},
	  {"kind":"program","bin":"other","via":"npm","package":"q"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	installs := m.InstallContributions()
	if len(installs) != 3 {
		t.Fatalf("installs = %+v, want three", installs)
	}
	want := &LaunchSelection{Surface: ".x/config.toml", Each: []string{"-c", "{key}={value}"},
		Rows:        &LaunchSelectionRows{Table: "model_providers", NamedBy: []string{"model_provider"}},
		Defaults:    map[string]string{"model_provider": "openai"},
		Surfaces:    map[string]string{".x/list.json": "X_LIST"},
		Subcommands: []string{"login", "mcp"}}
	if !reflect.DeepEqual(installs[0].LaunchSelection, want) {
		t.Errorf("codex's LaunchSelection = %+v, want %+v", installs[0].LaunchSelection, want)
	}
	flags := &LaunchSelection{Surface: ".t/settings.json",
		Flags: []LaunchSelectionFlag{{Key: "defaultProvider", Argv: []string{"--provider", "{value}"},
			Requires: []string{"defaultModel"}}, {Key: "defaultModel", Argv: []string{"--model", "{value}"}}}}
	if !reflect.DeepEqual(installs[1].LaunchSelection, flags) {
		t.Errorf("tool's LaunchSelection = %+v, want %+v", installs[1].LaunchSelection, flags)
	}
	if installs[2].LaunchSelection != nil {
		t.Errorf("a program that declares none projects %+v, want nil", installs[2].LaunchSelection)
	}
	// A copy, not an alias: a consumer editing its Install cannot reach the manifest.
	installs[0].LaunchSelection.Each[0] = "edited"
	installs[0].LaunchSelection.Rows.NamedBy[0] = "edited"
	installs[0].LaunchSelection.Defaults["model_provider"] = "edited"
	installs[0].LaunchSelection.Surfaces[".x/list.json"] = "EDITED"
	installs[0].LaunchSelection.Subcommands[0] = "edited"
	installs[1].LaunchSelection.Flags[0].Argv[0] = "edited"
	installs[1].LaunchSelection.Flags[0].Requires[0] = "edited"
	again := m.InstallContributions()
	if !reflect.DeepEqual(again[0].LaunchSelection, want) || !reflect.DeepEqual(again[1].LaunchSelection, flags) {
		t.Errorf("editing a projected LaunchSelection reached the manifest: %+v, %+v",
			again[0].LaunchSelection, again[1].LaunchSelection)
	}
}

func TestLaunchSelectionIsRefusedWhereNoLaunchCouldHandIt(t *testing.T) {
	each := func() *LaunchSelection {
		return &LaunchSelection{Surface: ".x/config.toml", Each: []string{"-c", "{key}={value}"}}
	}
	program := func(ls *LaunchSelection) Contribution {
		return Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "p", LaunchSelection: ls}
	}
	with := func(edit func(*LaunchSelection)) Contribution {
		ls := each()
		edit(ls)
		return program(ls)
	}
	flag := func(key string, argv ...string) LaunchSelectionFlag { return LaunchSelectionFlag{Key: key, Argv: argv} }
	cases := []struct {
		name string
		c    Contribution
		want string
	}{
		{"a non-program kind", Contribution{Kind: KindSkills, From: "skills", Into: ".x/skills",
			LaunchSelection: each()}, `does not take "launch_selection"`},
		{"no surface", with(func(ls *LaunchSelection) { ls.Surface = "" }), `"surface" is empty`},
		{"a tilde surface", with(func(ls *LaunchSelection) { ls.Surface = "~/.x/config.toml" }), "not home-relative"},
		{"no form", with(func(ls *LaunchSelection) { ls.Each = nil }), "names no way to hand the selection"},
		{"two forms", with(func(ls *LaunchSelection) { ls.Env = "X_CONFIG" }), "more than one way"},
		{"each never names the leaf", with(func(ls *LaunchSelection) { ls.Each = []string{"-c", "{value}"} }),
			`"{key}"`},
		{"each never carries the value", with(func(ls *LaunchSelection) { ls.Each = []string{"-c", "{key}"} }),
			`"{value}"`},
		{"an empty each word", with(func(ls *LaunchSelection) { ls.Each = []string{"", "{key}={value}"} }),
			"empty word"},
		{"a flag with no value", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{flag("defaultProvider", "--provider")}
		}), `"flags"[0].argv must carry the value`},
		{"a flag with no key", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{flag("", "--provider", "{value}")}
		}), `names no "key"`},
		{"a key flagged twice", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{flag("m", "--m", "{value}"), flag("m", "--n", "{value}")}
		}), "a second time"},
		{"a flag with no argv", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{{Key: "m"}}
		}), `has no "argv"`},
		{"a flag requirement with no declared key", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{
				flag("p", "--provider", "{value}"),
				{Key: "p", Requires: []string{"missing"}, Argv: []string{"--other", "{value}"}},
			}
		}), `requires key "missing", which no "flags" entry names`},
		{"an empty flag requirement", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{
				{Key: "p", Requires: []string{""}, Argv: []string{"--provider", "{value}"}},
			}
		}), `has an empty "requires" key`},
		{"a repeated flag requirement", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{
				flag("p", "--provider", "{value}"),
				{Key: "m", Requires: []string{"p", "p"}, Argv: []string{"--model", "{value}"}},
			}
		}), `requires key "p" a second time`},
		{"a bad env name", with(func(ls *LaunchSelection) { ls.Each, ls.Env = nil, "X-CONFIG" }), "not a variable name"},
		{"rows with no table", with(func(ls *LaunchSelection) {
			ls.Rows = &LaunchSelectionRows{NamedBy: []string{"model_provider"}}
		}), `names no "table"`},
		{"rows named by nothing", with(func(ls *LaunchSelection) {
			ls.Rows = &LaunchSelectionRows{Table: "model_providers"}
		}), `no "named_by" key`},
		{"rows by flags", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{flag("p", "--provider", "{value}")}
			ls.Rows = &LaunchSelectionRows{Table: "providers", NamedBy: []string{"p"}}
		}), `cannot be handed by "flags"`},
		{"an empty default", with(func(ls *LaunchSelection) { ls.Defaults = map[string]string{"model_provider": ""} }),
			"an empty value"},
		{"a default no flag hands", with(func(ls *LaunchSelection) {
			ls.Each, ls.Flags = nil, []LaunchSelectionFlag{flag("p", "--provider", "{value}")}
			ls.Defaults = map[string]string{"m": "x"}
		}), `which no "flags" entry hands`},
		{"a surface that is the selection's own", with(func(ls *LaunchSelection) {
			ls.Surfaces = map[string]string{".x/config.toml": "X_CONFIG"}
		}), "the selection's own surface"},
		{"an escaping surface", with(func(ls *LaunchSelection) { ls.Surfaces = map[string]string{"../x.json": "X"} }),
			"leaves the home"},
		{"a surface in a bad variable", with(func(ls *LaunchSelection) {
			ls.Surfaces = map[string]string{".x/list.json": "1X"}
		}), "not a variable name"},
		{"two surfaces in one variable", with(func(ls *LaunchSelection) {
			ls.Surfaces = map[string]string{".x/a.json": "X_LIST", ".x/b.json": "X_LIST"}
		}), "already uses"},
		{"a surface in the env form's variable", with(func(ls *LaunchSelection) {
			ls.Each, ls.Env = nil, "X_CONFIG"
			ls.Surfaces = map[string]string{".x/a.json": "X_CONFIG"}
		}), `which "env" already uses`},
		{"subcommands for the env form", with(func(ls *LaunchSelection) {
			ls.Each, ls.Env, ls.Subcommands = nil, "X_CONFIG", []string{"run"}
		}), `"subcommands" are for an argv form`},
		{"an empty subcommand", with(func(ls *LaunchSelection) { ls.Subcommands = []string{""} }),
			`"subcommands"[0] is empty`},
		{"a flag as a subcommand", with(func(ls *LaunchSelection) { ls.Subcommands = []string{"--help"} }),
			"is a flag"},
		{"a spaced subcommand", with(func(ls *LaunchSelection) { ls.Subcommands = []string{"mcp add"} }),
			"holds a space"},
		{"a subcommand twice", with(func(ls *LaunchSelection) { ls.Subcommands = []string{"update", "update"} }),
			`names "update" a second time`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if probs := validateContribution("c[0]", tc.c); !containsSubstr(probs, tc.want) {
				t.Errorf("problems = %v, want one saying %q", probs, tc.want)
			}
		})
	}
	for name, ls := range map[string]*LaunchSelection{
		"each": each(),
		"flags": {Surface: ".x/settings.json", Flags: []LaunchSelectionFlag{flag("p", "--provider", "{value}")},
			Defaults: map[string]string{"p": "x"}, Surfaces: map[string]string{".x/list.json": "X_LIST"},
			Subcommands: []string{"update", "install"}},
		"env": {Surface: ".x/config.json", Env: "X_CONFIG",
			Rows: &LaunchSelectionRows{Table: "provider", NamedBy: []string{"enabled_providers"}}},
	} {
		if probs := validateContribution("c[0]", program(ls)); len(probs) != 0 {
			t.Errorf("a whole %s declaration on a program is refused: %v", name, probs)
		}
	}
}

// A READER OLDER THAN THE FIELD IGNORES IT: the tolerant decoder a staged jail boots through keeps
// the program and drops nothing for the field it does not know, which is what lets the shipped
// packs carry it to an older jail (packs/releasedecode_test.go runs the last release's reader).
// A malformed declaration is still a problem on the tolerant path, which both ends understand.
func TestLaunchSelectionKeepsTheProgramOnTheTolerantPath(t *testing.T) {
	m, problems, skipped := DecodeTolerant([]byte(`{"contributes":[{"kind":"program","bin":"codex",
	  "via":"npm","package":"codex","launch_selection":` + validLaunchSelection + `}]}`))
	if len(problems) != 0 || len(skipped) != 0 {
		t.Fatalf("problems=%v skipped=%v", problems, skipped)
	}
	if ins := m.InstallContributions(); len(ins) != 1 || ins[0].LaunchSelection == nil {
		t.Fatalf("installs = %+v, want codex with its launch selection", ins)
	}
	_, problems, _ = DecodeTolerant([]byte(`{"contributes":[{"kind":"program","bin":"codex",
	  "via":"npm","package":"codex","launch_selection":{"surface":".x/config.toml"}}]}`))
	if !containsSubstr(problems, "names no way to hand the selection") {
		t.Errorf("a declaration with no form passed the tolerant path: %v", problems)
	}
}

// A SUBCOMMAND IS argv[1] AS TYPED, and only a declared word: the launch compares the user's own
// argv[1] with the declaration, so a later word, a flag before it, a prompt that merely starts
// with the word, and a declaration naming none take no subcommand.
func TestLaunchSelectionTakesASubcommandOnlyAsArgvOne(t *testing.T) {
	ls := &LaunchSelection{Subcommands: []string{"update", "auth"}}
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"pi", "update"}, true},
		{[]string{"/usr/local/bin/pi", "auth", "check"}, true},
		{[]string{"pi"}, false},
		{[]string{"pi", "--continue", "update"}, false},
		{[]string{"pi", "update the readme"}, false},
		{[]string{"pi", "install"}, false},
	} {
		if got := ls.TakesSubcommand(tc.argv); got != tc.want {
			t.Errorf("TakesSubcommand(%q) = %v, want %v", tc.argv, got, tc.want)
		}
	}
	if (&LaunchSelection{}).TakesSubcommand([]string{"pi", "update"}) || (*LaunchSelection)(nil).TakesSubcommand([]string{"pi", "update"}) {
		t.Error("a declaration naming no subcommand took one")
	}
}
