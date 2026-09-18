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

// A FAILED `podman machine start` TAKES THE WHOLE SHARD, so it is retried — and the retry
// must never be a second `init`.
//
// MEASURED 2026-09-14 (run 34870117573, shard 1): `Error: EOF` at rc 125, 3m34s in, on a
// machine that had just initialized cleanly. It was the one red shard in a run where every
// other one passed, and nothing in the shard ran after it.
//
// The second assertion is the load-bearing one. `parseMachineInitShares` above reads the
// FIRST `podman machine init` and only that one, so a retry that re-inits would carry a
// SECOND copy of the four shares with nothing checking it — and a share list that drifts
// from the one under test is exactly what cost the 2026-09-13 nightly 47 tests. Retry the
// start; if a fresh machine is ever genuinely needed, single-source the share list and
// teach parseMachineInitShares to read it.
func TestTheNightlyRetriesTheMachineStartWithoutReinitialising(t *testing.T) {
	body := readWorkflow(t, nightlyWorkflow)

	step, ok := stepContaining(body, "podman machine start")
	if !ok {
		t.Fatalf("%s no longer has a step running `podman machine start`.", nightlyWorkflow)
	}
	code := uncommentedYAML(step)

	// NOT a count of the string: ONE `start` inside a `for` loop is a retry, and two
	// unguarded ones in a row are not. What actually decides the shard's fate under
	// `bash -e` is whether the failure is HANDLED, so that is what this asks.
	if n := bareMachineStartRe.FindAllString(code, -1); len(n) > 0 {
		t.Errorf("%s runs `podman machine start` unguarded (%d occurrence(s)).\n\n"+
			"The step runs under `bash -e`, so the first `Error: EOF` ends it and takes the "+
			"whole shard — measured in run 34870117573, shard 1, at rc 125 three and a half "+
			"minutes in. Put it behind `if`/`while` so a transient boot failure can be "+
			"retried.", nightlyWorkflow, len(n))
	}
	// EITHER guarded shape satisfies this; neither is required on its own. The property is
	// "a boot failure is handled", not "the start is spelled the way it was in September".
	backgrounded := backgroundMachineStartRe.MatchString(code) && waitedMachineStartRe.MatchString(code)
	if !guardedMachineStartRe.MatchString(code) && !backgrounded {
		t.Errorf("%s no longer runs `podman machine start` in a handled position — neither "+
			"`if podman machine start` nor a backgrounded start whose `wait` collects the "+
			"status with `||`. This test cannot say whether a boot failure is handled.",
			nightlyWorkflow)
	}
	if backgroundMachineStartRe.MatchString(code) && !waitedMachineStartRe.MatchString(code) {
		t.Errorf("%s backgrounds `podman machine start` but never handles the status of its "+
			"`wait`. Backgrounding hides the failure from errexit; without a handled wait the "+
			"failure is not retried, it is LOST — which is worse than the bare form this test "+
			"was written for.", nightlyWorkflow)
	}
	// The RECOVERY, not the loop. A `for`/`while` check was written here first and was
	// worthless: the share probe further down the same step opens its own `for share in …`
	// loop, so the assertion passed with the retry deleted. `podman machine stop` exists in
	// this workflow for one reason — clearing a half-started VM between attempts — so its
	// absence is the honest signal that the retry went with it.
	//
	// ⚠ What is deliberately NOT pinned: how MANY times. Nothing here would notice a retry
	// count of one, and pinning `for attempt in 1 2 3` would pin a spelling rather than a
	// property. The two assertions above are the load-bearing pair.
	if !strings.Contains(code, "podman machine stop") {
		t.Errorf("%s guards `podman machine start` but no longer stops the machine between "+
			"attempts.\n\nA failed start can leave the VM half-up, and a second start on that "+
			"state fails identically — so a retry without the stop is a retry that cannot "+
			"succeed.", nightlyWorkflow)
	}
	if n := podmanMachineInits(code); n != 1 {
		t.Errorf("%s runs `podman machine init` %d times; exactly one is allowed.\n\n"+
			"parseMachineInitShares reads the FIRST one only, so a second carries a copy of "+
			"the share list that nothing verifies. `-v` is init-only, and a drifted share "+
			"list is what took 47 tests down on 2026-09-13. Retry `start`, not `init`.",
			nightlyWorkflow, n)
	}
}

