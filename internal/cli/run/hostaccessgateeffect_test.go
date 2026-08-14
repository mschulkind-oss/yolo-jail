package run

// hostaccessgateeffect_test.go is the BEHAVIOURAL half of the invariant
// packload/hostaccessgates_test.go pins statically, and it exists because that AST scan has a
// hole a real change can walk through.
//
// # What the static test pins, and what it does not
//
// It asserts that each gate CALLS packload.Pack.HostAccessClaims and names no producer
// directly. That catches the drift it was written for (two hand-built unions, a producer added
// to one). It does NOT catch a POST-HOC FILTER — inserting
//
//	for _, c := range want { if !strings.HasPrefix(c, "loophole ") { keep(c) } }
//
// AFTER the merged call leaves the scan satisfied and (measured) left the whole `-short` suite
// green, while every loophole crossing became unprompted. The invariant the AST expresses is
// "the gate calls the helper"; the invariant that matters is "the gate ACTS ON THE WHOLE SET".
//
// # Why it cannot be closed statically, and what this does instead
//
// Any AST rule strong enough to see the filter has to reason about what happens to the VALUE
// the helper returned — which is dataflow, not a call scan, and a rule shaped like "no `range`
// over `want`" or "no `strings.HasPrefix` in this function" would forbid ordinary code and
// break the moment the gate is legitimately refactored. That is the brittleness the AST test's
// own doc warns about.
//
// So the property is asserted by EFFECT, per producer: a fetched pack whose ONLY host-access
// claim comes from producer X must be REFUSED when the lockfile does not hold that claim and
// GRANTED when it does. A filter that drops X's claims makes the refusal case pass (the pack
// arrives at `len(want) == 0` and is granted with no prompt) and this test fails. One case per
// producer, so a filter aimed at any one of the three is caught, and a fourth producer added
// to the helper without a row here is caught too — TestEveryProducerHasAGateEffectCase.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// gateEffectCase is one producer, the pack tree that makes it the SOLE claim source, and a
// fragment of the claim string it produces.
type gateEffectCase struct {
	// producer names the packload method under test, matching hostaccessgates_test.go's list
	// so a producer added there without a case here is visible.
	producer string
	// write lays out a pack tree whose only host-access claim comes from this producer.
	write func(t *testing.T, root string)
	// fragment identifies the claim in the merged set, for the lockfile-approval half.
	fragment string
}

