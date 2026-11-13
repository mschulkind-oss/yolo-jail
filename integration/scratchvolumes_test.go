package integration

// scratchvolumes_test.go pins, end to end, that quitting a podman jail does not wait for
// its scratch volumes to be deleted, and that they ARE deleted shortly after.
//
// THE BUG. /tmp, /var/tmp and the nested container dirs were ANONYMOUS volumes under
// `podman run --rm`, which makes the attached client delete them itself, file by file,
// before it exits — while the launcher waits on it and the terminal waits on the
// launcher. 32 s on the maintainer's host after a 59 h session (docs/reference/perf-logging.md,
// "The linger was the scratch volumes"). Reproduced in a nested jail 2026-09-28 with bare
// podman: 200,000 empty files in an anonymous /tmp held the client 8.8 s past its
// container's exit; the same files in a named volume, 0.2 s.
//
// These are the CALL-SITE pins: runContainer minting a real per-launch id (the mount
// source must parse as a scratch volume the reaper recognises), the teardown starting
// the remover without waiting, the remover finishing, and the housekeeping slot reaping
// what no remover reached. The unit tier (internal/cli/run/scratchremoval_test.go,
// internal/prune/scratchvolumes_test.go) pins each callee.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"gopkg.in/yaml.v3"
)

// scratchVolumesOf lists the scratch volumes the runtime holds for cname. stdout alone is parsed;
// a failed runtime call returns an error with its argv, context state and captured stderr.
func scratchVolumesOf(ctx context.Context, runtime, cname string) ([]string, error) {
	args := []string{"volume", "ls", "--format", "{{.Name}}"}
	command := strings.Join(append([]string{runtime}, args...), " ")
	cmd := exec.CommandContext(ctx, runtime, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("runtime=%q command=%q ctx.Err()=%v stderr=%q: %w",
			runtime, command, ctx.Err(), stderr.String(), err)
	}
	var mine []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if n := strings.TrimSpace(line); strings.HasPrefix(n, cname+".scratch.") {
			mine = append(mine, n)
		}
	}
	return mine, nil
}

const scratchDiagnosticLogTailBytes = 8 * 1024

func scratchRemovalFailureMessage(dir, problem string) string {
	stateDir := filepath.Join(dir, ".yolo", "logs")
	return fmt.Sprintf("%s\nworkspace diagnostics (each log capped at the last %d bytes):\n"+
		"housekeeping.log:\n%s\nlaunch.log:\n%s",
		problem, scratchDiagnosticLogTailBytes,
		scratchDiagnosticLog(filepath.Join(stateDir, "housekeeping.log")),
		scratchDiagnosticLog(filepath.Join(stateDir, "launch.log")))
}

func scratchDiagnosticLog(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("[unavailable: %v]", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Sprintf("[unavailable: %v]", err)
	}
	truncated := info.Size() > scratchDiagnosticLogTailBytes
	if truncated {
		if _, err := file.Seek(info.Size()-scratchDiagnosticLogTailBytes, io.SeekStart); err != nil {
			return fmt.Sprintf("[unavailable: %v]", err)
		}
	}
	contents, err := io.ReadAll(io.LimitReader(file, scratchDiagnosticLogTailBytes))
	if err != nil {
		return fmt.Sprintf("[unavailable: %v]", err)
	}
	if truncated {
		return fmt.Sprintf("[truncated to last %d bytes]\n%s", scratchDiagnosticLogTailBytes, contents)
	}
	return string(contents)
}

func TestScratchVolumeListFailureIncludesRuntimeDiagnostics(t *testing.T) {
	runtime := fakeScratchRuntime(t,
		"diagnostic-workspace.scratch.partial.tmp\n",
		"fake podman volume-list failure: store is unavailable",
		125,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	volumes, err := scratchVolumesOf(ctx, runtime, "diagnostic-workspace")
	if err == nil {
		t.Fatal("fake volume-list failure unexpectedly succeeded")
	}
	if len(volumes) != 0 {
		t.Fatalf("failed volume listing returned entries that could be mistaken for a complete list: %q", volumes)
	}
	for _, want := range []string{
		"runtime=\"" + runtime + "\"",
		"volume ls --format {{.Name}}",
		"ctx.Err()=<nil>",
		"exit status 125",
		"fake podman volume-list failure: store is unavailable",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("listing error does not contain %q: %v", want, err)
		}
	}
}

