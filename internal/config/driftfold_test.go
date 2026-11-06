package config

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The drift diff names its two sides by what they are now that a live apply folds changes into
// the baseline (docs/design/live-config-apply.md LA-D8): the config this jail HAS, which is the
// config it booted with plus every change applied since, against the file on disk.
func TestTheDriftDiffNamesTheConfigThisJailHas(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packages":["jq"]}`)
	wsCfg, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	if _, err := WriteWorkspaceBootBaseline(ws, "podman-self", wsCfg); err != nil {
		t.Fatal(err)
	}
	writeWSConfig(t, ws, `{"packages":["jq","ripgrep"]}`)
	diff, _, _, err := WorkspaceConfigDrift(ws, "podman-self")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(diff, "\n")
	for _, want := range []string{"--- config this jail has", "+++ workspace config on disk now"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the drift diff does not label a side %q:\n%s", want, joined)
		}
	}
}

// Folding one canonical path replaces exactly that value in the baseline, leaves every other
// value at its boot value, and deletes a path the workspace no longer has.
func TestFoldBootBaselineKeyReplacesOneKey(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"mounts":["/a"],"install":{"nix":["jq"],"mise":{"python":"3.12"}},"machine":{"ports":[{"host":1,"jail":1}]}}`)
	boot, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	if _, err := WriteWorkspaceBootBaseline(ws, "podman-self", boot); err != nil {
		t.Fatal(err)
	}
	writeWSConfig(t, ws, `{"mounts":["/a","/b"],"install":{"nix":["jq","ripgrep"]},"machine":{"ports":[{"host":2,"jail":2}]}}`)
	cur, _ := LoadWorkspaceConfig(ws, false, func(string) {})

	if err := FoldBootBaselineKey(ws, "podman-self", "mounts", cur); err != nil {
		t.Fatal(err)
	}
	if err := FoldBootBaselineKey(ws, "podman-self", "install.nix", cur); err != nil {
		t.Fatal(err)
	}
	if err := FoldBootBaselineKey(ws, "podman-self", "install.mise", cur); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadWorkspaceBootBaseline(ws, "podman-self")
	if err != nil || !ok {
		t.Fatalf("ReadWorkspaceBootBaseline: ok=%v err=%v", ok, err)
	}
	mounts, _ := got.Get("mounts")
	if s, _ := jsonx.DumpsCompact(mounts); s != `["/a", "/b"]` {
		t.Errorf("mounts was not folded to the current value: %s", s)
	}
	installValue, _ := got.Get("install")
	install := installValue.(*jsonx.OrderedMap)
	nix, _ := install.Get("nix")
	if s, _ := jsonx.DumpsCompact(nix); s != `["jq", "ripgrep"]` {
		t.Errorf("install.nix was not folded to the current value: %s", s)
	}
	if _, has := install.Get("mise"); has {
		t.Error("a nested key the workspace no longer has was not deleted from the baseline")
	}
	machine, _ := got.Get("machine")
	ports, _ := machine.(*jsonx.OrderedMap).Get("ports")
	if s, _ := jsonx.DumpsCompact(ports); !strings.Contains(s, `"host": 1`) {
		t.Errorf("an unfolded nested sibling moved: machine.ports = %s", s)
	}
	// The drift that remains is exactly the unfolded nested path.
	diff, hasDrift, _, _ := WorkspaceConfigDrift(ws, "podman-self")
	joined := strings.Join(diff, "\n")
	if !hasDrift || !strings.Contains(joined, `"host": 2`) || strings.Contains(joined, `"/b"`) || strings.Contains(joined, `"ripgrep"`) {
		t.Errorf("after folding mounts and install paths, only machine.ports should drift:\n%s", joined)
	}
}

// Canonical dotted paths traverse tables without disturbing sibling values and remove only
// their addressed leaf.
func TestFoldBootBaselineKeySetsAndDeletesExactCanonicalPaths(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"install":{"nix":["jq"],"mise":{"python":"3.12","node":"22"}},"machine":{"ports":[{"host":1,"jail":1}],"resources":{"memory":"4g"}}}`)
	boot, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	if _, err := WriteWorkspaceBootBaseline(ws, "podman-self", boot); err != nil {
		t.Fatal(err)
	}
	cur := mapOfConfigTest(t, `{"install":{"nix":["jq","ripgrep"],"mise":{"python":"3.13"}},"machine":{"ports":[{"host":2,"jail":2}],"resources":{"memory":"8g"}}}`)
	for _, path := range []string{"install.nix", "install.mise", "machine.ports"} {
		if err := FoldBootBaselineKey(ws, "podman-self", path, cur); err != nil {
			t.Fatalf("FoldBootBaselineKey(%q): %v", path, err)
		}
	}
	got, ok, err := ReadWorkspaceBootBaseline(ws, "podman-self")
	if err != nil || !ok {
		t.Fatalf("ReadWorkspaceBootBaseline: ok=%v err=%v", ok, err)
	}
	install, _ := got.Get("install")
	nix, _ := install.(*jsonx.OrderedMap).Get("nix")
	if s, _ := jsonx.DumpsCompact(nix); s != `["jq", "ripgrep"]` {
		t.Fatal("setting install.mise changed its install.nix sibling")
	}
	machine, _ := got.Get("machine")
	resources, _ := machine.(*jsonx.OrderedMap).Get("resources")
	if s, _ := jsonx.DumpsCompact(resources); !strings.Contains(s, `"4g"`) {
		t.Fatalf("folding machine.ports changed machine.resources: %s", s)
	}
}

func mapOfConfigTest(t *testing.T, js string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatal("expected object")
	}
	return m
}

// With no baseline there is nothing to fold into: an error, and no file is created, so a jail
// launched before baselines existed keeps reporting "cannot determine" rather than a baseline
// holding one key.
func TestFoldBootBaselineKeyNeedsABaseline(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packages":["jq"]}`)
	cur, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	if err := FoldBootBaselineKey(ws, "podman-self", "packages", cur); err == nil {
		t.Fatal("folding into a missing baseline must fail")
	}
	if _, err := os.Stat(WorkspaceConfigBootPath(ws, "podman-self")); !os.IsNotExist(err) {
		t.Error("a fold with no baseline created one")
	}
}

// ConfigPartChanged is the side-effect-free half of the gate's comparison: true when the
// workspace config part differs from the approval record, false once it is recorded, and it
// writes nothing either way.
func TestConfigPartChangedComparesWithoutRecording(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packages":["jq"]}`)
	cur, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	changed, err := ConfigPartChanged(ws, cur)
	if err != nil || !changed {
		t.Fatalf("an unrecorded non-empty config must read as changed: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(ApprovalSnapshotPath(ws)); !os.IsNotExist(err) {
		t.Fatal("ConfigPartChanged wrote the approval record")
	}
	if err := RecordApproval(ws, cur, nil); err != nil {
		t.Fatal(err)
	}
	if changed, err := ConfigPartChanged(ws, cur); err != nil || changed {
		t.Errorf("a recorded config must read as unchanged: changed=%v err=%v", changed, err)
	}
	empty := t.TempDir()
	if changed, _ := ConfigPartChanged(empty, jsonx.NewOrderedMap()); changed {
		t.Error("an empty config with no record is recorded silently by the gate, so it is not a change")
	}
}
