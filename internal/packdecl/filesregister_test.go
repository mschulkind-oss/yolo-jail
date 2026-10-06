package packdecl

// filesregister_test.go pins where `register` and `expects` decode: on a `files` slot and nowhere
// else, with config-list's pointer rules and one template token (docs/design/pack-pi-resources.md
// §3.1).

import (
	"strings"
	"testing"
)

func decodeSlot(t *testing.T, contribution string) (*Manifest, []string) {
	t.Helper()
	return Decode([]byte(`{"name":"x","contributes":[` + contribution + `]}`))
}

func TestARegisteringSlotDecodes(t *testing.T) {
	for _, c := range []string{
		`{"kind":"files","agent":"pi","into":".pi/agent/yolo-packs",
		  "register":{"surface":"pi/settings","path":"/packages"}}`,
		`{"kind":"files","agent":"pi","into":".pi/agent/yolo-packs",
		  "register":{"surface":"pi/settings","path":"/packages","entry":"~/{landing}/"},
		  "expects":["extensions","package.json"]}`,
	} {
		m, problems := decodeSlot(t, c)
		if len(problems) != 0 {
			t.Errorf("%s: %v", c, problems)
			continue
		}
		if m.Contributes[0].Register == nil {
			t.Errorf("%s: register was not decoded", c)
		}
	}
}

func TestRegisterAndExpectsAreRefusedOffASlot(t *testing.T) {
	for _, c := range []string{
		// an addressed tree: content, not a slot
		`{"kind":"files","agents":["pi"],"from":"pi","register":{"surface":"pi/settings","path":"/packages"}}`,
		`{"kind":"files","agents":["pi"],"from":"pi","expects":["extensions"]}`,
		// a pack's own tree at a path
		`{"kind":"files","from":"t","into":".x/t","register":{"surface":"pi/settings","path":"/packages"}}`,
		// another kind's destination
		`{"kind":"skills","agent":"pi","into":".pi/agent/skills","expects":["x"]}`,
		`{"kind":"env","vars":{"A":"1"},"register":{"surface":"pi/settings","path":"/packages"}}`,
	} {
		_, problems := decodeSlot(t, c)
		if !strings.Contains(strings.Join(problems, "\n"), "files SLOT's field") {
			t.Errorf("%s: want the slot-only refusal, got %v", c, problems)
		}
	}
}

func TestARegistrationIsCheckedLikeAConfigList(t *testing.T) {
	for _, tc := range []struct{ register, want string }{
		{`{"path":"/packages"}`, `needs "surface"`},
		{`{"surface":"pi/settings"}`, `needs "path"`},
		{`{"surface":"pi/settings","path":"packages"}`, `a pointer, not a dotted path`},
		{`{"surface":"pi/settings","path":"/packages/"}`, `empty key`},
		{`{"surface":"pi/settings","path":"/packages","entry":"~/fixed"}`, `names no {landing}`},
		{`{"surface":"pi/settings","path":"/packages","entry":"~/{landing}/{pack}"}`, `unknown token {pack}`},
	} {
		_, problems := decodeSlot(t, `{"kind":"files","agent":"pi","into":".pi/agent/yolo-packs","register":`+
			tc.register+`}`)
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("register %s: want a problem naming %q, got %v", tc.register, tc.want, problems)
		}
	}
	_, problems := decodeSlot(t, `{"kind":"files","agent":"pi","into":".pi/agent/yolo-packs","expects":["a/b"]}`)
	if !strings.Contains(strings.Join(problems, "\n"), "bare top-level name") {
		t.Errorf("an expects path was not refused: %v", problems)
	}
}

func TestEntryForSubstitutesTheLanding(t *testing.T) {
	r := FilesRegister{Surface: "pi/settings", Path: "/packages"}
	if got := r.EntryFor(".pi/agent/yolo-packs/matt"); got != "~/.pi/agent/yolo-packs/matt" {
		t.Errorf("default entry = %q", got)
	}
	r.Entry = "./{landing}"
	if got := r.EntryFor("a/b"); got != "./a/b" {
		t.Errorf("declared entry = %q", got)
	}
}
