package packdecl

import (
	"strings"
	"testing"
)

// hookfields_test.go pins the hook contract the HOST validates.
//
// Until 2026-09-21 the `hook` case checked the NAME and nothing else, so a declaration
// missing the path it acts on passed `yolo check`, reached the jail, and failed at boot
// through badHookError — the host-validates/jail-breaks direction TestHookSetsAgree's own
// comment calls "silent in the worst direction", arrived at by a different route. The
// required set is data (hookRequiredFields), because it is each hook's contract rather than
// the kind's.

// TestAHookMissingARequiredFieldIsRefusedOnTheHost walks the required set itself, so a hook
// added to the map without a matching refusal is caught here rather than in a jail.
func TestAHookMissingARequiredFieldIsRefusedOnTheHost(t *testing.T) {
	for hook, required := range hookRequiredFields {
		for _, missing := range required {
			t.Run(hook+" without "+missing, func(t *testing.T) {
				// Every required field but the one under test, so the refusal can only be
				// about that one.
				fields := []string{`"kind":"hook"`, `"hook":"` + hook + `"`}
				for _, f := range required {
					if f == missing {
						continue
					}
					fields = append(fields, `"`+f+`":".x/thing"`)
				}
				manifest := `{"name":"x","contributes":[
					{"kind":"state","at":".x-shared","scope":"machine","because":"the shared tier"},
					{` + strings.Join(fields, ",") + `}]}`

				_, problems := Decode([]byte(manifest))
				if len(problems) == 0 {
					t.Fatalf("hook %q without %q was accepted on the host — it validates "+
						"here and then fails at boot", hook, missing)
				}
				joined := strings.Join(problems, "\n")
				for _, want := range []string{hook, `"` + missing + `"`, "contributes[1]"} {
					if !strings.Contains(joined, want) {
						t.Errorf("the refusal does not name %q:\n%s", want, joined)
					}
				}
			})
		}
	}
}

// TestEveryRequiredFieldNameIsOneThisSchemaHas: a typo in hookRequiredFields would silently
// require nothing at all, because the validator switches on the field name. The map's keys
// must likewise be hooks that exist.
func TestEveryRequiredFieldNameIsOneThisSchemaHas(t *testing.T) {
	known := map[string]bool{}
	for _, k := range KnownHooks {
		known[k] = true
	}
	for hook, required := range hookRequiredFields {
		if !known[hook] {
			t.Errorf("hookRequiredFields names %q, which is not a known hook — nothing "+
				"requires anything of it", hook)
		}
		for _, f := range required {
			switch f {
			case "from", "at":
			default:
				t.Errorf("hook %q requires %q, which the validator's switch does not "+
					"handle, so it requires nothing", hook, f)
			}
		}
	}
}

// TestTheSharedDirectoryHookValidatesAndAdapts is the new hook's whole host-side contract:
// it is in the closed set, a complete declaration passes BOTH decode paths, and the
// contribution adapts onto the Hook the entrypoint dispatches — `from` to File (the path it
// acts on) and `at` to SharedDir (the machine-scope dir it links into). The adaptation is
// where a plausible-looking manifest key would silently do nothing.
func TestTheSharedDirectoryHookValidatesAndAdapts(t *testing.T) {
	const manifest = `{"name":"x","contributes":[
		{"kind":"state","at":".x-shared-npm","scope":"machine","because":"one store per machine"},
		{"kind":"hook","hook":"shared_directory","from":".x/agent/npm","at":".x-shared-npm"}]}`

	m, problems := Decode([]byte(manifest))
	if len(problems) != 0 {
		t.Fatalf("a complete shared_directory declaration was refused: %v", problems)
	}
	if _, problems, skipped := DecodeTolerant([]byte(manifest)); len(problems) != 0 || len(skipped) != 0 {
		t.Fatalf("the tolerant path refused or skipped it: %v / %v", problems, skipped)
	}
	hooks := m.HookContributions()
	if len(hooks) != 1 {
		t.Fatalf("HookContributions() = %d, want the one hook", len(hooks))
	}
	if got := hooks[0]; got.Name != "shared_directory" ||
		got.File != ".x/agent/npm" || got.SharedDir != ".x-shared-npm" {
		t.Errorf("adapted hook = %+v, want from->File and at->SharedDir", got)
	}
	// And the declaration it links into is readable as the machine tier from the same
	// manifest — which is what the entrypoint's refusal of an undeclared shared dir rests on.
	if got := m.SharedDirContributions(); len(got) != 1 || got[0] != ".x-shared-npm" {
		t.Errorf("SharedDirContributions() = %v, want the declared machine-scope dir", got)
	}
}
