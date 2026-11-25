package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The launch's offer says it was the offer in the durable config-changes log, for each file a
// yes wrote: the call site's Command, not only the logger.
func TestTheOffersYesIsRecordedInTheConfigChangesLog(t *testing.T) {
	l, userPath := runCeilingOffer(t, "y\ny\n")
	l.mustLaunch(t)
	data, err := os.ReadFile(filepath.Join(paths.LogsDir(), configmigrate.ConfigChangesLogName))
	if err != nil {
		t.Fatalf("a yes left no config-changes log: %v", err)
	}
	if !strings.Contains(string(data), "launch offer") || !strings.Contains(string(data), userPath) {
		t.Errorf("the log does not name the launch offer and %s:\n%s", userPath, data)
	}
}

// A linked user config the conversion rewrites for its ceiling lines is disclosed before the
// question as what it is: a link that is replaced by a regular file, the link kept as
// <name>.pre-migrate, and why it is touched at all. The owner answered y to an offer that named
// only the workspace file and found the dotfiles link detached (2026-11-18).
func TestTheOfferSaysAUserConfigLinkIsReplacedByARegularFile(t *testing.T) {
	var userPath, target string
	l := runOfferLaunchWith(t, "{}\n", true, "", func(o *Options) {
		o.Stdin = &lineReader{lines: []string{"n\n"}}
		home := os.Getenv("HOME")
		userPath = filepath.Join(home, ".config", "yolo-jail", "config.toml")
		if err := os.Remove(filepath.Join(filepath.Dir(userPath), "config.jsonc")); err != nil {
			t.Fatal(err)
		}
		target = filepath.Join(home, "dotfiles", "config.toml")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(ceilingUserTOML), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, userPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(o.Workspace, "yolo-jail.jsonc"),
			[]byte(`{"loopholes": {"audio": {"enabled": true}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	s := l.stderr.String()
	q := strings.Index(s, "Convert to TOML now?")
	if q < 0 {
		t.Fatalf("no question:\n%s", s)
	}
	before := s[:q]
	for _, want := range []string{userPath + " is a link to " + target, "regular file", userPath + ".pre-migrate"} {
		if !strings.Contains(before, want) {
			t.Errorf("the offer does not say %q before the question:\n%s", want, before)
		}
	}
	if info, err := os.Lstat(userPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("a no changed the link: %v", err)
	}
}
