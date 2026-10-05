package entrypoint

import (
	"encoding/json"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The SHIPPED claude pack, rendered at the host notch, must NOT write the jail-bypass
// permission keys into the real ~/.claude/settings.json — the concrete §4.2 fix for the
// host apply bypass leak. RenderHostPack drives the same path yolo host apply --assert does.
// (The jail notch staying byte-identical is enforced by TestRenderFingerprintStable.)
func TestHostRenderClaudeDropsBypass(t *testing.T) {
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	home := t.TempDir()

	// observe=false (the --assert posture) so the file actually lands, then inspect it.
	results, rerr := RenderHostPack(claude, home, render.OwnershipOwn, false, nil, nil)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	// The settings surface must have rendered (not been refused).
	var rendered bool
	for _, r := range results {
		if r.Surface == "claude/settings" && r.Action == "rendered" {
			rendered = true
		}
	}
	if !rendered {
		t.Fatalf("claude/settings should render at host; results=%+v", results)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read host settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse host settings: %v\n%s", err, data)
	}

	// The bypass keys must be absent or benign — never the jail values on a real host.
	if got["skipDangerousModePermissionPrompt"] == true {
		t.Errorf("host settings must not skip the dangerous-mode prompt:\n%s", data)
	}
	if perms, _ := got["permissions"].(map[string]any); perms != nil {
		if perms["defaultMode"] == "acceptEdits" {
			t.Errorf("host settings must not be acceptEdits:\n%s", data)
		}
		if ad, _ := perms["additionalDirectories"].([]any); len(ad) != 0 {
			t.Errorf("host settings must not allow the filesystem root:\n%s", data)
		}
	}
	// AND THE GUARDED POSTURE IS POSITIVELY THERE. The two checks above are negative — they
	// pass just as well on an empty file — so the surface has to be shown to have written
	// the guarded values, not merely to have omitted the autonomous ones.
	//
	// This used to key on preferences.autoUpdaterStatus, the one benign always-managed key
	// claude/settings had. It was deleted on 2026-09-04 as unreadable by Claude Code (see
	// internal/agentcfg's claude/settings cell), so the positive evidence moved onto the
	// guarded autonomy values themselves, which is where it should have been.
	if v, present := got["skipDangerousModePermissionPrompt"]; !present || v != false {
		t.Errorf("the guarded value must be WRITTEN, not merely not-bypassed (got %v, "+
			"present=%v):\n%s", v, present, data)
	}
	if perms, _ := got["permissions"].(map[string]any); perms == nil || perms["defaultMode"] != "default" {
		t.Errorf("the guarded permissions.defaultMode must render at host:\n%s", data)
	}
}

// Across every shipped agent pack, the host notch must NOT carry the autonomous
// (prompts-off) value into the real config — the guarded posture (or the agent's own
// default) must win. This guards all four migrations at once against a regression that
// re-leaks one agent's bypass. Checks are per-agent because each expresses "prompts off"
// with a different key/value.
func TestHostRenderAllAgentsGuarded(t *testing.T) {
	// forbidden maps an agent to the autonomous value that must NOT appear at host, as a
	// substring of the rendered file (codec-agnostic: json and toml both serialize these).
	forbidden := map[string][]string{
		"codex":    {"danger-full-access", `"never"`, "never"},
		"agy":      {`"allow"`},
		"opencode": {`"permission": "allow"`, `"permission":"allow"`},
		"pi":       {`"always"`},
	}
	// the config file each agent's guarded posture writes.
	settingsPath := map[string]string{
		"codex":    ".codex/config.toml",
		"agy":      ".gemini/antigravity-cli/settings.json",
		"opencode": ".config/opencode/opencode.json",
		"pi":       ".pi/agent/settings.json",
	}
	for agentName, rel := range settingsPath {
		p, err := embeddedPack(agentName)
		if err != nil {
			t.Fatalf("embedded %s: %v", agentName, err)
		}
		home := t.TempDir()
		if _, rerr := RenderHostPack(p, home, render.OwnershipOwn, false, nil, nil); rerr != nil {
			t.Fatalf("RenderHostPack %s: %v", agentName, rerr)
		}
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatalf("%s: read %s: %v", agentName, rel, err)
		}
		for _, bad := range forbidden[agentName] {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s host config leaks the autonomous value %q:\n%s", agentName, bad, data)
			}
		}
	}
}

