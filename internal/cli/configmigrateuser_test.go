package cli

// configmigrateuser_test.go drives `yolo config migrate` (configRunW) over the user-scope shapes
// RC-D32 converts: loopholes, the profile selection and profiles, and the sibling keys of a
// table that also holds an unconverted key.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// migrateUser writes the old user config, runs --yes, and returns the converted file.
func migrateUser(t *testing.T, old string) (rc int, toml, out string) {
	t.Helper()
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), old)
	rc, stdout, stderr := runConfigMigrateVerb(t, "--yes")
	data, _ := os.ReadFile(filepath.Join(e.userDir, "config.toml"))
	return rc, string(data), stdout + stderr
}

func TestConfigMigrateConvertsLoopholeSettingsToAllow(t *testing.T) {
	rc, toml, out := migrateUser(t, `{
  "packs": ["aws-auth"],
  "loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "sso", "unnarrowed": true}}}
}`)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	if !strings.Contains(toml, "[allow.aws-auth]") || !strings.Contains(toml, `profile = "sso"`) ||
		!strings.Contains(toml, "unnarrowed = true") || strings.Contains(toml, "loopholes") || strings.Contains(toml, "enabled") {
		t.Fatalf("converted file:\n%s", toml)
	}
}

func TestConfigMigrateLoopholeRefusalsNameTheNextStep(t *testing.T) {
	for _, tc := range []struct{ name, old, want string }{
		{"pack not listed", `{"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "sso"}}}}`, "yolo pack add aws-auth"},
		{"switched off", `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": false}}}`, `remove "aws-auth" from packs`},
		{"settings without enabled", `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"settings": {"profile": "sso"}}}}`, `Set "enabled": true`},
		{"declared here", `{"loopholes": {"mine": {"command": ["x"]}}}`, "yolo pack init"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, toml, out := migrateUser(t, tc.old)
			if rc == 0 || toml != "" || !strings.Contains(out, tc.want) {
				t.Fatalf("rc=%d, want a refusal naming %q, wrote %q\n%s", rc, tc.want, toml, out)
			}
		})
	}
}

func TestConfigMigrateConvertsProfilesToProvidersAndActiveProvider(t *testing.T) {
	rc, toml, out := migrateUser(t, `{
  "providers": {"bedrock": {"region": "us-east-1"}},
  "profile": {"*": "bedrock", "claude": null, "pi": ["bedrock", "fast"]},
  "profiles": {
    "bedrock": {"provider": "bedrock", "model": "m1"},
    "fast": {"provider": "bedrock", "model": "m2", "enforce_models": true}
  }
}`)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	for _, want := range []string{
		"[active_provider]", `default = "bedrock"`, `claude = "native"`, `pi = ["bedrock", "bedrock/fast"]`,
		"[providers.bedrock]", `region = "us-east-1"`, `model = "m1"`,
		"[providers.bedrock.variants.fast]", `model = "m2"`, "enforce_models = true",
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("converted file lacks %q:\n%s", want, toml)
		}
	}
	if strings.Contains(toml, "profiles") || strings.Contains(toml, "[profile]") {
		t.Errorf("an old key survived:\n%s", toml)
	}
}

func TestConfigMigrateProfileRefusalsNameTheNextStep(t *testing.T) {
	for _, tc := range []struct{ name, old, want string }{
		{"selects a pack's profile", `{"profile": {"*": "codex"}}`, `active_provider.<agent> = "<provider>"`},
		{"same name, variant-only key", `{"profiles": {"bedrock": {"provider": "bedrock", "enforce_models": true}}}`, "rename the profile"},
		{"no provider", `{"profiles": {"odd": {"model": "m"}}}`, `add "provider"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, toml, out := migrateUser(t, tc.old)
			if rc == 0 || toml != "" || !strings.Contains(out, tc.want) {
				t.Fatalf("rc=%d, want a refusal naming %q, wrote %q\n%s", rc, tc.want, toml, out)
			}
		})
	}
}

// A table that holds one key the conversion cannot spell keeps its other keys in the check: the
// dry run must not report the converted key (region) as new.
func TestConfigMigrateDoesNotInventAKeyBesideAnUnconvertedOne(t *testing.T) {
	rc, _, out := migrateUser(t, `{"providers": {"p": {"region": "us-east-1", "base_url": "http://x"}}}`)
	if rc == 0 {
		t.Fatalf("a base_url has no conversion yet, so the plan must refuse\n%s", out)
	}
	if strings.Contains(out, "providers.p.region") {
		t.Fatalf("the proof reports region as a difference:\n%s", out)
	}
}

// A linked user config is renamed to its new name, and the report says so with the dotfiles
// warning (RC-D41).
func TestConfigMigrateReportsARenamedSymlink(t *testing.T) {
	e := newMigrateEnv(t)
	dots := filepath.Join(filepath.Dir(e.userDir), "dotfiles")
	writeMigrateFile(t, filepath.Join(dots, "config.jsonc"), `{"packs": []}`)
	if err := os.Symlink(filepath.Join(dots, "config.jsonc"), filepath.Join(e.userDir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr := runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, stdout, stderr)
	}
	if !strings.Contains(stdout, "Renamed the symlink ") || !strings.Contains(stdout, "if a dotfiles manager made it, you may need to update that too.") {
		t.Fatalf("the report does not name the renamed link:\n%s", stdout)
	}
	if info, err := os.Lstat(filepath.Join(e.userDir, "config.toml")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is not the renamed link: %v", err)
	}
}
