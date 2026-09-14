package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE FAILURE THIS FILE EXISTS TO PREVENT A THIRD TIME.
//
// `podman machine init --volume` REPLACES podman's default share set rather than
// adding to it. podman registers the flag with the containers.conf machine volumes
// as its default value, and pflag's StringArray clears a flag's default the first
// time it is set (spf13/pflag, string_array.go: `if !s.changed { *s.value =
// []string{val} }`). On darwin that default is three entries — `/Users:/Users`,
// `/private:/private`, `/var/folders:/var/folders` (containers/common,
// pkg/config/default_darwin.go).
//
// So the nightly's single `-v /nix:/nix` did not add /nix to the machine's shares.
// It made /nix the ONLY share, and silently removed the three the whole integration
// suite binds from. Measured on the runner, both directions:
//
//   - run 33864347239 (2026-09-04), no `-v` at all: 86 PASS / 0 FAIL, with every
//     workspace under /var/folders.
//   - run 34774210302 (2026-09-13), `-v /nix:/nix` alone: 59 `Error: statfs …: no
//     such file or directory` at rc 125 across 47 tests. Every one of the 59 paths
//     is under /var/folders, /private/var/folders or /private/tmp; none is under
//     /nix. The machine-os blob is byte-identical between the two runs, so the VM
//     image is not the variable.
//
// WHY A TEST AND NOT A COMMENT. The failure names no cause: podman statfs's each
// bind SOURCE inside the VM, so a directory the host created correctly and the VM
// cannot see is reported as if yolo had never made it — 47 tests all pointing at
// yolo. It has now cost two nightlies (2026-09-07 for /nix, 2026-09-13 for the
// rest) and neither was diagnosable from the failing tests. This test runs on
// Linux, under -short, on every push: the class is macOS-only but the mistake is
// visible in the YAML.

// nightlyWorkflow is the workflow whose machine the container suite runs on.
const nightlyWorkflow = "nightly-macos.yml"

// machineShareRoots are the host roots `podman machine init` must share, and what
// each one carries. A missing entry costs a nightly, so each is stated with the
// bind source that needs it rather than left to be re-derived.
var machineShareRoots = map[string]string{
	"/Users": "the runner's HOME: the machine's real yolo store, podman's graphroot " +
		"and podman's CONNECTIONS dir, which every isolated home symlinks back to " +
		"(packs_test.go, packHomeSharedStores)",
	"/private": "the temp tree in RESOLVED spelling (/var is a symlink to /private/var, " +
		"and run/mounts.go's resolvePath EvalSymlinks's the workspace), plus /private/tmp, " +
		"which paths.HostServicesDir resolves to on macOS",
	"/var/folders": "t.TempDir() — where writeProject puts a workspace and isolateHome " +
		"puts a redirected HOME. RAW spelling: HOME-derived store paths are passed unresolved",
	"/nix": "yolo's own binaries, bind-mounted into the jail from a /nix/store install " +
		"prefix since 2026-09-06 rather than baked into the image",
}

// TestNightlyMachineSharesEveryRootTheSuiteBindsFrom pins the `-v` list on
// `podman machine init`.
//
// It is deliberately BOTH directions of the 2026-09-13 mistake: a missing root
// fails (that is the outage), and so does adding a `-v` for a new root without
// re-listing the ones already needed — which is the only way this can break, since
// the first `-v` is what discards the defaults.
func TestNightlyMachineSharesEveryRootTheSuiteBindsFrom(t *testing.T) {
	shares := parseMachineInitShares(t, readWorkflow(t, nightlyWorkflow))
	if len(shares) == 0 {
		t.Fatalf("%s runs `podman machine init` with no --volume at all. That is not "+
			"safe-by-default here: podman's darwin defaults do not include /nix, and "+
			"every launch then dies as `Error: statfs /nix/store/…-yolo-jail-install-"+
			"prefix/opt/yolo-jail/bin: no such file or directory` (run 34117863296, "+
			"2026-09-07).", nightlyWorkflow)
	}
	for root, why := range machineShareRoots {
		if _, ok := shares[root]; !ok {
			t.Errorf("%s does not share %s into the podman machine.\nIt carries: %s.\n"+
				"Passing ANY --volume replaces podman's whole default share set "+
				"(/Users, /private, /var/folders on darwin), so this list is the complete "+
				"one — a root missing here is a root the VM cannot see, and every bind "+
				"source under it makes `podman run` exit 125 with `Error: statfs <path>: "+
				"no such file or directory` naming a path that exists.",
				nightlyWorkflow, root, why)
		}
	}
	for src, dst := range shares {
		if src != dst {
			t.Errorf("%s shares %s at %s inside the machine. Every bind source yolo "+
				"passes to `podman run` is a HOST path, so a share whose target differs "+
				"from its source is unreachable under the name podman will statfs.",
				nightlyWorkflow, src, dst)
		}
	}
}

