package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
// repo has the nine-day nightly to prove it. A handful of tests that MUST pass is a signal;
// forty that mostly fail is not. Grow it one test at a time, each added only once it has
// passed on hardware.
//
// ⚠ ONE TEST HERE IS AN EXPERIMENT RATHER THAN A CHECK, and it is the one exception to the
// sentence above: TestAppleContainerReachesHostLoopback landed UNRUN, because it exists to
// produce a first measurement rather than to re-check a known fact, and no Linux machine and
// no nested jail can produce it. Its header states the rule it keeps instead — both of its
// possible answers PASS, and only a run that failed to conduct the experiment is red — which
// is how a new test can arrive here without the "green on day one" property being a lie.
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
		// THE BACKEND IS HERE, AND FROM THIS POINT A SKIP IS A DEFECT — on a declared run.
		//
		// ⚠ THIS IS THE GAP THAT HID THE `:ro` MEASUREMENT FOR MONTHS. The check above
		// answers one question — is Apple Container reachable — and it answered it
		// correctly the whole time. What it cannot see is a SECOND skip from inside a test
		// body, where a helper cannot answer and calls t.Skip on its own: that is what
		// TestAppleContainerIgnoresReadOnlyBinds did on every run until `e38e8432`, because
		// its image probe could not speak this CLI. Its three siblings passed, so the suite
		// was never vacuous, so nothing was ever red, and a test asserting a platform
		// behaviour reported nothing for months while looking exactly like coverage.
		//
		// The rule is narrow on purpose: once the runtime is confirmed present AND the job
		// declared itself, no legitimate reason to skip remains. An undeclared run — every
		// developer machine, and this repo's own Linux jail — is untouched.
		if v, fail := lateSkipVerdict(declared, true); fail {
			t.Cleanup(func() {
				if t.Skipped() {
					t.Error(v)
				}
			})
		}
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

