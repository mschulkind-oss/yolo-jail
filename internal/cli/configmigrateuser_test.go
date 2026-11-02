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

// The owner's shape: aws-auth switched on in an INCLUDED legacy file. The conversion writes the
// settings to the included file's TOML twin as `[allow.aws-auth]`, and the proof, which compares
// the loopholes a launch runs, holds: aws-auth runs with the same settings either way.
func TestConfigMigrateKeepsAnIncludedFilesLoopholeRunning(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), `{"include_if_found": ["work.jsonc"]}`)
	writeMigrateFile(t, filepath.Join(e.userDir, "work.jsonc"), `{
  "packs": ["aws-auth"],
  "loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "sso", "unnarrowed": true}}}
}`)
	rc, stdout, stderr := runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, stdout, stderr)
	}
	work, _ := os.ReadFile(filepath.Join(e.userDir, "work.toml"))
	if !strings.Contains(string(work), "[allow.aws-auth]") || !strings.Contains(string(work), `profile = "sso"`) {
		t.Fatalf("work.toml:\n%s", work)
	}
}

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
		// A grant-declaring loophole converts to its grants (configmigrate.TestLegacyLoopholeEnabledMigratesToAllowAndRequests); one that declares none and does not run on selection keeps its old file.
		{"gated loophole switched on", `{"packs": ["hello-daemon"], "loopholes": {"hello-daemon": {"enabled": true}}}`, "Keep the old file (this yolo keeps reading it) until those grants land, then run `yolo config migrate` again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, toml, out := migrateUser(t, tc.old)
			if rc == 0 || toml != "" || !strings.Contains(out, tc.want) {
				t.Fatalf("rc=%d, want a refusal naming %q, wrote %q\n%s", rc, tc.want, toml, out)
			}
		})
	}
}

// A selected pack's loophole runs in the old file too now (no manifest defaults one off), so
// selecting aws-auth with no `loopholes` entry changes nothing a conversion could see: the proof,
// which compares which loopholes run, finds no difference and the file converts.
func TestConfigMigrateConvertsASelectedLoopholeWithNoEntry(t *testing.T) {
	rc, toml, out := migrateUser(t, `{"packs": ["aws-auth"]}`)
	if rc != 0 || !strings.Contains(toml, `packs = ["aws-auth"]`) {
		t.Fatalf("rc=%d, want the file converted, wrote %q\n%s", rc, toml, out)
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

// A profile a selected pack declares (codex-auth's `codex`) is selected by its own name in
// active_provider, which the launch resolves exactly as it resolved the legacy key.
func TestConfigMigrateCarriesAPacksProfileByName(t *testing.T) {
	rc, toml, out := migrateUser(t, `{"packs": ["codex", "codex-auth"], "profile": {"*": "codex", "claude": ["codex"]}}`)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	for _, want := range []string{"[active_provider]", `default = "codex"`, `claude = ["codex"]`} {
		if !strings.Contains(toml, want) {
			t.Errorf("converted file lacks %q:\n%s", want, toml)
		}
	}
}

func TestConfigMigrateProfileRefusalsNameTheNextStep(t *testing.T) {
	for _, tc := range []struct{ name, old, want string }{
		{"a name active_provider reads as a variant", `{"profile": {"*": "a/b"}}`, "provider/variant"},
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

// A linked user config is never written through: the link moves to config.jsonc.pre-migrate,
// config.toml is a regular file, the dotfiles copy is untouched, and the plan and the closing
// report both say so (RC-D41, the owner's ruling of 2026-11-01).
func TestConfigMigrateMovesALinkAndLeavesItsTarget(t *testing.T) {
	e := newMigrateEnv(t)
	dots := filepath.Join(filepath.Dir(e.userDir), "dotfiles")
	target := filepath.Join(dots, "config.jsonc")
	writeMigrateFile(t, target, `{"packs": []}`)
	link := filepath.Join(e.userDir, "config.jsonc")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr := runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, stdout, stderr)
	}
	toml := filepath.Join(e.userDir, "config.toml")
	before := link + " is a link to " + target + ". The converted file is written as a regular file at " + toml + ", and " + target + " is not changed.\n" +
		"If a dotfiles manager keeps this file, copy the new file into it yourself.\n"
	if !strings.Contains(stdout, before) {
		t.Fatalf("the plan does not warn about the link:\n%s", stdout)
	}
	after := link + " was a link to " + target + " and is kept as " + link + ".pre-migrate. The converted file is the regular file " + toml +
		", and " + target + " was not changed; if a dotfiles manager keeps it, copy the new file into it yourself.\n"
	if !strings.Contains(stdout, after) {
		t.Fatalf("the report does not name the moved link:\n%s", stdout)
	}
	if info, err := os.Lstat(toml); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("config.toml is not a regular file: %v", err)
	}
	if text, err := os.Readlink(link + ".pre-migrate"); err != nil || text != target {
		t.Fatalf("config.jsonc.pre-migrate = %q (%v), want the original link", text, err)
	}
	entries, err := os.ReadDir(dots)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the dotfiles directory changed: %v (%v)", entries, err)
	}
	if b, _ := os.ReadFile(target); string(b) != `{"packs": []}` {
		t.Fatalf("the link's target changed: %q", b)
	}
}
