package loopholegrant

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func journalBlock(launchID, container string, pid int) Block {
	return Block{
		Pack: "journal", Workspace: "/w", Container: container, LaunchID: launchID, PID: pid,
		Kinds: map[string]Kind{
			"read":     {Standing: Granted, Text: "read your user journal"},
			"read_all": {Standing: NotAllowed, Text: "read the whole host journal"},
		},
	}
}

// writeGrantFile is the launch's write: the frozen settings bytes, the grant block merged in,
// and the private snapshot file holding the result.
func writeGrantFile(t *testing.T, dir, name string, settings []byte, b Block) string {
	t.Helper()
	data, err := Merge(settings, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadReturnsSettingsAndGrants(t *testing.T) {
	dir := resolvedTempDir(t)
	b := journalBlock("one", "c", os.Getpid())
	b.Kinds["view"] = Kind{Standing: Pending, Values: []string{"postgres"}, Text: "see these host processes"}
	path := writeGrantFile(t, dir, "settings-one", []byte(`{"fields":["pid","comm"],"limit":3}`+"\n"), b)

	settings, grants, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := settings["grants"]; ok {
		t.Errorf("the reserved key leaked into the settings: %v", settings)
	}
	if !reflect.DeepEqual(settings["fields"], []any{"pid", "comm"}) || settings["limit"] != float64(3) {
		t.Errorf("settings %v", settings)
	}
	if grants == nil {
		t.Fatal("no grant block read")
	}
	if !reflect.DeepEqual(*grants, b) {
		t.Fatalf("grants %+v, want %+v", *grants, b)
	}
}

// A grant-declaring loophole with no settings still gets a file (plan PD-2): Merge accepts no
// settings bytes and Read hands back an empty settings map.
func TestMergeWithoutSettingsWritesOnlyTheGrants(t *testing.T) {
	dir := resolvedTempDir(t)
	path := writeGrantFile(t, dir, "settings-empty", nil, journalBlock("one", "c", 1))
	settings, grants, err := Read(path)
	if err != nil || len(settings) != 0 || grants == nil || grants.Pack != "journal" {
		t.Fatalf("settings %v, grants %+v, err %v", settings, grants, err)
	}
}

// The daemon must read the settings it was validated with, so Merge keeps the frozen bytes'
// members in their order and appends the block.
func TestMergeKeepsTheSettingsBytesInOrder(t *testing.T) {
	got, err := Merge([]byte(`{"zeta":[1,2],"alpha":"x"}`+"\n"), journalBlock("one", "c", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), `{"zeta":[1,2],"alpha":"x","grants":{"pack":"journal",`) || !strings.HasSuffix(string(got), "}\n") {
		t.Fatalf("merged %s", got)
	}
}

func TestMergeRefusesASettingsObjectHoldingTheReservedKey(t *testing.T) {
	if _, err := Merge([]byte(`{"grants":1}`), journalBlock("one", "c", 1)); err == nil {
		t.Fatal("a settings object already holding `grants` was merged")
	}
	if _, err := Merge([]byte(`["not","an","object"]`), journalBlock("one", "c", 1)); err == nil {
		t.Fatal("a settings array was merged")
	}
}

func TestReadWithoutGrantsKeyReturnsNilBlock(t *testing.T) {
	dir := resolvedTempDir(t)
	path := filepath.Join(dir, "settings-legacy")
	if err := os.WriteFile(path, []byte(`{"full":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, grants, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if grants != nil {
		t.Errorf("grants %+v, want nil for a file with no `grants` key", grants)
	}
	if settings["full"] != true {
		t.Errorf("settings %v", settings)
	}
}

func TestReadRefusesAMalformedStanding(t *testing.T) {
	dir := resolvedTempDir(t)
	path := filepath.Join(dir, "settings-bad")
	body := `{"grants":{"pack":"journal","workspace":"/w","container":"c","launch_id":"x","pid":1,` +
		`"kinds":{"read":{"standing":"maybe","text":"read your user journal"}}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Read(path)
	if err == nil || !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("err %v, want a refusal naming the malformed standing", err)
	}
	// The writer refuses it too, so no launch can produce one.
	b := journalBlock("one", "c", 1)
	b.Kinds["read"] = Kind{Standing: "maybe"}
	if _, err := Merge(nil, b); err == nil {
		t.Fatal("Merge wrote a malformed standing")
	}
}

func TestReadRefusesASymlink(t *testing.T) {
	dir := resolvedTempDir(t)
	real := writeGrantFile(t, dir, "real", nil, journalBlock("one", "c", 1))
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(link); err == nil {
		t.Fatal("a symlinked grant file was read")
	}
}

func TestLiveFilesMatchesWorkspaceContainerAndLivePid(t *testing.T) {
	dir := resolvedTempDir(t)
	livePath := writeGrantFile(t, dir, "settings-live", []byte(`{"a":1}`), journalBlock("live", "c", os.Getpid()))
	writeGrantFile(t, dir, "settings-dead", nil, journalBlock("dead", "c", 1<<30))
	writeGrantFile(t, dir, "settings-other", nil, journalBlock("other", "elsewhere", os.Getpid()))
	// A settings file with no grants key is never a grant file.
	if err := os.WriteFile(filepath.Join(dir, "settings-legacy"), []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LiveFiles(context.Background(), dir, "c", "/w")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != livePath || got[0].Block.LaunchID != "live" {
		t.Fatalf("live files %+v, want only %s", got, livePath)
	}
	if got, err := LiveFiles(context.Background(), dir, "c", "/other-workspace"); err != nil || len(got) != 0 {
		t.Fatalf("another workspace: %+v, %v", got, err)
	}
	if got, err := LiveFiles(context.Background(), filepath.Join(dir, "missing"), "c", "/w"); err != nil || len(got) != 0 {
		t.Fatalf("a missing state dir: %+v, %v", got, err)
	}
}

func TestReplaceRewritesTheGrantsAndKeepsTheSettings(t *testing.T) {
	dir := resolvedTempDir(t)
	b := journalBlock("live", "c", os.Getpid())
	path := writeGrantFile(t, dir, "settings-live", []byte(`{"a":1}`), b)
	next := b
	next.Kinds = map[string]Kind{
		"read":     {Standing: Granted, Text: "read your user journal"},
		"read_all": {Standing: Granted, Text: "read the whole host journal"},
	}
	if err := Replace(context.Background(), path, b, next); err != nil {
		t.Fatal(err)
	}
	settings, grants, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings["a"] != float64(1) || grants == nil || grants.Kinds["read_all"].Standing != Granted {
		t.Fatalf("settings %v, grants %+v", settings, grants)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", fi, err)
	}

	// The identity is the file's, never the caller's: another launch's expectation is refused,
	// and so is a next block that changes the identity.
	other := b
	other.LaunchID = "someone-else"
	if err := Replace(context.Background(), path, other, next); err == nil {
		t.Error("a file of another launch was rewritten")
	}
	moved := next
	moved.Container = "elsewhere"
	if err := Replace(context.Background(), path, next, moved); err == nil {
		t.Error("Replace changed the file's identity")
	}
}
