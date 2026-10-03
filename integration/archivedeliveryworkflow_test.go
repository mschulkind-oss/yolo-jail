package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// THE WORKFLOW HALF OF THE MAC ARCHIVE-DELIVERY CHECKS (macarchivedelivery_test.go), pinned on
// Linux, under -short, on every push — the same reason macosmachineshares_test.go exists: the
// mistakes are visible in the YAML, and a Mac finds them an hour into a nightly.
//
// Four facts, each one a way the checks could stop meaning anything without going red:
//
//   - the archive-delivery job's podman machine (scripts/ci-macos-podman-machine.sh) is the
//     shards' machine: same shares, same probes, same release, a bounded retried start — the
//     second copy macosmachineshares_test.go says must be "single-sourced and read";
//   - image B's `packages:` list is realized on BOTH Linux arches, spelled exactly as the Go
//     test launches it, so a Mac substitutes B instead of failing to build it;
//   - the podman job takes NO preload — the thing it exists to get past — and every step after
//     the Cachix check is gated on it, so a fork skips instead of failing;
//   - each job DECLARES its backend, and on Apple Container the steps sit after the parity tests.

const archiveDeliveryJob = "archive-delivery-macos"

const acWorkflow = "apple-container.yml"

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("cannot read %s: %v", rel, err)
	}
	return string(b)
}

const podmanMachineScript = "scripts/ci-macos-podman-machine.sh"

var podmanPkgURLRe = regexp.MustCompile(`https://github\.com/containers/podman/releases/download/[^"\s]+\.pkg`)

// TestArchiveDeliveryMachineMatchesTheShards: the script's machine is the shards' machine.
func TestArchiveDeliveryMachineMatchesTheShards(t *testing.T) {
	wf := readWorkflow(t, nightlyWorkflow)
	script := readRepoFile(t, podmanMachineScript)

	// The inline init the shards run is the only one in the workflow — otherwise
	// parseMachineInitShares (which reads the FIRST) would be checking one of two.
	if n := podmanMachineInits(uncommentedYAML(wf)); n != 1 {
		t.Errorf("%s runs `podman machine init` inline %d times; exactly one (the shards') is "+
			"allowed — a second machine comes from %s, which this test compares to the first",
			nightlyWorkflow, n, podmanMachineScript)
	}
	want := parseMachineInitShares(t, wf)
	if machineInitRe.FindString(script) == "" {
		t.Fatalf("%s runs no `podman machine init`", podmanMachineScript)
	}
	got := parseMachineInitShares(t, script)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s shares %v into its podman machine; the shards share %v.\n\n`--volume` "+
			"REPLACES podman's default share set, so each list is complete on its own, and a root "+
			"missing from one makes every bind under it exit 125 with `Error: statfs` there only.",
			podmanMachineScript, got, want)
	}
	if n := podmanMachineInits(script); n != 1 {
		t.Errorf("%s runs `podman machine init` %d times; retry the start, never the init", podmanMachineScript, n)
	}

	wantProbe, gotProbe := parseMachineShareProbe(t, wf), parseMachineShareProbe(t, script)
	sort.Strings(wantProbe)
	sort.Strings(gotProbe)
	if !reflect.DeepEqual(gotProbe, wantProbe) {
		t.Errorf("%s probes %v after the start; the shards probe %v", podmanMachineScript, gotProbe, wantProbe)
	}

	// The start: bounded per attempt, retried, stopped between attempts, never bare.
	if !deadlineCallRe.MatchString(script) || !backgroundMachineStartRe.MatchString(script) ||
		!waitedMachineStartRe.MatchString(script) {
		t.Errorf("%s no longer runs `podman machine start` backgrounded, under a per-attempt "+
			"deadline, with its status collected — the shape a 14-minute hang (run 35335702509) "+
			"needs to leave the retry reachable", podmanMachineScript)
	}
	if bareMachineStartRe.MatchString(script) || !strings.Contains(script, "podman machine stop") {
		t.Errorf("%s runs an unguarded `podman machine start`, or no `podman machine stop` "+
			"between attempts", podmanMachineScript)
	}

	if w, g := podmanPkgURLRe.FindString(wf), podmanPkgURLRe.FindString(script); w == "" || w != g {
		t.Errorf("the podman installer differs: the shards fetch %q, %s fetches %q", w, podmanMachineScript, g)
	}

	// And the job actually uses it, under a cap.
	job := workflowJobs(t, wf)[archiveDeliveryJob]
	step, ok := stepContaining(job, podmanMachineScript)
	if !ok {
		t.Fatalf("%s: job %q does not run %s", nightlyWorkflow, archiveDeliveryJob, podmanMachineScript)
	}
	if !strings.Contains(step, "timeout-minutes:") {
		t.Errorf("%s: the step running %s has no `timeout-minutes` — a VM that never boots "+
			"would run to the job deadline and be reported as `cancelled` with no step named",
			nightlyWorkflow, podmanMachineScript)
	}
}

