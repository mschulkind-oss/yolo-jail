package cli

// hostmodelroles_test.go pins the role environment (docs/research/extension-model-defaults.md
// OQ-XM4) at the host notch: `yolo host -- <agent>` hands the exec'd agent YOLO_MODEL_<ROLE> for
// the provider its profile selects, through the same gate a jail's env files are written from,
// and removes a role its provider does not name that the invoking shell inherited from an agent
// on another provider.

import "testing"

func TestHostLaunchCarriesTheAgentsOwnTiers(t *testing.T) {
	env, _ := hostGateLaunchWith(t, `{"packs": ["pi", "zai", "cerebras"],
		"providers": {"zai": {"models": {"fast": "glm-5.3-flash"}}},
		"env_sources": [{"ZAI_API_KEY": "tok-host"}]}`,
		// The shell this launch was started from is another agent's, on cerebras.
		map[string]string{"YOLO_MODEL_DEFAULT": "cerebras/qwen-3.8-27b"},
		[]string{"-p", "zai"}, "pi")
	if got := env["YOLO_MODEL_FAST"]; got != "zai/glm-5.3-flash" {
		t.Errorf("pi on zai: YOLO_MODEL_FAST = %q, want zai/glm-5.3-flash", got)
	}
	if got, ok := env["YOLO_MODEL_DEFAULT"]; ok {
		t.Errorf("pi on zai, whose provider names no `default`, kept YOLO_MODEL_DEFAULT=%q "+
			"from the invoking shell: a model of another provider", got)
	}
}
