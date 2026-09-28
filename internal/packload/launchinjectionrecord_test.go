package packload

import (
	"strings"
	"testing"
)

// The injection RECORD is what the launch disclosure prints, so its exactness is the
// disclosure's honesty. Three properties, each of which fails differently:
//
//   - `Flags` are the flags ACTUALLY ADDED, in the order they now appear — not the declared
//     list, which would name a flag the user had already typed and yolo therefore skipped;
//   - `Pack` names who declared them, which the merged map[string][]string cannot answer;
//   - a rewrite that added NOTHING returns nil, which is what lets the printer be silent
//     without deciding for itself what "nothing happened" means.
func TestTheInjectionRecordNamesWhatWasActuallyAdded(t *testing.T) {
	p := &Pack{Name: "acme", Decl: declFrom(t,
		`{"contributes":[{"kind":"autonomy","autonomous":{"launch":[`+
			`{"bin":"tool","flags":["--one","--two"]}]}}]}`)}
	packs := []*Pack{p}

	out, inj := InjectLaunchFlags(packs, true, []string{"tool", "sub"})
	if inj == nil {
		t.Fatal("a rewrite reported no injection record, so the launch discloses nothing")
	}
	if inj.Pack != "acme" {
		t.Errorf("Pack = %q, want %q — the disclosure cannot say who asked for the flags", inj.Pack, "acme")
	}
	if got := strings.Join(inj.Flags, " "); got != "--one --two" {
		t.Errorf("Flags = %q, want %q", got, "--one --two")
	}
	if got := strings.Join(inj.Before, " "); got != "tool sub" {
		t.Errorf("Before = %q, want the argv the user typed", got)
	}
	if got := strings.Join(inj.After, " "); got != strings.Join(out, " ") {
		t.Errorf("After = %q but the returned argv is %q — the disclosure would describe a "+
			"command line nothing runs", got, strings.Join(out, " "))
	}

	// One flag already typed: the record names the OTHER one only.
	_, inj = InjectLaunchFlags(packs, true, []string{"tool", "--one"})
	if inj == nil {
		t.Fatal("a partial rewrite reported no injection record")
	}
	if got := strings.Join(inj.Flags, " "); got != "--two" {
		t.Errorf("Flags = %q, want %q — a disclosure naming a flag it did not add is a "+
			"disclosure a reader learns to distrust", got, "--two")
	}

	// Every flag already typed: nothing changed, so there is nothing to disclose.
	if _, inj := InjectLaunchFlags(packs, true, []string{"tool", "--one", "--two"}); inj != nil {
		t.Errorf("an argv yolo did not change produced an injection record: %+v", inj)
	}
	if _, inj := InjectLaunchFlags(packs, true, []string{"other", "--one"}); inj != nil {
		t.Errorf("a binary no pack declares produced an injection record: %+v", inj)
	}
}
