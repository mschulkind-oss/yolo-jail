package loopholes

// §4.3a's PLACEMENT rule applied to a loophole's MANIFEST faces — the "still owed"
// half of landing item 1a. The config faces landed a batch earlier; these are the
// three targets only this package can resolve: the module dir, and the two host-side
// argvs after {loophole_dir} substitution.

import (
	"path/filepath"
	"strings"
	"testing"
)

// A module dir inside the mounted workspace is refused by name, and the refusal
// covers the whole module rather than the one script the argv named.
func TestPlacementProblemsRefusesAModuleDirInTheWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	mod := mkdir(t, filepath.Join(ws, "loopholes", "acme"))
	writeManifest(t, mod, map[string]any{
		"name":        "acme",
		"host_daemon": map[string]any{"cmd": []any{"python3", "{loophole_dir}/srv.py"}},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	probs := lp.PlacementProblems(ws)
	if len(probs) != 1 {
		t.Fatalf("problems = %v, want one (the dir refusal subsumes the argv it contains)", probs)
	}
	for _, want := range []string{"loophole 'acme'", "module dir", "WHOLE module", "§4.3a"} {
		if !strings.Contains(probs[0], want) {
			t.Errorf("refusal does not carry %q:\n  %s", want, probs[0])
		}
	}
}

// The argv faces need POST-substitution argvs, which is exactly what a resolved
// record carries: a module dir placed safely but a doctor_cmd reaching into the
// workspace is refused, and {loophole_dir} in the daemon argv is NOT mistaken for a
// framework placeholder because it is already gone by then.
func TestPlacementProblemsChecksResolvedArgvs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	mod := mkdir(t, filepath.Join(t.TempDir(), "acme"))
	writeManifest(t, mod, map[string]any{
		"name":        "acme",
		"host_daemon": map[string]any{"cmd": []any{"python3", "{loophole_dir}/srv.py"}},
		"doctor_cmd":  []any{"python3", filepath.Join(ws, "check.py")},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	probs := lp.PlacementProblems(ws)
	if len(probs) != 1 || !strings.Contains(probs[0], "doctor_cmd[1]") {
		t.Fatalf("problems = %v, want one doctor_cmd refusal", probs)
	}
	if !strings.Contains(probs[0], filepath.Join(ws, "check.py")) {
		t.Errorf("the refusal must name the resolved target: %s", probs[0])
	}
}

// A CONFIG loophole has no module dir — Path holds a synthetic marker, not a path —
// so the dir face must not judge it. Its `command` is the config faces' business.
func TestPlacementProblemsSkipsAConfigLoopholesSyntheticPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	lps := synthesizeConfigLoopholes(orderedFromPairs("svc", map[string]any{
		"command": []any{"/usr/bin/daemon"},
	}))
	if len(lps) != 1 {
		t.Fatalf("synthesized %d loopholes", len(lps))
	}
	if probs := lps[0].PlacementProblems(ws); len(probs) != 0 {
		t.Errorf("a config loophole's synthetic Path was read as a module dir: %v", probs)
	}
}

// A legitimately-placed loophole draws nothing. A false positive here refuses a
// working loophole at every launch, which costs more than the miss.
func TestPlacementProblemsLeavesALegitimateLoopholeAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mod := mkdir(t, filepath.Join(home, "tools", "loopholes", "hp"))
	writeManifest(t, mod, map[string]any{
		"name":        "hp",
		"host_daemon": map[string]any{"cmd": []any{"yolo", "internal", "daemon", "hp", "--endpoint", "{endpoint}"}},
		"doctor_cmd":  []any{"yolo", "internal", "daemon", "hp", "--self-check"},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	if probs := lp.PlacementProblems(t.TempDir()); len(probs) != 0 {
		t.Errorf("a legitimate loophole drew %v", probs)
	}
}
