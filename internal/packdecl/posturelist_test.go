package packdecl

// posturelist_test.go pins the POSTURE LIST's declaration — a `config-list` body inside an
// autonomy posture, the term docs/design/notch-scoped-config-contributions.md §4.1 coins: the
// shape it decodes into, that config-list's own rules refuse a malformed one, and the one
// projection (ListContributions) that carries it to the collector in declaration order. Which
// notch places it is packoverlay's, and is tested there.

import (
	"encoding/json"
	"strings"
	"testing"
)

const automodeEntry = "npm:@czottmann/pi-automode@1.17.0"

// THE MOTIVATING CASE decodes clean — a posture holding ONLY lists is a valid posture — and
// reaches the projection tagged with its posture, at its autonomy contribution's position
// among the pack's plain config-lists. A field that decodes but never leaves the Contribution
// is an entry no fold can append, and a projection that lost the position would fold a
// posture's entries in an order the author did not write.
func TestPostureListsDecodeAndTravelInDeclarationOrder(t *testing.T) {
	m, problems := Decode([]byte(`{"name": "matt", "contributes": [
		{"kind": "config-list", "surface": "pi/settings", "path": "/packages", "add": ["npm:first"]},
		{"kind": "autonomy",
		 "autonomous": {"lists": [{"surface": "pi/settings", "path": "/packages", "add": ["npm:jail-only"]}]},
		 "guarded": {"lists": [{"surface": "pi/settings", "path": "/packages", "add": ["` + automodeEntry + `"]}]}},
		{"kind": "config-list", "surface": "pi/settings", "path": "/other", "add": []}
	]}`))
	if len(problems) != 0 {
		t.Fatalf("a well-formed posture list was refused: %v", problems)
	}
	if g := m.PostureFor(false); g == nil || len(g.Lists) != 1 || g.Lists[0].Path != "/packages" {
		t.Fatalf("the guarded posture does not carry its list: %+v", g)
	}

	type row struct {
		posture Posture
		path    string
		entry   string
	}
	var got []row
	for _, cl := range m.ListContributions() {
		var add []string
		_ = json.Unmarshal(cl.Add, &add)
		entry := ""
		if len(add) > 0 {
			entry = add[0]
		}
		got = append(got, row{cl.Posture, cl.Path, entry})
	}
	want := []row{
		{"", "/packages", "npm:first"},
		{PostureAutonomous, "/packages", "npm:jail-only"},
		{PostureGuarded, "/packages", automodeEntry},
		{"", "/other", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("ListContributions() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListContributions()[%d] = %+v, want %+v — declaration order is fold order, "+
				"and a posture's lists stand at the autonomy contribution's position", i, got[i], want[i])
		}
	}

	// ConfigListContributions stays the config-list KIND's projection: the footprint's
	// config-list claims read it, and a posture list is declared under kind autonomy.
	for _, cl := range m.ConfigListContributions() {
		if cl.Posture != "" {
			t.Errorf("a posture list leaked into ConfigListContributions: %+v", cl)
		}
	}
	if n := len(m.ConfigListContributions()); n != 2 {
		t.Errorf("ConfigListContributions() = %d entries, want the 2 config-list contributions", n)
	}
}

