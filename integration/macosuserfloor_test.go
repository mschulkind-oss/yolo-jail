package integration

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
)

// THE FIRST TEST BEHIND THE macos-user GATE, and the root of the runbook's dependency
// chain: docs/plans/runbooks/macos-user-manual-checks.md item 6.
//
// WHAT IT SETTLES. `OQ-P1`'s FLOOR — the native darwin closure every macos-user launch
// builds, so the sandbox has mise, node, git, ripgrep and the rest of what the container
// image bakes (darwinpkg.FloorNames(): that image core, minus what darwin cannot build
// and minus the GNU userland OQ-P2 rules out; docs/design/macos-user-provisioning.md §9)
// — is the single largest unmeasured claim on this backend. Nothing has ever observed it
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

	r := runMacosUser(t, macosUserWorkspace(t, `{}`), macosUserFloorProbe(macosUserFloorProbeBins()))
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
	for _, b := range macosUserFloorBins {
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
	// THE POLICY HALF, one probe per excluded package rather than one for the whole
	// ruling. `which` alone was the runbook's spelling and it settles `which` alone;
	// OQ-P2 is a claim about NINE packages, and the eight it does not name are the ones
	// whose flags differ most from the Mac's own (`sed -i`, `find -printf`,
	// `tar --wildcards`). "Absent" and "the system's copy" are both passes here — only a
	// store path is a failure, because the exclusion's whole job is to leave macOS's own
	// binary in place.
	for pkg, bin := range floorPolicyProbes {
		path, seen := resolved[bin]
		if !seen {
			t.Errorf("%s: the probe printed no line for it at all", bin)
			continue
		}
		if strings.HasPrefix(path, "/nix/store/") {
			t.Errorf("%s resolved to %s — the GNU userland the floor is supposed to "+
				"exclude, from the `%s` entry of darwinpkg.FloorExcludedPolicy (OQ-P2). "+
				"The exclusion list is right and the SHIPPED PROFILE does not honour it, "+
				"which is exactly the gap a unit test over the list cannot see: the agent "+
				"is now getting GNU `%s` on somebody's Mac, where every flag that differs "+
				"from the BSD copy is a silent behaviour change.", bin, path, pkg, bin)
		}
	}
}

// macosUserFloorBins is the FLOOR half of item 6's probe: names that must resolve, and
// must resolve into the nix profile.
//
// They are the runbook's, which is the spec, and they are spelled here rather than
// derived from darwinpkg.FloorNames() because a floor ENTRY is not a binary name —
// `nodejs_24` ships `node` and `npm`, `ripgrep` ships `rg`, and `cacert`, `zlib` and
// `tzdata` ship no command at all — so a derived list would need a mapping table that is
// itself the thing nobody checks.
var macosUserFloorBins = []string{"mise", "node", "npm", "git", "rg", "fd", "jq", "gh", "curl"}

// floorPolicyProbes maps each darwinpkg.FloorExcludedPolicy entry to the command it would
// put on the sandbox's PATH if the exclusion failed to take effect.
//
// A MAPPING TABLE IS UNAVOIDABLE HERE for macosUserFloorBins' reason — a package name is
// not a binary name — so the table is written down and then GATED: the Linux-side test
// below fails if it and the exclusion list ever disagree. That is the half a derived list
// could not have, and the half a hand-maintained list normally lacks.
//
// The binary chosen for each is one macOS ships its own copy of, and (deliberately) not
// `ls`: yolo's generated bashrc defines `alias ls=…`, and `command -v` reports an alias
// rather than a path. `stat` stands for coreutils instead — same package, no alias, and
// its BSD and GNU spellings share almost no flags.
var floorPolicyProbes = map[string]string{
	"coreutils-full": "stat",
	"findutils":      "find",
	"gnused":         "sed",
	"gnugrep":        "grep",
	"gawk":           "awk",
	"gnupatch":       "patch",
	"diffutils":      "diff",
	"gnutar":         "tar",
	"which":          "which",
}

// floorPolicyBins returns the probe binaries in a stable order (map iteration is not one,
// and a probe script that reorders itself between runs makes two CI logs undiffable).
func floorPolicyBins() []string {
	out := make([]string, 0, len(floorPolicyProbes))
	for _, bin := range floorPolicyProbes {
		out = append(out, bin)
	}
	sort.Strings(out)
	return out
}

