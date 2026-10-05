package jailcontent

// lspplugin_test.go covers option D's renderer (docs/reference/mcp-configuration.md#oq-lsp1): ONE
// yolo-authored plugin whose lspServers comes from the user's own lsp_servers table.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func lspTable(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("fixture is not an object: %T", v)
	}
	return m
}

func readPlugin(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, LSPPluginDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("no plugin manifest: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	return out
}

// ANY language works, which is the whole point of the ruling — a table with a language none of the
// three deleted hardcoded pairs covered must reach Claude.
func TestLSPPluginRendersAnyLanguageFromTheUsersTable(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	SetLSPServers(lspTable(t, `{
  "rust": {"command": "rust-analyzer", "args": [], "fileExtensions": {".rs": "rust"}},
  "zig":  {"command": "zls", "args": ["--stdio"], "fileExtensions": {".zig": "zig"}}
}`))
	dir := t.TempDir()
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}

	got := readPlugin(t, dir)
	if got["x-yolo-managed-by"] != yoloPluginManagedBy {
		t.Errorf("without the ownership marker the host adoption walk cannot prove this dir is "+
			"yolo's and would offer to migrate it; got %v", got["x-yolo-managed-by"])
	}
	servers, ok := got["lspServers"].(map[string]any)
	if !ok {
		t.Fatalf("lspServers missing/!object: %v", got["lspServers"])
	}
	// Neither of these is one of the three languages the deleted hardcoding covered.
	for _, name := range []string{"rust", "zig"} {
		if _, present := servers[name]; !present {
			t.Errorf("%q did not reach the plugin — the ruling exists so ANY configured language "+
				"works, not a fixed three", name)
		}
	}
	zig, _ := servers["zig"].(map[string]any)
	if zig["command"] != "zls" {
		t.Errorf("command must pass through verbatim; got %v", zig["command"])
	}
	// yolo's `fileExtensions` is the plugin's `extensionToLanguage` — the one renamed key, and
	// getting it wrong yields a plugin Claude loads and never triggers.
	e2l, ok := zig["extensionToLanguage"].(map[string]any)
	if !ok || e2l[".zig"] != "zig" {
		t.Errorf("fileExtensions must render as extensionToLanguage; got %v", zig)
	}
	if _, present := zig["fileExtensions"]; present {
		t.Error("the yolo-side key name must not leak into the manifest")
	}
}

// An empty `args` is OMITTED rather than written as [], so a hand-read manifest shows only what was
// declared.
func TestLSPPluginOmitsEmptyArgs(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	SetLSPServers(lspTable(t, `{"rust": {"command": "rust-analyzer", "args": []}}`))
	dir := t.TempDir()
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}
	servers := readPlugin(t, dir)["lspServers"].(map[string]any)
	rust := servers["rust"].(map[string]any)
	if _, present := rust["args"]; present {
		t.Errorf("an empty args must be omitted, got %v", rust["args"])
	}
}

// No lsp_servers -> NO plugin directory. A jail that configured nothing must not acquire a plugin,
// and the manifest must not exist for Claude to load.
func TestLSPPluginAbsentWhenNothingIsConfigured(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	SetLSPServers(nil)
	dir := t.TempDir()
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, LSPPluginDir)); !os.IsNotExist(err) {
		t.Errorf("a jail with no lsp_servers must have no plugin dir (err=%v)", err)
	}
}

// Dropping the last entry must REMOVE a previously rendered plugin, or the servers keep reaching
// Claude after the user deleted them.
func TestLSPPluginRemovesAStaleRender(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	dir := t.TempDir()
	SetLSPServers(lspTable(t, `{"rust": {"command": "rust-analyzer"}}`))
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}
	SetLSPServers(nil)
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, LSPPluginDir)); !os.IsNotExist(err) {
		t.Error("a stale plugin must go when the table empties — otherwise a deleted server keeps " +
			"reaching Claude")
	}
}

// An entry with no `command` is SKIPPED: a server yolo cannot spawn is not a server, and rendering
// it makes Claude report a failure yolo could have declined to cause.
func TestLSPPluginSkipsAnEntryWithNoCommand(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	SetLSPServers(lspTable(t, `{
  "broken": {"args": ["--stdio"]},
  "rust": {"command": "rust-analyzer"}
}`))
	dir := t.TempDir()
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}
	servers := readPlugin(t, dir)["lspServers"].(map[string]any)
	if _, present := servers["broken"]; present {
		t.Error("an entry with no command must not be rendered")
	}
	if _, present := servers["rust"]; !present {
		t.Error("one bad entry must not cost the good ones")
	}
}

// TestPluginMarkerMatchesTheHostSkillsComposer pins the DUPLICATED constant.
//
// The ownership marker is spelled twice (hostskills keeps its own unexported). If they drift,
// nothing fails loudly — the host adoption walk simply stops recognising yolo's own plugin and
// offers to migrate it into the user's local pack, which is the class of defect the sync-root fence
// exists for. So the drift is what gets pinned, by reading the composer's source.
func TestPluginMarkerMatchesTheHostSkillsComposer(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "hostskills", "tier.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := `yoloManagedMarker = "` + yoloPluginManagedBy + `"`
	if !strings.Contains(string(data), want) {
		t.Errorf("internal/hostskills/tier.go does not declare %s — the two spellings of the "+
			"ownership marker have drifted, and the host walk will no longer recognise the plugin "+
			"jailcontent writes", want)
	}
}

