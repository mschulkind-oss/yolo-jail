package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// and minus the GNU userland OQ-P2 rules out; docs/reference/macos-user-provisioning.md, "The floor")
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

	resolved := parseFloorProbe(got)
	home := resolved[floorProbeHomeKey]
	mise := resolved[floorProbeMiseKey]
	if home == "" || mise == "" {
		t.Fatalf("the probe did not print the sandbox's HOME and MISE_DATA_DIR, so no resolution "+
			"below can be classified:\n%s", got)
	}
	for _, b := range macosUserFloorBins {
		full, seen := resolved[b]
		floor, floorSeen := resolved[b+floorProbeFloorSuffix]
		if !seen || !floorSeen {
			t.Errorf("%s: the probe printed no line for it at all", b)
			continue
		}
		problem, note := floorBinProblem(b, full, floor, home, mise, "/nix/store/")
		if note != "" {
			t.Log(note)
		}
		if problem != "" {
			t.Errorf("%s (runbook item 6; %d floor entries were built)", problem, len(darwinpkg.FloorNames()))
		}
	}
	// THE POLICY HALF, one probe per excluded package rather than one for the whole
	// ruling. `which` alone was the runbook's spelling and it settles `which` alone;
	// OQ-P2 is a claim about NINE packages, and the eight it does not name are the ones
	// whose flags differ most from the Mac's own (`sed -i`, `find -printf`,
	// `tar --wildcards`). "Absent" and "the system's copy" are both passes here — only a
	// store path is a failure, because the exclusion's whole job is to leave macOS's own
	// binary in place. Both resolutions are asked: the agent's, and the one past the sandbox
	// home's own directories, which is the floor profile's alone.
	for pkg, bin := range floorPolicyProbes {
		full, seen := resolved[bin]
		floor, floorSeen := resolved[bin+floorProbeFloorSuffix]
		if !seen || !floorSeen {
			t.Errorf("%s: the probe printed no line for it at all", bin)
			continue
		}
		path := full
		if !strings.HasPrefix(path, "/nix/store/") {
			path = floor
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
//
// TWO LINES PER BINARY. `name=` is what the agent gets, on the sandbox's whole PATH. `name@floor=`
// is the same lookup on that PATH with every directory under the sandbox's $HOME and under its
// $MISE_DATA_DIR taken out, and the two roots it took out are printed first. Those directories —
// the mise shims among them — head the PATH by design and outrank the floor
// (entrypoint.HomePathDirs, OQ-PD27; on mise, OQ-DM3 in
// docs/design/macos-user-suite-on-a-developer-mac.md, ruled 2026-10-18 for parity with the jail).
// The mise store is the MACHINE root's, shared by every workspace and test on the Mac
// (macos-user-model-plan.md MMP-D6; in the account mode `/Users/_yolojail/.yolo/mise` since slice
// 12a moved HOME into the workspace, MMP-D25): a mise tool another workspace installed leaves a
// shim there that shadows the floor's `node` whatever this workspace configures. Before 12a that
// store sat under HOME, which is why the filter once named HOME alone. So `@floor` is the floor's
// own reach, which no stale install can hide, and `name=` is checked only for a winner outside
// both roots.
func macosUserFloorProbe(bins []string) string {
	var probe strings.Builder
	probe.WriteString(`echo "=== FLOOR ==="` + "\n")
	fmt.Fprintf(&probe, "printf '%s=%%s\\n' \"$HOME\"\n", floorProbeHomeKey)
	fmt.Fprintf(&probe, "printf '%s=%%s\\n' \"${MISE_DATA_DIR:-$HOME}\"\n", floorProbeMiseKey)
	probe.WriteString(`_mise="${MISE_DATA_DIR:-$HOME}"` + "\n")
	probe.WriteString(`floor_path=$(IFS=:; out=; for d in $PATH; do case "$d" in ("$HOME"|"$HOME"/*|"$_mise"|"$_mise"/*) ;; ` +
		`(*) out="${out:+$out:}$d" ;; esac; done; printf '%s' "$out")` + "\n")
	for _, b := range bins {
		fmt.Fprintf(&probe, "printf '%s=%%s\\n' \"$(command -v %s || echo MISSING)\"\n", b, b)
		fmt.Fprintf(&probe, "printf '%s%s=%%s\\n' \"$(PATH=$floor_path; command -v %s || echo MISSING)\"\n",
			b, floorProbeFloorSuffix, b)
	}
	probe.WriteString(`echo "=== END ==="` + "\n")
	return probe.String()
}

// floorProbeHomeKey, floorProbeMiseKey and floorProbeFloorSuffix name the probe's extra lines: the
// sandbox's HOME, its mise store (MISE_DATA_DIR, HOME when unset), and a binary's resolution past
// both roots' directories.
const (
	floorProbeHomeKey     = "@home"
	floorProbeMiseKey     = "@mise"
	floorProbeFloorSuffix = "@floor"
)

// parseFloorProbe reads the probe's `name=path` lines.
func parseFloorProbe(out string) map[string]string {
	resolved := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if name, path, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			resolved[name] = path
		}
	}
	return resolved
}