func TestScratchVolumeListFailureIncludesContextError(t *testing.T) {
	runtime := fakeScratchRuntime(t, "", "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scratchVolumesOf(ctx, runtime, "diagnostic-workspace")
	if err == nil || !strings.Contains(err.Error(), "ctx.Err()=context canceled") {
		t.Fatalf("canceled volume listing error = %v, want its context error", err)
	}
}

func TestScratchRemovalFailureIncludesRetainedLogs(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, ".yolo", "logs")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"housekeeping.log": "fixture housekeeping diagnostic",
		"launch.log":       "fixture retained launch output",
	} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime := fakeScratchRuntime(t, "", "fake podman volume-list failure: store is unavailable", 125)
	_, err := waitForScratchRemoval(dir, "diagnostic-workspace", 1, runtime)
	if err == nil {
		t.Fatal("fake volume-list failure unexpectedly succeeded")
	}
	for _, want := range []string{
		"runtime=\"" + runtime + "\"",
		"volume ls --format {{.Name}}",
		"ctx.Err()=<nil>",
		"exit status 125",
		"fake podman volume-list failure: store is unavailable",
		"housekeeping.log:\nfixture housekeeping diagnostic",
		"launch.log:\nfixture retained launch output",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("scratch-removal failure does not contain %q: %v", want, err)
		}
	}
}

func TestScratchRemovalDiagnosticLogTailIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "housekeeping.log")
	contents := strings.Repeat("x", scratchDiagnosticLogTailBytes+1) + "tail-marker"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scratchDiagnosticLog(path)
	if !strings.Contains(got, "truncated to last") || !strings.HasSuffix(got, "tail-marker") ||
		len(got) > scratchDiagnosticLogTailBytes+80 {
		t.Fatalf("diagnostic log tail is not bounded or does not retain the end: length=%d suffix=%q",
			len(got), got[len(got)-min(len(got), 32):])
	}
}

func TestScratchVolumeListingParsesStdoutOnly(t *testing.T) {
	runtime := fakeScratchRuntime(t,
		"other-volume\ndiagnostic-workspace.scratch.0123456789abcdef.tmp\n",
		"diagnostic-workspace.scratch.must-not-be-parsed.tmp",
		0,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := scratchVolumesOf(ctx, runtime, "diagnostic-workspace")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"diagnostic-workspace.scratch.0123456789abcdef.tmp"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("scratch volume listing = %q, want only stdout scratch entry %q", got, want)
	}
}

func TestCIUploadsFailedIntegrationShardDiagnostics(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	var document yaml.Node
	if err := yaml.Unmarshal(workflow, &document); err != nil {
		t.Fatalf("ci.yml is not valid YAML: %v", err)
	}
	start := strings.Index(text, "      - name: Integration tests\n")
	if start < 0 {
		t.Fatal("could not locate integration-test workflow step")
	}
	end := strings.Index(text[start:], "  # THE CHECK THAT A COMPUTED PARTITION STILL NEEDS.")
	if end < 0 {
		t.Fatal("could not locate executed-test-record workflow step")
	}
	steps := text[start : start+end]
	for _, want := range []string{
		"id: integration",
		"- name: Record which tests this shard ran\n        if: ${{ always() && (steps.integration.outcome == 'success' || steps.integration.outcome == 'failure') }}",
		"if [ ! -s /tmp/shard/integration.json ]; then",
		"refusing to fabricate an executed-test record",
		"- name: Upload failed integration shard diagnostics\n        if: ${{ always() && failure() && steps.integration.outcome != 'skipped' }}",
		"integration-failure-attempt-${{ github.run_attempt }}-${{ matrix.os }}-shard-${{ matrix.shard }}",
		"/tmp/shard/integration.json",
		"/tmp/shard/all-tests",
		"/tmp/shard/my-tests",
		"/tmp/shard/ran-tests",
		"/tmp/shard/total",
		"if-no-files-found: warn",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("CI integration workflow does not route failed-shard diagnostics as required; missing %q", want)
		}
	}
	if !strings.Contains(steps, "go test -count=1 -timeout 0 -json -run \"$PATTERN\" ./integration") ||
		!strings.Contains(text, "result='${{ needs.integration.result }}'") {
		t.Fatal("workflow no longer runs the integration gate or preserves aggregate shard completeness checking")
	}
}

