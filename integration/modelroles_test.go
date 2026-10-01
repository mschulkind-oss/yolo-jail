package integration

// modelroles_test.go is the integration tier of the role environment
// (docs/research/extension-model-defaults.md OQ-XM4, built 2026-10-01): each agent's own env
// file, sourced in a real jail as its launcher sources it, carries YOLO_MODEL_<ROLE> for the
// provider its profile selects, and an agent started from another one loses a tier its own
// provider does not name. Through config resolution, the credential gate, the per-agent file's
// delivery and bash itself. The unit pins are internal/packload/modelroles_test.go (the
// composition) and internal/cli/run/modelroles_test.go (the file's lines).

import (
	"strings"
	"testing"
)

func TestEachAgentReadsItsOwnProvidersTiersInTheJail(t *testing.T) {
	requireJail(t)
	// Both providers' keys ride env_sources, the channel the credential gate delivers into each
	// agent's own file; the shell's own copies are blanked so the launch cannot lean on them.
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("CEREBRAS_API_KEY", "")

	dir := writeProject(t, `{}`)
	// zai ships no tier alias, so the user names `fast`; cerebras ships `default`.
	packHome(t, `{"packs": ["pi", "opencode", "zai", "cerebras"],
		"profile": {"pi": "zai", "opencode": "cerebras"},
		"providers": {"zai": {"models": {"fast": "glm-5.3-flash"}}},
		"env_sources": [{"ZAI_API_KEY": "integration-probe-not-a-real-key",
		                 "CEREBRAS_API_KEY": "integration-probe-not-a-real-key"}]}`)
	// Each agent's file alone, then opencode's sourced over pi's: what opencode sees when pi
	// starts it.
	script := `d=~/.config/yolo-agent-env
show() { printf '%s FAST=%s DEFAULT=%s\n' "$1" "${YOLO_MODEL_FAST-unset}" "${YOLO_MODEL_DEFAULT-unset}"; }
( . "$d/pi.sh"; show pi )
( . "$d/opencode.sh"; show opencode )
( . "$d/pi.sh"; . "$d/opencode.sh"; show opencode-from-pi )`
	r := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc", script))
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	for _, want := range []string{
		"pi FAST=zai/glm-5.3-flash DEFAULT=unset\n",
		"opencode FAST=unset DEFAULT=cerebras/qwen-3.8-27b\n",
		// pi's fast model is zai's, so a child on cerebras must not keep it.
		"opencode-from-pi FAST=unset DEFAULT=cerebras/qwen-3.8-27b\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the jail's agent files do not give %q:\n%s", strings.TrimSpace(want), r.combined())
		}
	}
}