// TestAppleContainerBindsASingleFile IS THE SECOND UNMEASURED BELIEF, ASKED THE SAME WAY THE
// FIRST ONE FINALLY WAS.
//
// `apple/container#1089` — "Apple Container cannot bind a single FILE" — is cited in seven Go
// files and five docs, and it drives real behaviour: six `acMaterialize` call sites copy
// instead of binding, and the two `/dev/null` shadow binds are SKIPPED outright with no
// fallback at all (shadowbinds_test.go). Until this test nothing measured it — which is
// exactly the shape `#889` had when it inverted on the first run that could check it.
//
// ⚠ THE TWO SHAPES HAVE DIFFERENT ANSWERS, and folding them together is how this stays
// wrong. A REGULAR FILE binds correctly on 1.1.0. A CHARACTER DEVICE — which is what
// `/dev/null` is, and the shadows are the only site that binds one — arrives as a node with
// the WRONG major:minor and is unreadable. So "cannot bind a single file" is false, while the
// shadow skip that cites it is still right, for a reason nobody had written down.
//
// Measured 2026-09-14, macOS 26.5 arm64, `container` 1.1.0.
//
// IT DOES NOT GO THROUGH yolo, for the reasons TestAppleContainerHonorsReadOnlyBinds gives:
// the question is about the RUNTIME, and a throwaway temp dir is the only safe subject.
func TestAppleContainerBindsASingleFile(t *testing.T) {
	requireAppleContainer(t)
	requireJail(t)

	ref := imageExists("container")
	if ref == "" {
		t.Skip("no jail image is loaded into Apple Container yet, and this probe needs one " +
			"to run anything at all; the launching tests above are what load it")
	}
	ver, _ := exec.Command("container", "--version").Output()
	version := strings.TrimSpace(string(ver))

	dir := t.TempDir()
	reg := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(reg, []byte("ARRIVED"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A regular file, read-only: the shape every acMaterialize site would bind.
	out, err := exec.Command("container", "run", "--rm",
		"-v", reg+":/probe/regular.txt:ro", ref,
		"sh", "-c", "cat /probe/regular.txt 2>&1",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("the single-file probe could not run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ARRIVED") {
		t.Errorf("MEASURED: Apple Container did NOT deliver a single-file bind.\n\n"+
			"version: %s\noutput: %s\n\n"+
			"That is apple/container#1089 holding, and the six acMaterialize copies plus the "+
			"/dev/null shadow skip resting on it are justified. If this machine is BELOW the "+
			"version where it was measured working (1.1.0) then only this test is wrong to "+
			"run here; at or above it, the belief has re-inverted.", version, out)
	} else {
		t.Logf("MEASURED: Apple Container DELIVERS a single-file bind (regular file, :ro).\n"+
			"version: %s\n\nSo apple/container#1089 does NOT hold here. The six acMaterialize "+
			"copies are a CHOICE now (a copy works on every version and needs no version "+
			"gate), not a necessity — see internal/cli/run/helpers.go.", version)
	}

	// A CHARACTER DEVICE: the /dev/null shadow, and the one site with no fallback.
	//
	// Asked separately because it is a DIFFERENT capability with a different answer, and
	// because this is the site that currently loses a feature: the shadow is skipped, and
	// shadowbinds_test.go records why the obvious alternative (write an empty file in-jail)
	// is a data-loss bug — /workspace is bound read-write, so that truncates the real file.
	shadowed := filepath.Join(dir, "shadowed.json")
	if err := os.WriteFile(shadowed, []byte("REAL USER CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("container", "run", "--rm",
		"-v", dir+":/ws", "-v", "/dev/null:/ws/shadowed.json", ref,
		"sh", "-c", `echo "type=$(stat -c %F /ws/shadowed.json 2>&1)"; `+
			`echo "dev=$(stat -c %t:%T /ws/shadowed.json 2>&1)"; `+
			`echo "realdev=$(stat -c %t:%T /dev/null)"; `+
			`echo "read=[$(cat /ws/shadowed.json 2>&1)]"`,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("the shadow probe could not run: %v\n%s", err, out)
	}
	got := string(out)

	// The shadow's PURPOSE is "reads as empty". Anything else means the agent does not get an
	// empty file, so the skip stays correct — but the REASON has to match reality.
	switch {
	case strings.Contains(got, "read=[]") && strings.Contains(got, "type=character special file"):
		t.Errorf("MEASURED: the /dev/null shadow WORKS on Apple Container.\n\n"+
			"version: %s\n%s\n\n"+
			"This is news, not a regression: both shadows are currently SKIPPED on this "+
			"backend (assemble.go, guarded by TestTheDevNullShadowsAreSkippedOnAppleContainer), "+
			"so an agent there reads the user's real .vscode/mcp.json and .overmind.sock. If a "+
			"char-device bind now reads as empty, that skip is withheld for no reason.",
			version, got)
	case !strings.Contains(got, "character special file"):
		t.Errorf("MEASURED: the /dev/null shadow does not arrive AT ALL on Apple Container "+
			"(no device node at the destination).\n\nversion: %s\n%s\n\n"+
			"That is what shadowbinds_test.go's comment claims, and it would make this the "+
			"first measurement of it. Record this output there rather than leaving a bare "+
			"citation.", version, got)
	default:
		t.Logf("MEASURED: the /dev/null shadow arrives as a NON-FUNCTIONAL device node.\n"+
			"version: %s\n%s\n\n"+
			"The node is created with the wrong major:minor — compare `dev` to `realdev` "+
			"above, and note the container's own /dev/null reads fine. So reads fail with "+
			"ENXIO rather than returning empty. The skip in assemble.go is therefore still "+
			"CORRECT, but its stated reason (\"a bind whose HOST side is not a directory does "+
			"not arrive\") is not what happens: it arrives and does not work.", version, got)
	}
}

// TestAppleContainerHonorsReadOnlyBinds MEASURES THE PREMISE the whole refusal rule rests
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
func TestAppleContainerHonorsReadOnlyBinds(t *testing.T) {
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

	// The version, for the message. Read here rather than assumed because the answer is a
	// property of the RELEASE, and a failure that does not name it sends the next reader
	// looking in yolo.
	ver, _ := exec.Command("container", "--version").Output()

	switch {
	case strings.Contains(got, "RO-HONORED") && !landed:
		t.Logf("MEASURED: Apple Container HONORS :ro — the write was refused and nothing "+
			"reached the host. This is what roBindsUnsupported's version floor "+
			"(acROBindsFloor, internal/cli/run/backendcaps.go) now allows.\n%s\n%s",
			strings.TrimSpace(string(ver)), out)
	case strings.Contains(got, "RO-IGNORED") && landed:
		t.Errorf("MEASURED: Apple Container IGNORED a :ro bind — the write reached the "+
			"host.\n\nversion: %s\n\n"+
			"THIS INVERTS THE MEASUREMENT THIS TEST WAS REWRITTEN FOR. Until 2026-09-14 the "+
			"tree believed the suffix was always ignored (apple/container#889, observed on "+
			"0.12.3) and refused four capabilities on that basis; the first run that could "+
			"actually check reported HONORED on 1.1.0, and `roBindsUnsupported` became a "+
			"version floor at that release.\n\n"+
			"So one of two things is true, and the version above says which:\n"+
			"  • this machine is BELOW the floor — then yolo is already refusing correctly "+
			"and only this test is wrong to run here;\n"+
			"  • this machine is AT OR ABOVE the floor — then the floor is wrong and yolo is "+
			"handing out WRITABLE mounts the user granted read-only, which is the dangerous "+
			"direction and wants fixing first.\n%s",
			strings.TrimSpace(string(ver)), out)
	default:
		t.Errorf("the probe and the host disagree, which is the answer nobody predicted: "+
			"container said %q and the host %s see the file.\n\n"+
			"Do not fold this into either branch. A write the container believes succeeded "+
			"and the host never received is a third behaviour, and every rule in "+
			"backendcaps.go assumes there are only two.",
			strings.TrimSpace(got), map[bool]string{true: "DOES", false: "does NOT"}[landed])
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// OQ-BP-4's GATE: does an Apple Container container reach a HOST loopback listener?
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestAppleContainerReachesHostLoopback IS THE MEASUREMENT THE LOOPHOLE RULING WAITS ON, and
// the only question in this file that neither a Linux test nor a nested jail can approximate.
//
// WHAT IT IS FOR. docs/design/backend-parity.md's OQ-BP-4 ruled 2026-09-14 that loopholes
// should be supported as fully as possible on EVERY backend, and that the blanket Apple
// Container skip in `startLoopholes` (loopholesruntime.go, *"no socket bind-mount there"*)
// describes the unix-socket era: FOUR of the six shipped loopholes declare
// `transport: loopback-tls` — claude-oauth-broker, host-processes, journal, serial — and
// reach the host over the NETWORK, learning their endpoint from a 0600 file in a bind-mounted
// DIRECTORY, which this backend mounts fine. So the stated reason covers none of them.
// What nobody has ever measured is the premise underneath: whether a container here can reach
// a listener bound to the Mac's own 127.0.0.1 at all. `advertiseHostFor` excludes Apple
// Container before it even reads the network mode, so the tree has no answer to "what address
// does an AC container reach the host on" — this test's job is to produce one.
//
// THE SEQUENCING IS PART OF THE RULING, which is why this lands ALONE and changes no
// production code. The in-jail reachability witness is FATAL (internal/entrypoint/
// reachability.go, since 2026-08-18): an enabled jail-facing service the jail cannot use
// REFUSES the launch. Enabling a loopback-tls loophole here on an unmeasured assumption would
// therefore convert a currently-working Apple Container launch into a refusing one.
//
// IT DOES NOT GO THROUGH yolo, for TestAppleContainerHonorsReadOnlyBinds' reasons: the
// question is about the RUNTIME, so asking the runtime directly is the shorter evidence chain
// (AGENTS.md's reachability carve-out reaches for a bare `podman run` for the same reason),
// and the subject is a listener this test process owns rather than any host service.
//
// # Both answers PASS. Only a run that failed to conduct the experiment is red
//
// This is a MEASUREMENT, and its two possible results are both successful runs of it, so
// neither is a failure:
//
//   - The tree asserts NOTHING here today. Unlike its two siblings — where one branch means
//     yolo is doing something wrong (handing out a writable mount the user granted `:ro`) —
//     no branch of this test contradicts shipped behaviour, because the backend is skipped
//     wholesale and nothing publishes an address for it.
//   - The safety asymmetry runs the OTHER way. "Unreachable" is the outcome under which the
//     current skip is CORRECT and nothing must change; failing on it would redden the job
//     exactly when yolo is behaving properly, for a fact about Apple's runtime that no yolo
//     commit can fix. That is this file's documented failure mode — *"a parity job that is red
//     on day one is a job nobody reads"* — and the workflow says the same of the whole subset.
//   - A logged verdict IS legible here: `.github/workflows/apple-container.yml`'s test step
//     runs `go test -v`, so every t.Logf line below lands in the job log verbatim. Every line
//     this test emits is prefixed `AC-HOST-REACH` so one grep recovers the whole record.
//
// What DOES fail: the experiment not happening (no listener, no container, a dial script that
// did not complete, a candidate/listener pair with no result line) and an INCOHERENT result
// (something answered our port without sending our token). That is the distinction this suite
// exists for — a skip, or a silently vacuous run, must never read as a pass.
//
// # The candidates, and why a control listener is bound as well
//
// The report has to say WHICH address works, not just whether one does, so every candidate is
// dialled and recorded: three host/gateway DNS names, the container's own loopback, and the
// numeric gateway DISCOVERED from the container's own routing table (upstream warns AC's vmnet
// subnet varies by machine, so it is derived, never assumed — see acHostCandidates).
//
// ⚠ AND EACH IS DIALLED AGAINST UP TO THREE LISTENERS, because "no candidate reached the
// loopback listener" is ambiguous on its own: it cannot distinguish *"the host's loopback is
// not forwarded"* from *"the container cannot reach this Mac at all"*, and those have opposite
// consequences for the follow-up work. So a wildcard (0.0.0.0) listener and, when the bridge
// address is known, a listener bound to just that address are bound alongside the 127.0.0.1
// SUBJECT. A hit on a control with a miss on the subject is the third outcome, and it is the
// most actionable one: it says a jail-facing service could be reached here, but only by
// binding wider than svcendpoint does today, and it prices that change (bridge address only,
// versus the LAN).
//
// ⚠ The wildcard bind is the one thing here a reader should know touches the MACHINE: on a Mac
// with the application firewall set to block incoming connections, binding 0.0.0.0 in a test
// process can be refused or prompted. It is best-effort for exactly that reason — a failed
// control is logged, never fatal — and the all-miss verdict names the firewall as a thing to
// rule out before believing the result.
//
// # WHAT HAS RUN, AND WHAT HAS NOT
//
// This test reached the repository UNRUN against its subject, which the header of this file
// otherwise forbids; it is the exception because it exists to produce a first measurement, and
// no machine without this hardware can produce it. What that leaves unverified is exactly two
// things: the `container` argv in acRunProbe (identical in shape to the sibling probes above,
// which are green on this hardware) and the ANSWER.
//
// The INSTRUMENT is not among them. Every portable half — acNetFactsScript, the candidate
// derivation, the dial script's shell, the listeners, the token round-trip, the parser and the
// classification — was exercised end to end against a real container before this landed, using
// podman in this repo's own Linux jail (2026-09-14, podman 5.8.6, `--network=pasta`): the
// `ip`-absent branch was taken, the gateway was decoded out of /proc/net/route, the
// bridge-bind failure path was taken and logged, all ten PROBE lines parsed, and the run
// produced the THIRD verdict — the wildcard listener hit at `host.containers.internal` while
// the 127.0.0.1 listener was missed by every candidate. So the branch that is hardest to
// believe has already been produced once, by a stack that behaves the way this backend might.
func TestAppleContainerReachesHostLoopback(t *testing.T) {
	requireAppleContainer(t)
	requireJail(t)

	ref := imageExists("container")
	if ref == "" {
		t.Skip("no jail image is loaded into Apple Container yet, and this probe needs one " +
			"to run anything at all; the launching tests above are what load it")
	}
	ver, _ := exec.Command("container", "--version").Output()
	version := strings.TrimSpace(string(ver))

	// PHASE 1 — ASK THE CONTAINER WHAT ITS NETWORK IS. First, because the bridge-address
	// control below cannot be bound until the bridge address is known.
	facts := acRunProbe(t, ref, "network discovery", acNetFactsScript)
	t.Logf("AC-HOST-REACH FACTS (`container` %s) — the container's own view of its network:\n%s",
		version, facts)

	cands := acHostCandidates(facts)
	if len(cands) == 0 {
		t.Fatalf("AC-HOST-REACH: no candidate host addresses were derived, so nothing would be "+
			"dialled and nothing measured. acHostCandidates always yields the DNS names plus a "+
			"documented fallback, so an empty list means that function changed and this test is "+
			"now vacuous.\nfacts:\n%s", facts)
	}
	var list strings.Builder
	for _, c := range cands {
		fmt.Fprintf(&list, "  %-28s %s\n", c.addr, c.why)
	}
	t.Logf("AC-HOST-REACH CANDIDATES (%d):\n%s", len(cands), list.String())

	// PHASE 2 — BIND THE HOST SIDE. The 127.0.0.1 listener is the SUBJECT: it is bound
	// exactly as internal/svcendpoint's Listen binds a real jail-facing service.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	subject, err := startACHostListener("loopback", "127.0.0.1", nonce)
	if err != nil {
		t.Fatalf("AC-HOST-REACH: could not bind this Mac's own 127.0.0.1, so the experiment "+
			"could not be conducted at all: %v\n\n"+
			"This is a host-side fault, not an answer about Apple Container. Nothing about the "+
			"loophole skip may be concluded from this run.", err)
	}
	t.Cleanup(subject.close)
	lns := []*acHostListener{subject}

	// The controls are best-effort by design — see the header's firewall note.
	if wild, err := startACHostListener("wildcard", "0.0.0.0", nonce); err != nil {
		t.Logf("AC-HOST-REACH CONTROL: could not bind 0.0.0.0 (%v). The run continues, but an "+
			"all-miss result below is then AMBIGUOUS: with no reachable-at-all control there is "+
			"nothing to separate \"the host's loopback is not forwarded\" from \"the container "+
			"cannot reach this Mac\".", err)
	} else {
		t.Cleanup(wild.close)
		lns = append(lns, wild)
	}
	if gw := acFirstNumericCandidate(cands); gw != "" {
		if bridge, err := startACHostListener("bridge", gw, nonce); err != nil {
			t.Logf("AC-HOST-REACH CONTROL: %s is the address the container routes through but "+
				"is not bindable on this Mac (%v). That is itself worth knowing: the cheapest "+
				"remedy for an unreachable loopback — bind the vmnet bridge address only — is "+
				"not available if that address is not local here.", gw, err)
		} else {
			t.Cleanup(bridge.close)
			lns = append(lns, bridge)
		}
	}

	// PHASE 3 — DIAL EVERY CANDIDATE AT EVERY LISTENER, from inside a container.
	dialOut := acRunProbe(t, ref, "host dial", acDialScript(cands, lns))
	t.Logf("AC-HOST-REACH PROBES:\n%s", dialOut)
	for _, l := range lns {
		t.Logf("AC-HOST-REACH PEERS at the %s listener (%s:%s): %v",
			l.label, l.bind, l.port, l.peers())
	}

	if fault := acSelftestFault(dialOut); fault != "" {
		t.Fatalf("AC-HOST-REACH: the in-container dialer failed its own self-test — %s\n\n"+
			"NOTHING WAS MEASURED. With a broken dialer every candidate misses, so an "+
			"\"unreachable\" verdict here would have been manufactured by this image rather than "+
			"observed on this backend — which is the one wrong answer this test is built to make "+
			"impossible.\ndial output:\n%s", fault, dialOut)
	}

	probes := acParseProbes(dialOut)
	want := len(cands) * len(lns)
	if !strings.Contains(dialOut, "DIALS-DONE") || len(probes) != want {
		t.Fatalf("AC-HOST-REACH: the dial script did not complete — %d result lines for %d "+
			"candidate×listener pairs, DIALS-DONE %s.\n\n"+
			"The experiment is INCOMPLETE, so neither verdict may be read off it. A partial run "+
			"is the shape that would otherwise pass while measuring less than it claims.",
			len(probes), want,
			map[bool]string{true: "present", false: "ABSENT"}[strings.Contains(dialOut, "DIALS-DONE")])
	}

	byPort := map[string]*acHostListener{}
	for _, l := range lns {
		byPort[l.port] = l
	}
	hits := map[string][]string{}
	var strangers []string
	for _, p := range probes {
		l := byPort[p.port]
		switch {
		case l == nil:
			strangers = append(strangers, fmt.Sprintf("%s:%s (port belongs to no listener) rc=%s out=%q",
				p.addr, p.port, p.rc, p.out))
		case strings.Contains(p.out, l.token):
			hits[l.label] = append(hits[l.label], p.addr)
		case strings.Contains(p.out, "CONNECTED"):
			strangers = append(strangers, fmt.Sprintf("%s → %s:%s rc=%s out=%q",
				l.label, p.addr, p.port, p.rc, p.out))
		}
	}

	if len(strangers) > 0 {
		t.Errorf("AC-HOST-REACH: a dial CONNECTED but our listener's token never arrived: %v\n\n"+
			"Do not fold this into either verdict. Two readings, and rc tells them apart: rc=124 "+
			"is a connection that was accepted and sent nothing within %ds (a forwarder in the "+
			"path, or a listener that never wrote); any other rc means something on this Mac "+
			"other than this test answered an ephemeral port the kernel had just assigned to it, "+
			"which taints the whole table above. Re-run before concluding anything.",
			strangers, acDialTimeoutSecs)
	}

	// THE VERDICT. Every branch is a successful measurement; see the header for why none of
	// them is an assertion failure.
	wildcardBound := acListenerBound(lns, "wildcard")
	switch {
	case len(hits["loopback"]) > 0:
		t.Logf("AC-HOST-REACH VERDICT: REACHABLE. An Apple Container container reached a "+
			"listener bound to THIS MAC's 127.0.0.1, at %v (`container` %s).\n\n"+
			"What follows for OQ-BP-4: the transport half of the loophole skip is dead — a "+
			"loopback-tls service CAN be dialled from a container here.\n"+
			"  • If `host.containers.internal` is among the addresses above, nothing needs to "+
			"change to advertise it: svcendpoint.DefaultAdvertiseHost already publishes exactly "+
			"that name.\n"+
			"  • If ONLY a numeric address worked, `advertiseHostFor` (internal/cli/run/"+
			"loopholesruntime.go) needs an Apple Container arm that publishes it — and it must "+
			"be DISCOVERED per launch, never hardcoded: upstream reports the vmnet subnet varies "+
			"by machine.\n"+
			"  • What is still blocked is per-LOOPHOLE, not per-backend: `--add-host` is "+
			"unsupported here (apple/container#673), so an INTERCEPTING loophole (the "+
			"claude-oauth-broker) stays off for that reason rather than this one.",
			hits["loopback"], version)
	case len(hits) > 0:
		t.Logf("AC-HOST-REACH VERDICT: THE HOST IS REACHABLE, A LOOPBACK-BOUND LISTENER IS NOT "+
			"(`container` %s).\n\nhits by listener: %v\n\n"+
			"Every candidate missed the 127.0.0.1 listener while at least one reached this Mac "+
			"on a wider bind. So Apple Container does NOT forward the host's loopback the way "+
			"rootless podman does with pasta's --map-host-loopback: a jail-facing service would "+
			"have to bind an address the container can route to, which internal/svcendpoint "+
			"deliberately does not do (Listen binds 127.0.0.1, and its cert plus per-jail token "+
			"are the only authentication a wider bind would then be relying on).\n\n"+
			"Priced, cheapest first: a `bridge` hit means binding the vmnet bridge address alone "+
			"is enough, exposing the service to containers on that subnet only; a `wildcard`-only "+
			"hit means the LAN. Until that bind changes, the skip in loopholesruntime.go must "+
			"stay — enabling a loopback-tls loophole here would turn a working Apple Container "+
			"launch into a refusing one, because the in-jail witness is fatal.",
			version, hits)
	default:
		amb := ""
		if !wildcardBound {
			amb = "\n\n⚠ THE WILDCARD CONTROL DID NOT BIND on this run (see the CONTROL line " +
				"above), so this result cannot separate \"loopback is not forwarded\" from \"the " +
				"container cannot reach this Mac at all\". Fix the control before reading it as " +
				"the former."
		}
		t.Logf("AC-HOST-REACH VERDICT: NOT REACHABLE BY ANY CANDIDATE (`container` %s).\n\n"+
			"No candidate reached ANY listener, including the wildcard control — so this run says "+
			"the container could not reach this Mac at all, which is a stronger and less specific "+
			"statement than anything about loopback.%s\n\n"+
			"Rule out the host-side explanations before recording it as a fact about Apple "+
			"Container:\n"+
			"  • the macOS application firewall blocking incoming connections to this test "+
			"process — `/usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate`;\n"+
			"  • a competing default route: a VPN or a Tailscale exit node breaks the vmnet "+
			"subnet route on the host and does not self-heal (apple/container#1881);\n"+
			"  • the FACTS block above, which shows whether the container was given a default "+
			"route and a resolver at all.\n\n"+
			"Either way the conclusion for OQ-BP-4 is the same: the skip in loopholesruntime.go "+
			"stays, and no loopback-tls loophole may be enabled on this backend.", version, amb)
	}
}

// acDialTimeoutSecs bounds ONE dial from inside the container. It is the whole reason this
// test cannot hang: a dropped SYN would otherwise sit in Linux's connect retry for over a
// minute per candidate, and a refusal or an unresolvable name costs nothing at all.
//
// The worst case is every pair timing out, at candidates × listeners × this — well inside
// acRunProbe's jailTimeout() budget (300s by default) for a candidate list this size. That
// product is the arithmetic to redo before adding candidates: overrun it and this stops
// reporting an answer and starts failing on a deadline.
const acDialTimeoutSecs = 5

// acDocumentedVmnetGateway is the gateway upstream issues name for Apple Container's default
// vmnet network: apple/container#989 gives the network as 192.168.64.0/24, and #402 shows
// 192.168.64.1 acting as both the container's gateway and its resolver.
//
// IT IS A LAST RESORT AND NOT A DEFAULT, deliberately: the address is DISCOVERED from the
// container's own routing table when the container has one, because the subnet varies between
// machines. This literal is dialled only when discovery came back empty — which is itself
// worth a dial, since a hit would then mean the routing table was unreadable rather than that
// the network was absent.
const acDocumentedVmnetGateway = "192.168.64.1"

// acNetFactsScript dumps the container's own view of its network.
//
// Everything read with `cat` is guaranteed present in any jail image — procfs, plus the files
// vminitd writes at boot. `ip` and `getent` are NOT: they come from the image's full package
// set (iproute2, glibc.bin), so they are probed for rather than assumed. A discovery step that
// died on a missing tool would take the measurement with it, which is the whole failure this
// file is about.
const acNetFactsScript = `
set -u
echo "--- container-ip"
{ command -v ip >/dev/null 2>&1 && ip -o -4 addr show; } 2>&1 || echo "(no ip binary in this image)"
echo "--- proc-net-route"
cat /proc/net/route 2>&1 || true
echo "--- etc-hosts"
cat /etc/hosts 2>&1 || true
echo "--- etc-resolv-conf"
cat /etc/resolv.conf 2>&1 || true
echo "--- ip-route"
{ command -v ip >/dev/null 2>&1 && ip route show; } 2>&1 || echo "(no ip binary in this image)"
echo "--- end"
exit 0
`

// acDialPreamble is the in-container half of the dial. %d is acDialTimeoutSecs.
//
// ONE dial reports TWO facts, and keeping them in one probe is what makes an odd result
// readable: `echo CONNECTED` fires when the TCP handshake succeeded, and the bytes after it
// are whatever the peer sent. A connect with no token is therefore distinguishable from no
// connect at all — the difference between "something else is on that port" and "nothing
// answered", which the verdict treats as opposite outcomes.
//
// `set -o pipefail` is load-bearing: rc is read after a pipeline, so without it rc would be
// `tr`'s status, which is always 0, and every dial would look like it succeeded.
//
// ⚠ THE SELFTEST LINE GUARDS THE ONE FALSE ANSWER THIS DESIGN CAN PRODUCE. If the dialer
// itself is broken in the image — no `timeout`, or a bash built without net redirections —
// then every probe fails, and "no candidate reached anything" is reported as a MEASUREMENT
// when the truth is that nothing was measured. So the image is asked to prove it can dial:
// `timeout` must resolve, and a dial at the container's own port 1 must come back REFUSED
// (a bash with no `/dev/tcp` says "No such file or directory" instead, which the caller
// treats as fatal). The subshell parens are not optional — a redirection failure in `exec`
// is fatal to a non-interactive shell, so an inline dial here would kill the script.
const acDialPreamble = `
set -u
set -o pipefail
probe() {
  addr="$1"; port="$2"; tag="$3"
  out=$(timeout %d /bin/bash -c "exec 3<>/dev/tcp/$addr/$port && echo CONNECTED && head -c 120 <&3" 2>&1 | tr -d '\r' | tr '\n' ' ')
  rc=$?
  echo "PROBE tag=$tag addr=$addr port=$port rc=$rc out=[$out]"
}
selftest() {
  echo "SELFTEST timeout=[$(command -v timeout || echo MISSING)] dialer=[$( (exec 3<>/dev/tcp/127.0.0.1/1) 2>&1 | tr '\n' ' ' )]"
}
resolve() {
  if command -v getent >/dev/null 2>&1; then
    r=$(getent hosts "$1" 2>&1 | tr '\n' ' ')
  else
    r="(no getent in this image)"
  fi
  echo "RESOLVE name=$1 out=[$r]"
}
`

// acHostCandidate is one address a container might reach the host on, and the reason it is
// being dialled. The reason travels with the address because the log is the deliverable here:
// a bare list of addresses tells the next reader nothing about what a hit would MEAN.
type acHostCandidate struct {
	addr string
	why  string
}

// acHostCandidates derives every address worth dialling from the container's own network
// facts, ordered names-first, deduplicated with the reasons merged.
//
// PURE, and unit-tested from Linux (TestACHostCandidatesFromFacts), because it is the one
// place this test can silently measure less than it claims: a candidate list that quietly
// loses its numeric entries — a changed procfs format, a section marker that stopped
// matching — would still produce a green "not reachable by any candidate" verdict having
// never dialled the address that works.
func acHostCandidates(facts string) []acHostCandidate {
	var out []acHostCandidate
	add := func(addr, why string) {
		if addr == "" {
			return
		}
		for i := range out {
			if out[i].addr == addr {
				out[i].why += "; also " + why
				return
			}
		}
		out = append(out, acHostCandidate{addr: addr, why: why})
	}

	add("host.containers.internal", "svcendpoint.DefaultAdvertiseHost — the name EVERY endpoint "+
		"file yolo publishes carries today, so a hit here means no advertise change is needed")
	add("host.docker.internal", "Docker Desktop's documented name for the host; tried because a "+
		"runtime aiming at Docker compatibility often aliases it — podman answers it, measured "+
		"in this repo's own jail")
	add("gateway.docker.internal", "Docker Desktop's documented name for the host's GATEWAY, "+
		"which is a different address from the host itself on some stacks; one dial settles "+
		"whether this runtime answers it (podman does not — measured)")
	add("127.0.0.1", "the container's OWN loopback — what advertiseHostFor publishes when the "+
		"jail shares the launcher's netns. It cannot cross a VM boundary, and is dialled so "+
		"that negative is measured rather than assumed")

	gateways, onLink := acRoutedHostGuesses(acFactsSection(facts, "proc-net-route"))
	for _, gw := range gateways {
		add(gw, "a gateway named by a route in the container's own /proc/net/route — on this "+
			"backend that is the host's vmnet bridge address")
	}
	for _, g := range onLink {
		add(g, "the first address of a subnet the container is on-link with, which is the "+
			"conventional gateway; dialled in case no default route was installed")
	}
	if len(gateways) == 0 && len(onLink) == 0 {
		add(acDocumentedVmnetGateway, "the DOCUMENTED default for Apple Container's vmnet "+
			"network, dialled ONLY because the container reported no usable route at all. A hit "+
			"here says the routing table was unreadable, not that this address may be hardcoded")
	}
	return out
}

// acRoutedHostGuesses reads a Linux /proc/net/route and returns every gateway any route in it
// names — the default route's is the one that matters, and taking the others costs one dial
// each — and, separately, the first address of each on-link subnet.
//
// THE FORMAT IS HEX AND LITTLE-ENDIAN, which is the whole reason this is a function with a
// test rather than an awk one-liner in the script: `0140A8C0` is 192.168.64.1, not
// 1.64.168.192, and getting it backwards would produce plausible-looking addresses that
// nothing answers — an unreachable verdict manufactured by a parse bug.
//
// IPv4 only, which is all the file holds; Apple Container's vmnet network is IPv4.
func acRoutedHostGuesses(procNetRoute string) (gateways, onLink []string) {
	seen := map[string]bool{}
	keep := func(dst *[]string, ip string) {
		if ip == "" || seen[ip] {
			return
		}
		seen[ip] = true
		*dst = append(*dst, ip)
	}
	for _, line := range strings.Split(procNetRoute, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] == "Iface" || f[0] == "lo" {
			continue
		}
		dest, gw := f[1], f[2]
		if gw != "00000000" {
			keep(&gateways, acHexLEIPv4(gw))
			continue
		}
		// An on-link route: no gateway, so the subnet base is all this row gives. `.1` is a
		// GUESS by convention, which is why it is returned separately from a fact.
		base := acHexLEIPv4(dest)
		if ip := net.ParseIP(base); ip != nil && ip.To4() != nil && ip.To4()[3] == 0 {
			four := ip.To4()
			keep(&onLink, net.IPv4(four[0], four[1], four[2], 1).String())
		}
	}
	return gateways, onLink
}

// acHexLEIPv4 converts one /proc/net/route address field to dotted quad, or "" if it is not
// one. Little-endian: the lowest byte is the FIRST octet.
func acHexLEIPv4(field string) string {
	v, err := strconv.ParseUint(field, 16, 32)
	if err != nil || v == 0 {
		return ""
	}
	return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
}

// acFactsSection returns the lines of one `--- <name>` section of acNetFactsScript's output.
func acFactsSection(facts, name string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(facts, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "--- "); ok {
			in = rest == name
			continue
		}
		if in {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// acFirstNumericCandidate returns the first non-loopback IP literal among the candidates —
// the address the bridge-bound control listener tries to bind.
func acFirstNumericCandidate(cands []acHostCandidate) string {
	for _, c := range cands {
		if ip := net.ParseIP(c.addr); ip != nil && !ip.IsLoopback() {
			return c.addr
		}
	}
	return ""
}

// acHostListener is one host-side TCP listener the container tries to reach. It answers every
// connection with a per-run token and remembers who called.
//
// THE TOKEN IS WHAT MAKES A HIT EVIDENCE. A bare successful connect proves only that
// SOMETHING accepted on that address and port; reading back a nonce this process minted
// proves it was this listener, on this Mac, on this run.
//
// The PEERS are recorded for the reader rather than for an assertion: the source address a
// container appears as says whether the runtime NATs it, which is the next question anybody
// wiring a real service here will ask.
type acHostListener struct {
	label string // the tag the in-container script prints, and the listener's role
	bind  string // the address passed to net.Listen
	port  string
	token string
	ln    net.Listener

	mu        sync.Mutex
	seenPeers []string
}

// startACHostListener binds an ephemeral port on bind and serves the token. The caller
// decides whether a failure is fatal: it is for the 127.0.0.1 subject, and never for a
// control.
func startACHostListener(label, bind, nonce string) (*acHostListener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, "0"))
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, err
	}
	l := &acHostListener{
		label: label,
		bind:  bind,
		port:  port,
		token: "YOLO-AC-HOST-REACH-" + strings.ToUpper(label) + "-" + nonce,
		ln:    ln,
	}
	go l.serve()
	return l, nil
}

func (l *acHostListener) serve() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return // the listener was closed by the test's cleanup
		}
		l.mu.Lock()
		l.seenPeers = append(l.seenPeers, c.RemoteAddr().String())
		l.mu.Unlock()
		_ = c.SetWriteDeadline(time.Now().Add(acDialTimeoutSecs * time.Second))
		_, _ = c.Write([]byte(l.token + "\n"))
		_ = c.Close()
	}
}

func (l *acHostListener) peers() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.seenPeers...)
}