func fakeScratchRuntime(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-podman")
	script := "#!/bin/sh\nprintf '%s\\n' '" + stdout + "'\nprintf '%s\\n' '" + stderr + "' >&2\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func requirePodman(t *testing.T) {
	t.Helper()
	if detectRuntime() != "podman" {
		t.Skip("scratch volumes exist on podman only; Apple Container's scratch is tmpfs")
	}
}

// TestQuitDoesNotWaitForTheScratchDelete pins that the quit returns while the scratch remover it
// started is still running, by holding that remover at a gate the test opens only after the quit.
//
// THE QUIT IT PINS. The jail's only session is its last, so its quit streams the keeper's
// teardown and returns once the keeper has exited (endSession, streamKeeperTeardown). The keeper
// stops the container, confirms it gone, and runs teardownAfterExit, whose first act starts the
// remover detached and never waits for it (startScratchRemoval). The delete is back on the quit if
// anything on that chain comes to wait for the remover, or for the volumes it deletes. The volumes
// going anonymous again is caught by launchAndQuit's mount check directly.
//
// THE GATE. The second launch runs with a `podman` shim first on its PATH (newScratchRemoverGate).
// The remover inherits the launcher's environment, so every runtime call it makes meets the shim,
// which recognises the remover as its parent and blocks until the test opens the gate; every other
// caller goes straight to the real podman. The remover therefore cannot finish, nor delete a
// volume, before the test lets it. A quit that waited for either would never return, and runYolo's
// deadline turns that hang into a red test. So the pass condition is an event, the launcher
// returning while the remover still holds its in-flight lock (WaitForScratchRemovers) and the
// volume is still there, and machine load cannot decide it.
//
// The one bound left is the product's own: a remover gives up on volumes it could not list once
// scratchRemovalWait, a minute, has passed since it started, and a gated call is killed at the
// listing timeout and retried. A quit that took longer than that after starting the remover is
// slow enough to be a defect by itself, and the failure says so.
//
// WHY NOT A TIMING. This test used to time the quit: against a fixed 5 s, which the macOS nightly's
// podman machine exceeded at ordinary quits (run 36521751485 at 5.33 s, run 36711874486 at
// 10.37 s), and then against half of a baseline `rm -rf` of 200,000 files measured in the same
// workspace, which failed once in a loaded landing gate on 2026-11-09. An elapsed-time pass
// condition is what the event-driven test rule in AGENTS.md forbids.
//
// macOS podman is the same case: the argv mounts the same named scratch volumes there
// (assembleRunCmd puts ScratchMountArgs after podmanBaseMounts with no host-OS branch), and the
// teardown hands them to the same remover, which only deletes differently (a podman remote client
// has no `podman unshare`, so `podman volume rm` does the whole delete inside the VM,
// prune.emptyScratchVolume).
func TestQuitDoesNotWaitForTheScratchDelete(t *testing.T) {
	requireJail(t)
	requirePodman(t)
	dir := writeProject(t, `{}`)
	cname := jailName(dir)

	first := launchAndQuit(t, dir, cname)
	awaitScratchRemoval(t, dir, cname, 1)

	gate := newScratchRemoverGate(t)
	second := launchAndQuit(t, dir, cname, gate.env())

	// The launcher has returned. The remover it started cannot have finished: the gate is shut.
	if err := run.WaitForScratchRemovers(dir, 0); err == nil {
		t.Fatalf("the launcher returned and no scratch remover for %s is still running, though the "+
			"gate it must pass is still shut (%s). Either the remover was never started, or it gave "+
			"up a minute after it started because the quit took longer than that (scratchRemovalWait)",
			dir, gate.reachedNote())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	left, err := scratchVolumesOf(ctx, detectRuntime(), cname)
	cancel()
	if err != nil {
		t.Fatalf("could not list scratch volumes after the quit: %v", err)
	}
	if !slices.Contains(left, second.volume) {
		t.Fatalf("the launcher returned with %s already gone while the remover was held at its gate "+
			"(%s): something on the quit deleted it, which is the delete on the critical path again; "+
			"left: %v", second.volume, gate.reachedNote(), left)
	}

	gate.open(t)
	awaitScratchRemoval(t, dir, cname, 2)
	// The remover is done, so it has made its calls: had none of them met the gate, nothing above
	// was ever held, and the quit returning would prove nothing.
	if _, err := os.Stat(filepath.Join(gate.dir, "reached")); err != nil {
		t.Fatalf("no call the scratch remover made reached the test's podman shim, so the gate held "+
			"nothing: %v", err)
	}

	// A launch id minted per launch, never per workspace: a relaunch handed the last session's
	// volumes would get whatever its remover had not yet deleted (newScratchLaunchID).
	if first.volume == second.volume {
		t.Errorf("two launches of one workspace mounted the same /tmp volume %s", second.volume)
	}
}

// quitSample is one launch of the scratch fixture.
type quitSample struct {
	// volume is the scratch volume the launch mounted at /tmp.
	volume string
}

// launchAndQuit launches the fixture in dir, leaving a file in /tmp for the teardown to delete,
// and returns once yolo has.
func launchAndQuit(t *testing.T, dir, cname string, opts ...runOption) quitSample {
	t.Helper()
	script := `set -e
mkdir -p /tmp/fill && touch /tmp/fill/left-for-the-remover
awk '$5=="/tmp"{print "TMP_SOURCE=" $4}' /proc/self/mountinfo`
	res := runYolo(t, dir, script, append([]runOption{withTimeout(10 * time.Minute)}, opts...)...)
	if res.rc != 0 {
		t.Fatalf("rc=%d\n%s", res.rc, res.combined())
	}

	// The mount source names a scratch volume with a real launch id — what the reaper
	// and the remover recognise. An empty id would be a name every launch reused.
	m := regexp.MustCompile(`TMP_SOURCE=\S*/volumes/(` + regexp.QuoteMeta(cname) + `\.scratch\.[0-9a-f]{16}\.tmp)/_data`).
		FindStringSubmatch(res.stdout)
	if m == nil {
		t.Fatalf("/tmp is not a per-launch named scratch volume of %s:\n%s", cname, res.stdout)
	}
	return quitSample{volume: m[1]}
}

// scratchRemoverGate is a `podman` shim that holds every runtime call the scratch remover makes
// until the test opens the gate, and passes every other caller straight to the real podman.
//
// The gate is a FIFO the shim opens for writing, which blocks until a reader exists. The test
// opens the reader once, non-blocking, and keeps it open for the rest of the process, so the gate
// stays open for every later call: there is no window in which a call checks a flag, the test
// opens the gate, and the call then blocks for good. A call the remover's own timeout kills while
// it waits is retried by the remover, and is held again.
type scratchRemoverGate struct {
	dir     string
	shimDir string
}

func newScratchRemoverGate(t *testing.T) *scratchRemoverGate {
	t.Helper()
	real, err := exec.LookPath("podman")
	if err != nil {
		t.Fatalf("podman: %v", err)
	}
	g := &scratchRemoverGate{dir: t.TempDir(), shimDir: t.TempDir()}
	if err := syscall.Mkfifo(filepath.Join(g.dir, "gate"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// The remover is `<yolo> internal scratch-rm …`, and it runs podman directly, so it is the
	// shim's parent. `ps -o args=` reads that on Linux and macOS alike.
	shim := "#!/bin/sh\n" +
		"case \" $(ps -o args= -p \"$PPID\" 2>/dev/null) \" in\n" +
		"*\" internal " + run.ScratchRemoverVerb + " \"*)\n" +
		"\t: >" + shquote.Quote(filepath.Join(g.dir, "reached")) + "\n" +
		"\t: >" + shquote.Quote(filepath.Join(g.dir, "gate")) + "\n" +
		"\t;;\n" +
		"esac\n" +
		"exec " + shquote.Quote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(g.shimDir, "podman"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	// Whatever happens to the test, the remover is let go before the workspace's own cleanup
	// waits for it: cleanups run last-registered first, and writeProject's came before this one.
	t.Cleanup(func() { g.open(t) })
	return g
}

// env is the launch option that puts the shim first on the launcher's PATH, which the remover
// inherits.
func (g *scratchRemoverGate) env() runOption {
	return withEnv("PATH=" + g.shimDir + string(os.PathListSeparator) + os.Getenv("PATH"))
}

// reachedNote says whether any remover call has reached the gate yet, for a failure message.
func (g *scratchRemoverGate) reachedNote() string {
	if _, err := os.Stat(filepath.Join(g.dir, "reached")); err == nil {
		return "a remover call had reached the gate"
	}
	return "no remover call had reached the gate yet"
}

// gateReaders holds every opened gate's read end until the process exits, so a gate once
// opened never shuts again.
var gateReaders []*os.File

// open lets every held and every later remover call through. Idempotent.
func (g *scratchRemoverGate) open(t *testing.T) {
	t.Helper()
	path := filepath.Join(g.dir, "gate")
	for _, f := range gateReaders {
		if f.Name() == path {
			return
		}
	}
	// O_NONBLOCK: opening a FIFO's read end without it blocks until a writer comes.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("opening the scratch remover's gate: %v", err)
	}
	gateReaders = append(gateReaders, f)
}

// awaitScratchRemoval waits for the detached remover of dir's last launch to finish, and returns
// the housekeeping log once it has: the volumes going, then the remover itself, since its log line
// follows the last `volume rm`. runs is how many launches of dir have quit so far, each of which
// must have left the remover's record of its run.
func awaitScratchRemoval(t *testing.T, dir, cname string, runs int) string {
	t.Helper()
	log, err := waitForScratchRemoval(dir, cname, runs, detectRuntime())
	if err != nil {
		t.Fatalf("%s", err)
	}
	return log
}

func waitForScratchRemoval(dir, cname string, runs int, runtime string) (string, error) {
	logPath := filepath.Join(dir, ".yolo", "logs", "housekeeping.log")
	deadline := time.Now().Add(2 * time.Minute)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		left, err := scratchVolumesOf(ctx, runtime, cname)
		cancel()
		// podman 4.x fails the whole `volume ls` with "no such volume" when a volume is removed
		// while it lists, which is exactly what the remover is doing here: list again.
		if err != nil && strings.Contains(err.Error(), "no such volume") && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return "", fmt.Errorf("%s", scratchRemovalFailureMessage(dir,
				fmt.Sprintf("could not list scratch volumes after quit: %v", err)))
		}
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s", scratchRemovalFailureMessage(dir,
				fmt.Sprintf("scratch volumes still present 2 min after the quit: %v", left)))
		}
		time.Sleep(time.Second)
	}
	// The volumes going is not the remover being done: its log line follows the last
	// `volume rm`. Wait for the remover itself (its in-flight lock) before reading it.
	if err := run.WaitForScratchRemovers(dir, detachedWriterWait); err != nil {
		return "", fmt.Errorf("%s", scratchRemovalFailureMessage(dir,
			fmt.Sprintf("waiting for scratch removers failed: %v", err)))
	}
	log, _ := os.ReadFile(logPath)
	if got := strings.Count(string(log), "scratch: removed 4 volume(s)"); got < runs {
		return "", fmt.Errorf("%s", scratchRemovalFailureMessage(dir,
			fmt.Sprintf("after %d quits the remover left %d records of its runs", runs, got)))
	}
	return string(log), nil
}

