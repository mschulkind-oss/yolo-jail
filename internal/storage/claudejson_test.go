package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// claudejson_test.go pins the seed's FORWARD pass to the same allowlist as its reverse pass
// (docs/design/base-home-legacy-state.md#27-the-seed).
//
// The two passes used to disagree. Reverse (workspace → seed) wrote only claudeJSONSeedKeys,
// but forward (seed → workspace) copied EVERY key the workspace lacked, so a legacy seed — one
// written by a pre-April yolo whose home was shared and writable — carried its `projects` and
// `mcpServers` into every new workspace on the machine. The seed exists to carry a LOGIN, and
// the allowlist is what says what a login is.

func writeSeedJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSeedJSON(t *testing.T, path string) *jsonx.OrderedMap {
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

// A seed carrying `projects` and `oauthAccount` forwards ONLY `oauthAccount`.
func TestSeedForwardsOnlyTheLoginKeys(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "machine", ".claude", "claude.json")
	ws := filepath.Join(dir, "ws", "claude", "claude.json")
	writeSeedJSON(t, seed, `{
  "oauthAccount": {"emailAddress": "someone@example.invalid"},
  "projects": {"/home/someone/code/other-workspace": {"allowedTools": []}},
  "mcpServers": {"legacy": {"command": "/bin/true"}}
}`)

	syncWorkspace(t, seed, filepath.Join(dir, "ws"), filepath.Join("claude", "claude.json"))

	got := readSeedJSON(t, ws)
	if _, ok := got.Get("oauthAccount"); !ok {
		t.Fatalf("the login did not reach the new workspace; workspace claude.json keys = %v", got.Keys())
	}
	for _, leaked := range []string{"projects", "mcpServers"} {
		if _, ok := got.Get(leaked); ok {
			t.Errorf("the seed forwarded %q into a new workspace — only %v are a login, and "+
				"another workspace's %s is exactly what the seed must never carry",
				leaked, claudeJSONSeedKeys, leaked)
		}
	}
}

// A workspace's own keys are never overwritten by the seed, allowlisted or not.
func TestSeedDoesNotOverwriteAWorkspaceKey(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.json")
	ws := filepath.Join(dir, "ws.json")
	writeSeedJSON(t, seed, `{"oauthAccount": {"emailAddress": "machine@example.invalid"}, "hasCompletedOnboarding": true}`)
	writeSeedJSON(t, ws, `{"oauthAccount": {"emailAddress": "workspace@example.invalid"}}`)

	syncWorkspace(t, seed, dir, "ws.json")

	got := readSeedJSON(t, ws)
	acct, _ := got.Get("oauthAccount")
	m, _ := acct.(*jsonx.OrderedMap)
	if email, _ := m.Get("emailAddress"); email != "workspace@example.invalid" {
		t.Errorf("the workspace's own oauthAccount was replaced by the seed's: %v", email)
	}
	if v, ok := got.Get("hasCompletedOnboarding"); !ok || v != true {
		t.Errorf("an allowlisted key the workspace lacked was not filled: %v (present=%v)", v, ok)
	}
}

// A LOGGED-IN WORKSPACE STILL BACK-PROPAGATES: narrowing the forward pass must not touch the
// reverse one, which is how the machine learns a login in the first place.
func TestALoggedInWorkspaceStillBackPropagates(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "machine", ".claude", "claude.json")
	ws := filepath.Join(dir, "ws", "claude", "claude.json")
	// A seed with no login, holding a legacy key of its own.
	writeSeedJSON(t, seed, `{"projects": {"/legacy": {}}}`)
	writeSeedJSON(t, ws, `{
  "oauthAccount": {"emailAddress": "someone@example.invalid"},
  "hasCompletedOnboarding": true,
  "projects": {"/workspace": {"allowedTools": []}},
  "mcpServers": {"mine": {"command": "/bin/true"}}
}`)

	syncWorkspace(t, seed, filepath.Join(dir, "ws"), filepath.Join("claude", "claude.json"))

	gotSeed := readSeedJSON(t, seed)
	for _, key := range claudeJSONSeedKeys {
		if _, ok := gotSeed.Get(key); !ok {
			t.Errorf("the logged-in workspace's %q did not back-propagate into the seed; seed keys = %v",
				key, gotSeed.Keys())
		}
	}
	if _, ok := gotSeed.Get("mcpServers"); ok {
		t.Error("the reverse pass leaked the workspace's mcpServers into the machine seed")
	}
	// The seed's own unrelated key is preserved, and it is NOT forwarded into the workspace.
	projects, _ := gotSeed.Get("projects")
	if pm, ok := projects.(*jsonx.OrderedMap); !ok || pm.Len() != 1 {
		t.Errorf("the seed's own projects were rewritten: %v", projects)
	} else if _, ok := pm.Get("/legacy"); !ok {
		t.Errorf("the seed's own projects lost /legacy: %v", pm.Keys())
	}
	wsProjects, _ := readSeedJSON(t, ws).Get("projects")
	if pm, ok := wsProjects.(*jsonx.OrderedMap); ok {
		if _, leaked := pm.Get("/legacy"); leaked {
			t.Error("the workspace received the seed's legacy project")
		}
	}
}
