package paths

import (
	"os"
	"os/user"
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