// managedOverwrites is the host-notch "warn before you clobber" (§4.2 / Phase 9): when a
// managed key's value differs from what the user already has, the render reports it; an
// added key or an identical re-assert is silent, and a sibling the user owns under a
// managed parent is preserved (not reported).
func TestHostRenderReportsOverwrites(t *testing.T) {
	home := t.TempDir()
	// The user already has permissions.defaultMode=plan (differs from the pack's guarded
	// "default") plus a sibling `ask` the pack never manages.
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"permissions":{"defaultMode":"plan","ask":["Bash"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	// Observe: must report the overwrite BEFORE writing anything.
	results, rerr := RenderHostPack(claude, home, render.OwnershipOwn, true, nil, nil)
	if rerr != nil {
		t.Fatalf("RenderHostPack observe: %v", rerr)
	}
	var found bool
	for _, r := range results {
		if r.Surface != "claude/settings" {
			continue
		}
		for _, o := range r.Overwrites {
			if o == "permissions.defaultMode" {
				found = true
			}
			if o == "permissions.ask" {
				t.Errorf("ask is not managed — must not be reported as an overwrite: %v", r.Overwrites)
			}
		}
	}
	if !found {
		t.Errorf("observe should report the differing managed key permissions.defaultMode as an overwrite; got results=%+v", results)
	}
	// Observe wrote nothing.
	after, _ := os.ReadFile(settings)
	if !strings.Contains(string(after), `"plan"`) {
		t.Errorf("observe must not write — the user's defaultMode should still be 'plan':\n%s", after)
	}

	// A file that already matches the managed value reports NO overwrite (idempotent).
	if err := os.WriteFile(settings,
		[]byte(`{"permissions":{"defaultMode":"default"},`+
			`"skipDangerousModePermissionPrompt":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	results, _ = RenderHostPack(claude, home, render.OwnershipOwn, true, nil, nil)
	for _, r := range results {
		if r.Surface == "claude/settings" && len(r.Overwrites) > 0 {
			t.Errorf("an identical value must not be reported as an overwrite: %v", r.Overwrites)
		}
	}
}

// NO SHIPPED PACK'S HOST POSTURE REPLACES A LIST. The posture the host profile selects renders
// its `config` into the real home's files: on the pack's own surface as managed keys, and on
// another pack's as a posture overlay. In both, an array replaces the file's array whole (RFC
// 7386). In a user's own config file that empties or overwrites the user's list at every apply:
// a managed key through either of an owned host's mechanisms, a posture overlay through rmw — a
// surface its pack declares `rmw`, as every surface was under the retired `assert` (composing
// `stateful`, the user's captured list outranks an overlay). That is what claude's guarded
// `permissions.additionalDirectories: []` did until it was removed. A pack that wants an entry in a host list declares a posture `lists` entry, which
// appends and keeps the user's own entries (docs/reference/pack-system.md#autonomy).
//
// A walk over the shipped manifests, so it covers every pack at once. The behavior it guards is
// pinned through `yolo host apply` by internal/cli's hostguardeddirs_test.go.
func TestNoShippedHostPostureReplacesAList(t *testing.T) {
	packs, err := embeddedPackSet()
	if err != nil {
		t.Fatalf("embedded packs: %v", err)
	}
	hostAutonomy := render.ProfileFor(render.KindHost).AgentAutonomy
	var walk func(pack, at string, v any)
	walk = func(pack, at string, v any) {
		switch x := v.(type) {
		case []any:
			t.Errorf("pack %s's host posture sets the array %s = %v, which replaces the user's "+
				"own list in their real config file at every `yolo host apply` (on the pack's own "+
				"surface always, as a posture overlay on one declaring `rmw`). Add entries with a posture "+
				"`lists` entry, or leave the key to the user.", pack, at, x)
		case map[string]any:
			for k, sub := range x {
				walk(pack, at+"."+k, sub)
			}
		}
	}
	checked := 0
	for _, p := range packs {
		if p.Decl == nil {
			continue
		}
		posture := p.Decl.PostureFor(hostAutonomy)
		if posture == nil || len(posture.Config) == 0 {
			continue
		}
		var entries []struct {
			Agent   string `json:"agent"`
			Name    string `json:"name"`
			Managed any    `json:"managed"`
		}
		if err := json.Unmarshal(posture.Config, &entries); err != nil {
			t.Fatalf("pack %s: decode the host posture's config: %v", p.Name, err)
		}
		for _, e := range entries {
			walk(p.Name, e.Agent+"/"+e.Name, e.Managed)
			checked++
		}
	}
	// Fixture guard: with no host posture to walk, this would pass having checked nothing.
	if checked == 0 {
		t.Fatal("no shipped pack declares a host posture config entry, so this checked nothing")
	}
}