// What no remover reached — a launcher SIGKILLed before its teardown, a host that went
// down mid-delete — is dangling, and the next launch's housekeeping slot removes it once
// it is past the age floor. A young dangling volume (a jail being created right now) and
// a volume that is not a scratch volume are left alone.
//
// The one launch here turns the automatic reapers back on (withAutoReapers), which the suite
// keeps off: this reaper is its subject. It runs on a private store (isolateReaperStore).
func TestTheSlotReapsLeftoverScratchVolumes(t *testing.T) {
	requireJail(t)
	requirePodman(t)
	rt := detectRuntime()
	mkvol := func(name string) {
		t.Helper()
		if out, err := exec.Command(rt, "volume", "create", name).CombinedOutput(); err != nil {
			t.Fatalf("volume create %s: %v\n%s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command(rt, "volume", "rm", name).Run() })
	}
	exists := func(name string) bool {
		return exec.Command(rt, "volume", "exists", name).Run() == nil
	}
	leftover := "yolo-scratchreap-1a2b3c4d.scratch.00000000deadbeef.var-lib-containers"
	decoy := "yolo-scratchreap-1a2b3c4d.scratch.notalaunchid.tmp"
	mkvol(leftover)
	mkvol(decoy)
	// Past prune.ScratchVolumeGrace (1 min).
	time.Sleep(65 * time.Second)
	young := "yolo-scratchreap-1a2b3c4d.scratch.00000000cafef00d.tmp"
	youngMade := time.Now()
	mkvol(young)

	// A PRIVATE STORE, with every debounced class stamped as just run (reaperstore_test.go): the
	// launch turns every automatic reaper on, and this class, which has no debounce, is the only
	// one that should run. It reaps the runtime's volumes, which no store isolates.
	isolateReaperStore(t)
	dir := writeProject(t, `{}`)
	// The slot dies at the launch's exit and the scratch class is its last, so the jail must
	// outlive every class ahead of it. A fixed sleep did not: on a CI runner the image class
	// had a stale image to reclaim and the jail quit before the slot reached the scratch class
	// (run 37819753351). So the jail waits for the slot's own note that it started the removal,
	// in the workspace's housekeeping log, bounded so a slot that never notes still ends.
	res := runYolo(t, dir, `for i in $(seq 1 90); do
  grep -q 'scratch: removing' /workspace/.yolo/logs/housekeeping.log 2>/dev/null && exit 0
  sleep 1
done`, withAutoReapers())
	if res.rc != 0 {
		t.Fatalf("rc=%d\n%s", res.rc, res.combined())
	}
	deadline := time.Now().Add(time.Minute)
	for exists(leftover) {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, ".yolo", "logs", "housekeeping.log"))
			t.Fatalf("the leftover %s was never reaped\nhousekeeping.log:\n%s", leftover, log)
		}
		time.Sleep(time.Second)
	}
	if !exists(decoy) {
		t.Errorf("a volume that is not a scratch volume was reaped: %s", decoy)
	}
	if !exists(young) {
		checkYoungReapWasPastTheFloor(t, filepath.Join(dir, ".yolo", "logs", "housekeeping.log"), young, youngMade)
	}
}

