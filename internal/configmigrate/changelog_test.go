package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A migration that moves a link or writes a file leaves one durable line per file in the host's
// config-changes.log: when, which command, the file, the link text moved and the new regular file.
// The owner's config stayed a detached copy for days and no log said when or by what it happened.
func TestApplyRecordsEachMovedLinkInTheConfigChangesLog(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	dots := filepath.Join(home, "dotfiles", "yolo")
	write(t, filepath.Join(dots, "config.jsonc"), `{"packs": ["claude"]}`+"\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "../../dotfiles/yolo/config.jsonc"
	if err := os.Symlink(text, filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	p := plan(t, Request{UserConfigDir: dir})
	if _, err := Apply(p, ApplyOptions{Command: "yolo config migrate"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(paths.LogsDir(), ConfigChangesLogName))
	if err != nil {
		t.Fatalf("no config-changes log: %v", err)
	}
	log := string(data)
	for _, want := range []string{"yolo config migrate", filepath.Join(dir, "config.jsonc"), "link to " + text,
		"kept as " + filepath.Join(dir, "config.jsonc.pre-migrate"), "regular file " + filepath.Join(dir, "config.toml")} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if n := strings.Count(strings.TrimSpace(log), "\n") + 1; n != 1 {
		t.Errorf("want one line for the one file, got %d:\n%s", n, log)
	}
}

// A log that cannot be written never fails the migration: the files are converted all the same.
func TestAnUnwritableConfigChangesLogDoesNotFailTheMigration(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["claude"]}`+"\n")
	// The logs directory is a regular file, so nothing can be created under it.
	write(t, paths.LogsDir(), "not a directory")
	p := plan(t, Request{UserConfigDir: dir})
	if _, err := Apply(p, ApplyOptions{Command: "yolo config migrate"}); err != nil {
		t.Fatalf("a log write failure failed the migration: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "config.toml")); err != nil {
		t.Fatalf("the migration did not write config.toml: %v", err)
	}
}
