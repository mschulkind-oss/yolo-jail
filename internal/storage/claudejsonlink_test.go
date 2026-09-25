package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// claudejsonlink_test.go pins that the seed sync never reads or writes THROUGH a link.
//
// The workspace side of SyncClaudeJSONSeed is a path the jail can write: wsState/claude/
// claude.json is bound read-write at ~/.claude/claude.json on podman, and on Apple Container
// wsState/.claude.json IS the jail's ~/.claude.json. The sync runs on the HOST, as the host
// user, on the next fresh launch. So a jail that replaced that file with a link chose which
// host file the host would read (the reverse pass then copies whatever login keys it holds
// into the machine seed) and which host file it would overwrite (the forward pass rewrote
// the link's target with the seed's JSON). docs/design/base-home-legacy-state.md#22-where-it-lives-host-only-never-in-wsstate names
// jail-planted links in wsState as the hazard.

// writeLinkFixture and readLinkFixture are this file's own, so it builds without any other
// test file in the package.
func writeLinkFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readLinkFixture(t *testing.T, path string) *jsonx.OrderedMap {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, err := jsonx.Decode(data)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("%s is not a JSON object", path)
	}
	return m
}

const loggedInSeed = `{"oauthAccount": {"emailAddress": "seed@example.invalid"}, "hasCompletedOnboarding": true}`

// A link at the workspace path is left alone: its target is neither read nor written, and
// nothing is created at a dangling link's target.
func TestTheSeedSyncNeverWritesThroughALink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		victim string // the link target's content, or "" for a dangling link
	}{
		{"a host file that is not JSON", "ssh-ed25519 AAAA host-user-key\n"},
		{"a host JSON file", `{"theme": "dark"}` + "\n"},
		{"a dangling link", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seed := filepath.Join(dir, "machine", ".claude", "claude.json")
			writeLinkFixture(t, seed, loggedInSeed)
			victim := filepath.Join(dir, "host", "victim")
			if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.victim != "" {
				if err := os.WriteFile(victim, []byte(tc.victim), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ws := filepath.Join(dir, "ws", "claude", "claude.json")
			if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, ws); err != nil {
				t.Fatal(err)
			}

			SyncClaudeJSONSeed(seed, ws)

			// The link takes no part in the sync, so it is not replaced either.
			if fi, err := os.Lstat(ws); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the workspace link was replaced (%v, err %v); a side that is not a "+
					"regular file is left alone", fi.Mode(), err)
			}
			got, err := os.ReadFile(victim)
			switch {
			case tc.victim == "" && err == nil:
				t.Errorf("the sync created the dangling link's target %s: %q", victim, got)
			case tc.victim != "" && string(got) != tc.victim:
				t.Errorf("the sync wrote through the workspace link into %s: %q, want it unchanged",
					victim, got)
			}
		})
	}
}

// ...and a login read through a link is not learned: the reverse pass would copy whatever
// oauthAccount the link's target holds (the host's own ~/.claude.json, say) into the seed,
// and from there into every new workspace.
func TestTheSeedDoesNotLearnALoginThroughALink(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "machine", ".claude", "claude.json")
	writeLinkFixture(t, seed, `{}`)
	hostFile := filepath.Join(dir, "host", ".claude.json")
	writeLinkFixture(t, hostFile, `{"oauthAccount": {"emailAddress": "host@example.invalid"}}`)
	ws := filepath.Join(dir, "ws", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostFile, ws); err != nil {
		t.Fatal(err)
	}

	SyncClaudeJSONSeed(seed, ws)

	if _, ok := readLinkFixture(t, seed).Get("oauthAccount"); ok {
		t.Error("the seed learned an oauthAccount read through a link the workspace planted")
	}
}

// The seed's own side gets the same treatment: a link there is neither read nor replaced.
func TestTheSeedSyncNeverWritesThroughASeedLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "host", "victim")
	writeLinkFixture(t, victim, "not json\n")
	seed := filepath.Join(dir, "machine", ".claude", "claude.json")
	if err := os.MkdirAll(filepath.Dir(seed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, seed); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(dir, "ws", "claude", "claude.json")
	writeLinkFixture(t, ws, `{"oauthAccount": {"emailAddress": "ws@example.invalid"}}`)

	SyncClaudeJSONSeed(seed, ws)

	if got, _ := os.ReadFile(victim); string(got) != "not json\n" {
		t.Errorf("the reverse pass wrote through the seed link into %s: %q", victim, got)
	}
	if fi, err := os.Lstat(seed); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the seed link was replaced (%v, err %v); a side that is not a regular file "+
			"is left alone", fi.Mode(), err)
	}
}

// The ordinary case still works after the write became a replace: a regular workspace file
// is updated in place, keeping its own keys and its mode.
func TestTheSeedSyncStillUpdatesARegularFile(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "machine", ".claude", "claude.json")
	writeLinkFixture(t, seed, loggedInSeed)
	ws := filepath.Join(dir, "ws", "claude", "claude.json")
	writeLinkFixture(t, ws, `{"projects": {"/workspace": {}}}`)
	if err := os.Chmod(ws, 0o600); err != nil {
		t.Fatal(err)
	}

	SyncClaudeJSONSeed(seed, ws)

	got := readLinkFixture(t, ws)
	for _, key := range []string{"oauthAccount", "projects"} {
		if _, ok := got.Get(key); !ok {
			t.Errorf("the workspace file lost or never got %q: keys %v", key, got.Keys())
		}
	}
	if fi, err := os.Lstat(ws); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Errorf("the workspace file is %v (err %v), want a regular file keeping mode 0600", fi.Mode(), err)
	}
	// A fresh workspace (no file yet) is created.
	fresh := filepath.Join(dir, "fresh", "claude", "claude.json")
	SyncClaudeJSONSeed(seed, fresh)
	if _, ok := readLinkFixture(t, fresh).Get("oauthAccount"); !ok {
		t.Error("a new workspace's claude.json was not created from the seed")
	}
	entries, _ := os.ReadDir(filepath.Dir(ws))
	if len(entries) != 1 {
		t.Errorf("the write left a temp file beside claude.json: %v", entries)
	}
}
