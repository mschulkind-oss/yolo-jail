package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallYoloLog writes an executable helper to ~/.local/bin/yolo-log.
func TestInstallYoloLog(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"HOME": home})
	body := "#!/bin/sh\nexec /usr/bin/log \"$@\"\n"
	if err := InstallYoloLog(e, body); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".local", "bin", "yolo-log")
	got := mustRead(t, p)
	if string(got) != body {
		t.Errorf("yolo-log body = %q, want %q", got, body)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("yolo-log is not executable: mode %v", info.Mode())
	}
}

// TestInstallYoloLogEmptyIsNoop: an empty script writes nothing.
func TestInstallYoloLogEmptyIsNoop(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"HOME": home})
	if err := InstallYoloLog(e, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "yolo-log")); !os.IsNotExist(err) {
		t.Errorf("empty script should write no file, got err=%v", err)
	}
}

// TestWriteLoginRC re-prepends the PATH in all three login rc files.
func TestWriteLoginRC(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"HOME": home})
	loginPath := "/Users/dev/.yolo/bin/block:/nix/store/x/bin"
	if err := WriteLoginRC(e, loginPath); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile"} {
		got := string(mustRead(t, filepath.Join(home, name)))
		if !strings.Contains(got, `export PATH="`+loginPath+`:$PATH"`) {
			t.Errorf("%s missing PATH re-prepend:\n%s", name, got)
		}
	}
}

// TestWriteLoginRCEmptyIsNoop: an empty loginPath writes nothing.
func TestWriteLoginRCEmptyIsNoop(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"HOME": home})
	if err := WriteLoginRC(e, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zprofile")); !os.IsNotExist(err) {
		t.Errorf("empty loginPath should write no rc, got err=%v", err)
	}
}

// TestRunDarwinBootstrapGeneratesConfig: the darwin entry runs the shared
// generators against a native home + writes the two macOS pieces, without the
// Linux-only boot steps. Uses a minimal env (no agents) so it exercises the
// generator sequence + the two writers end to end.
func TestRunDarwinBootstrapGeneratesConfig(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"HOME":              home,
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","block_flags":["-r"],"message":"no","suggestion":"rg"}]`,
	})
	e.Workspace = "/Users/dev/proj"
	e.ShimBinDir = "/usr/bin"

	RunDarwinBootstrap(e, DarwinBootstrapOptions{
		MacosLog:      "user",
		LoginPath:     "/Users/dev/.yolo/bin/block:/usr/bin",
		YoloLogScript: "#!/bin/sh\nexec /usr/bin/log \"$@\"\n",
	})

	// Shim generated, exec'ing the macOS /usr/bin path.
	shim := string(mustRead(t, filepath.Join(home, ".yolo/bin/block", "grep")))
	if !strings.Contains(shim, "/usr/bin/grep") {
		t.Errorf("darwin shim should exec /usr/bin/grep:\n%s", shim)
	}
	// yolo-log installed.
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "yolo-log")); err != nil {
		t.Errorf("yolo-log not installed: %v", err)
	}
	// Login rc written with the sandbox PATH.
	rc := string(mustRead(t, filepath.Join(home, ".zprofile")))
	if !strings.Contains(rc, "/Users/dev/.yolo/bin/block") {
		t.Errorf(".zprofile missing sandbox PATH:\n%s", rc)
	}
}

// The container MCP preset wrappers are Linux-absolute — /usr/bin/chromium,
// `exec /bin/node`, /etc/fonts/fonts.conf — and this backend bakes no image, so on
// macOS all three paths are absent. Generating them put three executables in the
// sandbox home that fail the moment anything execs one.
//
// Open Decision #4 is resolved by SKIPPING them and saying so. This pins both
// halves: no wrapper file appears, and a config that asked for presets is told.
func TestDarwinBootstrapSkipsLinuxMCPWrappers(t *testing.T) {
	home := t.TempDir()
	var warnings strings.Builder
	e := NewEnv(map[string]string{
		"JAIL_HOME":        home,
		"YOLO_MCP_PRESETS": `["chrome-devtools"]`,
	})
	e.Stderr = &warnings

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off", LoginPath: "/usr/bin:/bin"})

	// The wrapper the container path writes must not exist here.
	if _, err := os.Stat(filepath.Join(e.LocalBin(), "chrome-devtools-mcp-wrapper")); err == nil {
		t.Error("generated the chrome-devtools wrapper on macos-user — its body execs " +
			"/usr/bin/chromium, which this backend never provisions")
	}
	// And the skip must be reported: an agent told an MCP server exists, whose wrapper
	// is silently absent, is the same lie in the other direction.
	if !strings.Contains(warnings.String(), "mcp_presets are not delivered on macos-user") {
		t.Errorf("skipped the wrappers without saying so:\n%s", warnings.String())
	}
}

// No presets configured → no notice. A warning that fires when nothing was asked for
// is the noise that trains people to skip the line that matters.
func TestDarwinBootstrapSilentAboutMCPWhenNonePresetsAsked(t *testing.T) {
	var warnings strings.Builder
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	e.Stderr = &warnings

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off", LoginPath: "/usr/bin:/bin"})

	if strings.Contains(warnings.String(), "mcp_presets") {
		t.Errorf("warned about mcp_presets when none were configured:\n%s", warnings.String())
	}
}