func (l *acHostListener) close() { _ = l.ln.Close() }

// acListenerBound reports whether a listener with this role was bound at all — the wildcard
// control's absence is what makes an all-miss result ambiguous, and the verdict says so.
func acListenerBound(lns []*acHostListener, label string) bool {
	for _, l := range lns {
		if l.label == label {
			return true
		}
	}
	return false
}

// acDialScript emits one `resolve` line per DNS-name candidate and one `probe` line per
// candidate × listener pair, then a DIALS-DONE sentinel.
//
// THE SENTINEL AND THE PAIR COUNT ARE THE VACUITY GUARD. The caller requires both, so a
// script that died halfway — a missing `timeout`, a shell that rejected the function
// definitions — is a FAILURE rather than a table with fewer rows than it should have.
func acDialScript(cands []acHostCandidate, lns []*acHostListener) string {
	var b strings.Builder
	fmt.Fprintf(&b, acDialPreamble, acDialTimeoutSecs)
	b.WriteString("selftest\n")
	for _, c := range cands {
		if net.ParseIP(c.addr) == nil {
			fmt.Fprintf(&b, "resolve %s\n", c.addr)
		}
	}
	for _, l := range lns {
		for _, c := range cands {
			fmt.Fprintf(&b, "probe %s %s %s\n", c.addr, l.port, l.label)
		}
	}
	b.WriteString("echo DIALS-DONE\nexit 0\n")
	return b.String()
}

