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
