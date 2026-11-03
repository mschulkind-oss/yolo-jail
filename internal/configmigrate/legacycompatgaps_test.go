package configmigrate

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LC-D9 (docs/design/legacy-config-compat.md §4.3): `yolo config migrate` never declines a config
// that launches. Each case is a legacy shape v0.15.1 launched that the migration used to decline
// (the corpus rows lc-migrate-host-files-list, lc-migrate-host-path-list,
// lc-migrate-ws-model-facts, lc-migrate-provider-model-string and lc-migrate-blocked-tools): the
// config launches, `yolo config migrate` converts every file with nothing left "not converted",
// and the converted TOML reads back to the same effective config and the same consumer read.
// Fails if the readable schema stops accepting the TOML spelling the conversion writes (the
// plan's read-back then refuses it), or if the conversion drops the key as having no spelling.
func TestMigrationConvertsEveryLaunchingLegacyShape(t *testing.T) {
	key := func(path ...string) func(*jsonx.OrderedMap) string {
		return func(cfg *jsonx.OrderedMap) string {
			var v any = cfg
			for _, p := range path {
				m, _ := v.(*jsonx.OrderedMap)
				if m == nil {
					return "(absent)"
				}
				v, _ = m.Get(p)
			}
			return jsonx.MustDumpsCompact(v)
		}
	}
	blocked := func(cfg *jsonx.OrderedMap) string {
		sec, _ := cfg.Get("security")
		m, _ := sec.(*jsonx.OrderedMap)
		return jsonx.MustDumpsCompact(config.NormalizeBlockedTools(m))
	}
	const blockedTools = `{"security": {"blocked_tools": [{"name": "wget", "message": "Use curl", "suggestion": "curl <url>"}, "telnet"]}}`
	const zai = `{"providers": {"zai": {"endpoints": {"openai": {"base_url": "https://api.z.ai/api/paas/v4", ` +
		`"wire_api": "openai-chat-completions"}}, "api_key_env_name": "ZAI_API_KEY", ` +
		`"models": {"default": "glm-5.3", "fast": "glm-5.3-flash"}, "options": {"model": "default"}}}}`
	// A TOML user config whose route extends shared facts a legacy workspace file declares: the
	// facts reach the provider table, which is what the workspace's entries mean.
	const extendingUser = "[providers.zai]\napi_key_env_name = 'ZAI_API_KEY'\n" +
		"[providers.zai.endpoints.openai]\nbase_url = 'https://api.z.ai/api/paas/v4'\nwire_api = 'openai-chat-completions'\n" +
		"[providers.zai.models.big]\nmodel = 'glm-5.3-max'\nextends = 'glm-max'\n"
	sharedFactsRead := func(cfg *jsonx.OrderedMap) string {
		read := userSettingsRead(cfg)
		if !strings.Contains(read, "1000000") {
			t.Errorf("fixture: the workspace's shared facts do not reach the provider table, so this proves nothing: %s", read)
		}
		return read
	}
	for _, tc := range []struct {
		name     string
		semantic func(*jsonx.OrderedMap) string
		files    map[string]string
		// respelled: the conversion writes the readers' new spelling of a value, so only the
		// consumer's read, not the effective config's text, is the same before and after.
		respelled bool
		// want, when set, is what the consumer must read before and after.
		want string
	}{
		// A list-valued host_files: a path string and an object entry.
		{"host_files list", key("host_files"), map[string]string{
			"config.jsonc": `{"host_files": ["~/.npmrc", {"path": "~/.config/ripgrep/config", "content": "--smart-case\n"}]}`,
		}, false, ""},
		// A legacy empty list adds nothing to an include's entries (the lists union-merge); its
		// TOML conversion must not become the reset `[]` is in TOML.
		{"host_files empty over an include", key("host_files"), map[string]string{
			"config.jsonc": `{"host_files": [], "include_if_found": ["more.jsonc"]}`,
			"more.jsonc":   `{"host_files": [{"path": "~/.config/ripgrep/config", "content": "--smart-case\n"}]}`,
		}, false, `[{"path": "~/.config/ripgrep/config", "content": "--smart-case\n"}]`},
		{"host_path empty over an include", key("host_path"), map[string]string{
			"config.jsonc": `{"host_path": [], "include_if_found": ["more.jsonc"]}`,
			"more.jsonc":   `{"host_path": ["~/.cargo/bin"]}`,
		}, false, `["~/.cargo/bin"]`},
		// host_path is a list of folders.
		{"host_path list", key("host_path"), map[string]string{
			"config.jsonc": `{"host_path": ["~/.cargo/bin", "/opt/tools/bin"]}`,
		}, false, ""},
		// model_facts in a workspace file, which the legacy reader merges and composes.
		{"workspace model_facts", sharedFactsRead, map[string]string{
			"config.toml": extendingUser,
			"ws/yolo-jail.jsonc": `{"model_facts": {"glm-max": {"reasoning": true, "input": ["text", "image"], ` +
				`"context_window": 1000000, "max_tokens": 128000}}}`,
		}, true, ""},
		// A provider's model given as a plain string: the alias's wire id.
		{"provider model string", userSettingsRead, map[string]string{
			"config.jsonc": zai,
		}, false, ""},
		{"provider model string beside an object row", userSettingsRead, map[string]string{
			"config.jsonc": `{"providers": {"zai": {"endpoints": {"openai": {"base_url": "https://api.z.ai/api/paas/v4", ` +
				`"wire_api": "openai-chat-completions"}}, "api_key_env_name": "ZAI_API_KEY", ` +
				`"models": {"default": "glm-5.3", "big": {"id": "glm-5.3-max", "context_window": 200000}}, ` +
				`"options": {"model": "default"}}}}`,
		}, true, ""}, // the object row is respelled as a readable row; the string stays a string
		// security.blocked_tools with an object entry, at each scope.
		{"user blocked_tools", blocked, map[string]string{"config.jsonc": blockedTools}, false, ""},
		{"workspace blocked_tools", blocked, map[string]string{"ws/yolo-jail.jsonc": blockedTools}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertMigrationKeeps(t, gapMix{name: tc.name, files: tc.files}, tc.want, tc.semantic, !tc.respelled)
		})
	}
}
