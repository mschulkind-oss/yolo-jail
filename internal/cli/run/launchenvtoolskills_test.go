package run

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// MS-D1 and MS-D9 on macos-user: the provisioning stage runs under `env -i` with a closed list and
// reads everything else from the session env file, which is written from launchEnv. So the two
// tool-skill inputs must be on the launch env, or the slice-3 step is inert there. Built through
// composePackChannel, this fails if either the channel stops composing them or launchEnv stops
// carrying them. A dotenv entry naming either variable never wins: it is agent-writable.
func TestLaunchEnvCarriesTheToolSkillInputsToTheMacosUserStage(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeUserScope(t, `{"install": {"mise": {"zed": "1", "jq": "1.7"}}}`)
	o := goldenOptions("/ws", home)
	userEnv := jsonx.NewOrderedMap()
	userEnv.Set("YOLO_MISE_SKILL_TOOLS", `["evil"]`)
	userEnv.Set("YOLO_TOOL_SKILLS_TOKEN", "forged")
	env := channelFor(t, o, bareConfig(), zaiSelected(t), userEnv).launchEnv("claude")

	if got, want := envAt(env, "YOLO_MISE_SKILL_TOOLS"), `["zed", "jq"]`; got != want {
		t.Errorf("YOLO_MISE_SKILL_TOOLS = %q, want %q", got, want)
	}
	want, err := config.ToolSkillsToken("/ws")
	if err != nil {
		t.Fatal(err)
	}
	if got := envAt(env, "YOLO_TOOL_SKILLS_TOKEN"); got != want || len(want) != 64 {
		t.Errorf("YOLO_TOOL_SKILLS_TOKEN = %q, want the workspace's stored token", got)
	}
}

// An empty allowlist is inert on this backend too: neither variable is written, and a dotenv
// entry naming one is dropped rather than relayed.
func TestLaunchEnvCarriesNoToolSkillInputsForAnEmptyAllowlist(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions("/ws", home)
	userEnv := jsonx.NewOrderedMap()
	userEnv.Set("YOLO_MISE_SKILL_TOOLS", `["evil"]`)
	env := channelFor(t, o, bareConfig(), zaiSelected(t), userEnv).launchEnv("claude")
	for _, k := range []string{"YOLO_MISE_SKILL_TOOLS", "YOLO_TOOL_SKILLS_TOKEN"} {
		if _, ok := env.Get(k); ok {
			t.Errorf("%s is on the launch env with an empty allowlist", k)
		}
	}
}
