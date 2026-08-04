package paths

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// TestHomeResolution pins the audit-confirmed Python Path.home() parity: the
// paths constants must stay ABSOLUTE even when $HOME is unset or empty
// (Go's os.UserHomeDir would error there and yield relative paths).
func TestHomeResolution(t *testing.T) {
	orig, had := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if had {
			os.Setenv("HOME", orig)
		} else {
			os.Unsetenv("HOME")
		}
	})

	// $HOME set and non-empty -> $HOME.
	os.Setenv("HOME", "/home/someone")
	if got := home(); got != "/home/someone" {
		t.Errorf("home() with HOME=/home/someone = %q, want /home/someone", got)
	}
	if got := GlobalStorage(); got != "/home/someone/.local/share/yolo-jail" {
		t.Errorf("GlobalStorage = %q", got)
	}

	// $HOME empty -> "/" (Python expanduser: userhome="" then `or "/"`).
	os.Setenv("HOME", "")
	if got := home(); got != "/" {
		t.Errorf("home() with HOME='' = %q, want /", got)
	}
	if got := GlobalStorage(); got != "/.local/share/yolo-jail" {
		t.Errorf("GlobalStorage with empty HOME = %q, want /.local/share/yolo-jail", got)
	}

	// $HOME unset -> passwd database home (Python pwd.getpwuid), which must be
	// absolute — never a relative path.
	os.Unsetenv("HOME")
	got := home()
	if got == "" || got[0] != '/' {
		t.Errorf("home() with HOME unset = %q, want an absolute passwd-db path", got)
	}
	// Sanity: it should match the current user's passwd home when available.
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if got != u.HomeDir {
			t.Errorf("home() unset = %q, want passwd home %q", got, u.HomeDir)
		}
	}
}

// The conventional local pack sits BESIDE config.jsonc, and is DERIVED from it. That is the
// whole argument for the location — user-scope yolo config already lives there, so the
// convention extends an existing one rather than inventing a second place to remember — and a
// pair of independently-spelled suffixes could drift apart while both looked right.
func TestLocalPackDirIsBesideTheUserConfig(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	if got, want := LocalPackDir(), "/home/someone/.config/yolo-jail/local"; got != want {
		t.Errorf("LocalPackDir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(LocalPackDir()), filepath.Dir(UserConfigPath()); got != want {
		t.Errorf("LocalPackDir's parent %q is not the user config's dir %q — the convention is "+
			"\"beside config.jsonc\", so the two must share a parent by construction", got, want)
	}
	// Absolute even in a stripped environment, like every other path helper (see
	// TestHomeResolution): a relative pack root would be resolved against the process's cwd.
	t.Setenv("HOME", "")
	if got := LocalPackDir(); got[0] != '/' {
		t.Errorf("LocalPackDir with an empty HOME = %q, want an absolute path", got)
	}
}
