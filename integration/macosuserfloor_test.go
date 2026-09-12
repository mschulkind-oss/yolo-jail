package integration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
)

// THE FIRST TEST BEHIND THE macos-user GATE, and the root of the runbook's dependency
// chain: docs/plans/runbooks/macos-user-manual-checks.md item 6.
//
// WHAT IT SETTLES. `OQ-P1`'s FLOOR — the ~27-package native darwin closure every
// macos-user launch builds, so the sandbox has mise, node, git, ripgrep and the rest
// of what the container image bakes (docs/design/macos-user-provisioning.md §9) — is
// the single largest unmeasured claim on this backend. Nothing has ever observed it
// reaching a sandbox's PATH. Its other half is `OQ-P2`'s ruling that the floor carries
// NO GNU userland, which is a claim about the shipped article and not about the
// exclusion list a unit test can read.
//
// WHY IT HAS TO BE AN INTEGRATION TEST, in this repo's own terms: the unit suite pins
// darwinpkg.FloorNames() and the flake attribute it mirrors, and would stay green if
// the profile never reached the sandbox at all. Three things compose here and none of
// them is visible from a pure function — nix builds the closure natively, the launch
// puts its bin dir on YOLO_DARWIN_LOGIN_PATH, and the generated login rc files
// re-prepend that AHEAD of macOS's own path_helper. A Homebrew or /usr/bin answer
// below means the re-prepend lost, which is the failure the runbook's item 3 was
// written to catch and the one this repeats every run.
//
// IF THIS FAILS, STOP. The runbook says so and it is a real dependency, not a
// courtesy: no floor means no `mise` and no `npm`, so the provisioning stage's first
// line fails, so `mise_tools` and `lsp_servers` install nothing. Reporting those as
// separate bugs is reporting one bug four times.
func TestMacosUserFloorReachesTheSandboxPath(t *testing.T) {
	requireMacosUser(t)

	// The names are the runbook's, which is the spec. They are spelled here rather
	// than derived from darwinpkg.FloorNames() because a floor ENTRY is not a binary
	// name — `nodejs_24` ships `node` and `npm`, `ripgrep` ships `rg`, and `cacert`,
	// `zlib` and `tzdata` ship no command at all — so a derived list would need a
	// mapping table that is itself the thing nobody checks.
	floorBins := []string{"mise", "node", "npm", "git", "rg", "fd", "jq", "gh", "curl"}
	// `which` is the probe for the POLICY half. It is on darwinpkg.FloorExcludedPolicy
	// (OQ-P2, no GNU userland), and macOS ships its own /usr/bin/which — so "absent"
	// and "the system's" are both passes and only a store path is a failure. The
	// runbook spells this as `which --version` printing nothing useful; asserting on
	// the RESOLVED PATH says the same thing without depending on what Apple's `which`
	// prints when handed a flag it does not know.
	const excluded = "which"

	var probe strings.Builder
	probe.WriteString(`echo "=== FLOOR ==="` + "\n")
	for _, b := range append(append([]string{}, floorBins...), excluded) {
		fmt.Fprintf(&probe, "printf '%s=%%s\\n' \"$(command -v %s || echo MISSING)\"\n", b, b)
	}
	probe.WriteString(`echo "=== END ==="` + "\n")

	r := runMacosUser(t, macosUserWorkspace(t, `{}`), probe.String())
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before any floor probe could "+
			"answer. Runbook item 6 asks for `yolo --dry-run` output alongside this, "+
			"since it names the profile path the launch built.\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := section(r.stdout, "=== FLOOR ===", "=== END ===")
	if strings.TrimSpace(got) == "" {
		t.Fatalf("the sandbox produced no probe output at all, so nothing below can be "+
			"read as a floor result.\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}

	resolved := map[string]string{}
	for _, line := range strings.Split(got, "\n") {
		if name, path, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			resolved[name] = path
		}
	}
	for _, b := range floorBins {
		path, seen := resolved[b]
		switch {
		case !seen:
			t.Errorf("%s: the probe printed no line for it at all", b)
		case path == "MISSING":
			t.Errorf("%s does not resolve in the sandbox — the floor did not reach this "+
				"PATH (runbook item 6; %d floor entries were built)", b, len(darwinpkg.FloorNames()))
		case !strings.HasPrefix(path, "/nix/store/"):
			t.Errorf("%s resolved to %s rather than into the nix profile. The floor is "+
				"on PATH but LOST: macOS's path_helper or a Homebrew prefix is ahead of "+
				"the login rc files' re-prepend of $YOLO_DARWIN_LOGIN_PATH, so the agent "+
				"gets the host's copy of a tool yolo promised to supply.", b, path)
		}
	}
	if path := resolved[excluded]; strings.HasPrefix(path, "/nix/store/") {
		t.Errorf("%s resolved to %s — a GNU userland tool the floor is supposed to "+
			"exclude (darwinpkg.FloorExcludedPolicy, OQ-P2). The exclusion list is right "+
			"and the shipped profile does not honour it, which is exactly the gap a unit "+
			"test over the list cannot see.", excluded, path)
	}
}
