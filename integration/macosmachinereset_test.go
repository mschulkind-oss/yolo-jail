package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// THE LAST `podman machine start` ATTEMPT RUNS ON A FRESH VM, and this file runs the step to
// prove it rather than reading it.
//
// MEASURED over the 38 runs of nightly-macos.yml from 34870117573 (2026-09-14) to 37118791671
// (2026-10-03): a start that FAILED FAST on attempt 1 (`Error: EOF` five times, `machine is
// not listening on ssh port` twice) came up on attempt 2 all seven times, and a start that HUNG on attempt 1 came up on attempt 2 twice in seven. The
// other five hung on attempts 2 and 3 as well, so a third start of the same machine recovered
// none of them. That third attempt now starts a machine made again from nothing:
// `podman machine rm -f`, then the step's own init.
//
// The re-init comes from the SAME `podman machine init` text, inside a shell function, because
// the share list must stay single-sourced: parseMachineInitShares reads the one init and
// TestTheNightlyRetriesTheMachineStartFromOneInit refuses a second.
//
// WHY RUN IT. Every pin in macosmachineshares_test.go reads the YAML, and a reading cannot
// tell a reset that runs from one that is written but never reached, or a watchdog that
// kills the subshell and leaves the hung podman running. So
// TestTheNightlysMachineStepResetsTheVMBeforeItsLastAttempt runs the step's own `run:`
// script under `bash -e` (the shell GitHub gives a step with no `shell:`), against a
// fake podman, with the 6-minute deadlines scaled to 2 seconds.

// workflowFile, workflowJob and workflowStep are a workflow as the runner reads it — parsed,
// so a `run:` script arrives with its indentation removed, exactly as bash gets it.
type workflowFile struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	TimeoutMinutes string         `yaml:"timeout-minutes"`
	Steps          []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name           string            `yaml:"name"`
	Run            string            `yaml:"run"`
	TimeoutMinutes string            `yaml:"timeout-minutes"`
	Env            map[string]string `yaml:"env"`
}

func parseWorkflow(t *testing.T, name string) workflowFile {
	t.Helper()
	var wf workflowFile
	if err := yaml.Unmarshal([]byte(readWorkflow(t, name)), &wf); err != nil {
		t.Fatalf("%s does not parse as YAML: %v", name, err)
	}
	return wf
}

// jobStepRunning returns the step of a job whose `run:` script contains needle.
func jobStepRunning(t *testing.T, wf workflowFile, job, needle string) workflowStep {
	t.Helper()
	j, ok := wf.Jobs[job]
	if !ok {
		t.Fatalf("%s has no job %q", nightlyWorkflow, job)
	}
	for _, s := range j.Steps {
		if strings.Contains(s.Run, needle) {
			return s
		}
	}
	t.Fatalf("%s: job %q has no step whose script contains %q — this test has lost its subject",
		nightlyWorkflow, job, needle)
	return workflowStep{}
}

// minutesOf reads a `timeout-minutes:` value, which must be a plain integer here.
func minutesOf(t *testing.T, what, v string) time.Duration {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		t.Fatalf("%s: `timeout-minutes: %s` is not a plain integer, so this test cannot do the "+
			"arithmetic it exists for", what, v)
	}
	return time.Duration(n) * time.Minute
}

const nightlyShardJob = "integration-macos"

func nightlyMachineStep(t *testing.T) workflowStep {
	t.Helper()
	return jobStepRunning(t, parseWorkflow(t, nightlyWorkflow), nightlyShardJob, "podman machine start")
}

// fakePodman stands in for podman. It records every call and answers `machine start` from
// FAKE_STARTS (`ok`, `fail` or `hang`, one per attempt), and hangs the Nth `machine init`
// when FAKE_INIT_HANGS_ON is N.
//
// A HANG TICKS. It appends to a file named for its own pid until it is killed (or a minute
// passes), so after the step exits the test can see whether anything the step bounded is
// still running — which an exit status cannot show, since the watchdog ends the WAIT either
// way. It is the fake's own process that ticks, so it is the process the step started.
const fakePodman = `
printf '%s\n' "$*" >> "$FAKE_PODMAN_DIR/calls"
count() {
  n=$(( $(cat "$FAKE_PODMAN_DIR/$1" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$FAKE_PODMAN_DIR/$1"
}
hang() {
  echo "$$" >> "$FAKE_PODMAN_DIR/hung-pids"
  end=$(( $(date +%s) + 60 ))
  while [ "$(date +%s)" -lt "$end" ]; do
    echo tick >> "$FAKE_PODMAN_DIR/alive-$$"
    sleep 0.2
  done
}
case "$1 $2" in
"machine init")
  count inits
  if [ "$n" = "${FAKE_INIT_HANGS_ON:-0}" ]; then hang; fi
  echo "Machine init complete" ;;
"machine start")
  count starts
  IFS=, read -ra outcomes <<< "$FAKE_STARTS"
  case "${outcomes[$((n - 1))]}" in
  ok) echo "Machine \"podman-machine-default\" started successfully" ;;
  hang) hang ;;
  *) echo "Error: EOF" >&2; exit 125 ;;
  esac ;;
esac
exit 0
`

