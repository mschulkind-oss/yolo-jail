package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The owner's report of 2026-11-06: yolo-jail.jsonc was a link a side-file tool keeps, `yolo
// config migrate` moved the link to yolo-jail.jsonc.pre-migrate and wrote yolo-jail.toml, and the
// tool re-created the link. The launch then refused with one unreadable line of guarded shell.
// The refusal now says what happened and gives the one step, alone on its own line.
func TestAJSONCThatCameBackAfterMigrationNamesTheOneRemoval(t *testing.T) {
	dir := evalTempDir(t)
	write(t, filepath.Join(dir, "swarf", ".links", "yolo-jail.jsonc"), `{"packages":["legacy"]}`)
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['new']\n")
	legacy := filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)
	for _, p := range []string{legacy + ".pre-migrate", legacy} {
		if err := os.Symlink("swarf/.links/yolo-jail.jsonc", p); err != nil {
			t.Fatal(err)
		}
	}

	_, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err == nil {
		t.Fatal("the loader accepted a TOML beside the JSONC it was converted from")
	}
	msg := err.Error()
	for _, want := range []string{
		legacy + " came back after you migrated to yolo-jail.toml",
		"yolo-jail.jsonc.pre-migrate",
		"\n    rm " + shquote.Quote(legacy) + "\n",
		"symbolic link to swarf/.links/yolo-jail.jsonc",
		"swarf/.links/yolo-jail.jsonc is untouched",
		"re-creat",
		"Then rerun the command",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	assertReadableRefusal(t, msg)
	if strings.Contains(msg, "mv ") || strings.Contains(msg, "To keep") {
		t.Errorf("a JSONC that came back is offered a two-way choice instead of its one removal:\n%s", msg)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Fact == "" || !strings.Contains(ce.Step, "rm "+shquote.Quote(legacy)) {
		t.Errorf("refusal carries no fact and step for `yolo check`: %#v", ce)
	}
}

// A regular file byte-identical to the original migrate kept is the same case, with no link note.
func TestARegularJSONCIdenticalToItsPreMigrateCameBack(t *testing.T) {
	dir := evalTempDir(t)
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['new']\n")
	legacy := filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)
	write(t, legacy+".pre-migrate", `{"packages":["legacy"]}`)
	write(t, legacy, `{"packages":["legacy"]}`)

	_, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err == nil {
		t.Fatal("collision accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "came back after you migrated") || !strings.Contains(msg, "\n    rm "+shquote.Quote(legacy)+"\n") {
		t.Errorf("an identical copy of the migrated original is not recognized:\n%s", msg)
	}
	if strings.Contains(msg, "symbolic link") {
		t.Errorf("a regular file is described as a link:\n%s", msg)
	}
	assertReadableRefusal(t, msg)
}

// Two files with nothing tying the JSONC to a migration is the true choice: two ways out, each
// command alone on its own line, a backup name that does not exist yet.
func TestAnAmbiguousCollisionOffersTwoReadableChoices(t *testing.T) {
	dir := evalTempDir(t)
	toml := filepath.Join(dir, paths.WorkspaceConfigTOMLName)
	legacy := filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)
	write(t, toml, "[install]\nnix = ['new']\n")
	write(t, legacy, `{"packages":["legacy"]}`)
	write(t, legacy+".pre-migrate", `{"packages":["something else"]}`)
	write(t, legacy+".backup", "taken")

	_, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err == nil {
		t.Fatal("collision accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		dir + " has both yolo-jail.toml and yolo-jail.jsonc",
		"To keep yolo-jail.toml:\n    mv -n " + shquote.Quote(legacy) + " " + shquote.Quote(legacy+".backup-2") + "\n",
		"To keep yolo-jail.jsonc:\n    mv -n " + shquote.Quote(toml) + " " + shquote.Quote(toml+".backup") + "\n",
		"Then rerun the command",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "came back") {
		t.Errorf("a different JSONC is called the migrated one:\n%s", msg)
	}
	assertReadableRefusal(t, msg)
}

// A linked file in the two-way choice says moving the link leaves its target alone.
func TestAnAmbiguousCollisionWithALinkSaysTheTargetIsUntouched(t *testing.T) {
	dir := evalTempDir(t)
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['new']\n")
	write(t, filepath.Join(dir, "dots", "y.jsonc"), `{"packages":["legacy"]}`)
	if err := os.Symlink("dots/y.jsonc", filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)); err != nil {
		t.Fatal(err)
	}
	_, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err == nil {
		t.Fatal("collision accepted")
	}
	if msg := err.Error(); !strings.Contains(msg, "yolo-jail.jsonc is a symbolic link to dots/y.jsonc; moving it leaves dots/y.jsonc untouched") {
		t.Errorf("the link note is missing:\n%s", msg)
	}
}

// assertReadableRefusal is the shape the owner asked for: no inline shell one-liner, and every
// command alone on its own indented line.
func assertReadableRefusal(t *testing.T, msg string) {
	t.Helper()
	for _, bad := range []string{"if [", "&&", "printf", "exit 1", "`"} {
		if strings.Contains(msg, bad) {
			t.Errorf("refusal still carries shell plumbing %q:\n%s", bad, msg)
		}
	}
	for _, line := range strings.Split(msg, "\n") {
		if (strings.Contains(line, "rm /") || strings.Contains(line, "rm '") || strings.Contains(line, "mv -n /") || strings.Contains(line, "mv -n '")) && !strings.HasPrefix(line, "    ") {
			t.Errorf("a command is inline in prose, not on its own line: %q", line)
		}
	}
}

func evalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