// Every refusal config-list's rules make, made of a posture list too, each naming the
// posture list's own position so an author can find it. The last two are the strict decoder's:
// a misspelled field inside an entry is refused on the host rather than decoded to an entry
// that silently renders nothing.
func TestPostureListRefusals(t *testing.T) {
	const at = "contributes[0].guarded.lists[0]"
	cases := []struct {
		name, list string
		want       []string
	}{
		{"missing surface", `{"path":"/packages","add":["x"]}`, []string{at, `needs "surface"`}},
		{"missing path", `{"surface":"pi/settings","add":["x"]}`, []string{at, `needs "path"`}},
		{"dotted path", `{"surface":"pi/settings","path":"packages","add":["x"]}`,
			[]string{at + ".path", "RFC 6901", `"/packages"`}},
		{"root-key pointer", `{"surface":"pi/settings","path":"/","add":["x"]}`,
			[]string{at + ".path", "empty"}},
		{"missing add", `{"surface":"pi/settings","path":"/packages"}`, []string{at, `needs "add"`}},
		{"scalar add", `{"surface":"pi/settings","path":"/packages","add":"x"}`,
			[]string{at + ".add", "array", "string"}},
		{"null entry", `{"surface":"pi/settings","path":"/packages","add":["x",null]}`,
			[]string{at + ".add[1]", "null", "TOML"}},
		{"misspelled field", `{"surface":"pi/settings","path":"/packages","adds":["x"]}`,
			[]string{`unknown field "adds"`}},
		{"config on a list", `{"surface":"pi/settings","path":"/packages","add":["x"],"config":{}}`,
			[]string{`unknown field "config"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(`{"name":"p","contributes":[` +
				`{"kind":"autonomy","guarded":{"lists":[` + tc.list + `]}}]}`))
			if len(problems) == 0 {
				t.Fatalf("posture list %s was accepted", tc.list)
			}
			joined := strings.Join(problems, "\n")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("problems do not mention %q:\n%s", w, joined)
				}
			}
		})
	}
}

// An EMPTY add is a declared no-op, exactly as it is on a config-list, and must stay
// distinguishable from an absent one (refused above) all the way to the collector.
func TestPostureListEmptyAddIsANoOp(t *testing.T) {
	m, problems := Decode([]byte(`{"name":"p","contributes":[{"kind":"autonomy",` +
		`"guarded":{"lists":[{"surface":"pi/settings","path":"/packages","add":[]}]}}]}`))
	if len(problems) != 0 {
		t.Fatalf("an empty add was refused: %v", problems)
	}
	if got := m.ListContributions(); len(got) != 1 || string(got[0].Add) != "[]" {
		t.Errorf("ListContributions() = %+v, want the one list with add []", got)
	}
}

// ACROSS THE VERSION BOUNDARY. A well-formed posture list decodes on the tolerant path with no
// problem, and a malformed one is refused there too — both builds understand a list with no
// "add". And the property the design's skew row rests on (§4.5): a posture field THIS build
// does not know is dropped silently by the tolerant decoder, never a problem and never a skip.
// That is what an entrypoint older than `lists` does with `lists` — the entry renders nowhere,
// which fails closed.
func TestPostureListTolerantPath(t *testing.T) {
	good := `{"name":"p","contributes":[{"kind":"autonomy",` +
		`"guarded":{"lists":[{"surface":"pi/settings","path":"/packages","add":["x"]}]}}]}`
	m, problems, skipped := DecodeTolerant([]byte(good))
	if len(problems) != 0 || len(skipped) != 0 {
		t.Fatalf("tolerant decode of a valid posture list: problems=%v skipped=%v", problems, skipped)
	}
	if got := m.ListContributions(); len(got) != 1 || got[0].Posture != PostureGuarded {
		t.Errorf("tolerant projection = %+v", got)
	}
	bad := `{"name":"p","contributes":[{"kind":"autonomy",` +
		`"guarded":{"lists":[{"surface":"pi/settings","path":"/packages"}]}}]}`
	if _, problems, _ := DecodeTolerant([]byte(bad)); len(problems) == 0 {
		t.Error("tolerant decode accepted a posture list with no \"add\"")
	}

	newer := `{"name":"p","contributes":[{"kind":"autonomy",` +
		`"guarded":{"lists_from_a_newer_build":[{"surface":"pi/settings"}]}}]}`
	m, problems, skipped = DecodeTolerant([]byte(newer))
	if len(problems) != 0 || len(skipped) != 0 {
		t.Fatalf("an unknown posture field must be dropped silently across the version "+
			"boundary: problems=%v skipped=%v", problems, skipped)
	}
	if got := m.ListContributions(); len(got) != 0 {
		t.Errorf("an unknown posture field produced lists: %+v", got)
	}
}

// PostureOf is PostureFor's selection as a value, and the two must agree: the collector gates
// on the first and every other posture reader selects by the second.
func TestPostureOfAgreesWithPostureFor(t *testing.T) {
	m, problems := Decode([]byte(`{"name":"p","contributes":[{"kind":"autonomy",` +
		`"autonomous":{"lists":[{"surface":"a/b","path":"/x","add":["jail"]}]},` +
		`"guarded":{"lists":[{"surface":"a/b","path":"/x","add":["host"]}]}}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	for _, autonomy := range []bool{true, false} {
		posture := m.PostureFor(autonomy)
		var fromWalk []string
		for _, cl := range m.ListContributions() {
			if cl.Posture == PostureOf(autonomy) {
				fromWalk = append(fromWalk, string(cl.Add))
			}
		}
		if len(fromWalk) != 1 || fromWalk[0] != string(posture.Lists[0].Add) {
			t.Errorf("autonomy=%v: PostureOf selects %v, PostureFor selects %s", autonomy,
				fromWalk, posture.Lists[0].Add)
		}
	}
}