// acProbe is one parsed PROBE line.
type acProbe struct{ tag, addr, port, rc, out string }

// acParseProbes reads the PROBE lines out of a dial run's combined output. Anything else the
// runtime printed is ignored rather than parsed, so a chatty `container run` cannot break the
// measurement.
func acParseProbes(dialOut string) []acProbe {
	var out []acProbe
	for _, line := range strings.Split(dialOut, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PROBE ") {
			continue
		}
		out = append(out, acProbe{
			tag:  acProbeField(line, "tag"),
			addr: acProbeField(line, "addr"),
			port: acProbeField(line, "port"),
			rc:   acProbeField(line, "rc"),
			out:  acProbeField(line, "out"),
		})
	}
	return out
}

// acProbeField pulls one `key=value` (or `key=[value with spaces]`) field out of a PROBE
// line. Bracketed because the peer's answer is arbitrary text — including bash's own error
// messages, which contain spaces — and a plain split on whitespace would silently truncate
// the one field the verdict reads.
func acProbeField(line, key string) string {
	var rest string
	switch {
	case strings.HasPrefix(line, key+"="):
		rest = line[len(key)+1:]
	default:
		i := strings.Index(line, " "+key+"=")
		if i < 0 {
			return ""
		}
		rest = line[i+1+len(key)+1:]
	}
	if after, ok := strings.CutPrefix(rest, "["); ok {
		if j := strings.Index(after, "]"); j >= 0 {
			return after[:j]
		}
		return after
	}
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		return rest[:j]
	}
	return rest
}

