package loopholedecl_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// decodeServes decodes a manifest body under the fixed name/dir this file uses.
func decodeServes(t *testing.T, body string) (*loopholedecl.Manifest, error) {
	t.Helper()
	return loopholedecl.Decode([]byte(`{"name": "acme", `+body+`}`), "/loopholes/acme")
}

// TestServesDecodes is the happy path: a bare string list, in declaration order,
// carried verbatim onto the manifest.
func TestServesDecodes(t *testing.T) {
	m, err := decodeServes(t, `"serves": ["a-job", "b-job"]`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(m.Serves, []string{"a-job", "b-job"}) {
		t.Errorf("Serves = %v, want [a-job b-job]", m.Serves)
	}
	if !m.ServesCapability("b-job") || m.ServesCapability("c-job") {
		t.Errorf("ServesCapability disagrees with Serves = %v", m.Serves)
	}
}

// TestServesIsAKnownKey: the strict decoder must not report `serves` as a typo.
// Every key added to the schema has to join topKeys or a shipped manifest using it
// fails `yolo pack lint` — which is exactly what happened to `version` before
// keys.go existed.
func TestServesIsAKnownKey(t *testing.T) {
	if _, err := decodeServes(t, `"serves": ["a-job"]`); err != nil {
		t.Fatalf("strict decode reported a problem for a known key: %v", err)
	}
	var found bool
	for _, k := range loopholedecl.KnownKeys() {
		if k == "serves" {
			found = true
		}
	}
	if !found {
		t.Errorf("KnownKeys() = %v, missing \"serves\" — authoring tools suggest spellings "+
			"from this list", loopholedecl.KnownKeys())
	}
}

// TestServesSilenceIsNotAClaim is the invariant the whole mechanism rests on
// (docs/design/pack-capabilities.md §4): absent and empty both mean "not
// participating", and neither may become a default claim. Pinned at the SCHEMA
// level; internal/loopholes pins the behavioural half.
func TestServesSilenceIsNotAClaim(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"absent", `"description": "x"`},
		{"empty list", `"serves": []`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := decodeServes(t, tc.body)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(m.Serves) != 0 {
				t.Errorf("Serves = %v, want empty — silence must never read as a claim", m.Serves)
			}
			if m.ServesCapability("anything") {
				t.Error("ServesCapability(anything) = true for a manifest that declares nothing")
			}
		})
	}
}

// TestServesRefusals covers every static rule, each with the consequence behind it.
func TestServesRefusals(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"not a list", `"serves": "a-job"`, "must be a list"},
		{"non-string entry", `"serves": [1]`, "must be a string"},
		{"empty name", `"serves": [""]`, "non-empty"},
		{"whitespace", `"serves": ["a job"]`, "rendezvous key"},
		{"newline", `"serves": ["a\njob"]`, "control character"},
		{"duplicate", `"serves": ["a-job", "a-job"]`, "declared twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeServes(t, tc.body)
			if err == nil {
				t.Fatalf("decode accepted %s", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestCapabilityNameProblemAcceptsRealNames guards the rule from getting stricter by
// accident: a capability name is a plain identifier, and the shipped one has to pass.
func TestCapabilityNameProblemAcceptsRealNames(t *testing.T) {
	for _, name := range []string{"claude-oauth-refresh", "audio.playback", "x", "a_b/c-2"} {
		if prob := loopholedecl.CapabilityNameProblem(name); prob != "" {
			t.Errorf("CapabilityNameProblem(%q) = %q, want ok", name, prob)
		}
	}
}

// TestPackShippedLoopholeMayServe: `serves` is NOT in the pack-shipped subset's
// refusal list, and must not become one. The design's §2 note is explicit — the
// implementation a pack ships has a manifest of its own, and a statement about an
// implementation belongs there — so a pack's loophole declaring what job it does is
// ordinary, not a privilege escalation.
func TestPackShippedLoopholeMayServe(t *testing.T) {
	m, err := decodeServes(t, `"serves": ["a-job"], "transport": "none"`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if probs := m.PackShippedProblems(filepath.Join("/loopholes/acme", "manifest.jsonc")); len(probs) > 0 {
		t.Errorf("pack-shipped subset refuses `serves`: %v", probs)
	}
}

// TestBundledServesDeclarations pins the FIRST-PARTY INSTANCE (design §7) and, in
// the same assertion, the blast radius: exactly one manifest yolo SHIPS declares
// `serves`, and the others are untouched — wherever each one now lives, which
// bundledManifest resolves through shippedManifestHome. If a future change adds `serves` to
// `audio`, `host-processes` or `journal` this fails, which is the point — every
// declaration makes a loophole retirable by a pack, so each one is a decision.
func TestBundledServesDeclarations(t *testing.T) {
	want := map[string][]string{
		"claude-oauth-broker": {"claude-oauth-refresh"},
		"audio":               nil,
		"host-processes":      nil,
		"journal":             nil,
	}
	for name, capabilities := range want {
		t.Run(name, func(t *testing.T) {
			m, err := loopholedecl.Decode(bundledManifest(t, name), filepath.Join("/loopholes", name))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(m.Serves) != len(capabilities) {
				t.Fatalf("Serves = %v, want %v", m.Serves, capabilities)
			}
			for i, c := range capabilities {
				if m.Serves[i] != c {
					t.Errorf("Serves[%d] = %q, want %q", i, m.Serves[i], c)
				}
			}
		})
	}
}
