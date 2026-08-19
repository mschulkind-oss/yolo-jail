package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholesettings_test.go pins the LAUNCH half of
// docs/design/pack-config-keys.md — where the merged config's values become the
// file the daemon's argv names — plus the temporary bridge from the retired
// top-level `host_processes` key.

// settingsCfg builds a merged-config shape carrying one loophole's settings.
func settingsCfg(t *testing.T, name string, pairs ...any) *jsonx.OrderedMap {
	t.Helper()
	cfg := jsonx.NewOrderedMap()
	block := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	values := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		values.Set(pairs[i].(string), pairs[i+1])
	}
	entry.Set("settings", values)
	block.Set(name, entry)
	cfg.Set("loopholes", block)
	return cfg
}

func hpLoophole() *loopholes.Loophole {
	return &loopholes.Loophole{
		Name: "host-processes",
		Settings: []loopholes.Setting{
			{Key: "visible", Type: loopholes.SettingTypeStringList, Default: []string{}},
			{Key: "fields", Type: loopholes.SettingTypeStringList,
				Default: []string{"pid", "comm"}},
		},
	}
}

// readSettings reads back the file yolo wrote for a loophole.
func readSettings(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(loopholes.SettingsFileFor(name))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// redirectState points StateDirFor at a temp tree so the launch path does not write
// into the developer's real state dir.
func redirectState(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	real := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string { return filepath.Join(root, name) }
	t.Cleanup(func() { loopholes.StateDirFor = real })
}

// TestWriteLoopholeSettingsWritesTheMergedValues is the baseline launch path: what
// the config supplies under the loophole's own name is what the file holds.
func TestWriteLoopholeSettingsWritesTheMergedValues(t *testing.T) {
	redirectState(t)
	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	lp := hpLoophole()
	o.writeLoopholeSettings([]*loopholes.Loophole{lp},
		settingsCfg(t, "host-processes", "visible", []any{"sway"}))

	got := readSettings(t, "host-processes")
	names, _ := got["visible"].([]any)
	if len(names) != 1 || names[0] != "sway" {
		t.Errorf("visible = %v", got["visible"])
	}
	if _, ok := got["fields"]; !ok {
		t.Error("fields is missing — the file must carry every DECLARED key, so the daemon " +
			"never has to distinguish absent from empty")
	}
}

// TestLegacyHostProcessesKeyStillReachesTheDaemon is the migration's load-bearing
// half. pack-config-keys.md §5 warns that honoring a retired key only in the
// VALIDATOR makes `yolo check` go green while the daemon honors the old spelling
// forever — the inverse of that mistake is just as bad, and this pins the read.
//
// The old top-level `host_processes` block still WORKS: deleting it before its
// loophole is a pack would strand every user who has one.
func TestLegacyHostProcessesKeyStillReachesTheDaemon(t *testing.T) {
	redirectState(t)
	cfg := jsonx.NewOrderedMap()
	legacy := jsonx.NewOrderedMap()
	legacy.Set("visible", []any{"sway"})
	legacy.Set("fields", []any{"pid"})
	cfg.Set("host_processes", legacy)

	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	o.writeLoopholeSettings([]*loopholes.Loophole{hpLoophole()}, cfg)

	got := readSettings(t, "host-processes")
	names, _ := got["visible"].([]any)
	if len(names) != 1 || names[0] != "sway" {
		t.Errorf("visible = %v — the retired top-level key must still reach the daemon "+
			"until it is deleted", got["visible"])
	}
	fields, _ := got["fields"].([]any)
	if len(fields) != 1 || fields[0] != "pid" {
		t.Errorf("fields = %v", got["fields"])
	}
	// And the reader is told where the key went, at the point of use.
	if !strings.Contains(buf.String(), "loopholes") ||
		!strings.Contains(buf.String(), "host-processes") {
		t.Errorf("output = %q, want a note naming the replacement spelling", buf.String())
	}
}

// TestNewSpellingWinsPerKeyOverTheLegacyBlock: not "whole block wins". A config
// mid-migration can name `visible` under the new spelling and still carry an old
// `fields`, and the surprising answer would be for the untouched key to vanish.
func TestNewSpellingWinsPerKeyOverTheLegacyBlock(t *testing.T) {
	redirectState(t)
	cfg := settingsCfg(t, "host-processes", "visible", []any{"new"})
	legacy := jsonx.NewOrderedMap()
	legacy.Set("visible", []any{"old"})
	legacy.Set("fields", []any{"pid"})
	cfg.Set("host_processes", legacy)

	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	o.writeLoopholeSettings([]*loopholes.Loophole{hpLoophole()}, cfg)

	got := readSettings(t, "host-processes")
	names, _ := got["visible"].([]any)
	if len(names) != 1 || names[0] != "new" {
		t.Errorf("visible = %v, want the NEW spelling to win", got["visible"])
	}
	fields, _ := got["fields"].([]any)
	if len(fields) != 1 || fields[0] != "pid" {
		t.Errorf("fields = %v, want the legacy block to still supply the key the new "+
			"spelling did not mention", got["fields"])
	}
}

// TestLegacyBridgeIsScopedToHostProcesses: the bridge is a named, temporary
// migration for ONE key, not a general "top-level block feeds a loophole" rule.
// OQ-K4 explicitly warns against building anything that assumes only
// `host_processes` will ever use the settings mechanism, and the inverse — a bridge
// that quietly applied to other loopholes — would be the same mistake pointed the
// other way.
func TestLegacyBridgeIsScopedToHostProcesses(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	legacy := jsonx.NewOrderedMap()
	legacy.Set("visible", []any{"sway"})
	cfg.Set("host_processes", legacy)
	if got := withLegacyHostProcessesSettings("audio", nil, cfg, nil); got != nil {
		t.Errorf("the legacy bridge fired for a different loophole: %v", got.Keys())
	}
}

// TestStartLoopholesWritesTheSettingsFile pins the CALL SITE, not the callee.
//
// Every other test in this file drives writeLoopholeSettings directly, so deleting
// the one line in startLoopholes that calls it would leave the whole unit gate green
// while the feature was switched off wholesale — the shape AGENTS.md says this repo
// has shipped five times. This is the test that fails if that line goes.
//
// The fixture loophole declares settings and NO host_daemon, so startLoopholes runs
// its real body — the discovery, the write, the spawn loop — without starting a
// single process. `transport: "none"` keeps it out of every endpoint path too.
func TestStartLoopholesWritesTheSettingsFile(t *testing.T) {
	redirectState(t)
	dir := t.TempDir()
	mod := filepath.Join(dir, "acme")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(`{
		"name": "acme", "default_enabled": true, "transport": "none",
		"settings": {"names": {"type": "string_list", "scope": "workspace", "default": []}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	origB, origR := loopholes.BundledLoopholesDir, loopholes.RetiredUserLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return dir }
	loopholes.RetiredUserLoopholesDir = func() string { return t.TempDir() }
	t.Cleanup(func() {
		loopholes.BundledLoopholesDir, loopholes.RetiredUserLoopholesDir = origB, origR
	})

	cname := "yolo-settings-" + t.Name()
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(socketsDir) })

	o := &Options{}
	fillDefaults(o)
	o.Stdout = discardBuf()
	o.Stderr = discardBuf()
	o.PathExists = func(string) bool { return false } // no cgroup delegate

	handles := o.startLoopholes(cname, "podman", settingsCfg(t, "acme", "names", []any{"sway"}))
	for _, h := range handles {
		if h.stop != nil {
			h.stop()
		}
	}

	got := readSettings(t, "acme")
	names, _ := got["names"].([]any)
	if len(names) != 1 || names[0] != "sway" {
		t.Errorf("settings file = %v — startLoopholes must resolve and write settings BEFORE "+
			"the spawn loop, because a daemon's argv already names the file", got)
	}
}