// acSelftestFault reads the dial script's SELFTEST line and describes anything that makes the
// whole run meaningless, or returns "" when the image proved it can dial.
//
// IT GUARDS THE ONE WRONG ANSWER THIS DESIGN CAN PRODUCE, which is why it is a named function
// with a test rather than an inline check: a dialer that cannot dial fails every probe, and
// every probe failing is exactly what "not reachable by any candidate" looks like. That
// verdict would then be a fact about the image, published as a fact about Apple Container, in
// a log nobody has reason to doubt.
func acSelftestFault(dialOut string) string {
	line := ""
	for _, l := range strings.Split(dialOut, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "SELFTEST ") {
			line = l
		}
	}
	if line == "" {
		return "the dial script printed no SELFTEST line, so the image was never asked whether " +
			"it can dial at all (an older script, or a shell that rejected the preamble)"
	}
	if tmo := acProbeField(line, "timeout"); tmo == "" || strings.Contains(tmo, "MISSING") {
		return "`timeout` does not resolve in this image, so no dial was bounded and none can be " +
			"believed: " + line
	}
	if d := acProbeField(line, "dialer"); strings.Contains(d, "No such file") {
		return "this image's bash reports /dev/tcp as a missing FILE, which is what a bash built " +
			"without net redirections says — every probe then fails for a reason that has nothing " +
			"to do with the host: " + line
	}
	return ""
}

