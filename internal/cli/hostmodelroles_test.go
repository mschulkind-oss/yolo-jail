package cli

// hostmodelroles_test.go pins the role environment (docs/research/extension-model-defaults.md
// OQ-XM4) at the host notch: `yolo host -- <agent>` hands the exec'd agent YOLO_MODEL_<ROLE> for
// the provider its profile selects, through the same gate a jail's env files are written from,
// and removes a role its provider does not name that the invoking shell inherited from an agent
// on another provider.

import "testing"

// kilo, a provider pi has none of its own for: on one pi has built in (zai, cerebras) pi uses its
// own list, so its provider names no tier for it (docs/design/pi-codex-provider-shadowing.md OQ-3;
// packload's builtinproviders_test.go pins that half).
func TestHostLaunchCarriesTheAgentsOwnTiers(t *testing.T) {
	env, _ := hostGateLaunchWith(t, `{"packs": ["pi", "kilo", "cerebras"],
		"providers": {"kilo": {"models": {"fast": "kilo-fast"}}},
		"env_sources": [{"KILO_API_KEY": "tok-host"}]}`,
		// The shell this launch was started from is another agent's, on cerebras.
		map[string]string{"YOLO_MODEL_DEFAULT": "cerebras/qwen-3.8-27b"},
		[]string{"-p", "kilo"}, "pi")
	if got := env["YOLO_MODEL_FAST"]; got != "kilo/kilo-fast" {
		t.Errorf("pi on kilo: YOLO_MODEL_FAST = %q, want kilo/kilo-fast", got)
	}
	if got, ok := env["YOLO_MODEL_DEFAULT"]; ok {
		t.Errorf("pi on kilo, whose provider names no `default`, kept YOLO_MODEL_DEFAULT=%q "+
			"from the invoking shell: a model of another provider", got)
	}
}
