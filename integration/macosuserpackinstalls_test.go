package integration

import (
	"fmt"
	"regexp"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// REAL VENDOR INSTALLS ON darwin — OQ-CI7, ruled 2026-10-05 as the doc's option (c)
// (docs/reference/agent-install-in-ci.md#OQ-CI7).
//
// darwin is the one platform whose vendor builds no other CI job installs: Pack Installs
// (packs.yml) installs linux-x64 and linux-arm64, and the podman nightly's jail is linux-x64 in
// a VM. A macos-user launch installs the vendor's DARWIN build, through the same generated
// launcher, into the sandbox account's home. Its `via: npm` half had never run on a Mac
// (docs/reference/macos-user-provisioning.md, "What each imperative config key delivers here").
//
// So .github/workflows/macos-user.yml runs this test as ONE JOB PER PACK, failing hard, as Pack
// Installs does (OQ-CI3: a sequential job dies on the first bad install and masks every vendor
// after it), with the `via: npm` packs listed first. The job selects one subtest with `-run`
// and declares itself with YOLO_TEST_MACOS_USER, so a cell whose subtest SKIPPED fails through
// the gate's vacuity check (macosusergate_test.go) instead of reporting green.
//
// The probe is packInstallProbe, the Linux cell's own (agents_test.go), so both backends ask one
// question. AGENTS.md's no-agent-tests rule holds: `<bin> --version` and file reads only, and the
// pre-launch refresh is throttled first so no vendor command beyond `--version` runs.

// macosUserPackInstallsTest is the test macos-user.yml's install cells select by name.
// TestMacosUserPackInstallsVersionsAndConfigures checks its own name against it, so a rename
// fails under -short instead of leaving every cell selecting nothing.
const macosUserPackInstallsTest = "TestMacosUserPackInstallsVersionsAndConfigures"

// packInstallsTest is the test packs.yml's install cells select by name, checked by
// TestPackInstallsVersionsAndConfigures against its own name for the same reason.
const packInstallsTest = "TestPackInstallsVersionsAndConfigures"

// The workflow files whose install matrices mirror packMatrix.
const (
	macosUserWorkflow    = "macos-user.yml"
	packInstallsWorkflow = "packs.yml"
)

// macosUserRunnerPlatforms is the platform each GitHub-hosted macOS runner label runs a job
// on, for the labels this repository's workflows name. `macos-latest` is Apple Silicon, and
// macos-user.yml says why its jobs use it; `macos-26-intel` is the Intel label
// nightly-macos.yml uses because Podman Machine needs nested virtualization.
//
// The install cells' pack list is the packMatrix packs whose vendor publishes for the
// platform of the install job's `runs-on`: a cell for any other pack would skip, and the
// vacuity check would turn that skip red every night. So the pin derives the list from the
// label rather than from a platform it assumes, and refuses a label missing from this map.
var macosUserRunnerPlatforms = map[string]struct{ goos, goarch string }{
	"macos-latest":   {"darwin", "arm64"},
	"macos-26-intel": {"darwin", "amd64"},
}

// TestMacosUserPackInstallsVersionsAndConfigures installs each shipped agent pack's program from
// its vendor in a macos-user sandbox, then checks the version probe, the install stamp, the
// rendered config marker and the declared project skills dirs (packInstallProbe).
//
// Each subtest is behind macosUserPackInstallGates, which TestMacosUserPackInstallsSkipInTheBackendJob
// pins.
func TestMacosUserPackInstallsVersionsAndConfigures(t *testing.T) {
	if t.Name() != macosUserPackInstallsTest {
		t.Fatalf("this test is named %s but macosUserPackInstallsTest says %s, and "+
			"macos-user.yml's install cells select it by that constant's value", t.Name(),
			macosUserPackInstallsTest)
	}
	for _, tc := range packMatrix {
		t.Run(tc.pack, func(t *testing.T) {
			macosUserPackInstallGates(t, tc)
			packHome(t, fmt.Sprintf(`{"packs": [%q]}`, tc.pack))
			ws := macosUserWorkspace(t, `{}`)
			checkPackInstall(t, tc, runMacosUser(t, ws, packInstallProbe(t, tc)))
		})
	}
}

// macosUserPackInstallGates skips an install subtest everywhere it must not install, and
// returns only where it should. Three gates, in this order:
//
//   - requireMacosUser: a Mac with the sandbox account, and the ledger entry the vacuity
//     check counts, so it goes first and a later skip is still listed;
//   - requireRealPackInstalls: the question is asked only where a job was scheduled to ask
//     it. macos-user.yml's backend job selects this test with `-run '^TestMacosUser'` on a
//     host that passes the first gate, and sets no YOLO_TEST_REAL_PACK_INSTALLS, so this is
//     what keeps seven darwin vendor installs out of the backend's verdict;
//   - the manifest's `platforms`, not packCase.vendorSkipArch: that field records a Linux
//     arch gap (omp's linux-arm64), and on darwin/arm64 omp IS published.
func macosUserPackInstallGates(t *testing.T, tc packCase) {
	t.Helper()
	requireMacosUser(t)
	requireRealPackInstalls(t)
	if why := shippedProgram(t, tc.pack, tc.binary).UnpublishedReason(goruntime.GOOS, goruntime.GOARCH); why != "" {
		t.Skipf("%s is not installable here: %s", tc.binary, why)
	}
}

// TestMacosUserPackInstallsSkipInTheBackendJob: on a host that passes the macos-user gate,
// with YOLO_TEST_REAL_PACK_INSTALLS unset, every install subtest skips before it installs
// anything. That is the backend job's `-run '^TestMacosUser'` step, and
// TestMacosUserPackInstallsWorkflowMirrorsPackMatrix pins the half saying that step sets no
// such variable. Without both, one vendor's darwin release could turn the backend job red,
// which is what the install job exists to prevent.
//
// The gate is FAKED to pass (fakeReadyMacosUserHost). Everywhere this runs but a ready Mac,
// requireMacosUser would skip first, and the check would pass whatever came after it.
func TestMacosUserPackInstallsSkipInTheBackendJob(t *testing.T) {
	t.Setenv(realPackInstallsEnv, "")
	ledger := fakeReadyMacosUserHost(t)
	for _, tc := range packMatrix {
		returned := false
		t.Run(tc.pack, func(t *testing.T) {
			macosUserPackInstallGates(t, tc)
			returned = true
		})
		if returned {
			t.Errorf("%s: macosUserPackInstallGates returned with %s unset on a host that "+
				"passes the macos-user gate, so the backend job's `-run '^%s'` step would "+
				"install %s from its vendor. requireRealPackInstalls must stay among its gates",
				tc.pack, realPackInstallsEnv, macosUserTestPrefix, tc.binary)
		}
	}
	got := ledger.snapshot()
	if len(got) != len(packMatrix) {
		t.Errorf("the ledger holds %d outcomes for %d subtests, so a subtest skipped before "+
			"requireMacosUser recorded it and the backend job's report would not list it: %+v",
			len(got), len(packMatrix), got)
	}
	for _, o := range got {
		if o.ran || o.reason != macosUserSkippedPastGate {
			t.Errorf("%s is recorded as %+v; it must have passed the faked gate and then "+
				"skipped (%q). Anything else means the fake did not reach requireMacosUser, "+
				"and this test checked nothing", o.test, o, macosUserSkippedPastGate)
		}
	}
}

// shippedProgram is the shipped pack's `program` contribution for bin.
func shippedProgram(t *testing.T, pack, bin string) packdecl.Install {
	t.Helper()
	for _, p := range packload.Embedded() {
		if p.Name != pack {
			continue
		}
		for _, in := range p.Decl.InstallContributions() {
			if in.Bin == bin {
				return in
			}
		}
	}
	t.Fatalf("no shipped pack %q declares a program %q, so packMatrix's row for it is stale",
		pack, bin)
	return packdecl.Install{}
}

// ---------------------------------------------------------------------------
// The two install matrices, pinned to packMatrix from Linux under -short.
//
// Each workflow's `pack:` list is a hand-maintained mirror of packMatrix, and until 2026-10-05
// nothing checked packs.yml's (docs/reference/agent-install-in-ci.md, "Invariants").
// TestPackMatrixCoversEveryShippedProgram ties packMatrix to the shipped packs; these two tie
// the workflows to packMatrix, so a pack added in one place and not the others fails
// `just check-ci` rather than leaving one platform without its install cell.
// ---------------------------------------------------------------------------

// installMatrixWorkflow is the part of a workflow these pins read.
type installMatrixWorkflow struct {
	Env  map[string]string           `yaml:"env"`
	Jobs map[string]installMatrixJob `yaml:"jobs"`
}

type installMatrixJob struct {
	RunsOn          any               `yaml:"runs-on"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Strategy        struct {
		FailFast *bool `yaml:"fail-fast"`
		Matrix   struct {
			Pack []string `yaml:"pack"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []installMatrixStep `yaml:"steps"`
}

type installMatrixStep struct {
	Name            string            `yaml:"name"`
	Run             string            `yaml:"run"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
}

// readInstallMatrixWorkflow parses the workflow file's part these pins read.
func readInstallMatrixWorkflow(t *testing.T, file string) installMatrixWorkflow {
	t.Helper()
	var wf installMatrixWorkflow
	if err := yaml.Unmarshal([]byte(readWorkflow(t, file)), &wf); err != nil {
		t.Fatalf("%s does not parse as YAML: %v", file, err)
	}
	return wf
}

// installJobRunning returns the one job in the workflow file whose steps run a script
// containing needle, and that step.
func installJobRunning(t *testing.T, wf installMatrixWorkflow, file, needle string) (string, installMatrixJob, installMatrixStep) {
	t.Helper()
	var found []string
	var job installMatrixJob
	var step installMatrixStep
	for name, j := range wf.Jobs {
		for _, s := range j.Steps {
			if strings.Contains(s.Run, needle) {
				found = append(found, name)
				job, step = j, s
				break
			}
		}
	}
	switch len(found) {
	case 0:
		t.Fatalf("%s has no job whose steps run %q, so it installs no pack from its vendor "+
			"(docs/reference/agent-install-in-ci.md)", file, needle)
	case 1:
	default:
		slices.Sort(found)
		t.Fatalf("%s has %d jobs running %q (%s); this pin reads exactly one", file, len(found),
			needle, strings.Join(found, ", "))
	}
	return found[0], job, step
}

// requireHardFailingInstallJob is OQ-CI3 for one install matrix: a cell per pack that fails
// like any other job. `fail-fast: false` keeps one vendor's red cell from cancelling the
// others, and no `continue-on-error` anywhere keeps a red cell red.
func requireHardFailingInstallJob(t *testing.T, file, name string, job installMatrixJob) {
	t.Helper()
	if job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Errorf("%s job %q does not set `fail-fast: false`, so one vendor's failed install "+
			"cancels every other pack's cell (docs/reference/agent-install-in-ci.md#oq-ci3)", file, name)
	}
	if softened(job.ContinueOnError) {
		t.Errorf("%s job %q is `continue-on-error`, which turns a vendor's broken install into "+
			"a cell nobody reads (docs/reference/agent-install-in-ci.md#p4)", file, name)
	}
	for _, s := range job.Steps {
		if softened(s.ContinueOnError) {
			t.Errorf("%s job %q: step %q is `continue-on-error`; no step of a vendor-install "+
				"cell may be (docs/reference/agent-install-in-ci.md#oq-ci3)", file, name, s.Name)
		}
	}
}

// softened reports whether a `continue-on-error` value can be anything but false.
func softened(v any) bool {
	if v == nil {
		return false
	}
	b, ok := v.(bool)
	return !ok || b
}

// sameSet reports the names in want missing from got and the names in got not in want.
func sameSet(got, want []string) (missing, extra []string) {
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			extra = append(extra, g)
		}
	}
	return missing, extra
}

func packMatrixNames() []string {
	var out []string
	for _, tc := range packMatrix {
		out = append(out, tc.pack)
	}
	return out
}

// TestPackInstallsWorkflowMirrorsPackMatrix: packs.yml's install matrix is every packMatrix
// pack, no more, its cells fail hard, and each selects its own subtest.
func TestPackInstallsWorkflowMirrorsPackMatrix(t *testing.T) {
	name, job, step := installJobRunning(t, readInstallMatrixWorkflow(t, packInstallsWorkflow),
		packInstallsWorkflow, packInstallsTest)
	requireHardFailingInstallJob(t, packInstallsWorkflow, name, job)
	got := job.Strategy.Matrix.Pack
	if dup := duplicates(got); len(dup) > 0 {
		t.Errorf("%s's install matrix lists %s more than once", packInstallsWorkflow, strings.Join(dup, ", "))
	}
	missing, extra := sameSet(got, packMatrixNames())
	if len(missing) > 0 {
		t.Errorf("%s's install matrix omits %s, which packMatrix (integration/agents_test.go) "+
			"covers: those packs' vendor installs never run on Linux. Add them to the job's "+
			"`pack:` list", packInstallsWorkflow, strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("%s's install matrix lists %s, which packMatrix has no row for, so the cell "+
			"selects no subtest", packInstallsWorkflow, strings.Join(extra, ", "))
	}
	requireCellsSelectTheirOwnSubtest(t, packInstallsWorkflow, step, packInstallsTest,
		macosUserPackInstallsTest, got)
}

// TestMacosUserPackInstallsWorkflowMirrorsPackMatrix: macos-user.yml's install matrix is every
// packMatrix pack published for its runner's platform, the `via: npm` ones first, each cell
// failing hard, declared as a macos-user job with real installs on, and selecting exactly its
// own subtest; and no other job of the workflow turns real installs on.
//
// Carries the TestMacosUser prefix for the reason the Q3 step's pin does
// (macosusersandboxstep_test.go): the workflow that depends on the job also verifies it. It is
// not behind requireMacosUser and needs no Mac.
func TestMacosUserPackInstallsWorkflowMirrorsPackMatrix(t *testing.T) {
	wf := readInstallMatrixWorkflow(t, macosUserWorkflow)
	name, job, step := installJobRunning(t, wf, macosUserWorkflow, macosUserPackInstallsTest)
	requireHardFailingInstallJob(t, macosUserWorkflow, name, job)
	got := job.Strategy.Matrix.Pack
	if dup := duplicates(got); len(dup) > 0 {
		t.Errorf("%s's install matrix lists %s more than once", macosUserWorkflow, strings.Join(dup, ", "))
	}

	// THE PLATFORM, read from the job's `runs-on`: the list below is derived for it.
	label, _ := job.RunsOn.(string)
	platform, known := macosUserRunnerPlatforms[label]
	if !known {
		t.Fatalf("%s job %q runs on %v, which macosUserRunnerPlatforms does not map to a "+
			"platform, so this pin cannot say which packs its cells can install. Add the "+
			"label's GOOS and GOARCH to that map (integration/macosuserpackinstalls_test.go)",
			macosUserWorkflow, name, job.RunsOn)
	}

	// THE LIST: packMatrix, less what the vendor does not publish for the runner's platform.
	var want []string
	kind := map[string]string{}
	for _, tc := range packMatrix {
		in := shippedProgram(t, tc.pack, tc.binary)
		kind[tc.pack] = in.Kind
		if why := in.UnpublishedReason(platform.goos, platform.goarch); why != "" {
			if slices.Contains(got, tc.pack) {
				t.Errorf("%s's install matrix lists %s, which cannot install on %s (%s/%s): %s. "+
					"Its subtest would skip and the cell would fail the vacuity check every "+
					"night; drop it from the list", macosUserWorkflow, tc.pack, label,
					platform.goos, platform.goarch, why)
			}
			continue
		}
		want = append(want, tc.pack)
	}
	missing, extra := sameSet(got, want)
	if len(missing) > 0 {
		t.Errorf("%s's install matrix omits %s: packMatrix covers them and their vendors "+
			"publish for %s/%s, so no CI job installs their darwin build before a user does "+
			"(docs/reference/agent-install-in-ci.md#OQ-CI7). Add them to the job's `pack:` list",
			macosUserWorkflow, strings.Join(missing, ", "), platform.goos, platform.goarch)
	}
	for _, e := range extra {
		if _, inMatrix := kind[e]; !inMatrix {
			t.Errorf("%s's install matrix lists %s, which packMatrix has no row for, so the "+
				"cell selects no subtest", macosUserWorkflow, e)
		}
	}

	// NPM FIRST, as ruled: the `via: npm` half is the one never measured on a Mac, so its cells
	// are listed, and therefore queued, ahead of the installer packs'.
	firstOther := -1
	for i, p := range got {
		if kind[p] != "npm" && firstOther < 0 {
			firstOther = i
		}
		if kind[p] == "npm" && firstOther >= 0 {
			t.Errorf("%s's install matrix lists the `via: npm` pack %s after %s, which is not "+
				"an npm pack (install kind %q). OQ-CI7 ruled the npm packs first "+
				"(docs/reference/agent-install-in-ci.md#OQ-CI7): move every npm pack ahead of "+
				"the rest", macosUserWorkflow, p, got[firstOther], kind[got[firstOther]])
		}
	}

	// THE DECLARATIONS. Without YOLO_TEST_MACOS_USER a cell whose subtest skipped would be
	// green; without YOLO_TEST_REAL_PACK_INSTALLS every subtest skips. YOLO_RUNTIME must stay
	// unset, as in the main job: the fixture selects the backend per launch, and a job-level
	// value is read by the harness's detectRuntime as a container runtime instead.
	for _, k := range []string{macosUserDeclareEnv, realPackInstallsEnv} {
		if strings.TrimSpace(step.Env[k]) == "" {
			t.Errorf("%s: the install step %q does not set %s", macosUserWorkflow, step.Name, k)
		}
	}
	if _, set := step.Env["YOLO_RUNTIME"]; set {
		t.Errorf("%s: the install step %q sets YOLO_RUNTIME. Pack Installs sets it to podman, "+
			"but here the fixture selects macos-user per launch (macosUserRunEnv), and a "+
			"job-level value is read by detectRuntime as a container runtime", macosUserWorkflow, step.Name)
	}

	// ONLY THIS JOB INSTALLS FROM A VENDOR. The backend job's `-run '^TestMacosUser'` step
	// selects this test too, and every subtest must skip there
	// (TestMacosUserPackInstallsSkipInTheBackendJob): with the variable set it would run every
	// darwin vendor install in a row, and one vendor's break would turn the backend's own
	// verdict red. So no other job may carry it, through the workflow's env, its own, a step's,
	// or a `run:` script that sets or exports it.
	if _, set := wf.Env[realPackInstallsEnv]; set {
		t.Errorf("%s sets %s in its workflow-level env, which reaches every job, the backend "+
			"job included. Set it on the %q job's install step only", macosUserWorkflow,
			realPackInstallsEnv, name)
	}
	for other, j := range wf.Jobs {
		if other == name {
			continue
		}
		if _, set := j.Env[realPackInstallsEnv]; set {
			t.Errorf("%s job %q sets %s; only the %q job may, because a job that selects %s "+
				"with it set installs every pack from its vendor", macosUserWorkflow, other,
				realPackInstallsEnv, name, macosUserPackInstallsTest)
		}
		for _, s := range j.Steps {
			_, set := s.Env[realPackInstallsEnv]
			if set || strings.Contains(s.Run, realPackInstallsEnv) {
				t.Errorf("%s job %q: step %q sets %s; only the %q job may, because a job that "+
					"selects %s with it set installs every pack from its vendor",
					macosUserWorkflow, other, s.Name, realPackInstallsEnv, name,
					macosUserPackInstallsTest)
			}
		}
	}

	requireCellsSelectTheirOwnSubtest(t, macosUserWorkflow, step, macosUserPackInstallsTest,
		packInstallsTest, got)
}

// sudoFact is the Runner facts line that records whether sudo would prompt.
const sudoFact = "sudo -n true"

// TestMacosUserWorkflowRecordsSudoBeforeAccountSetup: every macos-user.yml job that runs
// `yolo macos-setup` records whether `sudo -n` would prompt, in a step before that one.
//
// macos-setup calls sudo itself, so on a runner without passwordless sudo its step HANGS
// rather than failing, until the job's timeout ends it with no cause given. The recorded fact
// is the line that names the cause. Both jobs need it, the install job's account step having
// no `continue-on-error` and a 60-minute cap to burn.
func TestMacosUserWorkflowRecordsSudoBeforeAccountSetup(t *testing.T) {
	wf := readInstallMatrixWorkflow(t, macosUserWorkflow)
	var setupJobs []string
	for name, j := range wf.Jobs {
		recorded := false
		for _, s := range j.Steps {
			if strings.Contains(s.Run, "macos-setup") {
				setupJobs = append(setupJobs, name)
				if !recorded {
					t.Errorf("%s job %q runs macos-setup in step %q with no earlier step "+
						"running `%s`, so a runner whose sudo would prompt hangs that step "+
						"until the job's timeout with nothing in the log saying why. Add "+
						"`%s && echo \"sudo -n: passwordless\" || echo \"sudo -n: WOULD "+
						"PROMPT\"` to its Runner facts step", macosUserWorkflow, name, s.Name,
						sudoFact, sudoFact)
				}
				break
			}
			if strings.Contains(s.Run, sudoFact) {
				recorded = true
			}
		}
	}
	if len(setupJobs) == 0 {
		t.Fatalf("no %s job runs macos-setup, so this pin checks nothing; it reads the step "+
			"that creates the sandbox account", macosUserWorkflow)
	}
}

