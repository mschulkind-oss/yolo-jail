package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// THE APPLE CONTAINER PARITY SUITE — the smallest set of facts that, if they hold, means
// the backend the README recommends for macOS actually works.
//
// WHY IT EXISTS. No CI job anywhere runs Apple Container: every job that sets YOLO_RUNTIME
// sets podman (ci.yml, nightly-macos.yml, packs.yml ×2 — counted 2026-09-14), and
// macos-user.yml sets none because that backend starts no container. So an AC-only divergence is caught by nothing, and both
// issues in this class — #39 (pack SharedDirs never mounted) and #44 (the staged pack tree
// never mounted, so no agent reaches the jail at all) — were found by a human on hardware,
// eight months apart.
//
// WHAT THIS IS FOR, AND WHAT IT IS NOT. The build-time guards are what actually stop the
// class: both defects were ARGV-LEVEL — a mount that was never emitted — and an argv is
// visible on Linux, on every push, with no Mac
// (internal/cli/run/hostpathenv_test.go, acbindsources_test.go, machinetierparity_test.go,
// hosthometier_test.go, and the census in backendparity_test.go). Hardware was needed to
// NOTICE those defects; it was never needed to CATCH them. This suite is for the residue an
// argv cannot see: whether `:ro` is really ignored, whether the OCI convert works, whether a
// path that IS mounted is really readable.
//
// ⚠ DELIBERATELY SMALL, AND IT MUST STAY THAT WAY UNTIL IT IS GREEN. Most of ./integration
// will legitimately fail or skip here — Apple Container has no loophole host services,
// ignores `:ro`, takes no `--net` selector, and `--add-host` is unsupported
// (apple/container#673). A parity job that is red on day one is a job nobody reads, and this
// repo has the nine-day nightly to prove it. Four tests that MUST pass is a signal; forty
// that mostly fail is not. Grow it one test at a time, each added only once it has passed on
// hardware.
//
// THE NAMING IS PART OF THE GATE, exactly as it is for macos-user
// (integration/macosusergate_test.go): every test here is called TestAppleContainer…, so the
// CI step can select the whole suite with `-run '^TestAppleContainer'` and that partition is
// exhaustive by construction rather than by a list somebody maintains. requireAppleContainer
// enforces the name.

// appleContainerDeclareEnv is how a CI step says "I am the Apple Container job, and a test
// that cannot run here is a FAILURE, not a skip".
//
// The declaration belongs to the step because only the step knows what it was scheduled to
// do: a developer's `just test` on Linux is supposed to skip these, and a job whose entire
// purpose is to run them is not. This is macosUserDeclareEnv's argument, one backend over.
const appleContainerDeclareEnv = "YOLO_TEST_APPLE_CONTAINER"

// appleContainerTestPrefix is the required name prefix — see the header.
const appleContainerTestPrefix = "TestAppleContainer"

