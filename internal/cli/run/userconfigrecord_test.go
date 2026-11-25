package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The host launch record watches the user config file actually in use (config.toml), says whether
// it is a link and the link's text, and shows a link that became a regular file as CHANGED. It
// pins RecordContext, the call site, not only the helper: on a TOML host the record used to name
// the legacy config.jsonc as "(absent)" on every launch.
func TestHostLaunchRecordShowsAUserConfigLinkBecomingARegularFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dots := filepath.Join(home, "dotfiles", "config.toml")
	if err := os.MkdirAll(filepath.Dir(dots), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dots, []byte("packs = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.toml")
	linkText := "../../dotfiles/config.toml"
	if err := os.Symlink(linkText, cfg); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(home, "code", "proj")

	launch := func() string {
		l := OpenHostLaunchLog(ws, "claude")
		if l == nil {
			t.Fatal("the log did not open")
		}
		l.RecordContext(HostLaunchContext{Workspace: ws, Profile: "none", Config: jsonx.NewOrderedMap()})
		l.Done(0)
		data, err := os.ReadFile(HostLaunchLogPath())
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, ln := range strings.Split(string(data), "\n") {
			if strings.Contains(ln, "config files:") {
				lines = append(lines, ln)
			}
		}
		if len(lines) == 0 {
			t.Fatalf("no config files record:\n%s", data)
		}
		return lines[len(lines)-1]
	}

	first := launch()
	if !strings.Contains(first, "config.toml") || strings.Contains(first, "config.jsonc") {
		t.Fatalf("the record does not name the config in use:\n%s", first)
	}
	if !strings.Contains(first, "symlink to "+linkText) {
		t.Fatalf("the record does not say it is a link, with its text:\n%s", first)
	}

	// The link is replaced by a regular file with the same bytes, as a migration does.
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("packs = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := launch()
	if !strings.Contains(second, "CHANGED") || !strings.Contains(second, "regular file") ||
		!strings.Contains(second, "symlink to "+linkText) {
		t.Fatalf("a link becoming a regular file is not shown as CHANGED, was a link:\n%s", second)
	}
}