// The three below match `podman machine …` in a COMMAND position: at the start of a line,
// optionally behind `if`/`while`/`until`. That is the same anchoring machineInitRe above
// already uses, and it is here for the same reason it is there.
//
// ⚠ A PLAIN strings.Count IS WRONG, and this is where that was found. The share probe's own
// `::error::` message explains the rule by QUOTING `podman machine init` — so counting
// substrings reported two inits where the file runs one, and the check failed on its own
// prose. Stripping comments is not enough: that mention is inside a live `echo`. Whenever a
// test reads a file that talks about the thing it is looking for, it has to say which half
// it means.
var (
	// A start on its own line, with nothing after it: the shape that ends the step under
	// `bash -e`. A trailing `&` is deliberately NOT matched — backgrounding is itself a
	// guard, because errexit never fires on an asynchronous command; what decides that
	// shape's safety is whether the `wait` handles the status, which waitedMachineStartRe
	// below requires.
	bareMachineStartRe = regexp.MustCompile(`(?m)^[ \t]*podman machine start[ \t]*$`)
	// THE TWO GUARDED SHAPES, and a change to either must add its own alternative here
	// rather than widening these:
	//   1. `if podman machine start; then …` — the failure is the condition.
	//   2. `podman machine start &` + `wait "$pid" || rc=$?` — the status is collected and
	//      handled, which is what a per-attempt deadline needs (the killer has to run
	//      while the start is in flight, so the start cannot be the condition).
	guardedMachineStartRe    = regexp.MustCompile(`(?m)^[ \t]*(?:if|while|until)[ \t]+podman machine start\b`)
	backgroundMachineStartRe = regexp.MustCompile(`(?m)^[ \t]*podman machine start[ \t]*&[ \t]*$`)
	// The CALL in conditional position, with its numeric deadline — not the definition,
	// which survives the deletion of every caller.
	deadlineCallRe       = regexp.MustCompile(`(?m)^[ \t]*(?:if|while|until)[ \t]+start_with_deadline[ \t]+[0-9]+`)
	waitedMachineStartRe = regexp.MustCompile(`(?m)^[ \t]*wait[ \t]+"?\$\{?start_pid\}?"?[ \t]*\|\|`)
	machineInitCmdRe     = regexp.MustCompile(`(?m)^[ \t]*podman machine init\b`)
)

// podmanMachineInits counts the `podman machine init` COMMANDS in code.
func podmanMachineInits(code string) int {
	return len(machineInitCmdRe.FindAllString(code, -1))
}

// A CACHIX CACHE IS PER-SYSTEM, AND FOR MOST OF THIS REPO'S LIFE ONLY ONE SYSTEM WAS
// EVER PUSHED ON A SCHEDULE.
//
// A darwin host cannot BUILD a Linux closure, so every derivation of the jail image has
// to come from a substituter. `nightly-macos.yml`'s `build-image` job is what makes that
// possible — it realizes the image under `cachix-action`, which pushes every path it
// realizes — and it ran only on `ubuntu-latest`, which is x86_64. An arm64 Mac asks for
// aarch64-linux derivations, and nothing had ever put those in the cache.
//
// MEASURED 2026-09-14, the first real run of apple-container.yml on the maintainer's
// arm64 Mac (34868855087):
//
//	required (system, features): (aarch64-linux, [])
//	Failed to find a machine for remote build!
//
// Hundreds of paths substituted from cache.nixos.org in that run; the few this repo
// builds itself were the ones missing, and for those the arch is the whole story.
//
// WHY A TEST AND NOT A COMMENT. Losing this cache costs TIME, never function — so the
// failure is not an error anywhere. It is a Mac quietly taking minutes to build a
// closure it could have downloaded, reported by nothing, which is how the x86-only push
// went unnoticed from the day it was written until a second architecture appeared.
// publish.yml already pushes both arches and is release-gated, so it cannot be the
// instrument either: between two tags the cache holds nothing newer than the last one.

// nightlyImageAttr is the flake attr a darwin launch has to obtain and cannot build. A
// job that realizes it under an active `cachix-action` is a job that publishes it.
//
// Matched with a right-hand boundary on purpose: `.#ociImageMinimal` and
// `.#ociImageLean` are DIFFERENT derivations with overlapping-but-unequal closures, so
// a job building one of those publishes only part of what a Mac asks for. ci.yml's arm
// cell builds `.#ociImageMinimal` on every push and is deliberately not counted here.
const nightlyImageAttr = ".#ociImage"

var nightlyImageAttrRe = regexp.MustCompile(`\.#ociImage(?:[^A-Za-z0-9_]|$)`)