// requireAppleContainer gates a test on actually being on Apple Container, and is the
// difference between this suite reporting and this suite evaporating.
//
// UNDECLARED it SKIPS, which is correct everywhere this repo is normally developed: the
// development jail is Linux and has no `container` binary at all.
//
// DECLARED it FAILS instead, and that asymmetry is the whole point. A suite that skips is
// indistinguishable from a suite that passes, and this one skips on every machine except the
// one Mac it was written for — so on that Mac, "the runtime is not there" must be a red job
// naming the reason, not a green one. That is the property macos-user's gate gets from a
// TestMain ledger; this one gets a weaker version of it here, because TestMain belongs to
// harness_test.go and this file may not touch it. The weakness is named rather than hidden:
// a `-run` pattern that selects NOTHING still exits 0, so the CI step computes its pattern
// from `go test -list` and refuses a zero-length selection — the same guard the macOS
// nightly's computed shards already carry.
func requireAppleContainer(t *testing.T) {
	t.Helper()
	if !strings.HasPrefix(t.Name(), appleContainerTestPrefix) {
		t.Fatalf("%s is behind requireAppleContainer and is not named %s… — the CI step "+
			"selects this suite with `-run '^%s'`, so a differently-named test is silently "+
			"never run. Rename it.", t.Name(), appleContainerTestPrefix, appleContainerTestPrefix)
	}
	// -short SKIPS UNCONDITIONALLY, declaration or not: it means "no containers" for the
	// whole suite (requireJail skips on it too), and a declared -short run is a developer's
	// `just test-fast` with a stray variable, not a parity job. The CI step never passes it.
	if testing.Short() {
		t.Skip("skipping Apple Container parity test (-short)")
	}
	declared := os.Getenv(appleContainerDeclareEnv) != ""

	var why string
	switch {
	case goruntime.GOOS != "darwin":
		why = "Apple Container is macOS-only and this is " + goruntime.GOOS
	default:
		if _, err := exec.LookPath("container"); err != nil {
			why = "no `container` binary on PATH"
		} else if out, err := exec.Command("container", "system", "status").CombinedOutput(); err != nil {
			why = "`container system status` failed: " + err.Error() + ": " + strings.TrimSpace(string(out))
		}
	}
	if why == "" {
		return
	}
	if declared {
		t.Fatalf("%s is set, so this run was SCHEDULED to exercise Apple Container, and it "+
			"cannot: %s.\n\nA skip here would be indistinguishable from a pass, which is the "+
			"one outcome this suite may not have — it is the only instrument that reaches "+
			"this backend at all.", appleContainerDeclareEnv, why)
	}
	t.Skipf("skipping Apple Container parity test: %s (set %s in a job that must run them)",
		why, appleContainerDeclareEnv)
}

// appleContainerWorkspace is the shared fixture: a workspace with the claude pack selected
// (so there is a staged pack tree and a machine-wide SharedDir to look for) and the runtime
// forced to Apple Container per launch.
//
// YOLO_RUNTIME is set PER LAUNCH rather than job-wide so a developer can run one of these by
// hand on a Mac without re-flagging their whole environment, and so the harness's own
// detectRuntime sees the same answer the launch does.
func appleContainerWorkspace(t *testing.T) string {
	t.Helper()
	requireAppleContainer(t)
	requireJail(t)
	return writeProjectWithPacks(t, `{"network": {"mode": "bridge"}}`, "claude")
}

func appleContainerEnv() runOption {
	return withEnv("YOLO_RUNTIME=container")
}