// TestArchiveDeliveryWorkflowsRealizeImageB: B's closure is pushed on both Linux arches, under
// the Go test's own spelling of the list.
func TestArchiveDeliveryWorkflowsRealizeImageB(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, nightlyWorkflow))
	re := regexp.MustCompile(`YOLO_EXTRA_PACKAGES='` + regexp.QuoteMeta(macArchivePackagesJSON) +
		`'[ \t]*\\\n[ \t]*nix build\b[^\n]*` + regexp.QuoteMeta(".#ociImage") + `(?:[^A-Za-z0-9_]|$)`)
	for name, arch := range map[string]string{"build-image": "x86_64-linux", "push-arm-image-cache": "aarch64-linux"} {
		body, ok := jobs[name]
		if !ok {
			t.Errorf("%s has no %q job; image B's %s closure is then pushed by nothing", nightlyWorkflow, name, arch)
			continue
		}
		if !re.MatchString(body) {
			t.Errorf("%s: job %q does not realize `.#ociImage` with YOLO_EXTRA_PACKAGES='%s'.\n\n"+
				"That is image B of the Mac archive-delivery check, spelled as the Go test launches "+
				"it (macArchivePackagesJSON). A Mac can substitute an %s derivation and cannot build "+
				"one, so unless this job pushes it the check fails on a missing derivation — or, on "+
				"the Apple Container Mac, builds it through its builder every run.",
				nightlyWorkflow, name, macArchivePackagesJSON, arch)
		}
	}
}

