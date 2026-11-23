package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// writeUserScope writes the user's config.jsonc at the test HOME.
func writeUserScope(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toolSkillsMergedConfig(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	cfg := sessionInputsConfig()
	cfg.Set("install", mustDecodeMap(t, `{"mise": {"jq": "1.9", "only-here": "2"}}`))
	cfg.Delete("mise_tools")
	return cfg
}

func mustDecodeMap(t *testing.T, doc string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.OrderedMap)
}

// MS-D1 and MS-D9 through the REAL call path: assembleRunCmd's channel build reaches
// composePackChannelWith, which hands composeSessionInputs the allowlist and token. The values
// are read off the channel text the launch delivers, so this fails if that call site stops
// passing them. The workspace overrides jq's version and adds a tool of its own: jq is admitted
// (in the user's declaration order), only-here and the user's "off" entry are not.
func TestTheChannelCarriesTheAllowlistAndTheSnapshotToken(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	a := assembleWithConfigAssembled(t, toolSkillsMergedConfig(t), func() {
		writeUserScope(t, `{"install": {"mise": {"zed": "1", "gone": "off", "jq": "1.7", "alpha": "3"}}}`)
	})
	got := map[string]string{}
	for _, kv := range a.channelEnv(t, "YOLO_MISE_SKILL_TOOLS", "YOLO_TOOL_SKILLS_TOKEN") {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if want := `["zed", "jq", "alpha"]`; got["YOLO_MISE_SKILL_TOOLS"] != want {
		t.Errorf("YOLO_MISE_SKILL_TOOLS = %q, want %q", got["YOLO_MISE_SKILL_TOOLS"], want)
	}
	want, err := config.ToolSkillsToken("/ws")
	if err != nil {
		t.Fatal(err)
	}
	if got["YOLO_TOOL_SKILLS_TOKEN"] != want || len(want) != 64 {
		t.Errorf("YOLO_TOOL_SKILLS_TOKEN = %q, want the workspace's stored token %q", got["YOLO_TOOL_SKILLS_TOKEN"], want)
	}
	if strings.Contains(got["YOLO_MISE_SKILL_TOOLS"], "only-here") {
		t.Errorf("a workspace-only tool is on the allowlist")
	}
}

func TestTheLegacyMiseToolsSpellingReachesTheChannelAllowlist(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	a := assembleWithConfigAssembled(t, sessionInputsConfig(), func() {
		writeUserScope(t, `{"mise_tools": {"ripgrep": "14", "old": "off", "jq": "1"}}`)
	})
	got := a.channelEnv(t, "YOLO_MISE_SKILL_TOOLS")
	if len(got) != 1 || got[0] != `YOLO_MISE_SKILL_TOOLS=["ripgrep", "jq"]` {
		t.Fatalf("channel = %q", got)
	}
}

// With no user tools the list is empty and NO token is minted.
func TestNoUserToolsMeansAnEmptyAllowlistAndNoTokenMinted(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	a := assembleWithConfigAssembled(t, toolSkillsMergedConfig(t))
	got := a.channelEnv(t, "YOLO_MISE_SKILL_TOOLS", "YOLO_TOOL_SKILLS_TOKEN")
	if len(got) != 2 || got[0] != "YOLO_MISE_SKILL_TOOLS=[]" || got[1] != "YOLO_TOOL_SKILLS_TOKEN=" {
		t.Fatalf("channel = %q", got)
	}
	if _, err := os.Stat(filepath.Join(paths.GlobalStorage(), "tool-skills-tokens")); !os.IsNotExist(err) {
		t.Fatalf("a token was minted for an empty allowlist (stat err %v)", err)
	}
}

func TestToolSkillInputsAreEmptyWhenSealedCapturedOrNestedAndStableOtherwise(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("HOME", t.TempDir())
	writeUserScope(t, `{"install": {"mise": {"jq": "1"}}}`)
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	plain := func() *Options { return &Options{Workspace: ws} }

	first, second := plain().toolSkillInputs(), plain().toolSkillInputs()
	if len(first.tools) != 1 || first.token == "" || first.token != second.token {
		t.Fatalf("ordinary launch: %+v then %+v, want the same non-empty token", first, second)
	}
	if _, err := os.Stat(filepath.Join(ws, ".yolo")); !os.IsNotExist(err) {
		t.Fatalf("the token machinery touched the workspace's .yolo")
	}

	sealed := plain()
	sealed.Sealed = true
	capture := plain()
	capture.CapturesDir = func() string { return "" }
	for name, o := range map[string]*Options{"sealed": sealed, "capture": capture} {
		if got := o.toolSkillInputs(); len(got.tools) != 0 || got.token != "" {
			t.Errorf("%s: %+v, want empty", name, got)
		}
	}
	t.Setenv("YOLO_VERSION", "9.9.9")
	if got := plain().toolSkillInputs(); len(got.tools) != 0 || got.token != "" {
		t.Errorf("nested: %+v, want empty", got)
	}
}