// TestPrepareSkillsRendersThePlugin pins the CALL SITE inside PrepareSkills, not writeLSPPlugin
// alone. Deleting the call would leave every test above green while no jail ever got a plugin.
func TestPrepareSkillsRendersThePlugin(t *testing.T) {
	t.Cleanup(func() {
		SetLSPServers(nil)
		SetPackSkillTargets(nil)
	})
	// PrepareSkills stages under paths.AgentsDir(), which is $HOME's; no XDG variable moves it,
	// so without this the staging tree lands in the real ~/.local/share/yolo-jail/agents.
	t.Setenv("HOME", t.TempDir())
	SetPackSkillTargets([]SkillTarget{{Agent: "claude", Staging: SkillStagingName("claude")}})
	SetLSPServers(lspTable(t, `{"rust": {"command": "rust-analyzer"}}`))

	staging, err := PrepareSkills("yolo-ws-test", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := readPlugin(t, filepath.Join(staging, SkillStagingName("claude")))
	if _, ok := got["lspServers"]; !ok {
		t.Errorf("PrepareSkills staged no plugin — the renderer is not wired in: %#v", got)
	}
}

// ONE RENDERER FOR BOTH NOTCHES. The jail's staging write and `yolo host apply` must hold the same
// file, so writeLSPPlugin writes exactly RenderLSPPlugin's bytes — the host half reads them from
// RenderLSPPlugin directly (internal/cli's applyHostLSPPlugin).
func TestWriteLSPPluginWritesTheRenderersBytes(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	table := lspTable(t, `{"gopls": {"command": "gopls", "fileExtensions": {".go": "go"}}}`)
	SetLSPServers(table)
	dir := t.TempDir()
	if err := writeLSPPlugin(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, LSPPluginDir, filepath.FromSlash(LSPPluginManifestRel)))
	if err != nil {
		t.Fatal(err)
	}
	want, ok := RenderLSPPlugin(table)
	if !ok {
		t.Fatal("RenderLSPPlugin rendered nothing for a table with a command")
	}
	if string(got) != string(want) {
		t.Errorf("the staged manifest is not RenderLSPPlugin's bytes:\n--- staged\n%s\n--- rendered\n%s", got, want)
	}
	// NOTCH-NEUTRAL: the same bytes land in a real home, where "this jail" would be false.
	if strings.Contains(strings.ToLower(string(want)), "jail's") {
		t.Errorf("the manifest names a notch, and the host writes the same bytes:\n%s", want)
	}
}

// Nothing to render is FALSE, never an empty manifest: no table, an empty one, and one whose only
// entry has no command all leave the caller nothing to write.
func TestRenderLSPPluginReportsNothingToRender(t *testing.T) {
	for name, table := range map[string]*jsonx.OrderedMap{
		"nil":        nil,
		"empty":      lspTable(t, `{}`),
		"no command": lspTable(t, `{"broken": {"args": ["--stdio"]}}`),
	} {
		if data, ok := RenderLSPPlugin(table); ok || data != nil {
			t.Errorf("%s: want (nil, false), got (%q, %v)", name, data, ok)
		}
	}
}

// IsLSPPlugin proves the plugin from its manifest, and a pack's namespaced subtree that happens to
// be called yolo-lsp is NOT it: that subtree carries the same marker and name (hostskills' tier A
// manifest) but no lspServers, and treating it as the plugin would let the host apply rewrite or
// retire a pack's skills.
func TestIsLSPPluginTellsThePluginFromAPackSubtreeOfTheSameName(t *testing.T) {
	t.Cleanup(func() { SetLSPServers(nil) })
	SetLSPServers(lspTable(t, `{"rust": {"command": "rust-analyzer"}}`))
	plugin := t.TempDir()
	if err := writeLSPPlugin(plugin); err != nil {
		t.Fatal(err)
	}
	if !IsLSPPlugin(filepath.Join(plugin, LSPPluginDir)) {
		t.Error("the plugin writeLSPPlugin wrote is not recognised")
	}
	manifest := func(body string) string {
		dir := filepath.Join(t.TempDir(), LSPPluginDir)
		path := filepath.Join(dir, filepath.FromSlash(LSPPluginManifestRel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	for name, dir := range map[string]string{
		"a namespaced pack subtree": manifest(`{"name":"yolo-lsp","skills":["./"],"x-yolo-managed-by":"yolo-jail"}`),
		"an unmarked plugin":        manifest(`{"name":"yolo-lsp","lspServers":{"x":{"command":"x"}}}`),
		"another marked plugin":     manifest(`{"name":"other","lspServers":{},"x-yolo-managed-by":"yolo-jail"}`),
		"malformed":                 manifest(`{`),
		"absent":                    filepath.Join(t.TempDir(), LSPPluginDir),
	} {
		if IsLSPPlugin(dir) {
			t.Errorf("%s was taken for yolo's LSP plugin", name)
		}
	}
}