// nixBuildRe captures one `nix build` COMMAND with its backslash continuations — the
// same anchoring machineInitRe uses, and here for the same reason. This workflow
// mentions the attr in prose far more often than it builds it — every job's comment
// explains which variant it realizes and why — so a plain Contains cannot tell a
// realization from an explanation of one, and `.#ociImageMinimal` answers it too.
var nixBuildRe = regexp.MustCompile(`(?m)^[ \t]*nix build\b((?:[^\n]*\\\n)*[^\n]*)`)

// cachixActionRe matches the step that does the pushing, in a `uses:` position.
var cachixActionRe = regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?uses:[ \t]*cachix/cachix-action@`)

// linuxRunnerArch maps the GitHub-hosted Linux runner labels this repo uses to the nix
// system their builds produce. A label missing here fails the test that reads it rather
// than being skipped: an unclassifiable runner is exactly the case where nobody can say
// which arches are covered.
var linuxRunnerArch = map[string]string{
	"ubuntu-latest":    "x86_64-linux",
	"ubuntu-24.04":     "x86_64-linux",
	"ubuntu-22.04":     "x86_64-linux",
	"ubuntu-24.04-arm": "aarch64-linux",
	"ubuntu-22.04-arm": "aarch64-linux",
}

// TestTheNightlyPushesBothLinuxArchesToCachix pins the property the arm64-Mac fast path
// rests on: this workflow realizes the full image on BOTH Linux arches, and every job
// that realizes it also pushes it.
//
// Both halves are load-bearing and each fails on its own. Delete the arm job and the
// aarch64 half is uncovered; point it at `.#ociImageMinimal` and it stops counting, for
// the reason nightlyImageAttrRe states; drop its `cachix-action` and it builds a closure
// nobody can substitute.
func TestTheNightlyPushesBothLinuxArchesToCachix(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, nightlyWorkflow))

	publishers := map[string][]string{}
	realizers := 0
	for _, name := range sortedKeys(jobs) {
		body := jobs[name]
		if !realizesFullImage(body) {
			continue
		}
		realizers++
		for _, label := range jobRunners(t, name, body) {
			system, ok := linuxRunnerArch[label]
			if !ok {
				t.Errorf("%s: job %q realizes %s on runner %q, which this test cannot "+
					"classify as a nix system. Add it to linuxRunnerArch — an unclassified "+
					"runner leaves nobody able to say which arches the cache covers.",
					nightlyWorkflow, name, nightlyImageAttr, label)
				continue
			}
			publishers[system] = append(publishers[system], name)
		}
		if !cachixActionRe.MatchString(uncommentedYAML(body)) {
			t.Errorf("%s: job %q realizes %s but has no `uses: cachix/cachix-action@` "+
				"step, so nothing publishes what it builds.\n\nA darwin host cannot build "+
				"a Linux closure at all; it can only substitute one. A build here that is "+
				"not pushed is a build that leaves the Mac with no way to get it.",
				nightlyWorkflow, name, nightlyImageAttr)
		}
		if !strings.Contains(uncommentedYAML(body), "secrets.CACHIX_AUTH_TOKEN") {
			t.Errorf("%s: job %q pushes to Cachix without referencing "+
				"secrets.CACHIX_AUTH_TOKEN. The push must be gated on that token ALONE "+
				"(the cache NAME is not a secret), so a fork without it skips the push and "+
				"still builds — losing this cache must cost TIME only, never function.",
				nightlyWorkflow, name)
		}
	}

	if realizers == 0 {
		t.Fatalf("%s no longer realizes %s in any job; this test has lost its subject "+
			"and would pass by finding nothing.", nightlyWorkflow, nightlyImageAttr)
	}
	for _, system := range []string{"x86_64-linux", "aarch64-linux"} {
		if len(publishers[system]) > 0 {
			continue
		}
		t.Errorf("%s realizes %s on no runner producing %s, so nothing pushes that "+
			"system's closure to Cachix.\n\nA Cachix cache is PER-SYSTEM. Measured "+
			"2026-09-14 on an arm64 Mac (run 34868855087): with only x86_64-linux "+
			"published, `required (system, features): (aarch64-linux, [])` had no "+
			"substituter and the closure had to be built through a Linux builder "+
			"container. publish.yml covers both arches but is release-gated, so it holds "+
			"nothing newer than the last tag.\n\nCovered here: %v",
			nightlyWorkflow, nightlyImageAttr, system, publishers)
	}
}