// acRunProbe runs one script in a throwaway Apple Container container and returns its
// combined output.
//
// A DEADLINE, unlike this file's older `exec.Command` probes: the suite runs under
// `-timeout 0`, so a hanging container would hang the whole job until CI's wall clock killed
// it, with no output — and this runtime has upstream reports of `container exec` and
// `container stop` hanging indefinitely after a host restart (apple/container#1321).
// jailTimeout() is the same per-command budget every run* helper in this harness honours
// (YOLO_TEST_JAIL_TIMEOUT, 300s by default).
//
// `/bin/bash` ABSOLUTELY, where this file's older probes say `sh`: the scripts above need
// bash (`/dev/tcp`, `pipefail`), the image bakes that exact symlink, and naming it in full
// leaves no question about how the runtime resolves argv[0] against the image's PATH.
//
// A failure is FATAL because it means the experiment did not happen. That is the one class
// this test refuses to report as an answer.
func acRunProbe(t *testing.T, ref, what, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
	defer cancel()
	out, err := exec.CommandContext(ctx, "container", "run", "--rm", ref, "/bin/bash", "-c", script).
		CombinedOutput()
	if err != nil {
		t.Fatalf("AC-HOST-REACH: the %s container could not run (%v), so nothing was measured. "+
			"Read this as a fault in the runtime or the image, NOT as an answer about host "+
			"reachability.\nimage: %s\noutput:\n%s\nscript:\n%s", what, err, ref, out, script)
	}
	return string(out)
}

