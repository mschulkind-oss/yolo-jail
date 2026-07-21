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
	loginPath := "/Users/dev/.yolo-shims:/nix/store/x/bin"
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
		LoginPath:     "/Users/dev/.yolo-shims:/usr/bin",
		YoloLogScript: "#!/bin/sh\nexec /usr/bin/log \"$@\"\n",
	})

	// Shim generated, exec'ing the macOS /usr/bin path.
	shim := string(mustRead(t, filepath.Join(home, ".yolo-shims", "grep")))
	if !strings.Contains(shim, "/usr/bin/grep") {
		t.Errorf("darwin shim should exec /usr/bin/grep:\n%s", shim)
	}
	// yolo-log installed.
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "yolo-log")); err != nil {
		t.Errorf("yolo-log not installed: %v", err)
	}
	// Login rc written with the sandbox PATH.
	rc := string(mustRead(t, filepath.Join(home, ".zprofile")))
	if !strings.Contains(rc, "/Users/dev/.yolo-shims") {
		t.Errorf(".zprofile missing sandbox PATH:\n%s", rc)
	}
}