// checkYoungReapWasPastTheFloor decides whether the young volume's removal was a misjudged
// age or a slot that simply reached the scratch class after the volume HAD cleared the floor.
//
// The young volume is made just before the launch, and the slot reaches the scratch class
// only after the container is up and every class ahead of it has run. On Linux that is
// seconds. On the 2026-09-28 macOS nightly (run 36425623325) this launch took at least 110 s,
// and the test failed with "a dangling scratch volume younger than the floor was
// reaped" without saying when. The slot's note names what it removed, stamped by the host's
// clock to the second, so the removal is judged by that: a note less than a floor after the
// volume was made is a reap of a volume that was provably young, and anything later proves
// nothing about the floor.
func checkYoungReapWasPastTheFloor(t *testing.T, logPath, young string, made time.Time) {
	t.Helper()
	log, _ := os.ReadFile(logPath)
	var noted time.Time
	for _, line := range strings.Split(string(log), "\n") {
		if !strings.Contains(line, "scratch: removing") || !strings.Contains(line, young) {
			continue
		}
		stamp, _, _ := strings.Cut(line, "  ")
		if ts, err := time.Parse(time.RFC3339, stamp); err == nil {
			noted = ts
			break
		}
	}
	if noted.IsZero() {
		t.Errorf("the young volume %s is gone and no slot note names it: something other than the "+
			"reaper removed it\nhousekeeping.log:\n%s", young, log)
		return
	}
	// The stamp is truncated to the second, so the removal was decided before noted+1s.
	if upper := noted.Add(time.Second).Sub(made); upper < prune.ScratchVolumeGrace {
		t.Errorf("a dangling scratch volume younger than the floor was reaped: %s, removed at most "+
			"%s after it was made (floor %s)\nhousekeeping.log:\n%s", young, upper, prune.ScratchVolumeGrace, log)
		return
	}
	t.Logf("the slot reached the scratch class %s after the young volume was made, past the %s "+
		"floor, so removing it was right and this launch could not test the floor; "+
		"internal/prune's unit tests pin it", noted.Sub(made).Round(time.Second), prune.ScratchVolumeGrace)
}
