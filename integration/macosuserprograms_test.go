package integration

import (
	"strings"
	"testing"
)

// `yolo programs` AND `programs.autoprune` ON macos-user, asked of a real sandbox.
//
// The boot catalog runs on this backend now that everything its one line points at works there
// (docs/plans/notch-convergence.md, NC-D26): the bootstrap keeps a boot log, the session names
// the staged pack tree and the workspace (YOLO_PACK_ROOT, YOLO_DARWIN_WORKSPACE), and the launch
// relays the user's `programs.autoprune`. The unit tests pin each half on Linux
// (internal/macosuser/packroot_test.go, internal/cli/programs_test.go,
// internal/entrypoint/catalog_test.go); these ask the sandbox.
//
// The orphan is planted FROM INSIDE THE SANDBOX, under ~/.npm-global, which the home layout
// links into <workspace>/.yolo/home: the exact place a dropped npm program's bytes stay.

// macosUserOrphan is the planted package. No pack or preset declares it.
const macosUserOrphan = "yolo-it-orphan"

// macosUserPlantOrphan is the shell that plants it.
const macosUserPlantOrphan = `mkdir -p ~/.npm-global/lib/node_modules/` + macosUserOrphan +
	` && echo '{}' > ~/.npm-global/lib/node_modules/` + macosUserOrphan + `/package.json`

// `yolo programs ls` inside the sandbox sees the staged packs (it does not decline with "No
// staged packs here"), lists the orphan, and exits 0, a report rather than a gate.
func TestMacosUserProgramsLsSeesTheStagedPacks(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["guardrails"]}`)
	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "programs ls", ws, strings.Join([]string{
		macosUserPlantOrphan,
		`echo "=== LS ==="`,
		`yolo programs ls; echo "RC=$?"`,
		`echo "=== END ==="`,
	}, "\n"))
	ls := section(r.stdout, "=== LS ===", "=== END ===")
	if strings.Contains(ls, "No staged packs here") {
		t.Fatalf("`yolo programs ls` in the sandbox found no staged pack tree, so the session "+
			"carries no YOLO_PACK_ROOT (macosuser.BuildRunPlanWithDaemons):\n%s", ls)
	}
	if !strings.Contains(ls, macosUserOrphan) || !strings.Contains(ls, "RC=0") {
		t.Errorf("`yolo programs ls` did not list the planted orphan and exit 0:\n%s", ls)
	}
}

// With `programs.autoprune` on in the user's config, the NEXT launch's bootstrap removes what
// the first planted, says so, and the sandbox finds it gone. Two launches, because the catalog
// observes the previous launch's state.
func TestMacosUserAutopruneRemovesAnOrphan(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["guardrails"], "programs": {"autoprune": true}}`)
	ws := macosUserWorkspace(t, `{}`)
	macosUserRunProbe(t, "autoprune plant", ws, macosUserPlantOrphan+"\necho \"=== END ===\"")

	r := macosUserRunProbe(t, "autoprune", ws, strings.Join([]string{
		`echo "=== PRESENT ==="`,
		`test -e ~/.npm-global/lib/node_modules/` + macosUserOrphan + ` && echo PRESENT || echo GONE`,
		`echo "=== END ==="`,
	}, "\n"))
	if !strings.Contains(r.combined(), "boot autoprune: removing "+macosUserOrphan) {
		t.Errorf("the second launch did not announce removing the orphan:\n%s", r.combined())
	}
	if got := section(r.stdout, "=== PRESENT ===", "=== END ==="); !strings.Contains(got, "GONE") {
		t.Errorf("the orphan survived a launch with programs.autoprune on:\n%s", got)
	}
}