// TestNightlyProbesEveryShareItDeclares keeps the workflow's two copies of this
// list honest.
//
// The init line and the `podman machine ssh -- test -d` probe beside it are
// independently-written spellings of one fact, which is the shape AGENTS.md records
// going wrong silently for months (the three copies of BootPath, "behind a test that
// only checked the ends"). Both directions matter: a probed path that nothing shares
// can never pass, and a shared root that nothing probes is one whose disappearance
// goes back to being reported 40 minutes later by 47 tests.
func TestNightlyProbesEveryShareItDeclares(t *testing.T) {
	body := readWorkflow(t, nightlyWorkflow)
	shares := parseMachineInitShares(t, body)
	probed := parseMachineShareProbe(t, body)

	if len(probed) == 0 {
		t.Fatalf("%s has no `podman machine ssh -- test -d` probe after `podman machine "+
			"start`. Without it a missing share is not reported where it happens — it "+
			"surfaces as tests failing on `Error: statfs <path>: no such file or "+
			"directory` for paths the host created, which is how this cost the "+
			"2026-09-07 and 2026-09-13 nightlies.", nightlyWorkflow)
	}

	for _, p := range probed {
		if strings.HasPrefix(p, "$") || strings.HasPrefix(p, "${") {
			continue // $TMPDIR is resolved on the runner, not here
		}
		if !underAnyShare(p, shares) {
			t.Errorf("%s probes %q, which no --volume on `podman machine init` shares. "+
				"That probe can only ever fail.", nightlyWorkflow, p)
		}
	}
	for src := range shares {
		if !anyProbeUnder(src, probed) {
			t.Errorf("%s shares %s but probes nothing under it, so its removal would go "+
				"unreported until the tests fail on it.", nightlyWorkflow, src)
		}
	}
}

// TestTheHostServicesProbeMatchesWhereYoloPublishesEndpoints pins the one entry in
// that probe list whose value is decided by Go rather than by the runner.
//
// /private/tmp is in the list because paths.HostServicesDir hard-codes /tmp and
// resolves it on macOS, where /tmp is a symlink to /private/tmp — so a jail with any
// host-facing loophole binds a source there, under NO test's TMPDIR. It was one of
// the 59 statfs failures (`Error: statfs /private/tmp/yolo-host-services-4fc654df`,
// TestHostPortForwardingData). Move that base and the workflow's entry is wrong
// without anything else changing, which is exactly the drift this asserts.
func TestTheHostServicesProbeMatchesWhereYoloPublishesEndpoints(t *testing.T) {
	dir := paths.HostServicesDir("yolo-000-deadbeef", false)
	if !strings.HasPrefix(dir, "/tmp/") {
		t.Fatalf("paths.HostServicesDir now publishes under %q, not /tmp. %s probes "+
			"/private/tmp because that is what /tmp resolves to on macOS; update both.",
			dir, nightlyWorkflow)
	}
	probed := parseMachineShareProbe(t, readWorkflow(t, nightlyWorkflow))
	for _, p := range probed {
		if p == "/private/tmp" {
			return
		}
	}
	t.Errorf("%s does not probe /private/tmp, which is where paths.HostServicesDir "+
		"(%q, resolved on macOS) publishes a jail's endpoint files — the bind source "+
		"for every host-facing loophole.", nightlyWorkflow, dir)
}

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("locating module root: %v", err)
	}
	path := filepath.Join(root, ".github", "workflows", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(body)
}

// machineInitRe captures the `podman machine init` command with its backslash
// continuations, stopping at the first line that does not continue.
var machineInitRe = regexp.MustCompile(`(?m)^[ \t]*podman machine init\b((?:[^\n]*\\\n)*[^\n]*)`)

// volumeFlagRe matches one `-v src:dst` / `--volume src:dst`, with or without
// trailing mount options (`:ro`, `security_model=none`), which are not part of the
// source→target pair this asserts.
var volumeFlagRe = regexp.MustCompile(`(?:-v|--volume)[ =]([^ \t\\\n:]+):([^ \t\\\n:]+)`)

