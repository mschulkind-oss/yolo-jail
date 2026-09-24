package integration

// configlist_test.go is the end-to-end check of `config-list` on pi's `stateful` settings
// surface (docs/design/additive-config-lists.md, OQ-AL1) — three launches of ONE workspace,
// with capture-on-terminate's host-side captureSurfaceAt running between them. It is the only
// test that exercises that teardown capture over a real jail: the unit suites call it
// directly, and none of them can show that the sidecar it reads is the one a real boot wrote.
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
