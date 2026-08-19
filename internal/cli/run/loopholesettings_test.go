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
// file the daemon's argv names — plus, since 2026-08-18, the fact that the retired
// top-level `host_processes` key no longer reaches it. The three tests that pinned
// the temporary bridge were replaced by the one that pins its absence: a bridge left
// standing would feed a host daemon values the validator now refuses.

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

// TestRetiredHostProcessesKeyNoLongerReachesTheDaemon is the migration's END,
// pinned from the READ side.
//
// pack-config-keys.md §5 warns that honoring a retired key only in the VALIDATOR
// makes `yolo check` go green while the daemon honors the old spelling forever. The
// deletion has the same two halves and this is the one a validator test cannot see:
// the bridge that folded the top-level block into the loophole's settings is gone,
// so a config carrying ONLY the retired spelling gets the manifest's declared
// defaults — an empty allowlist, which is the fail-closed direction.
//
// The other half is config.TestRetiredHostProcessesKeyIsRefusedAndNamesItsReplacement:
// the launch never gets here with that config on a host, because validation refuses
// it first. This test is what says the read agrees.
func TestRetiredHostProcessesKeyNoLongerReachesTheDaemon(t *testing.T) {
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
	if names, _ := got["visible"].([]any); len(names) != 0 {
		t.Errorf("visible = %v — the retired top-level key still reaches the daemon. "+
			"The key is REFUSED on the host now, so a value that arrived here anyway "+
			"would be one no validator ever saw", got["visible"])
	}
	// `fields` proves the same thing about the key whose declared default is NOT the
	// type zero: a bridge still firing would show the fixture's ["pid"] rather than the
	// DECLARED default, so this arm cannot pass by the write having silently failed.
	fields, _ := got["fields"].([]any)
	if len(fields) != 2 || fields[0] != "pid" || fields[1] != "comm" {
		t.Errorf("fields = %v, want the declaration's own default [pid comm]", got["fields"])
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
	origR := loopholes.RetiredUserLoopholesDir
	loopholes.RetiredUserLoopholesDir = func() string { return t.TempDir() }
	loopholes.SetPackModuleResolver(nil)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: true}})
	t.Cleanup(func() {
		loopholes.RetiredUserLoopholesDir = origR
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
	_ = dir

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
