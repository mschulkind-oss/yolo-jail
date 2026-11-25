package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// A user config that is a regular file with a <name>.pre-migrate symlink beside it is a config
// `yolo config migrate` detached from its link's target: the check warns, naming the two-command
// restore, and never acts. A plain regular file or a live link says nothing.
func TestADetachedUserConfigIsAWarningNamingTheRestore(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("packs = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func() (string, int) {
		var buf strings.Builder
		r := newReporter(&buf, false)
		(&Options{}).reportDetachedConfig(r, cfg)
		return buf.String(), r.warned
	}
	if out, n := run(); n != 0 || out != "" {
		t.Fatalf("a plain regular file warned (%d):\n%s", n, out)
	}
	if err := os.Symlink("../dotfiles/yolo/config.toml", cfg+".pre-migrate"); err != nil {
		t.Fatal(err)
	}
	out, n := run()
	if n != 1 {
		t.Fatalf("warned %d, want 1:\n%s", n, out)
	}
	for _, want := range []string{"../dotfiles/yolo/config.toml", "mv " + shquote.Quote(cfg) + " " + shquote.Quote(cfg+".detached"),
		"mv " + shquote.Quote(cfg+".pre-migrate") + " " + shquote.Quote(cfg)} {
		if !strings.Contains(out, want) {
			t.Errorf("the finding lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Lstat(cfg + ".detached"); err == nil {
		t.Error("the check acted: it moved the config")
	}

	// A live link (config is the symlink) is not detached, whatever sits beside it.
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../dotfiles/yolo/config.toml", cfg); err != nil {
		t.Fatal(err)
	}
	if out, n := run(); n != 0 || out != "" {
		t.Fatalf("a live link warned (%d):\n%s", n, out)
	}
}

// The finding is reached from the config section both `yolo check` and `yolo config check` run:
// deleting the call fails this.
func TestTheConfigFilesSectionCallsTheDetachedConfigFinding(t *testing.T) {
	src, err := os.ReadFile("check.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "o.reportDetachedConfig(r, userPath)") {
		t.Error("sectionConfigFilesWithStrictness no longer calls reportDetachedConfig")
	}
}
