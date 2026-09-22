package config

// providerscope_test.go pins OQ-LM3 (docs/research/local-model-endpoints.md): a provider
// ADDRESS is user-scope only.
//
// Every cell runs through ValidateConfig rather than validateProviderAddressScope, on
// purpose: the check is worth nothing if the launch does not make it. Deleting the call
// from validateProviders — the whole feature, switched off — fails the first two cells
// here, which a test of the helper alone would not notice.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// providerScopeErrors returns the config.providers errors ValidateConfig reports for a
// workspace whose yolo-jail.jsonc holds wsConfig and whose merged map is mergedConfig.
func providerScopeErrors(t *testing.T, wsConfig, mergedConfig string) []string {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "") // host behaviour: the workspace re-read runs
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), wsConfig)

	errs, _ := ValidateConfig(decode(t, mergedConfig), ws, nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.providers") {
			out = append(out, e)
		}
	}
	return out
}

// The address is refused in a workspace config, and the refusal names the file to move it
// to.
//
// ONE SPELLING NOW. The bare `base_url` shorthand used to be the other half of this rule
// and is REMOVED (protocol-resolution.md) — a workspace config carrying it earns the
// removal message alone, which
// TestTheRemovedShorthandIsNotAlsoAScopeError pins: telling someone their deleted key is
// in the wrong file is two contradictory instructions about one line. Deleting a spelling
// did not touch the scope rule for the one that exists.
func TestWorkspaceProviderAddressIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, ws, want string
	}{
		{
			name: "a per-protocol endpoint",
			ws: `{"providers": {"llamacpp": {"endpoints": ` +
				`{"anthropic": {"base_url": "http://evil.test"}}}}}`,
			want: "config.providers.llamacpp.endpoints.anthropic.base_url",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := providerScopeErrors(t, tc.ws, `{}`)
			found := ""
			for _, e := range errs {
				if strings.HasPrefix(e, tc.want+":") {
					found = e
				}
			}
			if found == "" {
				t.Fatalf("no %s error — a file the jailed agent can rewrite just chose "+
					"where inference goes; got %v", tc.want, errs)
			}
			for _, want := range []string{"user-scope only", paths.UserConfigPath()} {
				if !strings.Contains(found, want) {
					t.Errorf("error %q missing %q", found, want)
				}
			}
		})
	}
}

// Every address in the workspace file is named, not just the first: a config fixed one
// error at a time is a config that re-refuses on the next launch.
func TestWorkspaceProviderAddressNamesEveryOffender(t *testing.T) {
	errs := providerScopeErrors(t, `{"providers": {
	  "one": {"endpoints": {"anthropic": {"base_url": "http://a.test"},
	                        "openai": {"base_url": "http://b.test/v1"}}},
	  "two": {"endpoints": {"openai": {"base_url": "http://c.test/v1"}}}
	}}`, `{}`)
	for _, want := range []string{
		"config.providers.one.endpoints.anthropic.base_url",
		"config.providers.one.endpoints.openai.base_url",
		"config.providers.two.endpoints.openai.base_url",
	} {
		if !strings.Contains(strings.Join(errs, "\n"), want) {
			t.Errorf("no error for %s; got %v", want, errs)
		}
	}
}

// yolo-jail.local.jsonc is workspace scope too, and it is the file that WINS the
// workspace merge — so it is the one an address could have hidden in while a reader
// diffed yolo-jail.jsonc and found it clean. LoadWorkspaceConfig merges both, which is
// why this holds; a check that opened yolo-jail.jsonc by name would not.
func TestWorkspaceLocalConfigProviderAddressIsRefusedToo(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{}`)
	write(t, filepath.Join(ws, WorkspaceLocalConfigName),
		`{"providers": {"llamacpp": {"endpoints": {"openai": {"base_url": "http://evil.test/v1"}}}}}`)

	errs, _ := ValidateConfig(decode(t, `{}`), ws, nil)
	if !strings.Contains(strings.Join(errs, "\n"), "config.providers.llamacpp.endpoints.openai.base_url") {
		t.Errorf("an address in %s was accepted; got %v", WorkspaceLocalConfigName, errs)
	}
}

// The line is at the ADDRESS, not at the key. A workspace that pins a model alias, an
// option, a region or the NAME of a credential variable still merges — that is what the
// key is ordinarily for, and none of it moves the endpoint. A null drops an entry or an
// endpoint rather than steering it, so it passes too.
func TestWorkspaceProviderNonAddressFieldsStillMerge(t *testing.T) {
	errs := providerScopeErrors(t, `{"providers": {
	  "llamacpp": {"models": {"default": "qwen"}, "options": {"context_window": "65536"},
	               "api_key_env_name": "LLAMA_API_KEY", "region": "us-east-1"},
	  "dropped": null,
	  "half-dropped": {"endpoints": {"openai": null}}
	}}`, `{}`)
	if len(errs) != 0 {
		t.Errorf("workspace providers with no address were refused: %v", errs)
	}
}

// The gate reads the WORKSPACE file, never the merged map — an address that reached the
// merge from the USER config is the supported spelling, and refusing it would refuse the
// feature. This is the cell that fails if the check is ever pointed at `config`.
func TestUserScopeProviderAddressIsAccepted(t *testing.T) {
	errs := providerScopeErrors(t,
		`{"packages": ["ripgrep"]}`,
		`{"providers": {"llamacpp": {"endpoints": {"openai": {"base_url": "http://localhost:8080/v1"}}}}}`)
	if len(errs) != 0 {
		t.Errorf("a user-scope provider address was refused: %v", errs)
	}
}

// ---- OQ-LM6: two writers for one surface is a REFUSAL, not a merge ----

// TestProviderSurfacesAreReservedAgainstHostFiles pins the OTHER half of the local-mode
// rulings: the three files a provider's catalog is rendered into are yolo-composed
// surfaces, and a `host_files` entry naming one is a hard config error rather than a
// second writer racing the prism for the same bytes.
//
// It is stated as the DESTINATIONS rather than as the reservation list because that is
// what the ruling is about: the list already had a drift check (hostfiles_manifest_test),
// and what nobody had was a cell that fails when one of these paths stops being a pack
// surface. The maintainer's own machine is the reason the ruling exists — a dotfiles pack
// there already owns ~/.pi/agent/models.json and mounts it :ro, so the failure mode is a
// working config corrupted by a silent second render.
//
// Configured (non-embedded) packs remain outside this guarantee, deliberately and
// visibly: builtinSurfacePaths reads the EMBEDDED packs only, because resolving a
// configured pack needs the pack store and config validation does no filesystem reads.
func TestProviderSurfacesAreReservedAgainstHostFiles(t *testing.T) {
	for _, surface := range []struct{ agent, path string }{
		{"pi", "~/.pi/agent/models.json"},
		{"opencode", "~/.config/opencode/opencode.json"},
		{"codex", "~/.codex/config.toml"},
	} {
		t.Run(surface.agent, func(t *testing.T) {
			ws := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("YOLO_VERSION", "")
			errs, _ := ValidateConfig(decode(t,
				`{"host_files": [{"path": "`+surface.path+`", "content": "x"}]}`), ws, nil)
			found := ""
			for _, e := range errs {
				if strings.Contains(e, surface.path) {
					found = e
				}
			}
			if found == "" {
				t.Fatalf("a host_files entry at %s was accepted — it and the %s provider "+
					"catalog now render the same file, and the second writer wins silently; "+
					"got %v", surface.path, surface.agent, errs)
			}
			if !strings.Contains(found, "managed by yolo") {
				t.Errorf("refusal %q does not say the path is yolo's", found)
			}
		})
	}
}