// stepDeadlineRe matches the step's 6-minute deadline literal, which the run below scales.
var stepDeadlineRe = regexp.MustCompile(`\b360\b`)

type machineStepRun struct {
	out   string
	rc    int
	calls []string
	took  time.Duration
	// alive names the hung fakes still running after the step exited; it must be empty.
	alive []string
}

// runNightlyMachineStep runs the step's script against fakePodman and returns what it did.
func runNightlyMachineStep(t *testing.T, script, starts string, initHangsOn int) machineStepRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH; the step's script cannot be run here")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	shebang := "#!" + bash + "\n"
	for name, body := range map[string]string{
		"podman": fakePodman,
		// The installer half of the step: the download and the .pkg install do nothing here.
		"curl": "exit 0\n",
		"sudo": "exit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(shebang+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// ⚠ THE REAL PODMAN MUST NOT BE REACHABLE. The step puts /opt/podman/bin first on PATH,
	// and on a Mac with podman installed that is a real `podman machine rm -f`. Redirect it
	// to the fakes and refuse to run a script that still names it.
	const podmanBin = "/opt/podman/bin"
	if !strings.Contains(script, `export PATH="`+podmanBin+`:$PATH"`) {
		t.Fatalf("the step no longer puts %s on PATH the way this test redirects; refusing to "+
			"run it, since a real podman could then delete the developer's machine", podmanBin)
	}
	script = strings.ReplaceAll(script, podmanBin, bin)
	if !stepDeadlineRe.MatchString(script) {
		t.Fatalf("the step's deadlines are no longer the literal 360 this test scales, so a run "+
			"would take minutes; scale the new spelling here:\n%s", script)
	}
	script = stepDeadlineRe.ReplaceAllString(script, "2")
	script = strings.ReplaceAll(script, "sleep 5", "sleep 0")
	path := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	// Output goes to a FILE, not a pipe: the watchdogs' own `sleep`s outlive the script by
	// design (GitHub's runner does not wait for them either), and a pipe would make every run
	// wait out the longest one.
	logPath := filepath.Join(dir, "output")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "-e", path)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"GITHUB_PATH=" + filepath.Join(dir, "github-path"),
		"GITHUB_RUN_ID=4242",
		"FAKE_PODMAN_DIR=" + dir,
		"FAKE_STARTS=" + starts,
		"FAKE_INIT_HANGS_ON=" + strconv.Itoa(initHangsOn),
	}
	// Whatever happens below, nothing this run started may keep ticking past the test.
	t.Cleanup(func() {
		pids, _ := os.ReadFile(filepath.Join(dir, "hung-pids"))
		for _, f := range strings.Fields(string(pids)) {
			if pid, err := strconv.Atoi(f); err == nil {
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Kill()
				}
			}
		}
	})
	t0 := time.Now()
	err = cmd.Run()
	took := time.Since(t0)
	out, _ := os.ReadFile(logPath)
	rc := 0
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		t.Fatalf("the step did not finish within 60s with its deadlines scaled to 2s:\n%s", out)
	case errors.As(err, &ee):
		rc = ee.ExitCode()
	case err != nil:
		t.Fatalf("running the step: %v\n%s", err, out)
	}
	callsRaw, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return machineStepRun{
		out:   string(out),
		rc:    rc,
		calls: strings.Split(strings.TrimSpace(string(callsRaw)), "\n"),
		took:  took,
		alive: stillTicking(dir),
	}
}

// stillTicking returns the hung fakes that are still running a second after the step
// exited: a tick file that grew in that second belongs to a process nothing killed.
func stillTicking(dir string) []string {
	size := func() map[string]int64 {
		got := map[string]int64{}
		matches, _ := filepath.Glob(filepath.Join(dir, "alive-*"))
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil {
				got[filepath.Base(m)] = st.Size()
			}
		}
		return got
	}
	before := size()
	if len(before) == 0 {
		return nil
	}
	time.Sleep(time.Second)
	var alive []string
	for name, n := range size() {
		if n > before[name] {
			alive = append(alive, name)
		}
	}
	return alive
}

