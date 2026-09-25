package integration

// nodefloor_test.go is the container-level proof of a declared Node floor
// (docs/reference/agent-program-runtimes.md), both halves of it:
//
//   - OQ-AR3 (the first row of that doc's "What is measured"): a floor nothing satisfies REFUSES the launch — "the jail does not
//     start" — in one message naming the pack, the program, the floor and what is available.
//     The unit tier runs the bootstrap and the composed command against fakes; only a launch
//     proves the real `yolo internal node-floor-satisfied`, the real `mise install` failing, the
//     real stage wrapper and the container's exit status all carry the refusal to the host.
//   - The second row of "What is measured": a workspace pinning Node 20 keeps its node for everything but the program the
//     pack declares — pi's launcher execs an interpreter meeting pi's floor while the shell's
//     `node` stays the workspace's 20.
//
// NO AGENT IS STARTED (AGENTS.md: "No agent tests"). The first test's program is never installed
// at all; the second inspects pi's generated launcher and probes the interpreter it bakes with
// `--version`. The one `pi --version` probe is behind requireRealPackInstalls, because it makes
// the launcher install pi from its vendor.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// TestAnUnsatisfiableNodeFloorRefusesTheLaunch: a local pack declares a program with
// node_floor "99". No Node 99 exists anywhere and `mise install node@99` cannot fetch one, so
// the provisioning stage refuses and the target never runs.
//
// The target prints NODEFLOOR-TARGET-$((40+2)): the Executing banner would show that TEXT, and
// only running it prints NODEFLOOR-TARGET-42, so the marker's absence means the target did not
// run rather than that nothing was echoed.
func TestAnUnsatisfiableNodeFloorRefusesTheLaunch(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	const manifest = `{
  "name": "nodefloor-fixture",
  "description": "declares a program whose Node floor nothing can satisfy",
  "contributes": [
    {"kind": "program", "bin": "nodefloor-probe", "via": "npm",
     "package": "nodefloor-probe-never-installed", "node_floor": "99"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "nodefloor-fixture"}]}`)

	r := runYolo(t, dir, `true && echo NODEFLOOR-TARGET-$((40+2))`)
	all := r.stdout + r.stderr

	if r.rc == 0 {
		t.Fatalf("the launch succeeded with a floor nothing satisfies — OQ-AR3 says the jail does "+
			"not start\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if r.rc != provision.RefusedStatus {
		t.Errorf("rc = %d, want provision.RefusedStatus (%d) carried through the container's exit",
			r.rc, provision.RefusedStatus)
	}
	if strings.Contains(all, "NODEFLOOR-TARGET-42") {
		t.Errorf("the target ran after the refusal:\n%s", all)
	}
	if strings.Contains(all, "Executing:") {
		t.Errorf("the Executing banner printed, announcing a command that must not run:\n%s", all)
	}
	for what, want := range map[string]string{
		"the refusal":  "REFUSING to start this jail",
		"the pack":     "pack nodefloor-fixture",
		"the program":  "program nodefloor-probe",
		"the floor":    "Node >=99",
		"availability": "Available: ",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the refusal does not name %s (%q):\n%s", what, want, all)
		}
	}
}

// TestANode20WorkspacePinDoesNotChooseThePiInterpreter: a workspace mise.toml pins Node 20 and
// the user selects `pi`, whose pack declares node_floor 22.19 (pi's own engines.node). The
// shell's `node` must stay 20; pi's launcher must exec an interpreter that meets 22.19.
func TestANode20WorkspacePinDoesNotChooseThePiInterpreter(t *testing.T) {
	requireJail(t)

	dir := writeProjectWithPacks(t, `{}`, "pi")
	if err := os.WriteFile(filepath.Join(dir, "mise.toml"), []byte("[tools]\nnode = \"20\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The interpreter is read off the launcher's exec line, `exec <node> "$REAL_BIN" …`, the
	// shape npmLauncherTemplate renders when a floor resolves.
	script := strings.Join([]string{
		`echo "=== SHELL NODE ==="`,
		`node --version`,
		`echo "=== EXEC LINES ==="`,
		`grep -E '^[[:space:]]*exec .*REAL_BIN' "$HOME/.yolo/bin/launch/pi"`,
		`echo "=== INTERP ==="`,
		`interp=$(awk '$1=="exec" && $3 ~ /REAL_BIN/ {print $2; exit}' "$HOME/.yolo/bin/launch/pi" | tr -d "'")`,
		`echo "$interp"`,
		`echo "=== INTERP VERSION ==="`,
		`"$interp" --version`,
		`echo "=== SHELL NODE AFTER ==="`,
		`node --version`,
		`echo "=== END ==="`,
	}, "; ")
	r := runYolo(t, dir, script, withTimeout(600*time.Second))
	if r.rc != 0 {
		t.Fatalf("probe failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}

	for _, sec := range [][2]string{
		{"=== SHELL NODE ===", "=== EXEC LINES ==="},
		{"=== SHELL NODE AFTER ===", "=== END ==="},
	} {
		if v := strings.TrimSpace(section(r.stdout, sec[0], sec[1])); !strings.HasPrefix(v, "v20.") {
			t.Errorf("%s: the shell's node is %q, want the workspace's pinned v20.x — the fix must "+
				"change the agent's interpreter and nothing else (What this does not do)", sec[0], v)
		}
	}

	// EVERY exec path carries the interpreter: the launcher has two (the main one and the
	// re-entry guard's), and a bare `exec "$REAL_BIN"` on either runs pi under the pin.
	execLines := strings.Split(strings.TrimSpace(section(r.stdout, "=== EXEC LINES ===", "=== INTERP ===")), "\n")
	if len(execLines) < 2 {
		t.Errorf("want both exec paths of pi's launcher, got %d: %q", len(execLines), execLines)
	}
	for _, l := range execLines {
		if strings.Contains(l, `exec "$REAL_BIN"`) {
			t.Errorf("an exec path runs pi's bin directly, so its #!/usr/bin/env node shebang "+
				"picks the workspace's Node 20: %s", strings.TrimSpace(l))
		}
	}

	interp := strings.TrimSpace(section(r.stdout, "=== INTERP ===", "=== INTERP VERSION ==="))
	if !filepath.IsAbs(interp) || strings.Contains(interp, "/shims/") {
		t.Errorf("the baked interpreter %q must be an absolute node, never a mise shim (a shim "+
			"resolves the workspace's pin)", interp)
	}
	v := strings.TrimPrefix(strings.TrimSpace(section(r.stdout, "=== INTERP VERSION ===", "=== SHELL NODE AFTER ===")), "v")
	if !packdecl.SatisfiesNodeFloor(v, "22.19") {
		t.Errorf("pi's launcher execs %s, version %q, which does not meet pi's floor 22.19", interp, v)
	}

	// The end-to-end half — pi itself starting under the pin — needs pi installed from its
	// vendor, which is this suite's gated class of work.
	t.Run("pi --version under the pin", func(t *testing.T) {
		requireRealPackInstalls(t)
		r := runYolo(t, dir, `pi --version`, withTimeout(600*time.Second))
		if r.rc != 0 {
			t.Errorf("pi --version failed under a Node 20 workspace pin: rc %d\nstdout: %s\nstderr: %s",
				r.rc, r.stdout, r.stderr)
		}
	})
}