// TestAppleContainerJailStarts is the floor, and it is deliberately the dullest possible
// assertion: the image converts, the VM boots, the workspace bind arrives, and a command
// runs. Everything below it assumes this; if it fails, nothing else in this file means
// anything, which is why it is first.
func TestAppleContainerJailStarts(t *testing.T) {
	dir := appleContainerWorkspace(t)
	res := runYolo(t, dir, `echo JAIL-OK; pwd`, appleContainerEnv())
	if res.rc != 0 || !strings.Contains(res.stdout, "JAIL-OK") {
		t.Fatalf("a jail did not start on Apple Container (rc=%d)\nstdout:\n%s\nstderr:\n%s",
			res.rc, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "/workspace") {
		t.Errorf("the jail started but its cwd is not the workspace bind:\n%s", res.stdout)
	}
}

// TestAppleContainerPackTreeIsReadable is ISSUE #44, asked on the hardware that had it.
//
// The entrypoint renders every pack's surfaces from the tree YOLO_PACK_ROOT names. When that
// variable named a HOST path — which it did, on the belief that "the AC host filesystem is
// visible" — the jail came up with no packs, no agent, no briefing and no guardrails, and
// said nothing about it. internal/cli/run/hostpathenv_test.go makes that argv unwritable;
// this asks whether the path the argv now names is genuinely readable from inside, which is
// the half a Linux test cannot answer.
// ⚠ THE TREE IS TWO LEVELS, and this test asserted one until the hardware said so
// (2026-09-14, run 34873262674). `internal/entrypoint/packsurfaces.go` walks
// `<root>/_official/<name>` for the SHIPPED packs and `<root>/<slug>` for fetched ones —
// so `claude`, which ships, can never appear in a non-recursive listing of the root on
// any backend. The first green this file ever produced was also the first time anything
// had run these assertions, and a test written against hardware that does not exist yet
// is exactly where that class of mistake survives.
//
// It asks for the RENDERED OUTCOME now, not a directory listing. "The jail boots with no
// agent" is what issue #44 actually was, and a manifest the entrypoint read and acted on
// is the only thing that disproves it — a present directory only says the copy happened.
func TestAppleContainerPackTreeIsReadable(t *testing.T) {
	dir := appleContainerWorkspace(t)
	// Both levels are listed for the diagnosis, and the two assertions below are made
	// against markers rather than against the listing, so neither can be answered by a
	// path that merely appears in the other one's output.
	res := runYolo(t, dir, `echo "ROOT=$YOLO_PACK_ROOT"`+"\n"+
		`ls "$YOLO_PACK_ROOT" "$YOLO_PACK_ROOT/_official" 2>&1 | head -30`+"\n"+
		`test -r "$YOLO_PACK_ROOT/_official/claude/pack.json" && echo MANIFEST-READABLE`+"\n"+
		`test -x "$HOME/.yolo/bin/launch/claude" && echo LAUNCHER-RENDERED`,
		appleContainerEnv())
	if res.rc != 0 {
		t.Fatalf("reading the pack root failed (rc=%d)\nstdout:\n%s\nstderr:\n%s",
			res.rc, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "ROOT=/") {
		t.Fatalf("the jail has no YOLO_PACK_ROOT at all — the entrypoint renders nothing, "+
			"which is issue #44:\n%s", res.stdout)
	}
	if !strings.Contains(res.stdout, "MANIFEST-READABLE") {
		t.Errorf("the selected pack `claude` has no readable pack.json under YOLO_PACK_ROOT.\n%s\n\n"+
			"This is issue #44's symptom exactly: the launch believes it delivered the "+
			"declarations and the jail finds nothing, so it boots with no agent and no "+
			"guardrails while looking like a working jail.", res.stdout)
	}
	if !strings.Contains(res.stdout, "LAUNCHER-RENDERED") {
		t.Errorf("`claude`'s manifest is readable but the entrypoint rendered no launcher for "+
			"it at ~/.yolo/bin/launch/claude.\n%s\n\n"+
			"The delivery half of issue #44 is fixed and the RENDER half is not, which the "+
			"tree listing alone cannot tell you — that is the whole reason this asserts an "+
			"outcome rather than a path.", res.stdout)
	}
}

// TestAppleContainerMachineWideTierArrives is ISSUE #39, asked the same way.
//
// The machine-wide tier is the one a per-workspace bind cannot supply: a pack's
// `scope: machine` directory comes from GlobalHome so a credential survives across
// workspaces, and Apple Container's single wsState→/home/agent bind reaches none of it.
// machinetierparity_test.go makes the ABSENT mount unwritable on Linux; this asks whether
// the mount that is now emitted actually lands.
func TestAppleContainerMachineWideTierArrives(t *testing.T) {
	dir := appleContainerWorkspace(t)
	res := runYolo(t, dir, `for d in ~/.claude-shared-credentials ~/.cache; do `+
		`[ -d "$d" ] && echo "PRESENT $d" || echo "MISSING $d"; done`, appleContainerEnv())
	if res.rc != 0 {
		t.Fatalf("probing the machine-wide tier failed (rc=%d)\n%s\n%s", res.rc, res.stdout, res.stderr)
	}
	if strings.Contains(res.stdout, "MISSING") {
		t.Errorf("a machine-wide directory did not arrive:\n%s\n\n"+
			"This is issue #39's shape: the single wsState bind covers the PER-WORKSPACE "+
			"tier and can supply nothing machine-wide, so the mechanism degrades to "+
			"per-workspace with no error and no warning.", res.stdout)
	}
}

// TestAppleContainerIgnoresReadOnlyBinds MEASURES THE PREMISE the whole refusal rule rests
// on, and it is the one test here that is valuable whichever way it comes out.
//
// roBindsUnsupported refuses four different host grants on this backend — config `mounts`, a
// pack `mount`, the host nvim config, the capture store — on the stated ground that "Apple
// Container accepts `-v src:dest:ro` and IGNORES the suffix". Nothing in this repo has ever
// verified that. docs/design/backend-parity.md says outright that none of the AC fixes has
// run on a Mac, and its §4 residue 3 names this exact question as unknowable from Linux.
//
// If the premise holds, this passes and four refusals are justified. If Apple has fixed it,
// this FAILS — and that failure is a feature request, not a regression: four capabilities
// are being withheld from a backend that could honor them.
//
// IT DOES NOT GO THROUGH yolo, and that is deliberate twice over. First, the question is
// about the RUNTIME, not about yolo's argv, so asking the runtime directly is the shorter
// evidence chain — the same reason AGENTS.md's reachability carve-out reaches for a bare
// `podman run` rather than a nested jail. Second, every `:ro` bind a real launch emits is
// sourced from a nix store path or the user's own home, and a probe that WROTE to one to
// find out would be modifying the maintainer's machine to answer a question about it. A
// throwaway temp directory is the only safe subject.
func TestAppleContainerIgnoresReadOnlyBinds(t *testing.T) {
	requireAppleContainer(t)
	requireJail(t)

	ref := imageExists("container")
	if ref == "" {
		t.Skip("no jail image is loaded into Apple Container yet, and this probe needs one " +
			"to run anything at all; the launching tests above are what load it")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seed"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("container", "run", "--rm",
		"-v", dir+":/ro-probe:ro", ref,
		"sh", "-c", "touch /ro-probe/written 2>/dev/null && echo RO-IGNORED || echo RO-HONORED",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("the :ro probe could not run: %v\n%s", err, out)
	}
	got := string(out)

	// The container's own verdict and the HOST's must agree. A `touch` that appears to
	// succeed inside a VM whose write never lands on the host would otherwise be read as
	// "ignored" when the truth is stranger and worth knowing.
	_, hostErr := os.Stat(filepath.Join(dir, "written"))
	landed := hostErr == nil

	switch {
	case strings.Contains(got, "RO-IGNORED") && landed:
		t.Logf("MEASURED: Apple Container ignores :ro — the write reached the host. "+
			"roBindsUnsupported's premise holds and the four refusals resting on it are "+
			"justified.\n%s", out)
	case strings.Contains(got, "RO-HONORED") && !landed:
		t.Errorf("MEASURED: Apple Container HONORED a :ro bind.\n\n"+
			"This is not a regression, it is news. roBindsUnsupported "+
			"(internal/cli/run/backendcaps.go) refuses config `mounts`, pack `mount` grants, "+
			"the host nvim config and the capture store on this backend SOLELY because the "+
			"suffix was believed to be ignored. If it is honored, those four capabilities "+
			"are withheld for no reason and the rule should be narrowed or deleted.\n\n"+
			"Record the macOS and `container` versions from this run's Runner facts step "+
			"before changing anything — the answer is a property of that release, not of "+
			"yolo.\n%s", out)
	default:
		t.Errorf("the probe and the host disagree, which is the answer nobody predicted: "+
			"container said %q and the host %s see the file.\n\n"+
			"Do not fold this into either branch. A write the container believes succeeded "+
			"and the host never received is a third behaviour, and every rule in "+
			"backendcaps.go assumes there are only two.",
			strings.TrimSpace(got), map[bool]string{true: "DOES", false: "does NOT"}[landed])
	}
}