// parseMachineInitShares returns the source→target pairs the workflow's
// `podman machine init` passes.
func parseMachineInitShares(t *testing.T, body string) map[string]string {
	t.Helper()
	m := machineInitRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s no longer runs `podman machine init`; this test cannot say "+
			"what the machine shares.", nightlyWorkflow)
	}
	shares := map[string]string{}
	for _, v := range volumeFlagRe.FindAllStringSubmatch(m[1], -1) {
		shares[v[1]] = v[2]
	}
	return shares
}

// probeLoopRe captures the share list from the `for share in … ; do` probe.
var probeLoopRe = regexp.MustCompile(`(?m)^[ \t]*for share in ([^\n]*?); do`)

// parseMachineShareProbe returns the paths the workflow asserts the machine can see.
func parseMachineShareProbe(t *testing.T, body string) []string {
	t.Helper()
	m := probeLoopRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, f := range strings.Fields(m[1]) {
		out = append(out, strings.Trim(f, `"'`))
	}
	return out
}

func underAnyShare(p string, shares map[string]string) bool {
	for src := range shares {
		if isUnderOrEqualPath(p, src) {
			return true
		}
	}
	return false
}

func anyProbeUnder(src string, probed []string) bool {
	for _, p := range probed {
		if strings.HasPrefix(p, "$") || strings.HasPrefix(p, "${") {
			continue
		}
		if isUnderOrEqualPath(p, src) {
			return true
		}
	}
	return false
}

// isUnderOrEqualPath reports whether child is base or sits beneath it. Pure string
// work on purpose: these are macOS paths being checked from Linux, so nothing here
// may touch the local filesystem.
func isUnderOrEqualPath(child, base string) bool {
	child = strings.TrimSuffix(child, "/")
	base = strings.TrimSuffix(base, "/")
	return child == base || strings.HasPrefix(child, base+"/")
}

// A HANG IN THIS STEP COSTS THE WHOLE SHARD AND NAMES NOTHING, which is why the step-level
// cap is pinned rather than trusted to stay.
//
// `podman machine init` + `podman machine start` boot a VM, and when that does not come up
// the step does not fail — it waits. Without a step cap the JOB deadline is what eventually
// fires, and GitHub reports that as a `cancelled` job with no step attributed, so the run
// says "two shards were cancelled" and nothing about why. Every later step is skipped, so
// the shard's tests contribute nothing either.
//
// MEASURED 2026-09-14 (run 34862784409): 4m22s / 4m28s / 5m30s where the VM came up, against
// 49m12s and 46m+ on two shards where it did not — the entire `timeout-minutes: 50` budget,
// twice, for no information.
//
// This asserts only that SOME cap exists on the step that boots the machine, not what it is:
// the number is a measurement and will move, while "uncapped" is the defect and does not.
func TestTheNightlyCapsTheStepThatBootsThePodmanMachine(t *testing.T) {
	body := readWorkflow(t, nightlyWorkflow)

	step, ok := stepContaining(body, "podman machine start")
	if !ok {
		t.Fatalf("%s no longer has a step running `podman machine start` — this test has "+
			"lost its subject and would pass by finding nothing.", nightlyWorkflow)
	}
	// COMMENTS STRIPPED FIRST, and that is not fussiness. The step's own comment explains the
	// measurement by quoting `timeout-minutes: 50` — so a plain Contains matched its own prose
	// and the check passed with the real key deleted. Verified by deleting it.
	if !strings.Contains(uncommentedYAML(step), "timeout-minutes:") {
		t.Errorf("%s: the step that runs `podman machine start` has no `timeout-minutes`.\n\n"+
			"A VM that never boots then runs until the JOB deadline, which GitHub reports as a "+
			"cancelled job with NO step named — measured twice in run 34862784409, at 49 and 46 "+
			"minutes against a 50-minute cap, with every later step skipped.\n\n"+
			"The step:\n%s", nightlyWorkflow, step)
	}
}

// stepContaining returns the `- name:` block of the workflow step whose body contains needle.
// Blocks are delimited by the `      - name:` indentation this file's steps all use; a step
// list that stops matching it fails the caller above rather than silently returning nothing.
func stepContaining(body, needle string) (string, bool) {
	const marker = "\n      - name:"
	parts := strings.Split(body, marker)
	for _, p := range parts[1:] {
		if strings.Contains(p, needle) {
			return marker[1:] + p, true
		}
	}
	return "", false
}

// uncommentedYAML drops every whole-line YAML comment, so a check for a KEY cannot be
// answered by prose that mentions it. Only full-line comments: a `#` inside a shell `run:`
// block is script, and a trailing one after a value is rare enough here to leave alone.
func uncommentedYAML(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}
