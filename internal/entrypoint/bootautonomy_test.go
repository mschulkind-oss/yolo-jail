package entrypoint

// bootautonomy_test.go pins that the BOOT LOOP's §4.2 autonomy posture comes from its render
// target's confinement profile (plan §6c step 1/step 3).
//
// This gap was found by mutating `autonomy := e.renderTarget().Profile().AgentAutonomy` to its
// negation in ConfigurePackSurfaces: the whole suite stayed green. The reason is worth stating,
// because it is the trap and not an oversight. The jail-notch posture assertions
// (prism_claude_test.go and friends) all drive ConfigurePackByName, the NON-boot entry, so they
// pin that function's policy and say nothing about the loop the entrypoint actually runs. And
// the shipped-pack byte gate (TestRenderFingerprintStable) goes through the same non-boot entry.
// So the one function whose posture reaches a real jail had no test asserting which posture it
// picks — the boot path was the only path where flipping the policy was invisible.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// autonomyPack declares BOTH postures over one surface it owns: the autonomous side asserts
// the bypass key, the guarded side asserts prompts-on. A pack shaped exactly like the shipped
// agents' (packs/claude etc.), minimal enough that the assertion is about the posture and
// nothing else.
func autonomyPack(t *testing.T) *packload.Pack {
	t.Helper()
	base, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":    "~/.acme/settings.json",
		"managed": map[string]any{"benign": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	posture := func(mode string) json.RawMessage {
		raw, merr := json.Marshal([]any{map[string]any{
			"agent": "acme", "name": "settings", "codec": "json",
			"path":    "~/.acme/settings.json",
			"managed": map[string]any{"permissionMode": mode},
		}})
		if merr != nil {
			t.Fatal(merr)
		}
		return raw
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfig, Raw: base},
			{Kind: packdecl.KindAutonomy,
				Autonomous: &packdecl.AutonomyPosture{Config: posture("bypass")},
				Guarded:    &packdecl.AutonomyPosture{Config: posture("prompt")}},
		},
	}}
}

// THE BOOT LOOP renders the AUTONOMOUS posture, because its target is a jail and
// render.ProfileFor(KindJail) says autonomy is on. Not because the loop passes `true`.
func TestBootRenderUsesTheJailProfilesAutonomy(t *testing.T) {
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")

	ConfigurePackSurfaces(e, []*packload.Pack{autonomyPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}

	data, err := os.ReadFile(filepath.Join(e.Home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	if got["permissionMode"] != "bypass" {
		t.Errorf("the boot loop must render the AUTONOMOUS posture (the jail profile's "+
			"AgentAutonomy is ON) — got permissionMode=%v:\n%s", got["permissionMode"], data)
	}
	// The base managed key is untouched by the fold either way, so its absence would mean the
	// surface did not render at all rather than that the posture was wrong.
	if got["benign"] != true {
		t.Errorf("the pack's own managed key must survive the posture fold:\n%s", data)
	}
}

// And the HOST target through the same loop renders the GUARDED posture. This is what makes the
// assertion above about the PROFILE rather than about a constant that happens to be true: one
// function, two targets, two postures, with nothing in between but Kind.
//
// It drives the loop with a hostTarget Env directly, which no production caller does (the host
// path is RenderHostPack). That is deliberate: the point is that the loop's policy follows its
// target, so the test has to be able to change the target without changing the loop.
func TestBootRenderAtAHostTargetRendersTheGuardedPosture(t *testing.T) {
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Vars: map[string]string{}, Stderr: &errw, hostTarget: true}
	withCtxRoot(t, t.TempDir(), "acme")

	ConfigurePackSurfaces(e, []*packload.Pack{autonomyPack(t)})

	data, err := os.ReadFile(filepath.Join(e.Home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	if got["permissionMode"] != "prompt" {
		t.Errorf("a host target must render the GUARDED posture (its profile's AgentAutonomy is "+
			"OFF) — got permissionMode=%v:\n%s", got["permissionMode"], data)
	}
}
