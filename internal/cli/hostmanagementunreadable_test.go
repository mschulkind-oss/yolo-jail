package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unreadableUserConfigHome builds the reproduced home: a config.toml that DOES say
// `host_management = "own"`, beside a dangling legacy config.jsonc link, so the user config
// fails to load. The mode stays fail-closed ("none"), but no refusal may claim the key is unset
// in config.toml — it is set — and each must name the unreadable config and `yolo config check`.
// It returns the dangling link's missing target.
func unreadableUserConfigHome(t *testing.T) (missing string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_JAIL", "")
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, ".config", "yolo-jail")
	writeFile(t, filepath.Join(dir, "config.toml"), "host_management = \"own\"\n")
	missing = filepath.Join(t.TempDir(), "gone", "config.jsonc")
	if err := os.Symlink(missing, filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	return missing
}

func assertUnreadableRefusal(t *testing.T, what, msg, missing string) {
	t.Helper()
	if strings.Contains(msg, "is unset") || strings.Contains(msg, `is "none" in`) {
		t.Errorf("%s claims the key is unset or none, but it is set and the config failed to load:\n%s", what, msg)
	}
	for _, want := range []string{"could not read your user config", missing, "yolo config check", `counts as "none"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s does not say %q:\n%s", what, want, msg)
		}
	}
}

// TestHostRefusalsNameAnUnreadableUserConfig pins both doorways through their real entry
// point (hostMain), so deleting either call site's unreadable branch turns this red.
func TestHostRefusalsNameAnUnreadableUserConfig(t *testing.T) {
	t.Run("launch", func(t *testing.T) {
		missing := unreadableUserConfigHome(t)
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"-p", "x", "--", "no-such-agent-binary"}, &out, &errw, false, nil); rc != 1 {
			t.Fatalf("rc = %d, want 1\n%s%s", rc, out.String(), errw.String())
		}
		if !strings.Contains(errw.String(), "refusing to launch") {
			t.Errorf("the launch was not refused by the host_management gate:\n%s", errw.String())
		}
		assertUnreadableRefusal(t, "the host launch refusal", errw.String(), missing)
	})
	t.Run("apply --assert", func(t *testing.T) {
		missing := unreadableUserConfigHome(t)
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, nil); rc != 1 {
			t.Fatalf("rc = %d, want 1\n%s%s", rc, out.String(), errw.String())
		}
		assertUnreadableRefusal(t, "the host apply refusal", errw.String(), missing)
	})
	t.Run("apply dry-run json verdict", func(t *testing.T) {
		missing := unreadableUserConfigHome(t)
		var out, errw bytes.Buffer
		hostMain([]string{"apply", "--format", "json"}, &out, &errw, false, nil)
		got := out.String()
		if strings.Contains(got, "is unset") {
			t.Errorf("the dry run's verdict claims the key is unset:\n%s", got)
		}
		for _, want := range []string{"could not read your user config", "yolo config check", filepath.Base(filepath.Dir(missing))} {
			if !strings.Contains(got, want) {
				t.Errorf("the dry run's verdict does not say %q:\n%s", want, got)
			}
		}
	})
}
