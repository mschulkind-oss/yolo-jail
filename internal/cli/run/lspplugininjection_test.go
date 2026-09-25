package run

// lspplugininjection_test.go is the CALL-SITE half of Claude's generated LSP plugin
// (docs/reference/mcp-configuration.md#how-the-plugin-is-rendered, OQ-LSP1).
//
// internal/jailcontent/lspplugin_test.go pins the renderer, and every test there calls
// jailcontent.SetLSPServers itself. So the one line in refreshJailBriefings (prepare.go) that
// hands the launch's `lsp_servers` table to jailcontent was unpinned: deleting it switched the
// feature off with the unit gate green. These tests run the launch's own content path —
// stagePacks, then refreshJailBriefings, which is what calls PrepareSkills — and read the
// manifest back off disk, so they fail when that line goes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// lspInjectionLaunch configures one pack with a skills destination and returns Options for a
// launch that selects it.
func lspInjectionLaunch(t *testing.T) *Options {
	t.Helper()
	home := packHome(t)
	dir := filepath.Join(t.TempDir(), "alphacli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, `{"name":"alphacli","contributes":[`+
		`{"kind":"skills","into":".alphacli/skills"}]}`)
	writeUserPacks(t, home, `[{"source":"file://`+dir+`","name":"alphacli"}]`)

	o := goldenOptions(t.TempDir(), home)
	o.Stdout = discardBuf()
	return o
}

// resetLaunchRecords clears the three process-wide records the content path reads, now and at
// cleanup. The LSP one matters most: a table left behind by an earlier test would make the
// plugin appear with the production injection deleted.
func resetLaunchRecords(t *testing.T) {
	t.Helper()
	reset := func() {
		jailcontent.SetPackSkillDirs(nil)
		jailcontent.SetPackSkillTargets(nil)
		jailcontent.SetLSPServers(nil)
	}
	reset()
	t.Cleanup(reset)
}

// stageForLSP runs the jail content path with cfg and returns the pack's staged skills dir. It
// does NOT reset the records, so two calls in one test are two launches in one process.
func stageForLSP(t *testing.T, o *Options, cname string, cfg *jsonx.OrderedMap) string {
	t.Helper()
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	staging, err := o.refreshJailBriefings(cname, cfg, "podman",
		stagedPacks{root: root, packs: packs, briefings: briefings})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	return filepath.Join(staging, jailcontent.SkillStagingName("alphacli"))
}

func lspServersConfig(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	servers, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("fixture is not an object: %T", v)
	}
	return newConfig("lsp_servers", servers)
}

// THE WIRING: a launch whose config declares a server stages the plugin into the pack's skills
// tree, carrying that server. The language is one the deleted recipe table never knew, so the
// assertion cannot be satisfied by anything but the user's own table.
func TestLaunchStagesTheLSPPluginFromTheConfig(t *testing.T) {
	o := lspInjectionLaunch(t)
	resetLaunchRecords(t)
	skills := stageForLSP(t, o, "yolo-test-lspinjection", lspServersConfig(t, `{
  "rust": {"command": "rust-analyzer", "fileExtensions": {".rs": "rust"}}
}`))

	manifest := filepath.Join(skills, jailcontent.LSPPluginDir, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("the launch staged no LSP plugin for a config declaring lsp_servers — the "+
			"table never reached jailcontent (prepare.go's SetLSPServers call): %v", err)
	}
	var got struct {
		LSPServers map[string]struct {
			Command string `json:"command"`
		} `json:"lspServers"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("manifest is not JSON: %v\n%s", err, data)
	}
	if got.LSPServers["rust"].Command != "rust-analyzer" {
		t.Errorf("the staged plugin does not carry the configured server: %s", data)
	}
}

// DROPPING THE LAST SERVER STOPS IT REACHING CLAUDE, through the same pipeline: the launch
// hands over the CURRENT config's table every time, never the previous launch's. The records
// are NOT reset between the two launches, so an injection that skipped an absent table would
// leave the first launch's server in place and fail here.
func TestLaunchWithoutLSPServersStagesNoPlugin(t *testing.T) {
	o := lspInjectionLaunch(t)
	resetLaunchRecords(t)
	cname := "yolo-test-lspinjection-drop"
	withServer := lspServersConfig(t, `{"rust": {"command": "rust-analyzer"}}`)
	skills := stageForLSP(t, o, cname, withServer)
	plugin := filepath.Join(skills, jailcontent.LSPPluginDir)
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("precondition: the first launch staged no plugin: %v", err)
	}

	skills = stageForLSP(t, o, cname, jsonx.NewOrderedMap())
	if _, err := os.Stat(filepath.Join(skills, jailcontent.LSPPluginDir)); err == nil {
		t.Errorf("a launch with no lsp_servers still staged %s", jailcontent.LSPPluginDir)
	}
}