// TestACHostCandidatesFromFacts pins the candidate derivation from Linux, which is the only
// half of this measurement a machine without a Mac can verify at all.
//
// IT IS DELIBERATELY NOT NAMED TestAppleContainer…, unlike everything gated on hardware
// above. The Mac job selects that prefix as "facts only this hardware can produce", and this
// test needs no hardware — it runs on every push, inside `just test-fast`, which is exactly
// where a parse regression should be caught. Padding the hardware subset with tests that
// cannot fail there would dilute the one signal that job carries (and inflate the count its
// own step prints).
//
// The fixture is a real /proc/net/route from a Linux guest on Apple Container's default
// network: eth0 with a default route via 192.168.64.1 and the on-link 192.168.64.0/24.
func TestACHostCandidatesFromFacts(t *testing.T) {
	facts := "--- container-ip\n" +
		"1: lo    inet 127.0.0.1/8 scope host lo\n" +
		"2: eth0    inet 192.168.64.3/24 scope global eth0\n" +
		"--- proc-net-route\n" +
		"Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0140A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t0040A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"--- etc-hosts\n127.0.0.1 localhost\n--- end\n"

	got := acHostCandidates(facts)
	var addrs []string
	for _, c := range got {
		addrs = append(addrs, c.addr)
		if c.why == "" {
			t.Errorf("candidate %s carries no reason; the log is this test's deliverable and an "+
				"address with no stated meaning tells the next reader nothing", c.addr)
		}
	}

	// The gateway is the load-bearing one: it is the address the advertise decision would
	// have to publish, and it is derived from a hex little-endian field.
	if !acHasAddr(got, "192.168.64.1") {
		t.Errorf("the default route's gateway was not derived from /proc/net/route: got %v.\n\n"+
			"0140A8C0 is 192.168.64.1 — little-endian, lowest byte first. A parse that misses "+
			"it makes TestAppleContainerReachesHostLoopback report \"not reachable by any "+
			"candidate\" without ever having dialled the host.", addrs)
	}
	// The name every endpoint file already carries. If it stops being dialled, a green
	// unreachable verdict would say nothing about the transport yolo actually uses.
	if !acHasAddr(got, "host.containers.internal") {
		t.Errorf("svcendpoint.DefaultAdvertiseHost is not among the candidates: %v", addrs)
	}
	if !acHasAddr(got, "127.0.0.1") {
		t.Errorf("the container's own loopback is not among the candidates: %v", addrs)
	}
	// The documented literal is a LAST RESORT: with a real routing table it must not appear
	// as a separate candidate, or a machine on another subnet gets a dial that means nothing.
	// Here it coincides with the discovered gateway, so what is asserted is the count.
	if n := acCountAddr(got, "192.168.64.1"); n != 1 {
		t.Errorf("192.168.64.1 appears %d times; candidates must be deduplicated, with the "+
			"reasons merged: %v", n, addrs)
	}

	// No routing table at all — a lean image, a container with no network, a changed procfs
	// format. The list must still be non-empty and must still include a numeric address to
	// bind the bridge control to, or the run silently measures less than it reports.
	bare := acHostCandidates("--- proc-net-route\n--- end\n")
	if !acHasAddr(bare, acDocumentedVmnetGateway) {
		t.Errorf("with no usable route the documented fallback %s must still be dialled, so the "+
			"run can tell an unreadable routing table from an absent network: %v",
			acDocumentedVmnetGateway, bare)
	}
	if acFirstNumericCandidate(bare) == "" {
		t.Error("with no usable route there is no numeric candidate, so no bridge-bound control " +
			"listener can be started and an all-miss verdict loses its cheapest remedy")
	}
	if acFirstNumericCandidate(got) != "192.168.64.1" {
		t.Errorf("the bridge control would bind %q rather than the discovered gateway",
			acFirstNumericCandidate(got))
	}
}

func acHasAddr(cands []acHostCandidate, addr string) bool { return acCountAddr(cands, addr) > 0 }

func acCountAddr(cands []acHostCandidate, addr string) int {
	n := 0
	for _, c := range cands {
		if c.addr == addr {
			n++
		}
	}
	return n
}

