package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// writeGitUserConfigWithPacks writes a user config selecting packs (a TOML array body) beside a
// canonical `allow.git` that grants the push capability on request.
func writeGitUserConfigWithPacks(t *testing.T, packs, sources string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "packs = " + packs + "\n" + sources + "[allow.git]\nalways = ['fetch']\non_request = ['push']\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(packload.ReleaseEmbedded)
}

// A selected pack whose LOCAL source directory is gone declares nothing, so it cannot bear on a
// broker's write authority and must not withdraw it (2026-11-24: a removed `tillr` source cost a
// workspace its standing git push grant). Repairing or deselecting the pack changes the very
// next read, since each read resolves the selection afresh.
func TestAnUnrelatedPackWhoseLocalSourceIsGoneDoesNotWithdrawABrokersAuthority(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "gone")
	writeGitUserConfigWithPacks(t, "['git', 'src/gone']", "[pack_sources]\nsrc = '"+root+"'\n\n")

	kinds, canonical, notes, err := BrokeredGrantKindsWithNotes("git")
	if err != nil || !canonical || !slices.Contains(kinds, "branch") {
		t.Fatalf("kinds = %v, canonical %t, err %v; want the branch grant despite the vanished pack", kinds, canonical, err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "gone") || !strings.Contains(notes[0], gone) || !strings.Contains(notes[0], "yolo pack remove") {
		t.Fatalf("notes = %q; want one naming the pack, its missing source and the next step", notes)
	}
	if kinds, _, err := BrokeredGrantKindsUsingReadablePolicy("git"); err != nil || !slices.Contains(kinds, "branch") {
		t.Fatalf("the plain reader kinds = %v, err %v; want the same authority", kinds, err)
	}
	if kinds, _, err := BrokeredGrantKindsForRepo("git", "github.com/o/r"); err != nil || !slices.Contains(kinds, "branch") {
		t.Fatalf("the per-repo reader kinds = %v, err %v; want the same authority", kinds, err)
	}

	// The directory comes back with a manifest the reader cannot parse: it could declare anything,
	// so the read fails closed (the resolver's own error, not a skip).
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(gone, "pack.toml")
	if err := os.WriteFile(manifest, []byte("this is [not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kinds, _, notes, err := BrokeredGrantKindsWithNotes("git"); err == nil {
		t.Fatalf("an unreadable local pack: kinds = %v notes = %q; want an error", kinds, notes)
	}
	// Repaired: the very next read has the grant and nothing to disclose.
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if kinds, _, notes, err := BrokeredGrantKindsWithNotes("git"); err != nil || !slices.Contains(kinds, "branch") || len(notes) != 0 {
		t.Fatalf("after repair: kinds = %v notes = %q err %v; want the grant and no note", kinds, notes, err)
	}
	// Deselected: the next read restores the authority with nothing to disclose.
	writeGitUserConfigWithPacks(t, "['git']", "")
	kinds, _, notes, err = BrokeredGrantKindsWithNotes("git")
	if err != nil || !slices.Contains(kinds, "branch") || len(notes) != 0 {
		t.Fatalf("after deselecting: kinds = %v notes = %q err %v; want the grant and no note", kinds, notes, err)
	}
}

// What can bear on the source's policy still fails closed: a pack that fails to resolve for any
// reason but a vanished local directory might declare the source, and a vanished one that is
// named like a shipped pack might shadow the declarer.
func TestAnUnresolvedPackThatMightDeclareStillWithdrawsABrokersAuthority(t *testing.T) {
	root := t.TempDir()
	broken := filepath.Join(root, "broken")
	if err := os.MkdirAll(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "pack.toml"), []byte("this is [not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := "[pack_sources]\nsrc = '" + root + "'\n\n"
	for name, c := range map[string][2]string{
		"a local pack with an invalid manifest": {"['git', 'src/broken']", local},
		"a vanished pack shadowing a shipped":   {"['git', 'src/github']", local},
		"the declaring pack itself is missing":  {"['src/git']", local},
	} {
		t.Run(name, func(t *testing.T) {
			writeGitUserConfigWithPacks(t, c[0], c[1])
			kinds, canonical, err := BrokeredGrantKindsUsingReadablePolicy("git")
			if err == nil || !canonical || len(kinds) != 0 {
				t.Fatalf("kinds = %v, canonical %t, err %v; want a canonical error and no authority", kinds, canonical, err)
			}
		})
	}
}