func gateEffectCases() []gateEffectCase {
	return []gateEffectCase{{
		producer: "HostAccessClaims", // pack.json's own contributions (packdecl.Manifest)
		write: func(t *testing.T, root string) {
			writePackJSON(t, root, `{"contributes":[{"kind":"reads-host","host":".netrc"}]}`)
		},
		fragment: "reads-host",
	}, {
		producer: "PluginHostAccessClaims", // a wrapped plugin's code-running components
		write: func(t *testing.T, root string) {
			// A plugin declaring a HOOK: the component that makes a plugin run code.
			dir := filepath.Join(root, "skills", "acme-plugin", ".claude-plugin")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "plugin.json"),
				[]byte(`{"name":"acme-plugin","hooks":{"PreToolUse":[]}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			writePackJSON(t, root,
				`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
		},
		fragment: "acme-plugin",
	}, {
		producer: "LoopholeHostAccessClaims", // a shipped loophole's daemon/intercepts/binds/devices
		write: func(t *testing.T, root string) {
			mod := filepath.Join(root, "loopholes", "acme-proxy")
			if err := os.MkdirAll(mod, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(
				`{"name":"acme-proxy","transport":"none","host_devices":["/dev/snd"]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			writePackJSON(t, root,
				`{"contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"}]}`)
		},
		fragment: "loophole acme-proxy",
	}}
}

func writePackJSON(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLaunchGateActsOnEveryProducersClaims is the assertion the AST scan cannot make: each
// producer's claims REACH THE GATE'S DECISION.
//
// The refusal half is the one that catches a post-hoc filter. A dropped claim does not make the
// gate stricter — it makes it grant, because `len(want) == 0` is read as "reads nothing from
// the host, runs nothing on it; the gate is moot".
func TestLaunchGateActsOnEveryProducersClaims(t *testing.T) {
	for _, tc := range gateEffectCases() {
		t.Run(tc.producer, func(t *testing.T) {
			root := t.TempDir()
			tc.write(t, root)
			fetched := config.PackEntry{Name: "acme", Source: "git+ssh://h/o/r//p?ref=v1"}

			// Precondition: this tree's claims are exactly the ones this producer makes, so a
			// pass cannot come from some other producer keeping the set non-empty.
			p, probs := packload.LoadDir(root, "acme", false)
			if len(probs) > 0 || p == nil {
				t.Fatalf("fixture does not load: %v", probs)
			}
			claims := p.HostAccessClaims()
			if len(claims) == 0 {
				t.Fatalf("the %s fixture produces no claims at all — it cannot exercise the "+
					"gate", tc.producer)
			}
			var matching int
			for _, c := range claims {
				if strings.Contains(c, tc.fragment) {
					matching++
				}
			}
			if matching != len(claims) {
				t.Fatalf("the %s fixture also claims through another producer (%v) — the case "+
					"has to isolate one", tc.producer, claims)
			}

			// REFUSED with no approval. A filter that drops this producer's claims lands the
			// pack on the len(want)==0 branch and this flips to granted.
			if packMayAccessHost(fetched, root, nil) {
				t.Errorf("a fetched pack whose ONLY host access comes from %s was GRANTED with "+
					"no lockfile approval. Either the gate stopped reading that producer, or "+
					"something dropped its claims after the merged helper returned them — which "+
					"the AST scan in packload cannot see, because the call is still there", tc.producer)
			}

			// GRANTED once the lockfile holds exactly what the helper returns. This half fails
			// if the gate demands MORE than the prompt records, the other drift direction.
			approved := &packsrc.Lock{Schema: packsrc.LockSchema,
				Packs: map[string]packsrc.LockEntry{
					"acme": {Name: "acme", ApprovedHostAccess: claims},
				}}
			if !packMayAccessHost(fetched, root, approved) {
				t.Errorf("a fetched pack whose %s claims are ALL approved was still refused — "+
					"the gate is demanding something `pack install` would never have shown, so "+
					"there is no route to approving this pack", tc.producer)
			}
		})
	}
}

// The case list must cover every producer the merged helper unions, or a new producer is
// protected by the AST scan (which sees the call) and by nothing that sees its EFFECT.
//
// It reads the producer names off packload's own source rather than duplicating the list,
// because a second copy of a list is what drifted in the first place.
func TestEveryProducerHasAGateEffectCase(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "packload", "hostaccess.go"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, tc := range gateEffectCases() {
		covered[tc.producer] = true
	}
	for _, producer := range []string{
		"HostAccessClaims", "PluginHostAccessClaims", "LoopholeHostAccessClaims",
	} {
		if !strings.Contains(string(data), producer+"()") {
			t.Errorf("producer %q is no longer merged by packload.Pack.HostAccessClaims — if it "+
				"was renamed, rename it here too rather than deleting the case", producer)
		}
		if !covered[producer] {
			t.Errorf("producer %q has no gate-effect case, so nothing asserts its claims reach "+
				"the launch gate's DECISION — the AST scan only sees that the merged helper is "+
				"called", producer)
		}
	}
	// And the reverse: the helper must not have grown a producer nobody covers. Counted off
	// the union's own body, so a fourth line there fails here.
	appends := strings.Count(string(data), "out = append(out, p.")
	if appends != len(gateEffectCases()) {
		t.Errorf("packload.Pack.HostAccessClaims unions %d producers but there are %d "+
			"gate-effect cases — add a case for the new producer (a pack tree whose ONLY "+
			"host-access claim comes from it) so its claims are known to reach the gate",
			appends, len(gateEffectCases()))
	}
}