// shellCachixCacheRe matches the spelling that CANNOT work: a shell expansion of an
// environment variable, in a `run:` block, for a value GitHub only exposes through the
// `vars` context.
var shellCachixCacheRe = regexp.MustCompile(`\$\{CACHIX_CACHE\b`)

// TestTheNightlyResolvesTheCacheNameThroughTheVarsContext pins the half of the Cachix
// gate that is not the token.
//
// `build-image` read `${CACHIX_CACHE:-yolo-jail}` from 2026-09-13 (`1006fe6d`) until
// 2026-09-14. That is a SHELL expansion, and a repository variable is not exported into
// a `run:` block — this workflow's only `env:` is FORCE_JAVASCRIPT_ACTIONS_TO_NODE24 —
// so it always took the default. publish.yml, whose gate this one was copied from,
// documents `CACHIX_CACHE` as the override "e.g. for a fork pushing to its own cache",
// and spells it `${{ vars.CACHIX_CACHE || 'yolo-jail' }}`.
//
// WHY A TEST. Both spellings resolve to `yolo-jail` on THIS repository, which is the
// only place either has ever run — so the wrong one is invisible here and wrong only for
// the fork the override exists for, which then presents its own token to a cache it does
// not own. Nothing about that reports back to this repo, and the two forms differ by
// punctuation.
func TestTheNightlyResolvesTheCacheNameThroughTheVarsContext(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, nightlyWorkflow))

	gates := 0
	for _, name := range sortedKeys(jobs) {
		body := uncommentedYAML(jobs[name])
		if !cachixActionRe.MatchString(body) {
			continue
		}
		gates++
		if loc := shellCachixCacheRe.FindString(body); loc != "" {
			t.Errorf("%s: job %q resolves the Cachix cache name with %q — a shell "+
				"expansion of an environment variable that nothing sets.\n\nA repository "+
				"variable reaches a workflow only through the `vars` CONTEXT, never as an "+
				"env var in a `run:` block, so this form always takes the default and the "+
				"override publish.yml documents cannot work. Spell it "+
				"`${{ vars.CACHIX_CACHE || 'yolo-jail' }}`.", nightlyWorkflow, name, loc)
		}
		if !strings.Contains(body, "vars.CACHIX_CACHE") {
			t.Errorf("%s: job %q pushes to Cachix but never reads vars.CACHIX_CACHE, so "+
				"the cache name cannot be overridden at all.\n\nThat override is what lets "+
				"a fork push to its OWN cache. Without it a fork with a token gets a push "+
				"aimed at `yolo-jail`, which it does not own.", nightlyWorkflow, name)
		}
	}
	if gates == 0 {
		t.Fatalf("%s has no `uses: cachix/cachix-action@` step in any job; this test has "+
			"lost its subject and would pass by finding nothing.", nightlyWorkflow)
	}
}

// TestTheMacosShardsDoNotDependOnAnArmImageBuild is why the aarch64 push is a SEPARATE
// JOB rather than a second cell of `build-image`'s matrix, which is the shape
// publish.yml uses and the obvious simplification to reach for.
//
// `needs` waits on EVERY cell of a matrixed job and SKIPS the dependent job when any one
// cell fails. The eight macOS shards are `macos-26-intel` — x86_64 — and load the tar the
// x86_64 build uploads; they have no use for an aarch64 closure. Folding the arm build
// into a job they depend on would let an aarch64 failure delete the entire macOS
// integration report, which is the one thing this workflow exists to produce.
func TestTheMacosShardsDoNotDependOnAnArmImageBuild(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, nightlyWorkflow))

	shards, ok := jobs["integration-macos"]
	if !ok {
		t.Fatalf("%s has no `integration-macos` job; this test has lost its subject.",
			nightlyWorkflow)
	}
	needs := jobNeeds(shards)
	if len(needs) == 0 {
		t.Fatalf("%s: `integration-macos` declares no `needs`, so it cannot be reading "+
			"the image artifact a build job uploads.", nightlyWorkflow)
	}

	for _, dep := range needs {
		body, ok := jobs[dep]
		if !ok {
			t.Errorf("%s: `integration-macos` needs job %q, which this workflow does not "+
				"define.", nightlyWorkflow, dep)
			continue
		}
		for _, label := range jobRunners(t, dep, body) {
			if linuxRunnerArch[label] != "aarch64-linux" {
				continue
			}
			t.Errorf("%s: `integration-macos` needs job %q, which runs on %q "+
				"(aarch64-linux).\n\n`needs` waits on every matrix cell and SKIPS the "+
				"dependent job when any one fails, so an aarch64 build failure would take "+
				"all eight macOS shards with it — and those shards are `macos-26-intel`, "+
				"x86_64, which load the tar the x86_64 build uploads and have no use for "+
				"an aarch64 closure. Publish that arch from a job nothing needs.",
				nightlyWorkflow, dep, label)
		}
	}
}

