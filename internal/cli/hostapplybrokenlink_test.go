package cli

// hostapplybrokenlink_test.go pins the broken-link rule's REPORT at the command
// (entrypoint/hostbrokenlink.go is the render half).
//
// Measured on the maintainer's host 2026-09-28: ~/.pi/agent/settings.json was an rcm link into a
// deleted ~/.dotfiles/pi. The apply printed `yolo host apply: pi: pi/settings: open …: no such
// file or directory` to stderr in the middle of the report, skipped every other pi surface, and
// ended "Incomplete — 1 pack(s) failed to render (pi); see stderr." — and the launch gate then
// refused to start claude over it.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// brokenLinkHome is a home with claude and pi selected and pi's settings.json linked into a
// directory that does not exist. Returns the home and the link's target.
func brokenLinkHome(t *testing.T, extra string) (string, string) {
	t.Helper()
	home := t.TempDir()
	return home, brokenLinkHomeAt(t, home, `"claude","pi"`, extra)
}

// brokenLinkHomeAt is brokenLinkHome over a given home and pack list; returns the link's target.
//
// The user config declares `host_management: "own"`, the one contract that renders: the unset
// key is `none` since the `assert` retirement (OQ-CO14), and a home nothing renders into has no
// destination for the broken-link rule to stop at.
func brokenLinkHomeAt(t *testing.T, home, packs, extra string) string {
	t.Helper()
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[`+packs+`],"host_management":"own"`+extra+`}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	stubDeclaredBins(t)
	link := filepath.Join(home, ".pi", "agent", "settings.json")
	target := filepath.Join(home, ".dotfiles", "pi", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestABrokenLinkIsReportedOnceWithItsFixAndTheRestApplies(t *testing.T) {
	home, target := brokenLinkHome(t, "")
	rc, report := applyWith(t, true, nil)
	if rc != 1 {
		t.Errorf("rc=%d; an --assert that could not write a destination did not complete (OQ-RO5)\n%s",
			rc, report)
	}
	for _, want := range []string{
		"not written: ~/.pi/agent/settings.json is a symlink to ~/.dotfiles/pi/settings.json, " +
			"whose directory does not exist",
		"rm ~/.pi/agent/settings.json",
		"Incomplete — some of pi's config was not written",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
	if n := strings.Count(report, "~/.pi/agent/settings.json is a symlink"); n != 1 {
		t.Errorf("the broken link is stated %d times; once is the rule:\n%s", n, report)
	}
	for _, never := range []string{"see stderr", "no such file or directory", "failed to render"} {
		if strings.Contains(report, never) {
			t.Errorf("the report says %q:\n%s", never, report)
		}
	}
	// The rest of the pack set applied: claude's settings and another pi surface exist, and
	// nothing was created at the link's target.
	for _, rel := range []string{".claude/settings.json", ".pi/agent/models.json"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("%s was not written: %v\n%s", rel, err, report)
		}
	}
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Errorf("the apply recreated the link's missing directory (stat: %v)", err)
	}

	// The dry run says the same thing, exits 0 (it is information), and names what an
	// --assert would leave out.
	rc, dry := applyWith(t, false, nil)
	if rc != 0 {
		t.Errorf("dry run rc=%d, want 0\n%s", rc, dry)
	}
	if !strings.Contains(dry, "An --assert would be incomplete — some of pi's config cannot be written") ||
		!strings.Contains(dry, "~/.pi/agent/settings.json is a symlink to") {
		t.Errorf("the dry run does not report the broken link:\n%s", dry)
	}
}

// A PLAIN MISSING DESTINATION is not a failure of any kind: the apply creates it, parent
// directories included, and completes.
func TestAMissingDestinationIsCreatedNotFailed(t *testing.T) {
	home, _ := brokenLinkHome(t, "")
	if err := os.RemoveAll(filepath.Join(home, ".pi")); err != nil {
		t.Fatal(err)
	}
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("rc=%d over a home with no ~/.pi at all\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "settings.json")); err != nil {
		t.Errorf("pi/settings was not created: %v\n%s", err, report)
	}
}

// The broken link's remedy is for pasting, so the link has to come back out of a shell as ONE
// word, the tilde still expanding. A pack surface may sit under a path with a space in it, as a
// macOS app's settings do under ~/Library/Application Support; printed bare, the `rm` removed
// the path's first word.
func TestABrokenLinkRemedyNamesTheLinkAsOneShellWord(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Jane Doe")
	t.Setenv("HOME", home)
	link := filepath.Join(home, "Library", "Application Support", "Acme", "settings.json")
	var s hostApplySurvey
	s.noteBrokenLink([]string{"acme"}, entrypoint.BrokenLink{
		Link: link, Target: filepath.Join(home, ".dotfiles", "acme", "settings.json")})
	groups := failureGroups(&s, home, true)
	if len(groups) != 1 {
		t.Fatalf("failureGroups = %+v, want the one broken-link group", groups)
	}
	cmd, _, ok := strings.Cut(groups[0].Remedy, "   (or recreate")
	if !ok {
		t.Fatalf("the remedy offers no `rm <link>`: %q", groups[0].Remedy)
	}
	if got := testsupport.ShellWords(t, cmd); !slices.Equal(got, []string{"rm", link}) {
		t.Errorf("the remedy's %q reads as %q in a shell, want [rm %q]", cmd, got, link)
	}
}