// machineCalls keeps the `machine init|start|stop|rm` calls, dropping the share probes.
func machineCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		f := strings.Fields(c)
		if len(f) >= 2 && f[0] == "machine" && f[1] != "ssh" {
			out = append(out, c)
		}
	}
	return out
}

func TestTheNightlysMachineStepResetsTheVMBeforeItsLastAttempt(t *testing.T) {
	if _, err := exec.LookPath("pkill"); err != nil {
		t.Skip("no pkill on PATH; the step's reset watchdog uses it (macOS and the CI runners ship it)")
	}
	script := nightlyMachineStep(t).Run
	run := func(t *testing.T, starts string, initHangsOn int) machineStepRun {
		t.Helper()
		r := runNightlyMachineStep(t, script, starts, initHangsOn)
		if len(r.alive) > 0 {
			t.Errorf("the step exited with %v still running: a deadline fired and ended the wait "+
				"without killing the podman it bounded:\n%s", r.alive, r.out)
		}
		return r
	}

	// What the first init passes, so the reset's can be compared to it.
	firstInit := func(t *testing.T, calls []string) string {
		t.Helper()
		if len(calls) == 0 || !strings.HasPrefix(calls[0], "machine init ") {
			t.Fatalf("the step's first machine call is not `init`: %q", calls)
		}
		if !strings.Contains(calls[0], "-v /nix:/nix") {
			t.Fatalf("the first init carries no share list: %q", calls[0])
		}
		return calls[0]
	}

	t.Run("a restart that works is all it takes", func(t *testing.T) {
		r := run(t, "fail,ok", 0)
		if r.rc != 0 {
			t.Fatalf("rc %d, want 0:\n%s", r.rc, r.out)
		}
		init := firstInit(t, r.calls)
		want := []string{init, "machine start", "machine stop", "machine start"}
		if got := machineCalls(r.calls); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("machine calls:\n%s\nwant (no reset when attempt 2 comes up):\n%s",
				strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if !strings.Contains(r.out, "::warning::") || !strings.Contains(r.out, "attempt 1 failed") {
			t.Errorf("a shard that needed a retry must say so in a warning naming what ran:\n%s", r.out)
		}
	})

	t.Run("the last attempt runs on a fresh VM, made by the same init", func(t *testing.T) {
		r := run(t, "fail,fail,ok", 0)
		if r.rc != 0 {
			t.Fatalf("rc %d, want 0 — the fresh VM came up:\n%s", r.rc, r.out)
		}
		init := firstInit(t, r.calls)
		want := []string{init, "machine start", "machine stop", "machine start", "machine stop",
			"machine rm -f", init, "machine start"}
		if got := machineCalls(r.calls); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("machine calls:\n%s\nwant (the reset re-inits with the FIRST init's flags):\n%s",
				strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		for _, s := range []string{"::warning::", "attempt 3/3", "VM reset"} {
			if !strings.Contains(r.out, s) {
				t.Errorf("a shard that came up only on the fresh VM must say so (%q missing) — "+
					"that warning is how a reset's worth gets measured:\n%s", s, r.out)
			}
		}
	})

	t.Run("when nothing comes up the error names every attempt", func(t *testing.T) {
		r := run(t, "fail,hang,hang", 0)
		if r.rc == 0 {
			t.Fatalf("rc 0 with no machine up:\n%s", r.out)
		}
		var errLine string
		for _, l := range strings.Split(r.out, "\n") {
			if strings.HasPrefix(l, "::error::") {
				errLine = l
			}
		}
		for _, s := range []string{"attempt 1 failed", "attempt 2 HUNG", "VM reset",
			"attempt 3 HUNG", "gh run rerun 4242 --failed"} {
			if !strings.Contains(errLine, s) {
				t.Errorf("the step's error must say which attempts ran and what to do next; "+
					"%q is missing from:\n%s\n\nfull output:\n%s", s, errLine, r.out)
			}
		}
		got := machineCalls(r.calls)
		if n := len(got); n == 0 || got[n-1] != "machine start" {
			t.Errorf("the last machine call is not the third start — after the last attempt there is "+
				"nothing to retry, so a stop there only spends the step's budget:\n%s", strings.Join(got, "\n"))
		}
	})

	t.Run("a reset that hangs is killed, and the last attempt is not run", func(t *testing.T) {
		r := run(t, "fail,fail,ok", 2)
		if r.rc == 0 {
			t.Fatalf("rc 0 although the reset's init hung:\n%s", r.out)
		}
		starts := 0
		for _, c := range machineCalls(r.calls) {
			if c == "machine start" {
				starts++
			}
		}
		if starts != 2 {
			t.Errorf("%d starts; want 2 — a machine whose reset did not finish has nothing to start:\n%s",
				starts, strings.Join(r.calls, "\n"))
		}
		if !strings.Contains(r.out, "reset HUNG") || !strings.Contains(r.out, "attempt 3 never ran") {
			t.Errorf("the error must name the hung reset and the attempt it cost:\n%s", r.out)
		}
		// The reset's hung init is a CHILD of the subshell the watchdog targets; `run` above
		// checks that it stopped, and this that the step did not wait for it.
		if r.took > 20*time.Second {
			t.Errorf("the step took %s with a 2s reset deadline — the hung init was not killed", r.took)
		}
		if !strings.Contains(strings.Join(r.calls, "\n"), "machine rm -f") {
			t.Errorf("the reset that hung never removed the machine first:\n%s", strings.Join(r.calls, "\n"))
		}
	})
}

// THE STEP CAP HAS TO COVER EVERY ATTEMPT, OR THE LAST ONES ARE UNREACHABLE.
//
// The cap was last set by a sum that left out one term: 18m for three 6m starts plus ~2m for
// the download, install and init, but nothing for the `podman machine stop` after each hung
// start — 1m30s–1m32s every time it was measured (podman's graceful stop times out at 90s,
// then it hard-stops). With that term a shard whose starts all hang needed more than the 25m
// cap, and the cap fired INSIDE the last attempt twice: run 36862305695 shard 7 (during its
// stop, so the step's own error never printed) and run 37056529987 shard 10.
//
// So this redoes the sum from the step itself: its bounded phases read from the script, and
// its unbounded ones at the slowest the logs show.
const (
	// The download and the .pkg install: 4s–16s in the logs read.
	measuredPodmanInstall = 20 * time.Second
	// The first `podman machine init`, image pull included: slowest of the seven read, run
	// 36862305695 shard 7 (13:04:59 → 13:07:52).
	measuredSlowestMachineInit = 2*time.Minute + 53*time.Second
	// One `podman machine stop` after a hung start: e.g. run 37056529987 shard 2
	// (20:34:20 → 20:35:52).
	measuredHungMachineStop = 92 * time.Second
	// The `sleep 5` between attempts.
	machineRetryPause = 5 * time.Second
)

var (
	startDeadlineArgRe = regexp.MustCompile(`(?m)^[ \t]*(?:if|while|until)[ \t]+start_with_deadline[ \t]+([0-9]+)`)
	resetDeadlineArgRe = regexp.MustCompile(`(?m)^[ \t]*(?:if|while|until)[ \t]+(?:![ \t]+)?reset_with_deadline[ \t]+([0-9]+)`)
	attemptListRe      = regexp.MustCompile(`(?m)^[ \t]*for attempt in ([^;\n]+);[ \t]*do`)
)

func TestTheNightlysMachineStepCapCoversEveryAttempt(t *testing.T) {
	step := nightlyMachineStep(t)
	code := uncommentedYAML(step.Run)
	stepCap := minutesOf(t, step.Name, step.TimeoutMinutes)

	seconds := func(re *regexp.Regexp, what string) time.Duration {
		m := re.FindStringSubmatch(code)
		if m == nil {
			t.Fatalf("%s: the step has no %s in conditional position; this sum cannot be done", step.Name, what)
		}
		n, _ := strconv.Atoi(m[1])
		return time.Duration(n) * time.Second
	}
	start := seconds(startDeadlineArgRe, "`start_with_deadline <seconds>`")
	reset := seconds(resetDeadlineArgRe, "`reset_with_deadline <seconds>`")
	m := attemptListRe.FindStringSubmatch(code)
	if m == nil {
		t.Fatalf("%s: no `for attempt in …; do` loop", step.Name)
	}
	attempts := len(strings.Fields(m[1]))

	// No stop after the LAST attempt: nothing follows it.
	need := measuredPodmanInstall + measuredSlowestMachineInit +
		time.Duration(attempts)*start + reset +
		time.Duration(attempts-1)*(measuredHungMachineStop+machineRetryPause)
	if stepCap < need {
		t.Errorf("%s is capped at %s, and a shard whose starts all hang needs %s: the install "+
			"(%s), the first init (%s), %d starts at %s, the reset at %s, and a stop plus a pause "+
			"between attempts (%s + %s each). The cap then fires inside the last attempt and its "+
			"report never prints — measured twice at a 25m cap (runs 36862305695, 37056529987). "+
			"Raise `timeout-minutes` to at least %d.", step.Name, stepCap, need,
			measuredPodmanInstall, measuredSlowestMachineInit, attempts, start, reset,
			measuredHungMachineStop, machineRetryPause, int((need+time.Minute-1)/time.Minute))
	}
}
