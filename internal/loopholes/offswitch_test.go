package loopholes

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// TestOffSwitchLoopholesShip pins loopholedecl's off-switch list (LC-D3) against the official pack
// embed: every row names a loophole its pack ships, and the list is exactly the ungated shipped
// set, the loopholes whose running hands the jail a login, credentials or the cgroup delegate.
func TestOffSwitchLoopholesShip(t *testing.T) {
	for _, loophole := range loopholedecl.OffSwitchLoopholes() {
		pack, _ := loopholedecl.OffSwitchPack(loophole)
		path := pack + "/loopholes/" + loophole + "/" + loopholedecl.ManifestTOMLName
		if _, err := fs.Stat(packs.FS, path); err != nil {
			t.Errorf("off-switch loophole %s: pack %s ships no %s: %v", loophole, pack, path, err)
		}
		if got := loopholedecl.OffSwitchLoopholesOf(pack); !slices.Contains(got, loophole) {
			t.Errorf("OffSwitchLoopholesOf(%q) = %v, missing %s", pack, got, loophole)
		}
	}
	var ungated []string
	for name := range ungatedShippedLoopholes {
		ungated = append(ungated, name)
	}
	sort.Strings(ungated)
	if got := loopholedecl.OffSwitchLoopholes(); !slices.Equal(got, ungated) {
		t.Errorf("off-switch loopholes = %v, want the ungated shipped set %v", got, ungated)
	}
}

// shippedModules materializes the named official packs and returns their loophole modules, as a
// launch selecting them would hand Discover.
func shippedModules(t *testing.T, names ...string) []PackModule {
	t.Helper()
	root := t.TempDir()
	var modules []PackModule
	for _, name := range names {
		sub, err := fs.Sub(packs.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, name)
		if err := os.CopyFS(dir, sub); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(filepath.Join(dir, "loopholes"))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				modules = append(modules, PackModule{Dir: filepath.Join(dir, "loopholes", e.Name()), Pack: name, HostExecApproved: true})
			}
		}
	}
	return modules
}

func enabledNames(set Set) []string {
	var out []string
	for _, lp := range set.Enabled() {
		out = append(out, lp.Name)
	}
	sort.Strings(out)
	return out
}

func offEntry() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("enabled", false)
	return m
}

var offSwitchPacks = []string{"claude-auth", "codex-auth", "aws-auth", "cgroup-delegate", "journal"}

// TestEnabledFalseKeepsAnOffSwitchLoopholeOff: a legacy `loopholes.<name>.enabled: false` on one of
// LC-D3's four loopholes keeps it out of the launch's Enabled set, which every surface that starts
// a daemon, serves a pointer or grants the delegate reads. A grant-declaring loophole's switch is
// still a grant, never a record switch (loophole-grants.md §4.1), so journal runs.
func TestEnabledFalseKeepsAnOffSwitchLoopholeOff(t *testing.T) {
	modules := shippedModules(t, offSwitchPacks...)
	if got := enabledNames(NewSelectedSet(nil, modules, nil)); !slices.Equal(got,
		[]string{"aws-auth", "cgroup-delegate", "claude-oauth-broker", "journal", "openai-auth-broker"}) {
		t.Fatalf("control: with no config every selected loophole runs, got %v", got)
	}
	block := jsonx.NewOrderedMap()
	for _, name := range []string{"aws-auth", "cgroup-delegate", "claude-oauth-broker", "openai-auth-broker", "journal"} {
		block.Set(name, offEntry())
	}
	if got := enabledNames(NewSelectedSet(block, modules, nil)); !slices.Equal(got, []string{"journal"}) {
		t.Errorf("enabled: false on the four off-switch loopholes: Enabled = %v, want only journal", got)
	}
	on := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	on.Set("aws-auth", entry)
	if got := enabledNames(NewSelectedSet(on, modules, nil)); !slices.Contains(got, "aws-auth") {
		t.Errorf("enabled: true keeps aws-auth running, got %v", got)
	}
}

// TestTOMLOffKeepsAnOffSwitchLoopholeOff: the TOML spelling, `[allow.<pack>] off = true`, reaches
// discovery through LaunchBlock, the block every launch-side set is built from, and keeps each of
// that pack's off-switch loopholes off. `off = false` and an unselected pack's `off` change
// nothing, and the latter conjures no record.
func TestTOMLOffKeepsAnOffSwitchLoopholeOff(t *testing.T) {
	modules := shippedModules(t, offSwitchPacks...)
	cfg := jsonx.NewOrderedMap()
	allow := jsonx.NewOrderedMap()
	for _, pack := range []string{"aws-auth", "claude-auth"} {
		table := jsonx.NewOrderedMap()
		table.Set("off", true)
		allow.Set(pack, table)
	}
	notOff := jsonx.NewOrderedMap()
	notOff.Set("off", false)
	allow.Set("codex-auth", notOff)
	cfg.Set("allow", allow)
	if got := enabledNames(NewSelectedSet(LaunchBlock(cfg), modules, nil)); !slices.Equal(got,
		[]string{"cgroup-delegate", "journal", "openai-auth-broker"}) {
		t.Errorf("allow.aws-auth.off and allow.claude-auth.off: Enabled = %v, want cgroup-delegate, journal, openai-auth-broker", got)
	}
	if _, has := cfg.Get("loopholes"); has {
		t.Error("LaunchBlock wrote into the config it was handed")
	}

	unselected := NewSelectedSet(LaunchBlock(cfg), shippedModules(t, "journal"), nil)
	for _, lp := range unselected.All() {
		if lp.Name == "aws-auth" || lp.Name == "claude-oauth-broker" {
			t.Errorf("an unselected pack's off conjured a %s record (source %s)", lp.Name, lp.Source)
		}
	}
}
