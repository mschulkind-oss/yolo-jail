package mcpauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock is an injected clock whose Sleep advances time, so a lock wait ends on the
// clock's own event rather than on elapsed wall time.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time        { return c.now }
func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }
func newFakeClock() *fakeClock             { return &fakeClock{now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) source(dir string) SourceStore {
	return SourceStore{Dir: dir, File: ".credentials.json", LockDir: ".storage-write.lock", Stale: 15 * time.Second,
		Wait: 2 * time.Second, EntriesAt: []string{"mcpOAuth"}, Now: c.Now, Sleep: c.Sleep}
}

const claudeStore = `{"claudeAiOauth":{"accessToken":"login-a"},"mcpOAuth":{"plugin:slack:slack|abc":{"accessToken":"at-1","refreshToken":"rt-1","expiresAt":1000,"serverUrl":"https://mcp.slack.com/mcp"}}}`

func writeSource(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSourceReadNeverMistakesAnUnreadableStoreForASignOut(t *testing.T) {
	c := newFakeClock()
	for _, tc := range []struct {
		name, body string
		write      bool
		parsed     bool
		entries    int
	}{
		{"absent file", "", false, false, 0},
		{"malformed", "{not json", true, false, 0},
		{"entries map not an object", `{"mcpOAuth":[1]}`, true, false, 0},
		{"no mcpOAuth yet", `{"claudeAiOauth":{}}`, true, true, 0},
		{"one entry", claudeStore, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.write {
				writeSource(t, dir, tc.body)
			}
			entries, parsed, _ := c.source(dir).Read()
			if parsed != tc.parsed || len(entries) != tc.entries {
				t.Fatalf("Read = %d entries, parsed %v; want %d, %v", len(entries), parsed, tc.entries, tc.parsed)
			}
		})
	}
}

func TestSourceReadRefusesALinkedStore(t *testing.T) {
	c := newFakeClock()
	dir, elsewhere := t.TempDir(), t.TempDir()
	writeSource(t, elsewhere, claudeStore)
	if err := os.Symlink(filepath.Join(elsewhere, ".credentials.json"), filepath.Join(dir, ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	if _, parsed, err := c.source(dir).Read(); parsed || err == nil {
		t.Fatalf("a linked store was read (parsed %v, err %v)", parsed, err)
	}
	err := c.source(dir).Update(func(map[string]json.RawMessage) (bool, error) { return true, nil })
	if err == nil {
		t.Fatal("a linked store was written through")
	}
}

func TestSourceUpdateWritesUnderTheAgentsLockByRename(t *testing.T) {
	c := newFakeClock()
	dir := t.TempDir()
	writeSource(t, dir, claudeStore)
	before, _ := os.Stat(filepath.Join(dir, ".credentials.json"))
	sawLock := false
	err := c.source(dir).Update(func(entries map[string]json.RawMessage) (bool, error) {
		if fi, err := os.Lstat(filepath.Join(dir, ".storage-write.lock")); err == nil && fi.IsDir() {
			sawLock = true
		}
		entries["plugin:slack:slack|abc"] = json.RawMessage(`{"accessToken":"at-2","expiresAt":2000,"serverUrl":"https://mcp.slack.com/mcp"}`)
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawLock {
		t.Fatal("the update ran without Claude's .storage-write.lock held")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".storage-write.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the lock was not released: %v", err)
	}
	after, _ := os.Stat(filepath.Join(dir, ".credentials.json"))
	if os.SameFile(before, after) {
		t.Fatal("the store was rewritten in place; Claude's readers need a rename")
	}
	if after.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", after.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	var doc struct {
		Login map[string]any            `json:"claudeAiOauth"`
		MCP   map[string]map[string]any `json:"mcpOAuth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Login["accessToken"] != "login-a" {
		t.Fatal("the Claude login beside mcpOAuth was dropped")
	}
	if got := doc.MCP["plugin:slack:slack|abc"]; got["accessToken"] != "at-2" || got["refreshToken"] != nil {
		t.Fatalf("entry = %v", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSourceUpdateNeverWritesWithoutTheLock(t *testing.T) {
	c := newFakeClock()
	dir := t.TempDir()
	writeSource(t, dir, claudeStore)
	lock := filepath.Join(dir, ".storage-write.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	// Claude holds it: its mtime is "now" on the injected clock, so it never goes stale
	// within the wait.
	if err := os.Chtimes(lock, c.now, c.now); err != nil {
		t.Fatal(err)
	}
	called := false
	err := c.source(dir).Update(func(map[string]json.RawMessage) (bool, error) { called = true; return true, nil })
	if err == nil || called {
		t.Fatalf("Update = %v (mutate called %v); want a timeout and no write", err, called)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(data) != claudeStore {
		t.Fatal("the store changed while Claude held its lock")
	}
	if fi, err := os.Lstat(lock); err != nil || !fi.IsDir() {
		t.Fatal("a live lock was removed")
	}
}

func TestSourceUpdateBreaksAStaleLock(t *testing.T) {
	c := newFakeClock()
	dir := t.TempDir()
	writeSource(t, dir, claudeStore)
	lock := filepath.Join(dir, ".storage-write.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := c.now.Add(-16 * time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := c.source(dir).Update(func(map[string]json.RawMessage) (bool, error) { return true, nil }); err != nil {
		t.Fatalf("a lock older than proper-lockfile's stale limit was not broken: %v", err)
	}
}

func TestSourceUpdateLeavesAForeignLockAlone(t *testing.T) {
	c := newFakeClock()
	dir := t.TempDir()
	writeSource(t, dir, claudeStore)
	lock := filepath.Join(dir, ".storage-write.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := c.now.Add(-time.Hour)
	_ = os.Chtimes(lock, old, old)
	if err := c.source(dir).Update(func(map[string]json.RawMessage) (bool, error) { return true, nil }); err == nil {
		t.Fatal("a non-directory at the lock name was treated as a lock")
	}
	if fi, err := os.Lstat(lock); err != nil || !fi.Mode().IsRegular() {
		t.Fatal("the foreign file at the lock name was removed")
	}
}

func TestSourceUpdateRefusesAMalformedStore(t *testing.T) {
	c := newFakeClock()
	dir := t.TempDir()
	writeSource(t, dir, "{not json")
	if err := c.source(dir).Update(func(map[string]json.RawMessage) (bool, error) { return true, nil }); err == nil {
		t.Fatal("a malformed store was rewritten")
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(data) != "{not json" {
		t.Fatal("a malformed store changed")
	}
}

func TestSourceUpdateWithNoChangeWritesNothing(t *testing.T) {
	c := newFakeClock()
	dir := t.TempDir()
	writeSource(t, dir, claudeStore)
	before, _ := os.Stat(filepath.Join(dir, ".credentials.json"))
	if err := c.source(dir).Update(func(map[string]json.RawMessage) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(dir, ".credentials.json"))
	if !os.SameFile(before, after) {
		t.Fatal("an unchanged store was rewritten")
	}
}