// macosUserFloorProbeBins is EVERY name item 6's one launch asks about: the floor
// binaries that must come from the store, then the policy binaries that must not.
//
// It is a function rather than two lists spliced at the call site because it is what the
// Linux-side test below reaches for. Delete `floorPolicyBins()` from it and the probe
// silently stops asking the policy question while the assertion loop above finds nothing
// to complain about — the "green because it asked nothing" shape again, and the reason
// that test renders the REAL script instead of trusting this to stay wired.
func macosUserFloorProbeBins() []string {
	return append(append([]string{}, macosUserFloorBins...), floorPolicyBins()...)
}

// macosUserFloorProbe renders the probe script: one `name=<resolved path>` line per
// binary, fenced so section() can find it in the launch's stdout.
//
// `command -v` rather than `which --version`: the runbook spells the policy half as
// `which --version` printing nothing useful, which depends on what Apple's `which` does
// with a flag it does not know. The RESOLVED PATH says the same thing and depends on
// nothing (OQ-P2).
func macosUserFloorProbe(bins []string) string {
	var probe strings.Builder
	probe.WriteString(`echo "=== FLOOR ==="` + "\n")
	for _, b := range bins {
		fmt.Fprintf(&probe, "printf '%s=%%s\\n' \"$(command -v %s || echo MISSING)\"\n", b, b)
	}
	probe.WriteString(`echo "=== END ==="` + "\n")
	return probe.String()
}

// TestMacosUserFloorProbeAsksEveryQuestionItem6Claims is the ungated half of item 6, and
// it runs HERE — Linux, under -short, on the machine that develops this repo.
//
// It is not behind requireMacosUser and so is not in the vacuity ledger, which is correct:
// it exercises no backend. What it exercises is the TABLE above, whose failure mode is
// silent. Add a package to darwinpkg.FloorExcludedPolicy without a probe and the Mac test
// keeps passing while saying nothing about the new entry — the same "green because it
// asked nothing" this suite's gate exists to stop, one level down.
//
// It carries the TestMacosUser prefix so `-run '^TestMacosUser'` selects it too: a macOS
// job that runs the suite gets this check for free, and an ordinary Linux `just test-fast`
// gets it long before any Mac does.
func TestMacosUserFloorProbeAsksEveryQuestionItem6Claims(t *testing.T) {
	excluded := map[string]struct{}{}
	for _, pkg := range darwinpkg.FloorExcludedPolicy {
		excluded[pkg] = struct{}{}
		if _, ok := floorPolicyProbes[pkg]; !ok {
			t.Errorf("darwinpkg.FloorExcludedPolicy names %q and floorPolicyProbes has no "+
				"probe for it, so the macos-user floor test would pass without ever asking "+
				"whether %q's binaries reached the sandbox. Add the command name it "+
				"installs (OQ-P2, runbook item 6).", pkg, pkg)
		}
	}
	floor := map[string]struct{}{}
	for _, name := range darwinpkg.FloorNames() {
		floor[name] = struct{}{}
	}
	// THE CALL SITE, pinned. Everything above is a fact about a table, and a table
	// nothing reads is not a test (AGENTS.md: "does it fail if I delete the call site?").
	// So the REAL probe script is rendered from the REAL list and every name is required
	// to appear in it — which is what turns "floorPolicyBins() was dropped from
	// macosUserFloorProbeBins" from a silent loss into a red Linux run.
	//
	// ⚠ THE EXPECTED LIST IS SPELLED AGAIN HERE ON PURPOSE. Calling
	// macosUserFloorProbeBins() for both sides would make the two agree by construction
	// and assert nothing — the mutation above would pass. Do not "simplify" it.
	script := macosUserFloorProbe(macosUserFloorProbeBins())
	for _, bin := range append(append([]string{}, macosUserFloorBins...), floorPolicyBins()...) {
		if !strings.Contains(script, "command -v "+bin+" ") {
			t.Errorf("the probe item 6 actually runs never asks about %q. The launch would "+
				"come back green having measured one binary fewer than this file claims "+
				"(macosUserFloorProbeBins).\nscript:\n%s", bin, script)
		}
	}
	for pkg, bin := range floorPolicyProbes {
		if _, ok := excluded[pkg]; !ok {
			t.Errorf("floorPolicyProbes probes %q for %q, but %q is no longer on "+
				"darwinpkg.FloorExcludedPolicy — so the test asserts that a package the "+
				"floor may now SHIP must not come from the store. Drop the probe, or put "+
				"the package back on the exclusion list.", bin, pkg, pkg)
		}
		if _, ok := floor[pkg]; ok {
			t.Errorf("%q is both on the floor and probed as excluded: the two halves of "+
				"the item 6 assertion now contradict each other for %q.", pkg, bin)
		}
	}
}
