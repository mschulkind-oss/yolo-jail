package packdecl

// envoverride_test.go pins the `overridden_by` SCHEMA: what decodes, what is refused and
// with what words, and the two projections the evaluator reads (EnvOverrideContributions,
// EnvOverride.Covers). The evaluation itself is packload's and is pinned there.
//
// The variable names below are MADE UP on purpose. The declaration is generic — core knows
// no AWS variable — so a test that spelled AWS names would read as if they were special.

import (
	"strings"
	"testing"
)

// overrideManifest wraps one env contribution's `overridden_by` list in a manifest.
func overrideManifest(overrides string) string {
	return `{"name": "p", "contributes": [
	  {"kind": "env", "profile": "gate",
	   "vars": {"WIDGET_POINTER": "http://127.0.0.1:1/x"},
	   "overridden_by": ` + overrides + `}
	]}`
}

// TestEnvOverrideDecodesAndProjects is the happy path: both condition forms decode with no
// problem, and the projection carries the contribution's variable NAMES (never a value),
// its gate, and the declarations in manifest order.
func TestEnvOverrideDecodesAndProjects(t *testing.T) {
	m, problems := Decode([]byte(overrideManifest(`[
	  {"vars": ["WIDGET_TOKEN"], "because": "the widget client reads the token first"},
	  {"vars": ["WIDGET_ID", "WIDGET_SECRET"], "unless": ["WIDGET_PROFILE"],
	   "because": "the static pair answers before the pointer"},
	  {"host_file": ".widget", "because": "the widget config dir answers first"}
	]`)))
	if len(problems) > 0 {
		t.Fatalf("a well-formed declaration was refused: %v", problems)
	}
	decls := m.EnvOverrideContributions()
	if len(decls) != 1 {
		t.Fatalf("EnvOverrideContributions() = %d decls, want 1: %+v", len(decls), decls)
	}
	d := decls[0]
	if len(d.Sets) != 1 || d.Sets[0] != "WIDGET_POINTER" {
		t.Errorf("Sets = %v, want the contribution's one variable name", d.Sets)
	}
	if d.Profile != "gate" {
		t.Errorf("Profile = %q, want the contribution's gate", d.Profile)
	}
	if len(d.Overrides) != 3 || d.Overrides[1].Unless[0] != "WIDGET_PROFILE" ||
		d.Overrides[2].HostFile != ".widget" {
		t.Errorf("Overrides did not round-trip in order: %+v", d.Overrides)
	}
}

// TestEnvOverrideContributionsSkipsUndeclaringEnv: an env contribution without the key is
// not a declaration, and the projection must not hand the evaluator an empty one to walk.
func TestEnvOverrideContributionsSkipsUndeclaringEnv(t *testing.T) {
	m, problems := Decode([]byte(`{"name": "p", "contributes": [
	  {"kind": "env", "vars": {"PLAIN": "1"}}
	]}`))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if got := m.EnvOverrideContributions(); len(got) != 0 {
		t.Errorf("an env contribution declaring no overrides was projected: %+v", got)
	}
}

// TestEnvOverrideRefusals: every declaration that would be accepted and then do nothing,
// or refuse every launch, is refused at decode with a message naming the rule.
func TestEnvOverrideRefusals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides string
		want      string
	}{
		{"no because", `[{"vars": ["WIDGET_TOKEN"]}]`, `needs "because"`},
		{"blank because", `[{"vars": ["WIDGET_TOKEN"], "because": "  "}]`, `needs "because"`},
		{"no condition", `[{"because": "x"}]`, `needs "vars" or "host_file"`},
		{"both conditions", `[{"vars": ["A"], "host_file": ".a", "because": "x"}]`,
			`names both "vars" and "host_file"`},
		{"unless alone", `[{"host_file": ".a", "unless": ["B"], "because": "x"}]`,
			`"unless" needs "vars"`},
		{"self override", `[{"vars": ["WIDGET_POINTER"], "because": "x"}]`,
			"would override itself"},
		{"unless it always has", `[{"vars": ["A"], "unless": ["WIDGET_POINTER"], "because": "x"}]`,
			"could never fire"},
		{"unless also required", `[{"vars": ["A", "B"], "unless": ["B"], "because": "x"}]`,
			`also in "vars"`},
		{"empty name", `[{"vars": ["A", ""], "because": "x"}]`, "empty variable name"},
		{"repeated name", `[{"vars": ["A", "A"], "because": "x"}]`, "listed twice"},
		{"tilde path", `[{"host_file": "~/.a", "because": "x"}]`, `without "~/"`},
		{"absolute path", `[{"host_file": "/etc/a", "because": "x"}]`, "must be relative"},
		{"escaping path", `[{"host_file": "../a", "because": "x"}]`, `".."`},
		{"unclean path", `[{"host_file": "./.a", "because": "x"}]`, "clean home-relative"},
		{"trailing slash", `[{"host_file": ".a/", "because": "x"}]`, "clean home-relative"},
		{"the home itself", `[{"host_file": ".", "because": "x"}]`, "clean home-relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(overrideManifest(tc.overrides)))
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tc.want, joined)
			}
			if !strings.Contains(joined, "contributes[0].overridden_by[0]") {
				t.Errorf("the problem must name the entry it is about:\n%s", joined)
			}
		})
	}
}

// TestEnvOverrideRefusedOnOtherKinds: the key is env's alone, for validateContribution's
// standing reason — on any other kind it would be accepted and read by nothing.
func TestEnvOverrideRefusedOnOtherKinds(t *testing.T) {
	_, problems := Decode([]byte(`{"name": "p", "contributes": [
	  {"kind": "state", "at": ".p-state",
	   "overridden_by": [{"vars": ["A"], "because": "x"}]}
	]}`))
	if !strings.Contains(strings.Join(problems, "\n"), `does not take "overridden_by"`) {
		t.Errorf("overridden_by on a state contribution was accepted: %v", problems)
	}
}

// TestEnvOverrideIsStrictlyDecoded: a misspelled key inside an entry is an unknown field,
// not a silently absent condition — the whole reason the authoring decoder is strict.
func TestEnvOverrideIsStrictlyDecoded(t *testing.T) {
	_, problems := Decode([]byte(overrideManifest(
		`[{"vars": ["A"], "unles": ["B"], "because": "x"}]`)))
	if !strings.Contains(strings.Join(problems, "\n"), `unknown field "unles"`) {
		t.Errorf("a misspelled key inside overridden_by was accepted: %v", problems)
	}
}

// TestEnvOverrideCovers: the host_file match is the directory and what is inside it, and
// never a sibling that shares its letters — refusing a launch over `.awsfoo` would be the
// false positive the ruling forbids.
func TestEnvOverrideCovers(t *testing.T) {
	o := EnvOverride{HostFile: ".widget"}
	for _, dest := range []string{".widget", ".widget/config", ".widget/a/b", "~/.widget", "./.widget/x"} {
		if !o.Covers(dest) {
			t.Errorf("Covers(%q) = false, want true", dest)
		}
	}
	for _, dest := range []string{"", ".widgetfoo", ".widget-backup", "widget", ".config/.widget", "a/.widget"} {
		if o.Covers(dest) {
			t.Errorf("Covers(%q) = true — not under ~/.widget", dest)
		}
	}
	if (EnvOverride{Vars: []string{"A"}}).Covers(".widget") {
		t.Error("a vars entry claimed a host_files destination")
	}
}
