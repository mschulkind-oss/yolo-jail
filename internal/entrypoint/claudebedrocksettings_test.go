package entrypoint

// claudebedrocksettings_test.go pins the SETTINGS-FILE half of claude's Bedrock switch
// (docs/reference/providers.md#pv-d8: CLAUDE_CODE_USE_BEDROCK in ~/.claude/settings.json's env
// block, so a bare `claude` outside yolo still runs in Bedrock mode) as OQ-BR8 moved it: from a
// config-overlay gated on the profile NAME `bedrock` into claude's own settings derive, keyed on
// the selected provider's platform and on claude's own transport. Through ConfigurePackSurfaces,
// the entry the boot loop uses, over the real embedded claude pack and a provider table and
// resolved profiles composed the way a launch composes them, so deleting the derive's switch,
// or the boot's hand-off of the selection, fails here.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// renderClaudeSettings boots the surface render of packs with claude on profile, and returns
// ~/.claude/settings.json's `env` block. hostSettings, when non-empty, is the user's own host
// ~/.claude/settings.json, delivered on the /ctx mount the launcher binds.
func renderClaudeSettings(t *testing.T, names []string, userProviders string,
	userProfiles map[string]packload.UserProfile, profile, hostSettings string) map[string]any {
	t.Helper()
	var packs []*packload.Pack
	for _, n := range names {
		p, err := embeddedPack(n)
		if err != nil {
			t.Fatalf("embedded %s: %v", n, err)
		}
		packs = append(packs, p)
	}
	var user *jsonx.OrderedMap
	if userProviders != "" {
		v, err := jsonx.Decode([]byte(userProviders))
		if err != nil {
			t.Fatal(err)
		}
		user = v.(*jsonx.OrderedMap)
	}
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		t.Fatal(err)
	}
	use := jsonx.NewOrderedMap()
	if profile != "" {
		use.Set("claude", profile)
	}
	errw := &bytes.Buffer{}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
		"YOLO_USE_PROFILES": mustCompactJSON(t, use),
	}}
	hostDir := withCtxRoot(t, t.TempDir(), "claude")
	if hostSettings != "" {
		if err := os.WriteFile(filepath.Join(hostDir, "settings.json"), []byte(hostSettings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	data, err := os.ReadFile(filepath.Join(e.Home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading the rendered settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the rendered settings are not JSON: %v\n%s", err, data)
	}
	env, _ := got["env"].(map[string]any)
	return env
}

var bedrockPacks = []string{"claude", "aws-auth", "openai-auth", "wire-bridge"}

func TestClaudeSettingsSwitchBedrockOnForEveryBedrockSelection(t *testing.T) {
	const user = `{"bedrock":{"region":"us-west-2"},"bedrock-eu":{"platform":"aws-bedrock","region":"eu-west-1"}}`
	profiles := map[string]packload.UserProfile{
		"bedrock-sso": {Provider: "bedrock"},
		"eu":          {Provider: "bedrock-eu"},
	}
	for _, profile := range []string{"bedrock", "bedrock-sso", "eu"} {
		env := renderClaudeSettings(t, bedrockPacks, user, profiles, profile, "")
		if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
			t.Errorf("claude on %s: settings env must carry CLAUDE_CODE_USE_BEDROCK=1, got %v", profile, env)
		}
	}
	// No Bedrock selection, and a profile over the Bedrock provider that routes through the
	// wire bridge (the everything profile's shape): neither switches claude's own client on.
	bridged := map[string]packload.UserProfile{"bedrock-bridge": {Provider: "bedrock", Via: "wire-bridge"}}
	for _, profile := range []string{"", "codex", "bedrock-bridge"} {
		env := renderClaudeSettings(t, bedrockPacks, user, bridged, profile, "")
		if _, set := env["CLAUDE_CODE_USE_BEDROCK"]; set {
			t.Errorf("claude on %q: settings must not switch Bedrock on, got %v", profile, env)
		}
	}
}

// YOLO DELETES NOTHING IT DID NOT WRITE (PP-D1): a Bedrock switch the user wrote in their own
// host settings survives a launch that selects no Bedrock provider. The derive asserts the key
// and never tombstones it; the launch names the conflict instead.
func TestAUsersOwnBedrockSwitchSurvivesANonBedrockLaunch(t *testing.T) {
	env := renderClaudeSettings(t, bedrockPacks, "", nil, "codex",
		`{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","OTHER":"kept"}}`)
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" || env["OTHER"] != "kept" {
		t.Errorf("the user's own env block must reach the jail unchanged, got %v", env)
	}
}