// requireCellsSelectTheirOwnSubtest checks an install step's `-run` pattern for every pack in
// its matrix: the top level selects test and not sibling (the other backend's install test),
// and the subtest level selects that pack's subtest and no other packMatrix pack's.
//
// EACH LEVEL OF A -run PATTERN MATCHES UNANCHORED. packs.yml's cells read
// `TestPackInstallsVersionsAndConfigures/${{ matrix.pack }}` until 2026-10-05, and `pi`
// matches `copilot`, so the pi cell installed copilot too: a copilot break would have turned it
// red under pi's name, the masking OQ-CI3's one job per pack exists to prevent.
func requireCellsSelectTheirOwnSubtest(t *testing.T, file string, step installMatrixStep, test, sibling string, packs []string) {
	t.Helper()
	m := regexp.MustCompile(`-run '([^']+)'`).FindStringSubmatch(step.Run)
	if m == nil {
		t.Fatalf("%s: the install step %q has no `-run '<pattern>'`:\n%s", file, step.Name, step.Run)
	}
	const placeholder = "${{ matrix.pack }}"
	if !strings.Contains(m[1], placeholder) {
		t.Fatalf("%s: the install step's -run pattern %q does not name %s, so every cell runs "+
			"the same selection", file, m[1], placeholder)
	}
	for _, p := range packs {
		top, sub, ok := strings.Cut(strings.ReplaceAll(m[1], placeholder, p), "/")
		if !ok {
			t.Fatalf("%s: the -run pattern %q has no subtest level", file, m[1])
		}
		topRe, err := regexp.Compile(top)
		if err != nil {
			t.Fatalf("%s: the -run pattern's top level %q does not compile: %v", file, top, err)
		}
		if !topRe.MatchString(test) || topRe.MatchString(sibling) {
			t.Errorf("%s: the %s cell's -run top level %q must select %s and not %s", file, p,
				top, test, sibling)
		}
		subRe, err := regexp.Compile(sub)
		if err != nil {
			t.Fatalf("%s: the %s cell's subtest pattern %q does not compile: %v", file, p, sub, err)
		}
		for _, other := range packMatrixNames() {
			if hit := subRe.MatchString(other); hit != (other == p) {
				t.Errorf("%s: the %s cell's subtest pattern %q matches %s = %v; it must select "+
					"its own subtest and no other, or a red cell names the wrong vendor",
					file, p, sub, other, hit)
			}
		}
	}
}

// duplicates returns each name listed more than once.
func duplicates(names []string) []string {
	seen := map[string]int{}
	var out []string
	for _, n := range names {
		seen[n]++
		if seen[n] == 2 {
			out = append(out, n)
		}
	}
	return out
}
