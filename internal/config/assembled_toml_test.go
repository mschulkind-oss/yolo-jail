package config

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A jail validates the ASSEMBLED copy of its own workspace's config (assembled.go), a JSON file
// the host wrote, whose provenance comes from the launch-origins record beside it. The readable-
// only shapes a TOML host file writes — a provider's own `model`, a handle qualified by a named
// pack source, a bare git pack table — were judged by the copy's format, so `yolo check` in the
// jail FAILed `config.providers.bedrock.model: unknown key` that the host accepted. The format
// each value was written in is its host writer's, and the jail must judge it so.
func TestTheJailJudgesTheAssembledCopyByEachHostWritersFormat(t *testing.T) {
	host, hostDir := sourcesHome(t)
	write(t, filepath.Join(host, "mine-packs", "tools", "pack.toml"), "description = 'tools'\n")
	write(t, filepath.Join(hostDir, paths.UserConfigTOMLName), `include_if_found = ['work.toml']
packs = ['claude', 'mine/tools', {git = 'https://example.invalid/acme/craft', ref = 'main', bare = true}]

[pack_sources.mine]
path = '~/mine-packs'
bare = true
`)
	write(t, filepath.Join(hostDir, "work.toml"), "[providers.bedrock]\nregion = 'us-east-1'\nmodel = 'global.example.model-1'\n")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// The host half: the launch's own load, then the copy and its record beside it, and the
	// user scope delivered into the jail home.
	merged, src, err := LoadConfigWithSources(ws, true, discard)
	if err != nil || src == nil {
		t.Fatalf("host load: %v (record %v)", err, src)
	}
	if errs, _ := ValidateConfig(merged, ws, nil); len(errs) != 0 {
		t.Fatalf("fixture: the host itself refuses this config: %v", errs)
	}
	if err := WriteAssembledConfig(ws, merged); err != nil {
		t.Fatal(err)
	}
	if err := WriteAssembledOrigins(ws, merged, src); err != nil {
		t.Fatal(err)
	}
	effective, esrc, err := LoadConfigWithoutWorkspaceFileWithSources(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	filtered, _ := FilterInherit(effective, InheritPreflight)
	body, format, _, err := RenderFilteredForDelivery(filtered, esrc, InheritPreflight, "")
	if err != nil || format != configdoc.TOML {
		t.Fatalf("user scope delivery: format=%v err=%v", format, err)
	}

	// The jail half: a home of its own holding only the delivered user scope (and the local
	// pack tree, which a jail sees mounted), reading the copy for its own workspace as
	// `yolo check`'s Merged Configuration does.
	jailHome, jailDir := sourcesHome(t)
	write(t, filepath.Join(jailDir, paths.UserConfigTOMLName), body)
	write(t, filepath.Join(jailHome, "mine-packs", "tools", "pack.toml"), "description = 'tools'\n")
	t.Setenv("YOLO_VERSION", "9.9.9")
	t.Setenv("YOLO_WORKSPACE", ws)
	cfg, jsrc, err := LoadConfigWithSources(ws, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	if jsrc == nil {
		t.Fatal("fixture: the jail read no record beside the assembled copy, so this proves nothing")
	}
	errs, _ := ValidateConfig(cfg, ws, nil)
	for _, e := range jsrc.Annotate(errs) {
		t.Errorf("the jail refused what the host accepted: %s", e)
	}
	for _, path := range [][]string{{providersKey, "bedrock", "model"}} {
		if !jsrc.writtenInTOML(path...) {
			t.Errorf("%v: the jail does not know a TOML file on the host wrote it", path)
		}
	}
	for i := range 3 {
		item := "config.packs[" + string(rune('0'+i)) + "]"
		if !packEntryIsReadable(jsrc, item) {
			t.Errorf("%s: the jail judges a TOML-written pack entry as legacy", item)
		}
	}
}
