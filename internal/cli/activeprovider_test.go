package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// legacyUserFixture is a host workspace and a HOME whose user config directory holds the legacy
// JSONC file with text, or no user config at all when absent.
func legacyUserFixture(t *testing.T, text string, absent bool) (dir, ws string) {
	t.Helper()
	w := testconfig.Workspace(t, "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if !absent {
		if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	withConfigEditCWD(t, w.Root)
	return dir, w.Root
}

// launchErrors is what the launch's own load and validation say about the config now on disk.
func launchErrors(t *testing.T, ws string) []string {
	t.Helper()
	cfg, err := config.LoadConfig(ws, true, func(string) {})
	if err != nil {
		return []string{err.Error()}
	}
	errs, _ := config.ValidateConfig(cfg, ws, nil)
	return errs
}

func runActiveProviderVerb(t *testing.T, args ...string) (rc int, out, errs string) {
	t.Helper()
	return captureStd(t, func() int {
		return registry["active-provider"](append([]string{"active-provider"}, args...))
	})
}

func TestActiveProviderVerbJSONCWithoutKeyWritesALaunchableConfig(t *testing.T) {
	dir, ws := legacyUserFixture(t, "{\n  \"packs\": [\"claude\", \"zai\"]\n}\n", false)
	rc, out, errs := runActiveProviderVerb(t, "claude", "zai")
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, filepath.Join(dir, "config.jsonc"))
	if !strings.Contains(got, "\"active_provider\"") || !strings.Contains(got, "\"claude\": \"zai\"") || !strings.Contains(got, "\"packs\"") {
		t.Fatalf("config.jsonc lacks the selection or lost its packs:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
		t.Fatal("a config.toml was created beside the legacy JSONC, which the launch refuses as ambiguous")
	}
	if errs := launchErrors(t, ws); len(errs) != 0 {
		t.Fatalf("the written config does not pass the launch's validation: %v\n%s", errs, got)
	}
	sel, err := config.ConfigActiveProviderSelection(config.UserScopeConfigOrEmpty())
	if err != nil || len(sel.Named["claude"]) != 1 || sel.Named["claude"][0] != "zai" {
		t.Fatalf("selection not read back: %+v %v", sel, err)
	}
}

func TestActiveProviderVerbJSONCWithExistingTableAddsTheAgent(t *testing.T) {
	dir, ws := legacyUserFixture(t, "{\n  \"packs\": [\"claude\", \"zai\"],\n  \"active_provider\": {\"default\": \"native\"}\n}\n", false)
	rc, out, errs := runActiveProviderVerb(t, "claude", "zai")
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, filepath.Join(dir, "config.jsonc"))
	if !strings.Contains(got, "\"default\": \"native\"") || !strings.Contains(got, "\"claude\": \"zai\"") {
		t.Fatalf("table not extended:\n%s", got)
	}
	if errs := launchErrors(t, ws); len(errs) != 0 {
		t.Fatalf("launch validation: %v\n%s", errs, got)
	}
}