// TestACProbeLineParsing pins the two halves of the dial protocol that a Mac cannot help
// with: the script says one probe per pair, and the parser reads back what the script said.
//
// The `out=[…]` case is the one that matters. A dial's answer is arbitrary text — bash prints
// `bash: connect: Connection refused`, spaces and all — and the verdict decides between
// "reached us", "something else answered" and "nothing answered" by looking inside that
// field. A parser that truncated it at the first space would turn every miss into a miss and
// every stranger into a miss too, which is the failure that reads as a clean answer.
func TestACProbeLineParsing(t *testing.T) {
	lines := "container: some runtime chatter\n" +
		"PROBE tag=loopback addr=host.containers.internal port=54321 rc=1 out=[bash: connect: Connection refused ]\n" +
		"PROBE tag=wildcard addr=192.168.64.1 port=54322 rc=0 out=[CONNECTED YOLO-AC-HOST-REACH-WILDCARD-abc ]\n" +
		"DIALS-DONE\n"

	probes := acParseProbes(lines)
	if len(probes) != 2 {
		t.Fatalf("parsed %d PROBE lines, want 2: %+v", len(probes), probes)
	}
	if probes[0].tag != "loopback" || probes[0].addr != "host.containers.internal" ||
		probes[0].port != "54321" || probes[0].rc != "1" {
		t.Errorf("first probe parsed as %+v", probes[0])
	}
	if !strings.Contains(probes[0].out, "Connection refused") {
		t.Errorf("the answer field was truncated: %q — the verdict reads this field to tell a "+
			"refusal from a hit, so a truncation here makes every outcome look identical",
			probes[0].out)
	}
	if !strings.Contains(probes[1].out, "YOLO-AC-HOST-REACH-WILDCARD-abc") {
		t.Errorf("the token did not survive parsing: %q", probes[1].out)
	}

	// The script half: one probe per candidate × listener, and every candidate present. An
	// off-by-one that dropped the 127.0.0.1 subject or a whole listener would otherwise leave
	// the caller's pair-count guard asserting against an already-wrong expectation.
	cands := []acHostCandidate{{addr: "host.containers.internal", why: "x"}, {addr: "192.168.64.1", why: "y"}}
	lns := []*acHostListener{{label: "loopback", port: "1111"}, {label: "wildcard", port: "2222"}}
	script := acDialScript(cands, lns)
	for _, want := range []string{
		"probe host.containers.internal 1111 loopback",
		"probe 192.168.64.1 1111 loopback",
		"probe host.containers.internal 2222 wildcard",
		"probe 192.168.64.1 2222 wildcard",
		"resolve host.containers.internal",
		"DIALS-DONE",
		"timeout " + strconv.Itoa(acDialTimeoutSecs),
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the dial script is missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "resolve 192.168.64.1") {
		t.Error("an IP literal was sent to `resolve`, which asks the resolver about an address " +
			"it cannot answer for and prints a confusing line in the log")
	}
	if !strings.Contains(script, "\nselftest\n") {
		t.Errorf("the dial script never calls selftest, so a broken dialer would produce a full "+
			"table of misses and be reported as \"not reachable by any candidate\":\n%s", script)
	}

	// The self-test verdict, in all four states. A run whose dialer is broken must be FATAL
	// rather than unreachable: the two are indistinguishable in the probe table, and only this
	// line tells them apart.
	for _, tc := range []struct {
		name, dialOut string
		wantFault     bool
	}{
		{"a working dialer", "SELFTEST timeout=[/bin/timeout] dialer=[bash: connect: Connection refused ]", false},
		{"no timeout in the image", "SELFTEST timeout=[MISSING] dialer=[bash: connect: Connection refused ]", true},
		{"a bash with no /dev/tcp", "SELFTEST timeout=[/bin/timeout] dialer=[bash: /dev/tcp/127.0.0.1/1: No such file or directory ]", true},
		{"no self-test line at all", "PROBE tag=loopback addr=127.0.0.1 port=1 rc=1 out=[]\nDIALS-DONE", true},
	} {
		fault := acSelftestFault(tc.dialOut)
		if (fault != "") != tc.wantFault {
			t.Errorf("%s: acSelftestFault = %q, want fault=%v", tc.name, fault, tc.wantFault)
		}
	}
	if n := strings.Count(script, "\nprobe "); n != len(cands)*len(lns) {
		t.Errorf("the script has %d probe lines for %d pairs; the caller FAILS on a mismatch, "+
			"so this drift would be reported as an incomplete experiment rather than as a bug "+
			"here", n, len(cands)*len(lns))
	}
}

// lateSkipVerdict decides whether a skip that happens AFTER requireAppleContainer has
// confirmed the backend should turn into a failure, and says what to print.
//
// PURE — no globals, no env, no clock — for exactly the reason macosUserVacuityVerdict is
// pure: a guard that cannot be tested on the machine that develops it is a guard nobody
// re-checks. This repo has now twice shipped a belief that a test was measuring and was not,
// and both times the test was green-by-skipping on a machine no one could run.
//
// `skipped` is a parameter rather than a read of t.Skipped() so the decision can be exercised
// in both directions from Linux; the caller supplies the live value.
func lateSkipVerdict(declared, skipped bool) (string, bool) {
	if !declared || !skipped {
		return "", false
	}
	return "this test SKIPPED after requireAppleContainer confirmed the backend, on a run " +
		"that declared itself the Apple Container job (" + appleContainerDeclareEnv + " is " +
		"set).\n\n" +
		"That is not a legitimate skip: the runtime is present, so something inside the " +
		"test could not answer and skipped on its own — a probe that cannot speak this " +
		"CLI, a fixture that gave up. The skip reason is printed above this line.\n\n" +
		"It is reported because the suite-level gate cannot see it. " +
		"TestAppleContainerIgnoresReadOnlyBinds skipped this way on every run for months " +
		"while its siblings passed, so the job was never vacuous and never red — and the " +
		"`:ro` belief it was supposed to be measuring stayed wrong in four Go files and " +
		"five docs until 2026-09-14.", true
}

// TestLateSkipVerdict pins the rule in both directions, from Linux, which is the whole
// reason it is a pure function.
//
// The asymmetry is the content: an UNDECLARED run must stay silent, because that is every
// developer machine and this repo's own jail, where skipping is the correct outcome and
// making it loud would train everyone to ignore it. A DECLARED run that skips after the
// backend was confirmed has no innocent reading left.
func TestLateSkipVerdict(t *testing.T) {
	for _, tc := range []struct {
		name              string
		declared, skipped bool
		wantFail          bool
	}{
		{"declared and skipped — the defect", true, true, true},
		{"declared and ran — the normal case", true, false, false},
		{"undeclared and skipped — every dev machine", false, true, false},
		{"undeclared and ran — a Mac without the job var", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg, fail := lateSkipVerdict(tc.declared, tc.skipped)
			if fail != tc.wantFail {
				t.Fatalf("lateSkipVerdict(%v, %v) fail = %v, want %v",
					tc.declared, tc.skipped, fail, tc.wantFail)
			}
			if fail && msg == "" {
				t.Error("a failing verdict with no message: the reader is told a test failed " +
					"and not what to look at, which is the failure this whole file is about")
			}
			if !fail && msg != "" {
				t.Errorf("a passing verdict that still says something: %q", msg)
			}
		})
	}
}

// TestRequireAppleContainerInstallsTheLateSkipGuard is the CALL-SITE half, and it reads the
// source to get it.
//
// WHY NOT JUST CALL THE GATE. requireAppleContainer's first act is to fatal on a test whose
// name lacks the `TestAppleContainer` prefix — correct, because the CI step selects the suite
// with `-run '^TestAppleContainer'` and a differently-named test would silently never run. So
// a prober must take that name, and a test with that name is selected by the Mac job, where it
// would sit permanently skipped inside the very suite this guard exists to keep honest.
//
// So the check is over the SOURCE, which is what backendparity_test.go does for the same class
// of problem. It exists because lateSkipVerdict's own unit test passes whether or not anything
// calls it — the shape AGENTS.md names: "does it fail if I delete the call site?" Without this,
// no.
func TestRequireAppleContainerInstallsTheLateSkipGuard(t *testing.T) {
	src, err := os.ReadFile("applecontainer_test.go")
	if err != nil {
		t.Fatalf("reading own source: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "func requireAppleContainer(")
	if i < 0 {
		t.Fatal("requireAppleContainer is gone; this test has lost its subject")
	}
	j := strings.Index(body[i:], "\n}\n")
	if j < 0 {
		t.Fatal("could not find the end of requireAppleContainer")
	}
	fn := body[i : i+j]

	for _, want := range []string{"lateSkipVerdict(", "t.Cleanup(", "t.Skipped()"} {
		if !strings.Contains(fn, want) {
			t.Errorf("requireAppleContainer no longer contains %s.\n\n"+
				"The late-skip guard is not wired in, so a test that skips from inside its "+
				"own body — after the backend was confirmed, on a declared run — is silent "+
				"again. That is how TestAppleContainerIgnoresReadOnlyBinds reported nothing "+
				"for months while looking like coverage. lateSkipVerdict's own test keeps "+
				"passing either way, which is why this one reads the call site.", want)
		}
	}
}