// realizesFullImage reports whether a job body runs `nix build` on nightlyImageAttr in a
// COMMAND position. Comments are stripped first: this workflow explains the attr far more
// often than it builds it.
func realizesFullImage(job string) bool {
	for _, m := range nixBuildRe.FindAllStringSubmatch(uncommentedYAML(job), -1) {
		if nightlyImageAttrRe.MatchString(m[1]) {
			return true
		}
	}
	return false
}

// jobsKeyRe finds the `jobs:` mapping, which is where job names start being meaningful —
// `on:` and `env:` above it have their own two-space-indented keys.
var jobsKeyRe = regexp.MustCompile(`(?m)^jobs:[ \t]*$`)

// jobNameRe matches a job name: a bare key at exactly two spaces of indentation. Every
// key INSIDE a job sits at four or more, and a `run:` script sits deeper still.
var jobNameRe = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):[ \t]*$`)

// workflowJobs splits a workflow into its jobs, name → body.
//
// Comments are stripped BEFORE splitting, so a comment introducing the next job cannot
// land in the previous one's body and so no assertion can be answered by prose. Job
// boundaries survive that: a name is a non-comment line by construction.
func workflowJobs(t *testing.T, body string) map[string]string {
	t.Helper()
	loc := jobsKeyRe.FindStringIndex(body)
	if loc == nil {
		t.Fatalf("workflow has no `jobs:` mapping; nothing here can be read as a job.")
	}
	jobs := map[string]string{}
	name := ""
	var cur []string
	flush := func() {
		if name != "" {
			jobs[name] = strings.Join(cur, "\n")
		}
	}
	for _, line := range strings.Split(uncommentedYAML(body[loc[1]:]), "\n") {
		if m := jobNameRe.FindStringSubmatch(line); m != nil {
			flush()
			name, cur = m[1], nil
			continue
		}
		cur = append(cur, line)
	}
	flush()
	if len(jobs) == 0 {
		t.Fatalf("workflow defines no jobs at two-space indentation; the splitter has " +
			"lost its subject.")
	}
	return jobs
}

var (
	runsOnRe    = regexp.MustCompile(`(?m)^[ \t]+runs-on:[ \t]*(.+?)[ \t]*$`)
	needsRe     = regexp.MustCompile(`(?m)^[ \t]+needs:[ \t]*(.+?)[ \t]*$`)
	matrixRefRe = regexp.MustCompile(`matrix\.([A-Za-z0-9_-]+)`)
)

// jobRunners returns the runner labels a job can run on, expanding a
// `runs-on: ${{ matrix.<key> }}` through that job's own `<key>: [a, b]` list.
//
// The expansion is the point: `runs-on` alone says `${{ matrix.os }}`, which classifies
// as no architecture at all — and a test that cannot see through it would report a
// matrixed job as covering neither arch or, worse, be written to skip it.
func jobRunners(t *testing.T, name, job string) []string {
	t.Helper()
	m := runsOnRe.FindStringSubmatch(job)
	if m == nil {
		t.Fatalf("job %q declares no `runs-on`; this test cannot say what it runs on.", name)
	}
	value := m[1]
	if ref := matrixRefRe.FindStringSubmatch(value); ref != nil {
		listRe := regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(ref[1]) + `:[ \t]*\[(.*)\][ \t]*$`)
		list := listRe.FindStringSubmatch(job)
		if list == nil {
			t.Fatalf("job %q runs on %q but declares no `%s: [...]` matrix list, so its "+
				"runners cannot be resolved.", name, value, ref[1])
		}
		return splitYAMLList(list[1])
	}
	return splitYAMLList(strings.Trim(value, "[]"))
}

// jobNeeds returns the job names a job declares `needs` on, in either YAML spelling.
func jobNeeds(job string) []string {
	m := needsRe.FindStringSubmatch(job)
	if m == nil {
		return nil
	}
	return splitYAMLList(strings.Trim(m[1], "[]"))
}

// splitYAMLList splits a flow-style YAML list body into trimmed, unquoted entries.
func splitYAMLList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// THE RAISED JOB BUDGET IS ONLY SAFE WHILE THE INNER BOUNDS EXIST, so they are pinned
// together rather than one at a time.
//
// MEASURED on run 34984503308: zero failing tests and three shards CANCELLED at the
// 50-minute cap, because `Load jail image` costs 15–22 MINUTES ON EVERY SHARD before a test
// runs, and test time divides across shards while that does not. So the shard count went to
// 12 and the job cap to 75 — a cap that fires on healthy work reports only that the budget
// was wrong.
//
// ⚠ A BIGGER BUDGET WITHOUT STEP CAPS IS STRICTLY WORSE THAN THE OLD ONE: a genuine hang
// then burns 75 minutes to report "cancelled" with no step named, which is the least useful
// failure this workflow can produce and the exact thing the Install Podman cap was added to
// stop. Both bounds must survive together.
func TestTheNightlysInnerBoundsSurviveTheRaisedJobBudget(t *testing.T) {
	body := readWorkflow(t, nightlyWorkflow)

	for _, step := range []string{"podman machine start", "podman load < /tmp/jail-image.tar"} {
		blk, ok := stepContaining(body, step)
		if !ok {
			// The load step is identified by its own command; if that changed, say so rather
			// than passing because a substring moved.
			t.Errorf("%s has no step containing %q — this pin has lost that subject and cannot "+
				"say whether it is still bounded.", nightlyWorkflow, step)
			continue
		}
		if !strings.Contains(uncommentedYAML(blk), "timeout-minutes:") {
			t.Errorf("the %s step in %s has no `timeout-minutes`.\n\n"+
				"The job budget is 75 minutes precisely because this step is expensive and "+
				"variable; without a step cap a hang here reports `cancelled` with no step "+
				"named, 75 minutes later.", step, nightlyWorkflow)
		}
	}

	// And the job cap itself must still be there — an uncapped job loses the runner instead
	// of failing with a log.
	if !strings.Contains(uncommentedYAML(body), "timeout-minutes:") {
		t.Errorf("%s has no timeout-minutes at all", nightlyWorkflow)
	}
}

// THE STEP CAP CANNOT DELIVER THE RETRY, and that is why each attempt carries its own bound.
//
// The retry loop beside `podman machine start` was written for a start that FAILS FAST — the
// 2026-09-14 `Error: EOF`, rc 125 at 3m34s. A start that HANGS is the other half of the same
// fault and the loop had no answer for it: MEASURED 2026-09-18 (run 35335702509, shard 6) the
// machine began starting at 10:43:39 and the step cap killed the whole step at 10:57:47, so
// attempts 2 and 3 never ran and a three-attempt retry delivered exactly one.
//
// A step cap can only end the STEP. Bounding the attempt is what makes a second attempt
// reachable, and that is what this pins — not the number, which is a measurement and will
// move, but the existence of a per-attempt deadline inside the loop.
//
// It also pins that the bound is NOT `timeout(1)`: macOS ships no such binary (it is
// coreutils, and this workflow has no brew step), so a `timeout 360 podman machine start`
// would fail on every shard with `command not found` — passing this test while breaking the
// step it protects.
func TestTheNightlyBoundsEachPodmanMachineStartAttempt(t *testing.T) {
	body := readWorkflow(t, nightlyWorkflow)

	step, ok := stepContaining(body, "podman machine start")
	if !ok {
		t.Fatalf("%s no longer has a step running `podman machine start` — this test has "+
			"lost its subject and would pass by finding nothing.", nightlyWorkflow)
	}
	script := uncommentedYAML(step)

	// THE CALL, NOT THE DEFINITION. Written as a Contains on the function name first, and a
	// mutation caught it: replacing the call with a bare `if podman machine start` left the
	// helper defined and unused, and the check passed on a workflow whose retry was once
	// again unreachable. Pin the call site in its conditional position.
	if !deadlineCallRe.MatchString(script) {
		t.Errorf("%s: the `podman machine start` retry has no per-attempt deadline.\n\n"+
			"Without one a single hung start consumes the whole step budget and the remaining "+
			"attempts never run — measured in run 35335702509, where a 14m08s hang reduced a "+
			"three-attempt retry to one. The step cap cannot substitute: it ends the step, not "+
			"the attempt.\n\nThe step:\n%s", nightlyWorkflow, step)
	}
	if strings.Contains(script, "timeout ") && !strings.Contains(script, "start_with_deadline") {
		t.Errorf("%s: the attempt bound uses timeout(1), which macOS does not ship — the step "+
			"would fail with `command not found` on every shard.", nightlyWorkflow)
	}
}
