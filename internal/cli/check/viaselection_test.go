package check

// viaselection_test.go pins `yolo check`'s half of `via` (docs/design/wire-bridge-gateway.md
// WG-I11, WG-I13, WG-I14, WG-I15): the Packs section resolves the launch's selection closure,
// so a pack a via profile adds is listed and accounted, and it predicts the launch's via-route
// gate — a FAIL where the launch refuses, a WARN where it warns. Every case drives
// sectionPacks, not the helpers, so deleting a call site turns it red.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// viaAgentPack writes a local pack whose agent prefers the `openai` protocol (the
// chat-completions wire on a via route) and also speaks `anthropic`, and which ships one
// provider with the given endpoints. Speaking `anthropic` too keeps the protocol-pairing
// gate quiet for an anthropic-only provider, so what the section reports is the via gate's.
// Its derive points the selected provider's row at ctx.via_url, as pi's does, so the via
// gate treats the agent as re-pointed (WG-I15).
func viaAgentPack(t *testing.T, endpointsJSON string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
  "name": "viaagent",
  "contributes": [
    {"kind": "program", "bin": "someagent", "via": "npm", "package": "@example/someagent",
     "protocols": ["openai", "anthropic"]},
    {"kind": "config", "config": [
      {"agent": "someagent", "name": "models", "codec": "json", "mode": "computed",
       "path": "~/.someagent/models.json"}]},
    {"kind": "provider", "name": "upstream", "endpoints": ` + endpointsJSON + `}
  ]
}`
	derive := `yolo.derive("someagent", "models", function(ctx)
  if not ctx.via_url or ctx.via_url == "" then return {} end
  return { providers = { [ctx.selected_provider] = { baseUrl = ctx.via_url } } }
end)
`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"), []byte(derive), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// viaCheck runs the Packs section over a config selecting that pack and a user via profile
// over its provider, active for someagent.
func viaCheck(t *testing.T, endpointsJSON string) (*reporter, string) {
	t.Helper()
	pack := viaAgentPack(t, endpointsJSON)
	packsFixture(t, `{"packs": ["file://`+pack+`"],
	  "profiles": {"pv": {"provider": "upstream", "via": "wire-bridge"}}}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("someagent", "pv"))
	return r, buf.String()
}

// TestSectionPacksListsThePackAViaProfileAdds: the via closure's addition prints with the
// launch's own cause line, and a servable via fails nothing.
func TestSectionPacksListsThePackAViaProfileAdds(t *testing.T) {
	r, out := viaCheck(t, `{"openai": {"base_url": "https://up.example/v1"}}`)
	if r.failed != 0 || r.warned != 0 {
		t.Fatalf("a servable via must neither fail nor warn:\n%s", out)
	}
	if !strings.Contains(out, "+ wire-bridge (via of profile pv, active for someagent)") {
		t.Errorf("check must list the pack the via profile adds, as the launch does:\n%s", out)
	}
}

// TestSectionPacksPredictsTheViaRouteRefusal: a provider offering neither wire the via route
// passes through leaves the agent's prefix unserved; the launch refuses, so check FAILS.
func TestSectionPacksPredictsTheViaRouteRefusal(t *testing.T) {
	r, out := viaCheck(t, `{"anthropic": {"base_url": "https://up.example"}}`)
	if r.failed == 0 {
		t.Fatalf("a via the bridge serves no route for must FAIL check:\n%s", out)
	}
	for _, want := range []string{"REFUSED", `profile "pv" (active for someagent)`,
		"declares no chat-completions or Responses endpoint"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prediction must name %q:\n%s", want, out)
		}
	}
}

// TestSectionPacksPredictsTheViaWireWarning: the route serves only Responses and the agent
// prefers chat-completions; the launch warns, so check WARNS and does not fail.
func TestSectionPacksPredictsTheViaWireWarning(t *testing.T) {
	r, out := viaCheck(t, `{"openai": {"base_url": "https://up.example/v1", "wire_api": "openai-responses"}}`)
	if r.failed != 0 || r.warned == 0 {
		t.Fatalf("a route without the agent's preferred wire must WARN, not fail (failed %d, warned %d):\n%s",
			r.failed, r.warned, out)
	}
	if !strings.Contains(out, "provider upstream declares no chat-completions endpoint") {
		t.Errorf("the warning must name the missing endpoint:\n%s", out)
	}
}

// TestSectionPacksGradesAMalformedProfileOnce: the selection closure and the pairing
// prediction both need the user's profile declarations, and the section reads them once, so
// a malformed entry is one graded row, not two.
func TestSectionPacksGradesAMalformedProfileOnce(t *testing.T) {
	pack := viaAgentPack(t, `{"openai": {"base_url": "https://up.example/v1"}}`)
	packsFixture(t, `{"packs": ["file://`+pack+`"],
	  "profiles": {"pv": {"provider": "upstream", "via": "wire-bridge"},
	               "bad": {"options": {"x": "y"}}}}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("someagent", "pv"))
	out := buf.String()
	if n := strings.Count(out, "config.profiles.bad"); n != 1 || r.warned != 1 {
		t.Fatalf("the malformed profile must be one graded row (rows %d, warned %d):\n%s", n, r.warned, out)
	}
}

// TestSectionPacksPredictsAViaWithNoEffect: an agent whose derive never reads ctx.via_url is
// not re-pointed by a via, so a provider the bridge serves no route for costs it nothing.
// The launch warns that the via has no effect and starts, so check WARNS and does not fail.
func TestSectionPacksPredictsAViaWithNoEffect(t *testing.T) {
	pack := viaAgentPack(t, `{"anthropic": {"base_url": "https://up.example"}}`)
	if err := os.Remove(filepath.Join(pack, "derive.lua")); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": ["file://`+pack+`"],
	  "profiles": {"pv": {"provider": "upstream", "via": "wire-bridge"}}}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("someagent", "pv"))
	out := buf.String()
	if r.failed != 0 || r.warned == 0 || !strings.Contains(out, "has no effect on someagent") {
		t.Fatalf("a via that re-points nothing must WARN, not fail (failed %d, warned %d):\n%s",
			r.failed, r.warned, out)
	}
}