func TestActiveProviderVerbJSONCWithProfileRefusesNamingTheFix(t *testing.T) {
	dir, ws := legacyUserFixture(t, "{\n  \"packs\": [\"claude\", \"zai\"],\n  \"profile\": \"zai\"\n}\n", false)
	before := readFile(t, filepath.Join(dir, "config.jsonc"))
	rc, out, errs := runActiveProviderVerb(t, "claude", "zai")
	if rc != 1 || !strings.Contains(errs, "yolo config migrate") || !strings.Contains(errs, "profile") {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if readFile(t, filepath.Join(dir, "config.jsonc")) != before {
		t.Fatal("the refusal changed the config")
	}
	if errs := launchErrors(t, ws); len(errs) != 0 {
		t.Fatalf("the untouched config no longer launches: %v", errs)
	}
}

func TestActiveProviderVerbWithNoUserConfigCreatesConfigTOML(t *testing.T) {
	dir, ws := legacyUserFixture(t, "", true)
	rc, out, errs := runActiveProviderVerb(t, "default", "native")
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(got, "[active_provider]") || !strings.Contains(got, "default = \"native\"") {
		t.Fatalf("config.toml lacks the selection:\n%s", got)
	}
	if want, _ := config.UserConfigFile(); want != filepath.Join(dir, "config.toml") {
		t.Fatalf("the readable resolver names %s, not the file written", want)
	}
	if errs := launchErrors(t, ws); len(errs) != 0 {
		t.Fatalf("launch validation: %v\n%s", errs, got)
	}
}

func TestActiveProviderVerbDryRunWithNoUserConfigCreatesNothing(t *testing.T) {
	dir, _ := legacyUserFixture(t, "", true)
	rc, out, errs := runActiveProviderVerb(t, "default", "native", "--dry-run")
	if rc != 0 || !strings.Contains(out, "config.toml") {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
		t.Fatal("a dry run created the file")
	}
}

// activeProviderFixture is a host workspace whose user config selects the shipped claude and zai packs and a zai variant
// and holds [active_provider] in a config.d fragment, so a test can tell "the file that already
// holds it" from the default user file.
func activeProviderFixture(t *testing.T) (userConfig, fragment string) {
	t.Helper()
	ws := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs = [\"claude\", \"zai\"]\n[providers.zai.variants.fast]\nmodel = \"glm-5.3\"\n")
	fragment = filepath.Join(filepath.Dir(user.Path), "config.d", "work.toml")
	if err := os.MkdirAll(filepath.Dir(fragment), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte("[active_provider]\ndefault = \"native\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	withConfigEditCWD(t, ws.Root)
	return user.Path, fragment
}

func TestActiveProviderVerbRefusesUndeclaredNameListingDeclared(t *testing.T) {
	user, fragment := activeProviderFixture(t)
	before := readFile(t, fragment)
	rc, out, errs := captureStd(t, func() int {
		return registry["active-provider"]([]string{"active-provider", "claude", "nonesuch"})
	})
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%q stderr=%q", rc, out, errs)
	}
	for _, want := range []string{"nonesuch", "zai", "native", "yolo pack add"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("refusal does not name %q: %q", want, errs)
		}
	}
	if readFile(t, fragment) != before || strings.Contains(readFile(t, user), "active_provider") {
		t.Fatal("a refused verb wrote a config file")
	}
}

func TestActiveProviderVerbWritesNativeForOneAgent(t *testing.T) {
	user, fragment := activeProviderFixture(t)
	userBefore := readFile(t, user)
	rc, out, errs := captureStd(t, func() int {
		return registry["active-provider"]([]string{"active-provider", "claude", "native"})
	})
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, fragment)
	if !strings.HasPrefix(got, "[active_provider]\ndefault = \"native\"\n") || !strings.Contains(got, "claude = \"native\"") {
		t.Fatalf("the file holding [active_provider] was not spliced:\n%s", got)
	}
	if readFile(t, user) != userBefore {
		t.Fatal("config.toml changed although it does not hold [active_provider]")
	}
	if !strings.Contains(out, "takes effect at the next launch") || !strings.Contains(out, fragment) {
		t.Fatalf("stdout lacks the file or the next-launch line: %q", out)
	}
}

func TestActiveProviderVerbWritesProviderAndVariant(t *testing.T) {
	_, fragment := activeProviderFixture(t)

	for _, name := range []string{"zai", "zai/fast"} {
		rc, out, errs := captureStd(t, func() int {
			return registry["active-provider"]([]string{"active-provider", "default", name})
		})
		if rc != 0 {
			t.Fatalf("%s: rc=%d stdout=%q stderr=%q", name, rc, out, errs)
		}
		if got := readFile(t, fragment); !strings.Contains(got, "default = \""+name+"\"") || strings.Contains(got, "native") {
			t.Fatalf("%s: not written:\n%s", name, got)
		}
	}
}

func TestActiveProviderVerbInJailPrintsHostRun(t *testing.T) {
	_, fragment := activeProviderFixture(t)
	before := readFile(t, fragment)
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "/home/someone/code/proj")
	rc, out, errs := captureStd(t, func() int {
		return registry["active-provider"]([]string{"active-provider", "claude", "native"})
	})
	if rc != 1 || !strings.Contains(errs, "yolo host-run") || !strings.Contains(errs, "yolo active-provider claude native") {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if readFile(t, fragment) != before {
		t.Fatal("the in-jail refusal wrote a file")
	}
}

// A provider the user config declares with only `platform` (what `yolo config set
// providers.groq.platform groq --user` writes) is one the launch selects once active_provider
// names it, so the verb must accept it too: the verb and the launch share one resolver.
func TestActiveProviderVerbAcceptsAUserProviderDeclaredWithOnlyPlatform(t *testing.T) {
	ws := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs = [\"pi\"]\n\n[providers.groq]\nplatform = \"groq\"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	withConfigEditCWD(t, ws.Root)

	rc, out, errs := runActiveProviderVerb(t, "pi", "groq")
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, user.Path)
	if !strings.Contains(got, "[active_provider]") || !strings.Contains(got, "pi = \"groq\"") {
		t.Fatalf("active_provider not written:\n%s", got)
	}
	if errs := launchErrors(t, ws.Root); len(errs) != 0 {
		t.Fatalf("the written config does not pass the launch's validation: %v\n%s", errs, got)
	}
}
