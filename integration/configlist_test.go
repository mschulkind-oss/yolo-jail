package integration

// configlist_test.go is the end-to-end check of `config-list` on pi's `stateful` settings
// surface (docs/reference/pack-system.md#config-list-capture, OQ-AL1) — three launches of ONE
// workspace, with capture-on-terminate's host-side captureSurfaceAt running between them. It
// is the only test that exercises that teardown capture over a real jail: the unit suites
// call it directly, and none of them can show that the sidecar it reads is the one a real
// boot wrote.
//
// No agent runs. `jq` stands in for `pi install` appending to `packages`, which is the whole
// of what the motivating case needs from the agent.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	configListKilo = "git:github.com/mschulkind/kilo-pi-provider"
	configListEdit = "npm:in-jail-edit"
)

// configListPackages parses the `packages` array out of a fenced `cat settings.json`.
func configListPackages(t *testing.T, stdout, start, end string) []string {
	t.Helper()
	raw := strings.TrimSpace(section(stdout, start, end))
	var m struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("settings.json between %q and %q is not JSON: %v\n%s", start, end, err, raw)
	}
	return m.Packages
}

func countEntry(list []string, want string) int {
	n := 0
	for _, e := range list {
		if e == want {
			n++
		}
	}
	return n
}

func TestConfigListSurvivesInJailEditsAndPackDrop(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	manifest := `{
  "name": "kilo-list-fixture",
  "description": "config-list: add one pi package without copying the owner's list",
  "contributes": [
    {"kind": "config-list", "surface": "pi/settings", "path": "/packages",
     "add": ["` + configListKilo + `"]}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{}`)
	settings := `"$HOME/.pi/agent/settings.json"`

	// ── Launch 1: pi plus the contributing pack. The entry is there, once; then jq appends
	// the agent's own entry, and the jail exits — capture-on-terminate runs host-side.
	packHome(t, `{"packs": ["pi", "file://`+pack+`"]}`)
	r := runYolo(t, dir, strings.Join([]string{
		`echo "=== FIRST ==="`,
		`cat ` + settings,
		`echo "=== END ==="`,
		`tmp=$(mktemp) && jq '.packages += ["` + configListEdit + `"]' ` + settings + ` > "$tmp" && cat "$tmp" > ` + settings,
	}, "; "))
	if r.rc != 0 {
		t.Fatalf("launch 1: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	first := configListPackages(t, r.stdout, "=== FIRST ===", "=== END ===")
	if countEntry(first, configListKilo) != 1 {
		t.Fatalf("launch 1: packages = %v, want the contributed entry exactly once", first)
	}

	// ── Launch 2: same packs. The agent's entry was kept as the user's; the contributed
	// entry is still there, once — the capture did not freeze the whole array.
	r = runYolo(t, dir, `echo "=== SECOND ==="; cat `+settings+`; echo "=== END ==="`)
	if r.rc != 0 {
		t.Fatalf("launch 2: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	second := configListPackages(t, r.stdout, "=== SECOND ===", "=== END ===")
	if countEntry(second, configListKilo) != 1 || countEntry(second, configListEdit) != 1 {
		t.Fatalf("launch 2: packages = %v, want the contributed entry and the in-jail edit once each", second)
	}
	overlay, _ := os.ReadFile(filepath.Join(dir, ".yolo", "prism", "pi-settings.overlay.json"))
	if strings.Contains(string(overlay), "packages") {
		t.Fatalf("the capture overlay holds the whole packages array — the freeze OQ-AL1 rules out:\n%s", overlay)
	}

	// ── Launch 3: the contributing pack is dropped. Its entry is gone; the edit is kept.
	packHome(t, `{"packs": ["pi"]}`)
	r = runYolo(t, dir, `echo "=== THIRD ==="; cat `+settings+`; echo "=== END ==="`)
	if r.rc != 0 {
		t.Fatalf("launch 3: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	third := configListPackages(t, r.stdout, "=== THIRD ===", "=== END ===")
	if countEntry(third, configListKilo) != 0 || countEntry(third, configListEdit) != 1 {
		t.Fatalf("launch 3 (pack dropped): packages = %v, want the contributed entry gone and "+
			"the in-jail edit kept", third)
	}
}

// TestAPostureListRendersOnlyItsOwnSideOfTheLine is the launched-jail half of posture lists
// (docs/design/notch-scoped-config-contributions.md §4.1): a pack's `autonomy` contribution
// carries one `config-list` entry per posture, and a jail, whose notch selects the autonomous
// posture, renders that posture's entry and not the guarded one's. Until this test, unit tests
// and in-process chains were the only evidence; none of them booted a jail.
//
// The host half runs too, through the same isolated home, so the guarded entry's absence in
// the jail cannot pass vacuously: `config render --at host` must show it, which proves the
// fixture's guarded list is well-formed and placed, and that the jail dropped it by posture.
func TestAPostureListRendersOnlyItsOwnSideOfTheLine(t *testing.T) {
	requireJail(t)

	const (
		jailOnly = "npm:posture-jail-only"
		hostOnly = "npm:posture-host-only"
	)
	pack := t.TempDir()
	list := func(entry string) string {
		return `{"lists": [{"surface": "pi/settings", "path": "/packages", "add": ["` + entry + `"]}]}`
	}
	manifest := `{
  "name": "posture-list-fixture",
  "description": "posture lists: one pi package for each side of the confinement line",
  "contributes": [
    {"kind": "autonomy", "autonomous": ` + list(jailOnly) + `, "guarded": ` + list(hostOnly) + `}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{}`)
	// `host_management: own`, because the host preview below is what `yolo host apply` would
	// write, and since the `assert` retirement (OQ-CO14) an unset key is `none`, under which it
	// writes nothing and the preview refuses the surface.
	packHome(t, `{"packs": ["pi", "file://`+pack+`"], "host_management": "own"}`)

	r := runYolo(t, dir, `echo "=== JAIL ==="; cat "$HOME/.pi/agent/settings.json"; echo "=== END ==="`)
	if r.rc != 0 {
		t.Fatalf("launch: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	inJail := configListPackages(t, r.stdout, "=== JAIL ===", "=== END ===")
	if countEntry(inJail, jailOnly) != 1 || countEntry(inJail, hostOnly) != 0 {
		t.Fatalf("in the jail, packages = %v; want the autonomous posture's %q once and the "+
			"guarded posture's %q absent", inJail, jailOnly, hostOnly)
	}

	h := runYoloCLI(t, dir, "config", "render", "pi/settings", "--at", "host")
	if h.rc != 0 {
		t.Fatalf("config render --at host: rc %d\nstdout: %s\nstderr: %s", h.rc, h.stdout, h.stderr)
	}
	if !strings.Contains(h.stdout, hostOnly) || strings.Contains(h.stdout, jailOnly) {
		t.Fatalf("the host preview must carry the guarded posture's %q and not the autonomous "+
			"one's %q:\n%s", hostOnly, jailOnly, h.stdout)
	}
}
