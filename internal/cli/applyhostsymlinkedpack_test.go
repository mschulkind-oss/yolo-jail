package cli

// applyhostsymlinkedpack_test.go is the COMMAND-LEVEL half of "a pack deployed by a dotfile
// manager converges" — `yolo host apply --assert` run twice, over a pack whose skills and files
// are symlinks into a repo somewhere else.
//
// Why it cannot live in internal/hostskills. That suite proves the predicate and the copy agree;
// this one proves the property a user can observe, which is that the SECOND apply is a no-op over
// the WHOLE home. TestApplyHostIsWholeHomeIdempotent (applyhostidempotent_test.go) owns exactly
// that property and says why nothing else can: a kind's own suite narrows its snapshot to its own
// destinations. It selects the SHIPPED packs, none of which is symlink-deployed — so a home built
// out of links was outside every existing assertion, which is how this shipped.
//
// The shape is rcm/stow/chezmoi's: the pack directory holds links, the bytes live in a dotfiles
// repo, and both of yolo's host writers materialize through them.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// symlinkedPackFixture builds the dotfile-manager deployment: a dotfiles repo holding the real
// content, and a pack whose `skills/` and `files/` entries are links into it.
//
// The links are ABSOLUTE, which is what every dotfile manager writes, and they point OUT of the
// pack — so nothing about the source tree can be measured by walking its own bytes.
func symlinkedPackFixture(t *testing.T) (home, packDir string) {
	t.Helper()
	home = t.TempDir()
	dotfiles := t.TempDir()
	packDir = filepath.Join(t.TempDir(), "dotpack")

	writeFile(t, filepath.Join(dotfiles, "skills", "reviewer", "SKILL.md"),
		"---\nname: reviewer\ndescription: d\n---\nBODY FROM THE DOTFILES REPO\n")
	writeFile(t, filepath.Join(dotfiles, "skills", "reviewer", "run.sh"), "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(dotfiles, "skills", "reviewer", "run.sh"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dotfiles, "bin", "pick.sh"), "#!/bin/sh\necho pick\n")

	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"dotpack","description":"d","contributes":[`+
			`{"kind":"skills","from":"skills","into":".codex/skills"},`+
			`{"kind":"files","from":"files","into":".codex/bin"}]}`)
	// The whole skill is one link (the field shape dangling.go reports), and the `files` tree
	// holds a linked FILE — the two halves fail differently and both must converge.
	if err := os.MkdirAll(filepath.Join(packDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "skills", "reviewer"),
		filepath.Join(packDir, "skills", "reviewer")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "bin", "pick.sh"),
		filepath.Join(packDir, "files", "pick.sh")); err != nil {
		t.Fatal(err)
	}

	selectPacks(t, home, `"codex",{"source":"file://`+packDir+`","name":"dotpack"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, packDir
}

// THE PROPERTY: apply 2 changes nothing, and says so. Before the fix both kinds compared the
// source's link TARGETS against the destination's CONTENT, so every apply re-rendered and
// re-archived what it had just written — and the whole skill, being a link to a DIRECTORY, was
// never delivered at all (`is a directory`).
func TestApplyHostConvergesOverASymlinkedPack(t *testing.T) {
	home, _ := symlinkedPackFixture(t)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	// The delivered form is real content the tools can read, not a pointer at the dotfiles repo.
	skill := filepath.Join(home, ".codex", "skills", "reviewer", "SKILL.md")
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("the symlinked skill must be delivered: %v\n%s", err, report)
	}
	if !strings.Contains(string(body), "BODY FROM THE DOTFILES REPO") {
		t.Errorf("delivered skill = %q, want the link's target", body)
	}
	if fi, lerr := os.Lstat(filepath.Dir(skill)); lerr != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the delivery must materialize, not re-link (%v)", lerr)
	}
	first := linkAwareHashes(t, home)

	rc, report = applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	var diffs []string
	for p, h := range linkAwareHashes(t, home) {
		if was, had := first[p]; !had {
			diffs = append(diffs, p+" (added)")
		} else if was != h {
			diffs = append(diffs, p+" (content changed)")
		}
	}
	sort.Strings(diffs)
	if len(diffs) != 0 {
		t.Errorf("a second apply over a SYMLINKED pack changed the home: %v\n%s", diffs, report)
	}
	// And it must SAY unchanged: a silent no-op that still reports `rendered` (and an archive
	// line with it) is the noise that buries the one line a user must not skip.
	if !strings.Contains(report, "unchanged") {
		t.Errorf("the second apply must report `unchanged`:\n%s", report)
	}
	if strings.Contains(report, "archived to") {
		t.Errorf("the second apply must archive nothing:\n%s", report)
	}
}
