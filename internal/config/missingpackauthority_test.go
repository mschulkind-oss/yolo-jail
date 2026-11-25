package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// remoteSource is a pack_sources body naming a remote git pack source that no store holds.
const unfetchedRemote = "[pack_sources.rem]\ngit = 'https://example.invalid/acme/tools'\n\n"

// A selected pack whose artifact is not in the pack store (a remote never fetched) declares
// nothing the broker can see, so it must not withdraw git's authority: 2026-11-25, a branch-tracking
// `tillr` git pack that was not cached made every config-covered push ask and `yolo approve`
// refuse ("selection unknown"). The note names the pack, the resolver's exact error and the next
// step.
func TestAnUnfetchedRemotePackDoesNotWithdrawABrokersAuthority(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)

	kinds, canonical, notes, err := BrokeredGrantKindsWithNotes("git")
	if err != nil || !canonical || !slices.Contains(kinds, "branch") {
		t.Fatalf("kinds = %v, canonical %t, err %v; want the branch grant despite the unfetched pack", kinds, canonical, err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %q; want exactly one", notes)
	}
	for _, want := range []string{"rem/tools", "no matching default-branch pin", "yolo pack install", "yolo pack remove"} {
		if !strings.Contains(notes[0], want) {
			t.Fatalf("note %q lacks %q (the pack, the resolver's own error, the next steps)", notes[0], want)
		}
	}
	if kinds, _, err := BrokeredGrantKindsForRepo("git", "github.com/o/r"); err != nil || !slices.Contains(kinds, "branch") {
		t.Fatalf("the per-repo reader kinds = %v, err %v; want the same authority", kinds, err)
	}

	// The approval hook's reader agrees: the selection is complete, and the note is there.
	packs, notes, problems, complete := UserScopeSelectedPacksWithNotes()
	if !complete || len(problems) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "default-branch pin") {
		t.Fatalf("approval selection: complete %t problems %q notes %q; want complete with one note", complete, problems, notes)
	}
	var sawGit bool
	for _, p := range packs {
		sawGit = sawGit || p.Name == "git"
	}
	if !sawGit {
		t.Fatalf("approval selection lost the shipped git pack: %d packs", len(packs))
	}
}

// The same with an explicit ref: the shape of a branch-tracking pack, which is what the owner's host held.
func TestAnUnfetchedRemotePackWithAnExplicitRefDoesNotWithdrawAuthority(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", "[pack_sources.rem]\ngit = 'https://example.invalid/acme/tools'\nref = 'main'\n\n")
	kinds, _, notes, err := BrokeredGrantKindsWithNotes("git")
	if err != nil || !slices.Contains(kinds, "branch") || len(notes) != 1 || !strings.Contains(notes[0], "never been fetched") {
		t.Fatalf("kinds = %v notes %q err %v; want the grant and a note carrying the store's own error", kinds, notes, err)
	}
}

// An omitted-ref pack whose lock pin names a commit the store lacks, and one with no pin at all,
// are missing artifacts too (the fetch a launch makes is what repairs them).
func TestAPackWhoseLockedCommitIsNotInTheStoreDoesNotWithdrawAuthority(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
	if kinds, _, notes, err := BrokeredGrantKindsWithNotes("git"); err != nil || !slices.Contains(kinds, "branch") || len(notes) != 1 {
		t.Fatalf("no pin: kinds = %v notes %q err %v; want the grant and one note", kinds, notes, err)
	}

	entries, err := LoadPacks(func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	var remote PackEntry
	for _, e := range entries {
		if strings.Contains(e.Source, "example.invalid") {
			remote = e
		}
	}
	if remote.Name == "" {
		t.Fatalf("no remote entry among %+v", entries)
	}
	lock := &packsrc.Lock{}
	lock.Set(packsrc.LockEntry{Name: remote.Name, Source: remote.Source, Ref: "main", Commit: strings.Repeat("a", 40)})
	if err := lock.Save(packsrc.LockPath(paths.UserConfigPath())); err != nil {
		t.Fatal(err)
	}
	kinds, _, notes, err := BrokeredGrantKindsWithNotes("git")
	if err != nil || !slices.Contains(kinds, "branch") || len(notes) != 1 {
		t.Fatalf("pinned to a commit the store lacks: kinds = %v notes %q err %v; want the grant and one note", kinds, notes, err)
	}
}

// What can bear on git's policy still fails closed, for a missing remote exactly as for a present
// one: a pack named like a shipped pack (a present copy would shadow it), the source's own
// declaring pack, a present but invalid manifest, a profile the selection names that no readable
// pack declares (it may live in the missing pack), and the user's own `allow.git`.
func TestAMissingPackStillFailsClosedWhereItCouldBearOnGit(t *testing.T) {
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
		"an unfetched pack named like a shipped pack":  {"['git', 'rem/github']", unfetchedRemote},
		"the declaring pack is an unfetched remote":    {"['rem/git']", unfetchedRemote},
		"an invalid manifest beside an unfetched pack": {"['git', 'rem/tools', 'src/broken']", unfetchedRemote + local},
	} {
		t.Run(name, func(t *testing.T) {
			writeGitUserConfigWithPacks(t, c[0], c[1])
			kinds, canonical, err := BrokeredGrantKindsUsingReadablePolicy("git")
			if err == nil || !canonical || len(kinds) != 0 {
				t.Fatalf("kinds = %v, canonical %t, err %v; want a canonical error and no authority", kinds, canonical, err)
			}
			if _, _, _, complete := UserScopeSelectedPacksWithNotes(); complete {
				t.Fatalf("the approval selection reported itself complete")
			}
		})
	}

	t.Run("an active provider no readable pack declares", func(t *testing.T) {
		writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
		appendUserConfig(t, "[active_provider]\ndefault = 'ghost'\n")
		kinds, _, err := BrokeredGrantKindsUsingReadablePolicy("git")
		if err == nil || !strings.Contains(err.Error(), "ghost") || len(kinds) != 0 {
			t.Fatalf("kinds = %v err %v; want a refusal naming the provider", kinds, err)
		}
	})
	t.Run("an invalid allow.git", func(t *testing.T) {
		writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
		appendUserConfig(t, "[allow.git.nonsense]\n")
		if kinds, _, err := BrokeredGrantKindsUsingReadablePolicy("git"); err == nil || len(kinds) != 0 {
			t.Fatalf("kinds = %v err %v; want the invalid allow.git to refuse", kinds, err)
		}
	})
}

// appendUserConfig adds TOML to the user config writeGitUserConfigWithPacks wrote.
func appendUserConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml")
	prior, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(prior, []byte(body)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A legacy `profile` selection naming a profile that only a pack that could not be read could declare
// refuses the read, and with nothing skipped an unknown profile is what it always was.
func TestASkippedPackMayHoldTheSelectedProfile(t *testing.T) {
	user := jsonx.NewOrderedMap()
	user.Set("profile", "ghost")
	if err := skippedProfileProblem(user, nil, nil, []string{"rem/tools"}); err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "rem/tools") {
		t.Fatalf("err = %v; want a refusal naming the profile and the unread pack", err)
	}
	if err := skippedProfileProblem(user, nil, map[string]packload.UserProfile{"ghost": {Provider: "p"}}, []string{"rem/tools"}); err != nil {
		t.Fatalf("a profile the user declares themselves: err = %v", err)
	}
	if err := skippedProfileProblem(user, nil, nil, nil); err != nil {
		t.Fatalf("with nothing skipped: err = %v", err)
	}
}
