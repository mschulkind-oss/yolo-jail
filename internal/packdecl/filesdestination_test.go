package packdecl

// filesdestination_test.go pins "one `files` destination per agent; a second is a load error" —
// pi-pack-extensions.md §8 invariant 1 / OQ-1, which shipped with the slot mechanism and was never
// implemented (measured 2026-09-21: a manifest declaring two loaded with zero problems).
//
// What the silence cost, and why the ruling asked for a load error rather than a report: the two
// notches picked DIFFERENTLY. The jail's alias table is a map keyed by agent, so the LAST
// declaration won and one addressed contribution landed in one slot; destination borrowing dedups
// by PATH, so at the host two paths were two destinations and the same contribution landed in
// BOTH. Neither said anything.

import (
	"strings"
	"testing"
)

// decodeManifest runs a whole `contributes` list through the strict authoring path.
func decodeManifest(t *testing.T, entries string) string {
	t.Helper()
	_, probs := Decode([]byte(`{"name":"acme","contributes":[` + entries + `]}`))
	return strings.Join(probs, "; ")
}

// TestSecondFilesDestinationForOneAgentIsRefused, naming the first entry's index: the author has to
// find the other half of the pair, and "a second destination" without a position sends them
// looking through the whole file.
func TestSecondFilesDestinationForOneAgentIsRefused(t *testing.T) {
	probs := decodeManifest(t,
		`{"kind":"files","agent":"pi","into":".pi/agent/extensions"},`+
			`{"kind":"files","agent":"pi","into":".pi/agent/themes"}`)
	for _, want := range []string{"contributes[1]", "agent \"pi\"", "contributes[0]", "one slot per agent"} {
		if !strings.Contains(probs, want) {
			t.Errorf("the refusal must mention %q, got %q", want, probs)
		}
	}
}

// ONE destination per agent is the shape every agent pack writes, and two destinations for two
// DIFFERENT agents are two different slots in one pack — legal, and the shape a pack that provides
// one agent while exposing a tree for another would need. Neither may be caught by the check above.
func TestOneFilesDestinationPerAgentValidates(t *testing.T) {
	for _, entries := range []string{
		`{"kind":"files","agent":"pi","into":".pi/agent/extensions"}`,
		`{"kind":"files","agent":"pi","into":".pi/agent/extensions"},` +
			`{"kind":"files","agent":"claude","into":".claude/plugins"}`,
	} {
		if probs := decodeManifest(t, entries); probs != "" {
			t.Errorf("%s must validate, got %q", entries, probs)
		}
	}
}

// A SECOND ADDRESSED TREE FOR ONE AGENT IS REFUSED TOO, and the reason is the layout rather than
// the invariant above: the landing path is `<slot>/<contributing pack>`, so two trees from one pack
// aimed at one agent name ONE directory. MEASURED before the refusal existed, and the two notches
// disagreed maximally — the jail emitted two binds at one destination (podman: "duplicate mount
// destination", naming neither) and the host merged both trees there in silence. The declaration
// pre-flight could not see it: it reads the declared `into`, and an addressed contribution has none.
func TestSecondAddressedFilesTreeForOneAgentIsRefused(t *testing.T) {
	probs := decodeManifest(t,
		`{"kind":"files","agents":["pi"],"from":"pi-extensions"},`+
			`{"kind":"files","agents":["pi"],"from":"pi-themes"}`)
	for _, want := range []string{"contributes[1]", "agent \"pi\"", "contributes[0]", "single \"from\""} {
		if !strings.Contains(probs, want) {
			t.Errorf("the refusal must mention %q, got %q", want, probs)
		}
	}
}

// AND EVERY ARRANGEMENT THAT RESOLVES TO DISTINCT PATHS STILL VALIDATES. One contribution naming
// two agents is two slots in two agents' homes; two contributions naming different agents likewise.
// Refusing either would break the feature while looking like the rule above.
func TestAddressedFilesForDistinctAgentsValidates(t *testing.T) {
	for _, entries := range []string{
		`{"kind":"files","agents":["pi","claude"],"from":"trees"}`,
		`{"kind":"files","agents":["pi"],"from":"pi-extensions"},` +
			`{"kind":"files","agents":["claude"],"from":"claude-plugins"}`,
	} {
		if probs := decodeManifest(t, entries); probs != "" {
			t.Errorf("%s must validate, got %q", entries, probs)
		}
	}
}

// THE REFUSAL IS AUTHORING-ONLY, like every other "declared twice by one pack" check
// (validateServiceNames and its siblings): the tolerant decoder validates entries one at a time and
// cannot see siblings, and the boot path treats any problem as fatal — so a cross-version read must
// not acquire a new way to refuse a jail. A manifest staged by a newer host than the baked
// entrypoint keeps booting.
func TestSecondFilesDestinationIsNotFatalOnTheTolerantPath(t *testing.T) {
	_, probs, _ := DecodeTolerant([]byte(`{"name":"acme","contributes":[` +
		`{"kind":"files","agent":"pi","into":".pi/agent/extensions"},` +
		`{"kind":"files","agent":"pi","into":".pi/agent/themes"}]}`))
	if len(probs) != 0 {
		t.Errorf("the tolerant path must not refuse the pair — the boot treats a problem as "+
			"fatal, got %v", probs)
	}
}