// floorBinProblem judges one floor binary from its two resolutions: full, on the agent's PATH, and
// floor, past the directories under the sandbox's home and its mise store (mise); store is the nix
// store's prefix. problem is "" when the floor reached the PATH; note records a directory under
// either root that outranks it, which is by design.
//
//   - floor MISSING: the floor did not reach the PATH.
//   - floor outside the store: something ahead of the floor that is NOT the sandbox home's —
//     macOS's path_helper, a Homebrew prefix — beat the login rc files' re-prepend. The one
//     failure path_helper and Homebrew are named for.
//   - full under the home: a mise shim (or another home directory) outranks the floor, as on the
//     container jail, where /mise/shims sits ahead of /bin. A note, not a failure.
//   - full anything else: not a PATH lookup at all, since a file outside the home that came first
//     on the whole PATH comes first on the filtered one too. `command -v` printing an alias or a
//     function the login rc files define is what reaches here.
func floorBinProblem(bin, full, floor, home, mise, store string) (problem, note string) {
	under := func(p, root string) bool { return root != "" && (p == root || strings.HasPrefix(p, root+"/")) }
	inHome := func(p string) bool { return under(p, home) || under(p, mise) }
	switch {
	case floor == "MISSING":
		return fmt.Sprintf("%s does not resolve in the sandbox past its home's and mise store's directories — the "+
			"floor did not reach this PATH", bin), ""
	case !strings.HasPrefix(floor, store):
		return fmt.Sprintf("%s resolved to %s rather than into the nix profile, with the sandbox home's "+
			"directories (the mise shims among them) taken out of the lookup. The floor is on PATH but "+
			"LOST: macOS's path_helper or a Homebrew prefix is ahead of the login rc files' re-prepend "+
			"of $YOLO_DARWIN_LOGIN_PATH, so the agent gets the host's copy of a tool yolo promised to "+
			"supply.", bin, floor), ""
	case full == floor:
		return "", ""
	case inHome(full):
		kind := "a directory of the sandbox home"
		if strings.Contains(full, "/mise/shims/") {
			kind = "a mise shim"
		}
		return "", fmt.Sprintf("%s: the agent gets %s, %s, which outranks the floor's %s by design "+
			"(mise and the home's directories ahead of the floor, as in the container jail: OQ-PD27, "+
			"OQ-DM3). The mise store is the machine root's, shared by every workspace on this Mac, so "+
			"it may be another workspace's install.", bin, full, kind, floor)
	default:
		return fmt.Sprintf("%s: `command -v` in the agent's shell answers %q, which is no file on its "+
			"PATH (the floor's is %s): an alias or a function the login rc files define shadows the "+
			"floor's binary. Look for it in the sandbox home's rc files.", bin, full, floor), ""
	}
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

// TestMacosUserFloorProbeSeesTheFloorPastAMiseShim runs the REAL probe script here, under bash,
// over a stand-in sandbox laid out as slice 12a lays the account mode (MMP-D25): a HOME that is the
// workspace's sidecar, holding a stale `gh` in its npm prefix, and a mise store OUTSIDE it, in the
// machine root, whose shims dir holds a stale `jq` shim, both ahead of a stand-in floor's `jq`,
// `gh` and `git`. The agent's lookup must name the shim and the home's `gh`, the floor lookup the
// floor's binaries, and floorBinProblem must call both a note (mise and the home win, by OQ-DM3)
// and the reach a pass. That is the half a Mac cannot check before it fails: whether the probe's
// PATH filter and its lines do what the Mac test reads them as. The probe that filtered HOME
// alone failed the Mac run of 2026-11-03 this way: `node`, `npm` and `jq` resolved, past the
// filter, to `/Users/_yolojail/.yolo/mise/shims`.
func TestMacosUserFloorProbeSeesTheFloorPastAMiseShim(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	root := resolvedTempDir(t)
	home := filepath.Join(root, "ws", ".yolo", "home")
	homeBin := filepath.Join(home, ".npm-global", "bin")
	mise := filepath.Join(root, "machine", ".yolo", "mise")
	shims := filepath.Join(mise, "shims")
	store := filepath.Join(root, "store") + "/"
	floorBin := filepath.Join(store, "profile", "bin")
	for _, d := range []string{homeBin, shims, floorBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(shims, "jq"), filepath.Join(homeBin, "gh"),
		filepath.Join(floorBin, "jq"), filepath.Join(floorBin, "gh"), filepath.Join(floorBin, "git")} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c",
		macosUserFloorProbe([]string{"jq", "gh", "git", "yolo-no-such-tool"}))
	cmd.Env = []string{"HOME=" + home, "MISE_DATA_DIR=" + mise,
		"PATH=" + homeBin + ":" + shims + ":" + floorBin}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe failed: %v\n%s", err, out)
	}
	got := parseFloorProbe(section(string(out), "=== FLOOR ===", "=== END ==="))
	want := map[string]string{
		floorProbeHomeKey:             home,
		floorProbeMiseKey:             mise,
		"jq":                          filepath.Join(shims, "jq"),
		"jq" + floorProbeFloorSuffix:  filepath.Join(floorBin, "jq"),
		"gh":                          filepath.Join(homeBin, "gh"),
		"gh" + floorProbeFloorSuffix:  filepath.Join(floorBin, "gh"),
		"git":                         filepath.Join(floorBin, "git"),
		"git" + floorProbeFloorSuffix: filepath.Join(floorBin, "git"),
		"yolo-no-such-tool":           "MISSING",
		"yolo-no-such-tool@floor":     "MISSING",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("the probe printed %s=%q, want %q:\n%s", k, got[k], v, out)
		}
	}
	if problem, note := floorBinProblem("jq", got["jq"], got["jq@floor"], home, mise, store); problem != "" ||
		!strings.Contains(note, "a mise shim") {
		t.Errorf("a floor jq behind a mise shim in the machine root: problem %q, note %q; want a pass "+
			"with a note naming the shim", problem, note)
	}
	if problem, note := floorBinProblem("gh", got["gh"], got["gh@floor"], home, mise, store); problem != "" ||
		!strings.Contains(note, "a directory of the sandbox home") {
		t.Errorf("a floor gh behind the home's npm prefix: problem %q, note %q; want a pass with a note",
			problem, note)
	}
	if problem, note := floorBinProblem("git", got["git"], got["git@floor"], home, mise, store); problem != "" || note != "" {
		t.Errorf("an unshadowed floor git: problem %q, note %q; want a silent pass", problem, note)
	}
	if problem, _ := floorBinProblem("yolo-no-such-tool", "MISSING", "MISSING", home, mise, store); !strings.Contains(problem, "did not reach") {
		t.Errorf("a missing floor binary is not reported as the floor missing: %q", problem)
	}
	// The one case path_helper and Homebrew are blamed for: past both roots, the floor still lost.
	problem, _ := floorBinProblem("jq", "/opt/homebrew/bin/jq", "/opt/homebrew/bin/jq", home, mise, store)
	if !strings.Contains(problem, "path_helper") || !strings.Contains(problem, "mise shims among them") {
		t.Errorf("a Homebrew jq ahead of the floor is not reported as the re-prepend losing: %q", problem)
	}
}