// TestArchiveDeliveryJobsDeclareTheirBackendAndTakeNoPreload pins the job shapes.
func TestArchiveDeliveryJobsDeclareTheirBackendAndTakeNoPreload(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, nightlyWorkflow))
	job, ok := jobs[archiveDeliveryJob]
	if !ok {
		t.Fatalf("%s has no %q job; the podman half of the delta archive is exercised by nothing",
			nightlyWorkflow, archiveDeliveryJob)
	}
	for _, want := range []string{
		macArchiveDeclareEnv + ": podman",
		"YOLO_RUNTIME: podman",
		`YOLO_NIX_HOST_DAEMON: "1"`,
		macArchiveTestPrefix,
		"go test -list",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("%s: job %q lacks %q", nightlyWorkflow, archiveDeliveryJob, want)
		}
	}
	// THE POINT OF THE JOB: no preload. The shards' `podman load` + stock tag makes a launch
	// skip the delivery entirely (stockimage.go), which is right for them and would make this
	// job measure nothing.
	for _, preload := range []string{"actions/download-artifact", "podman load", "stock-"} {
		if strings.Contains(job, preload) {
			t.Errorf("%s: job %q contains %q — it must start from an EMPTY machine, or the stock-tag "+
				"short-circuit answers the launch and nothing is delivered", nightlyWorkflow,
				archiveDeliveryJob, preload)
		}
	}
	needs := jobNeeds(job)
	if len(needs) == 0 || !contains(needs, "build-image") {
		t.Errorf("%s: job %q needs %v, want build-image — the job that pushes images A and B", nightlyWorkflow,
			archiveDeliveryJob, needs)
	}
	for _, dep := range needs {
		for _, label := range jobRunners(t, dep, jobs[dep]) {
			if linuxRunnerArch[label] == "aarch64-linux" {
				t.Errorf("%s: job %q needs %q, an aarch64 job this x86_64 Mac has no use for", nightlyWorkflow,
					archiveDeliveryJob, dep)
			}
		}
	}
	// FORKS SKIP: every step after the Cachix check is gated on its answer.
	steps := strings.Split(job, "\n      - ")
	seenCheck := false
	for _, s := range steps[1:] {
		if strings.Contains(s, "id: cachix") {
			seenCheck = true
			continue
		}
		if !seenCheck {
			continue
		}
		if !strings.Contains(s, "steps.cachix.outputs.enabled") {
			t.Errorf("%s: a step of %q after the Cachix check is not gated on it, so a fork without "+
				"the token runs it against a closure it cannot build:\n%s", nightlyWorkflow,
				archiveDeliveryJob, firstLine(s))
		}
	}
	if !seenCheck {
		t.Errorf("%s: job %q has no Cachix check step (`id: cachix`)", nightlyWorkflow, archiveDeliveryJob)
	}

	// Apple Container: declared, capped, and after the parity tests.
	ac := readWorkflow(t, acWorkflow)
	decl := macArchiveDeclareEnv + ": container"
	step, ok := stepContaining(ac, decl)
	if !ok {
		t.Fatalf("%s declares no %q step; the Apple Container half is exercised by nothing", acWorkflow, decl)
	}
	if !strings.Contains(uncommentedYAML(step), "timeout-minutes:") {
		t.Errorf("%s: the archive-delivery step has no `timeout-minutes`", acWorkflow)
	}
	if p, d := strings.Index(ac, "name: Apple Container parity tests"), strings.Index(ac, decl); p < 0 || d < p {
		t.Errorf("%s: the archive-delivery step must come AFTER the parity tests (parity at %d, "+
			"delivery at %d), which it leaves untouched", acWorkflow, p, d)
	}
	if strings.Contains(uncommentedYAML(step), "YOLO_TEST_APPLE_CONTAINER") {
		t.Errorf("%s: the archive-delivery step sets YOLO_TEST_APPLE_CONTAINER, which is the parity "+
			"subset's declaration, not this one's", acWorkflow)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// THE OQ-LR2 STEP MUST END ON ITS OWN DEADLINES, NOT ON ITS CAP.
//
// The step's two tests bound their slow phases themselves: the uncompressed load, the gzip
// write, the gzip load and the in-VM copy. The last of those is the one that matters — when
// it fires, the copier is sent SIGQUIT inside the VM and the test prints its goroutine dump
// and a verdict. A step cap shorter than the deadlines before it kills the step first, and
// then nothing prints: until 2026-10-03 the cap was 125 minutes against 175 of deadlines.
//
// The job cap has the same relation to its steps: if it is shorter than their caps, a step
// still inside its own bound is cancelled with the job, which names no step.
const (
	// What the step spends outside those deadlines: the uncompressed archive's write (3m12s at
	// the slowest), each test's realize, evict and probe work (3m17s and 2m38s at the
	// slowest) and `go test`'s own start (19s) — 9m26s, read from the job logs of the runs
	// 36711874486 to 37056529987.
	oqLR2UnboundedAllowance = 10 * time.Minute
	// The job's uncapped steps: checkout, Nix, Cachix, Go, the test selection and the post
	// steps, about 4 minutes at the slowest in the same runs.
	archiveJobUncappedAllowance = 10 * time.Minute
)

func TestTheOQLR2StepEndsOnItsOwnDeadlines(t *testing.T) {
	wf := parseWorkflow(t, nightlyWorkflow)
	step := jobStepRunning(t, wf, archiveDeliveryJob, "steps.select.outputs.firstload")

	// The deadlines as the step's own environment sets them.
	t.Setenv("YOLO_TEST_JAIL_TIMEOUT", step.Env["YOLO_TEST_JAIL_TIMEOUT"])
	// The uncompressed load, the gzip load and the in-VM copy each take macArchiveLaunchTimeout.
	deadlines := 3*macArchiveLaunchTimeout() + gzipArchiveWriteTimeout
	need := deadlines + oqLR2UnboundedAllowance
	if got := minutesOf(t, step.Name, step.TimeoutMinutes); got < need {
		t.Errorf("%s: %q is capped at %s, but its tests' own deadlines add up to %s, plus %s for "+
			"what they do not bound. A run slow enough to need them is killed by the cap before the "+
			"in-VM copier's deadline fires, and the step ends with no verdict and no goroutine dump. "+
			"Raise its `timeout-minutes` to at least %d.", nightlyWorkflow, step.Name, got, deadlines,
			oqLR2UnboundedAllowance, int((need+time.Minute-1)/time.Minute))
	}

	job := wf.Jobs[archiveDeliveryJob]
	var steps time.Duration
	for _, s := range job.Steps {
		if strings.TrimSpace(s.TimeoutMinutes) != "" {
			steps += minutesOf(t, s.Name, s.TimeoutMinutes)
		}
	}
	jobNeed := steps + archiveJobUncappedAllowance
	if got := minutesOf(t, archiveDeliveryJob, job.TimeoutMinutes); got < jobNeed {
		t.Errorf("%s: job %q is capped at %s, but its capped steps add up to %s, plus %s for the "+
			"uncapped ones. A step still inside its own cap would be cancelled with the job, which "+
			"names no step. Raise the job's `timeout-minutes` to at least %d.", nightlyWorkflow,
			archiveDeliveryJob, got, steps, archiveJobUncappedAllowance,
			int((jobNeed+time.Minute-1)/time.Minute))
	}
	// GitHub stops a hosted runner's job at 6 hours whatever the cap says, so a cap past that
	// promises time the job cannot have.
	if got := minutesOf(t, archiveDeliveryJob, job.TimeoutMinutes); got > 6*time.Hour {
		t.Errorf("%s: job %q is capped at %s, past the 6 hours GitHub allows a hosted-runner job; "+
			"shorten a step instead.", nightlyWorkflow, archiveDeliveryJob, got)
	}
}
