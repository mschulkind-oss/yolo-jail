package integration

// THE KEEPER ON APPLE CONTAINER, ASKED ON THE HARDWARE — the fourth batch of the exception
// applecontainer_test.go's header names, and step 4 of docs/design/jail-lifetime-last-session-wins.md
// §7 for this backend: the two measures that step owes (whether `container exec`'s process
// outlives its client, and whether that client has a detach sequence) and the keeper's lifecycle,
// run on the Mac the Apple Container job dispatches to.
//
// ⚠ NONE OF THESE HAS RUN ON APPLE CONTAINER. They landed unrun, which this suite's header allows
// only for an experiment, so every one is an experiment: both of its answers pass, and only a run
// that could not conduct it is red. What has run is the instrument, against podman in this
// repo's own Linux jail before it landed (see "What has run" below).
//
// # Why a Mac, and why these
//
// The keeper is built at the container backends (§7 step 3), and every end-to-end pin on it has
// run only on Linux podman (keeper_test.go, jailmain_test.go, sessionhangup_test.go,
// stopreason_test.go, detachkeys_linux_test.go). Apple Container differs from podman in exactly the places the design
// leaves unmeasured (§9.8's Apple Container row): its session clients are `container exec`, whose
// process may or may not outlive its client; it gets no `--detach-keys` because none is known to
// exist (JL-D27); its main process's client carries no `--sig-proxy=false`; its launcher's signal
// arm is proxy_other.go's plain spawn, which has never run; its reaper covers keeper-era jails by a
// branch of their own (JL-D7); and an attach reads why its jail ended only for the exec statuses
// podman gives (jailEndStatus). Each test below asks one of those on the hardware.
//
//	TestAppleContainerExecClientDeath                     measure: does an exec's process outlive its client, by signal and shape
//	TestAppleContainerDetachSequence                      measure: does ctrl-p, ctrl-q detach a session's client
//	TestAppleContainerKeeperFirstSessionQuitsAlone        §8 items 1, 2 and 4
//	TestAppleContainerKeeperHangupEndsOneSession          JL-D4 through proxy_other.go's arm, an attach's and the first session's
//	TestAppleContainerKeeperKilledLeavesSessionsRunning   §8 item 7, OQ-JL7, JL-D30 ("on Apple Container too")
//	TestAppleContainerKeeperMainClientDeath               §9.5 item 3, JL-D64: the keeper's `container run` client killed
//	TestAppleContainerKeeperSweepSparesAKeptJail          JL-D7's Apple Container branch
//	TestAppleContainerKeeperStopSaysWhy                   JL-D53: an attach whose jail ended says why
//
// # Both answers pass. Only a run that failed to conduct the experiment is red
//
// That is TestAppleContainerReachesHostLoopback's rule, kept the way applecontainerparity_test.go
// keeps it. A MEASURE logs its answer and passes on either. A LIFECYCLE run checks each claim the
// design makes for it and records the claims that missed; it ends in one line,
// `AC-KEEPER <run> VERDICT: HOLDS` or `… DOES NOT HOLD — <the claims that missed>`. Every line is
// prefixed acKeeperTag, so one grep of the `go test -v` log recovers the whole record, and each
// verdict and measure is also written to the job's step summary. What IS red: a launch, an attach
// or a probe that did not happen, since then nothing was measured. A session that does not end
// where the design says it ends is a claim that missed, not red: its launcher is SIGKILLed and the
// run goes on (keeperRecord.awaitEnd).
//
// # The promotion rule
//
// A recorded HOLDS is the evidence the suite's header asks for: the commit that cites it sets
// that run's record strict (keeperRecord.strict), which turns each missed claim into a t.Error,
// so a regression after that is red. A recorded DOES NOT HOLD is a defect in shipped code: file it
// against the step in §7 it contradicts, with the log line. A measure is never promoted; its answer
// goes into the design (JL-D27 for the detach sequence, §9.7's table for the client's death) and
// decides whether Apple Container needs a half of its own there.
//
// # What has run
//
// On Apple Container: nothing, as above. When these were written the Apple Container job had run
// on one commit that has the keeper, and that run (36752464537, 2026-09-30) was cancelled while
// staging the flake bundle, before its parity tests, so even the keeper's plainest launch there,
// TestAppleContainerJailStarts, is unmeasured.
//
// The instrument was run on 2026-10-01, before this landed, by pointing every body below at podman
// 5.8.7 in this repo's own Linux jail (nested, so rootful), from a test file that was not
// committed. Every lifecycle run recorded HOLDS, every one of its claims holding. The client-death
// measure recorded the process SURVIVING its client for all four signals in both shapes, the
// client itself killed by SIGKILL and SIGHUP and exiting 1 on SIGTERM and SIGINT, which is §2.3
// item 1's hand measurement again. The detach measure answered REACHED with the flag yolo passes podman, and
// DETACHED without it, its `cat -v` left running headless. The main process's client, killed, left
// the container running, and an attached exec returned 137 at both kinds of stop. So the holding
// branch of every lifecycle claim, and two of the detach measure's three answers, have been
// produced once by a real runtime; SWALLOWED, an exec's process ENDING with its client and a
// container ENDING with its main process's client have not. The bodies take the runtime as an
// argument for that run; nothing in the tree calls them with anything but "container". A review the
// same day ran them the same way again after adding awaitEnd and the sweep's provisioning claim:
// every lifecycle run HOLDS, and awaitEnd's miss was produced once, by a session never released.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// acKeeperTag prefixes every line these experiments log.
const acKeeperTag = "AC-KEEPER"

// acKeeperWorkspace is the shared fixture: the Apple Container gate, then an empty workspace
// config, as the Linux keeper tests use, so no launch installs an agent or starts a host service.
func acKeeperWorkspace(t *testing.T) (dir, cname string) {
	t.Helper()
	requireAppleContainer(t)
	requireJail(t)
	dir = writeProject(t, `{}`)
	return dir, naming.FromWorkspace(dir)
}

func TestAppleContainerExecClientDeath(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	measureExecClientDeath(t, "container", dir, cname)
}

func TestAppleContainerDetachSequence(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	measureDetachSequence(t, "container", dir, cname)
}

func TestAppleContainerKeeperFirstSessionQuitsAlone(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	keeperFirstSessionQuitsAlone(t, "container", dir, cname)
}

func TestAppleContainerKeeperHangupEndsOneSession(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	keeperHangupEndsOneSession(t, "container", dir, cname)
}

func TestAppleContainerKeeperKilledLeavesSessionsRunning(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	keeperKilledLeavesSessionsRunning(t, "container", dir, cname)
}

func TestAppleContainerKeeperMainClientDeath(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	keeperMainClientDeath(t, "container", dir, cname)
}

func TestAppleContainerKeeperSweepSparesAKeptJail(t *testing.T) {
	dir, cname := acKeeperWorkspace(t)
	other := writeProject(t, `{}`)
	keeperSweepSparesAKeptJail(t, "container", dir, cname, other)
}

func TestAppleContainerKeeperStopSaysWhy(t *testing.T) {
	requireAppleContainer(t)
	requireJail(t)
	keeperStopSaysWhy(t, "container")
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// The record
// ─────────────────────────────────────────────────────────────────────────────────────────

// keeperRecord is one lifecycle run's claims and which of them missed.
type keeperRecord struct {
	t    *testing.T
	name string
	// strict turns a missed claim into a t.Error: the promotion rule's switch (see the header),
	// false for every run until one has recorded HOLDS on the hardware.
	strict   bool
	claims   int
	misses   []string
	finished bool
}

// newKeeperRecord starts a run's record. A run that stops before its verdict, which only a fatal
// precondition does, says so in the step summary, so a red test is never mistaken for a measured
// DOES NOT HOLD.
func newKeeperRecord(t *testing.T, name string) *keeperRecord {
	t.Helper()
	r := &keeperRecord{t: t, name: name}
	t.Cleanup(func() {
		if !r.finished {
			stepSummary(t, fmt.Sprintf("%s %s VERDICT: NOT CONDUCTED — the run stopped after %d claims "+
				"(%d missed); the failure above says what did not happen", acKeeperTag, r.name, r.claims, len(r.misses)))
		}
	})
	return r
}

// expect records one claim the design makes, and the evidence for a reader when it missed.
func (r *keeperRecord) expect(holds bool, claim, evidence string) {
	r.t.Helper()
	r.claims++
	if holds {
		r.t.Logf("%s %s: holds — %s", acKeeperTag, r.name, claim)
		return
	}
	r.misses = append(r.misses, claim)
	msg := fmt.Sprintf("%s %s: DOES NOT HOLD — %s\nevidence:\n%s", acKeeperTag, r.name, claim, lastLines(evidence, 40))
	if r.strict {
		r.t.Error(msg)
		return
	}
	r.t.Log(msg)
}

// awaitEnd is bgRun.wait for a session the run expects to end, and one claim of the record: that it
// ended within jailTimeout(). A launcher still running then is an answer, not a run that could not
// be conducted, so it is recorded as a miss, SIGKILLed so the run can go on, and its status reads
// sessionNotEnded, which the run's later claims about it record as missed too. bgRun.wait would
// fail the run there instead, and the hardware's likeliest misses (an arm that never exits, an
// exec client that outlives its jail) would read as NOT CONDUCTED.
func (r *keeperRecord) awaitEnd(b *bgRun) int {
	r.t.Helper()
	bound := jailTimeout()
	select {
	case <-b.exited:
		r.expect(true, fmt.Sprintf("the %s session's launcher ends within %s", b.name, bound), "")
		return b.wait(r.t, time.Second)
	case <-time.After(bound):
	}
	r.expect(false, fmt.Sprintf("the %s session's launcher ends within %s (it was still running, and was SIGKILLed)",
		b.name, bound), b.combined())
	_ = syscall.Kill(b.pid, syscall.SIGKILL)
	select {
	case <-b.exited:
	case <-time.After(30 * time.Second):
	}
	return sessionNotEnded
}

// sessionNotEnded is awaitEnd's status for a launcher that did not end: never an exit status, and
// not bgRun.wait's -1, which is a launcher a signal killed.
const sessionNotEnded = -2

// endedWord is a status awaitEnd returned, for a measure's line.
func endedWord(rc int) string {
	switch rc {
	case sessionNotEnded:
		return "did not end within " + jailTimeout().String()
	case -1:
		return "was killed by a signal"
	}
	return "returned " + strconv.Itoa(rc)
}

// verdict ends the record with its one line.
func (r *keeperRecord) verdict() {
	r.t.Helper()
	r.finished = true
	if len(r.misses) == 0 {
		stepSummary(r.t, fmt.Sprintf("%s %s VERDICT: HOLDS — all %d claims held", acKeeperTag, r.name, r.claims))
		return
	}
	stepSummary(r.t, fmt.Sprintf("%s %s VERDICT: DOES NOT HOLD — %d of %d claims missed: %s",
		acKeeperTag, r.name, len(r.misses), r.claims, strings.Join(r.misses, "; ")))
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// Shared steps
// ─────────────────────────────────────────────────────────────────────────────────────────

// keeperSession starts a background session that prints <NAME>-IN-42 and runs body. Its runtime
// is rt, set per launch as appleContainerEnv does. The arithmetic keeps the marker out of any
// echo of the command itself.
func keeperSession(t *testing.T, rt, name, dir, body string) *bgRun {
	t.Helper()
	r := startYoloBackground(t, name, dir, `echo `+name+`-IN-$((40+2)); `+body, "YOLO_RUNTIME="+rt)
	awaitOutput(t, r, regexp.MustCompile(name+`-IN-42`))
	return r
}

// keeperHolding is a session that holds until release is written into the workspace, then prints
// <NAME>-OUT-42. It holds for at most five minutes, longer than the Linux tests' two, because an
// Apple Container launch boots a VM.
func keeperHolding(t *testing.T, rt, name, dir, release string) *bgRun {
	t.Helper()
	return keeperHoldingAfter(t, rt, name, dir, release, "")
}

// keeperHoldingAfter is keeperHolding with pre run first, just after <NAME>-IN-42.
func keeperHoldingAfter(t *testing.T, rt, name, dir, release, pre string) *bgRun {
	t.Helper()
	r := keeperSession(t, rt, name, dir, pre+`for _ in $(seq 1 1500); do [ -f /workspace/`+release+
		` ] && break; sleep 0.2; done; echo `+name+`-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	return r
}

// mustHaveAttached fails the run when r did not attach: every lifecycle claim below is about
// sessions sharing one jail, so a second launch that did not join it leaves nothing to measure.
func mustHaveAttached(t *testing.T, r *bgRun) {
	t.Helper()
	if !strings.Contains(r.combined(), "Attaching to existing jail") {
		t.Fatalf("%s %s did not attach to the running jail, so NOTHING WAS MEASURED:\n%s",
			acKeeperTag, r.name, lastLines(r.combined(), 60))
	}
}

// stillRunning reports that a background session has not ended.
func stillRunning(r *bgRun) bool {
	select {
	case <-r.exited:
		return false
	default:
		return true
	}
}

// keeperEnv is a runYolo option naming the runtime.
func keeperEnv(rt string) runOption { return withEnv("YOLO_RUNTIME=" + rt) }

// jailCount counts the jail's processes whose command line begins with prefix, read from /proc
// in the jail by an exec of its own, so the count is no session. prefix is spelled by this file
// and holds only letters, digits, spaces, dashes and slashes, so it is safe inside the double
// quotes. The probe's own command line begins with `/bin/sh -c` and never matches.
func jailCount(rt, cname, prefix string) (int, string, error) {
	script := `n=0; for p in /proc/[0-9]*; do c=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); ` +
		`case "$c" in "` + prefix + `"*) n=$((n+1));; esac; done; echo "COUNT=$n"`
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := jailExec(ctx, rt, cname, script).CombinedOutput()
	m := regexp.MustCompile(`COUNT=(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		return 0, string(out), fmt.Errorf("the count probe printed no COUNT (%v)", err)
	}
	n, _ := strconv.Atoi(m[1])
	return n, string(out), nil
}

// mustJailCount is jailCount for a step that cannot go on without the answer.
func mustJailCount(t *testing.T, rt, cname, prefix string) int {
	t.Helper()
	n, out, err := jailCount(rt, cname, prefix)
	if err != nil {
		t.Fatalf("%s: counting %q in %s failed, so nothing could be measured: %v\n%s",
			acKeeperTag, prefix, cname, err, out)
	}
	return n
}

// awaitJailCount polls jailCount until it reads want or bound passes, and returns the last count.
func awaitJailCount(t *testing.T, rt, cname, prefix string, want int, bound time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		n := mustJailCount(t, rt, cname, prefix)
		if n == want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// jailKill ends the jail's processes whose command line begins with prefix, best effort, so a
// survivor a measure found does not run on until the jail is removed.
func jailKill(rt, cname, prefix string) {
	script := `for p in /proc/[0-9]*; do c=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); ` +
		`case "$c" in "` + prefix + `"*) kill -9 "${p#/proc/}" 2>/dev/null;; esac; done; true`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = jailExec(ctx, rt, cname, script).Run()
}

// jailExec is a probe's `<rt> exec <cname> /bin/sh -c <script>`, ended with ctx. Its WaitDelay
// bounds the wait for its output once it has exited or been killed, so a descendant the runtime
// left holding the pipe cannot hold the test.
func jailExec(ctx context.Context, rt, cname, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, rt, "exec", cname, "/bin/sh", "-c", script)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// exitWord says how a client process ended, from its Wait error.
func exitWord(err error) string {
	if err == nil {
		return "exited 0"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "killed by " + signalName(ws.Signal())
		}
		return "exited " + strconv.Itoa(exitErr.ExitCode())
	}
	return err.Error()
}

// signalName is a signal's SIG… spelling, for the record.
func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	}
	return s.String()
}

// runtimeVersion is the runtime's own version line, for the record.
func runtimeVersion(rt string) string {
	out, err := exec.Command(rt, "--version").CombinedOutput()
	if err != nil {
		return rt + " (version unreadable: " + err.Error() + ")"
	}
	return strings.TrimSpace(string(out))
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// Measure 1: does an exec's process outlive its client?
// ─────────────────────────────────────────────────────────────────────────────────────────

// measureExecClientDeath is §7 step 4's first measure: whether `<rt> exec`'s process survives its
// client's death. On podman it does, for SIGKILL, SIGHUP, SIGTERM and SIGINT to an `exec -it`
// client (§2.3 item 1, MEASURED), which is why a session's signal arm hangs its processes up in the
// jail before it kills its client (JL-D4). On Apple Container nobody has looked.
//
// Each of the four signals is sent to a client of each of the two shapes a session's client takes:
// `exec -i -t` at a terminal, which is a person's session, and `exec -i` with an open pipe, which
// is a scripted one (an attach's exec and the first session's both take `-t` only when the
// launcher's stdout is a terminal). The client runs `/bin/sleep N` with an N of its own, so the
// jail's /proc says which survived. A survivor is killed before the next case.
//
// IT DOES NOT GO THROUGH yolo's attach: the question is the runtime's, so the client is the bare
// runtime CLI, in a jail a yolo launch started and holds open. The record is one row per case.
func measureExecClientDeath(t *testing.T, rt, dir, cname string) {
	holder := keeperHolding(t, rt, "HOLDER", dir, "release-holder")
	awaitLaunchLockReleased(t, dir, holder)

	signals := []syscall.Signal{syscall.SIGKILL, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT}
	var rows []string
	n := 4400
	for _, tty := range []bool{true, false} {
		for _, sig := range signals {
			n++
			rows = append(rows, execClientDeathCase(t, rt, cname, tty, sig, n))
		}
	}
	head := fmt.Sprintf("%s MEASURE exec-client-death (%s): whether `%s exec`'s process outlives "+
		"its client, by the signal the client got and its shape", acKeeperTag, runtimeVersion(rt), rt)
	stepSummary(t, append([]string{head}, rows...)...)
	releaseHolder(t, dir, holder)
}

// releaseHolder ends a measure's holding session. How it ends is logged and not judged, a holder
// that does not end included: it is not part of what the measure asks, which is recorded by then.
func releaseHolder(t *testing.T, dir string, holder *bgRun) {
	t.Helper()
	writeRelease(t, dir, "release-holder")
	select {
	case <-holder.exited:
		if rc := holder.wait(t, time.Second); rc != 0 {
			t.Logf("%s the holding session ended rc %d (not part of the measure):\n%s",
				acKeeperTag, rc, lastLines(holder.combined(), 30))
		}
	case <-time.After(jailTimeout()):
		t.Logf("%s the holding session was still running %s after its release (not part of the measure); "+
			"SIGKILLing its launcher:\n%s", acKeeperTag, jailTimeout(), lastLines(holder.combined(), 30))
		_ = syscall.Kill(holder.pid, syscall.SIGKILL)
	}
}

// execClientDeathCase runs one client, signals it, and says what became of it and of its process.
func execClientDeathCase(t *testing.T, rt, cname string, tty bool, sig syscall.Signal, n int) string {
	t.Helper()
	// Absolute, as yolo's own execs name the entrypoint, so nothing rests on the exec's PATH; the
	// jail's /proc then shows exactly this prefix.
	marker := "/bin/sleep " + strconv.Itoa(n)
	shape := "exec -i (pipe)"
	argv := []string{rt, "exec", "-i"}
	if tty {
		shape = "exec -i -t (pty)"
		argv = append(argv, "-t")
	}
	argv = append(argv, cname, "/bin/sleep", strconv.Itoa(n))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	// EVERY STREAM IS A FILE, never a buffer exec.Cmd copies into: Wait then returns when the
	// client exits, whoever else holds its descriptors (podman's conmon outlives its client by
	// design), so the bounded wait below cannot hang on a pipe instead.
	out := &syncBuffer{}
	clientOutput := out.String
	var keep []*os.File // the parent's ends, closed when the case is over
	if tty {
		master, slave := openTestPty(t)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		go func() {
			buf := make([]byte, 1024)
			for {
				k, err := master.Read(buf)
				if k > 0 {
					_, _ = out.Write(buf[:k])
				}
				if err != nil {
					return
				}
			}
		}()
		keep = append(keep, slave)
	} else {
		// An open pipe for stdin, which this test never writes or closes before the signal: a
		// scripted session's stdin stays open while its command runs.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.CreateTemp(t.TempDir(), "exec-client-*.out")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = r, f, f
		keep = append(keep, r, w, f)
		clientOutput = func() string {
			b, _ := os.ReadFile(f.Name())
			return string(b)
		}
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: starting `%s`: %v", acKeeperTag, strings.Join(argv, " "), err)
	}
	defer func() {
		for _, f := range keep {
			_ = f.Close()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		jailKill(rt, cname, marker)
	})

	// The process must be in the jail before its client is signalled, or nothing is measured.
	deadline := time.Now().Add(jailTimeout())
	for mustJailCount(t, rt, cname, marker) != 1 {
		select {
		case err := <-done:
			t.Fatalf("%s: the `%s` client ended (%s) before its process appeared in the jail, so "+
				"nothing was measured:\n%s", acKeeperTag, shape, exitWord(err), clientOutput())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: `%s` never showed in the jail within %s, so nothing was measured:\n%s",
				acKeeperTag, marker, jailTimeout(), clientOutput())
		}
		time.Sleep(250 * time.Millisecond)
	}

	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatalf("%s: signalling the client: %v", acKeeperTag, err)
	}
	client := ""
	select {
	case err := <-done:
		client = exitWord(err)
	case <-time.After(10 * time.Second):
		client = "did NOT exit within 10s (then SIGKILLed)"
		_ = cmd.Process.Kill()
		<-done
	}
	// Five seconds for a runtime that ends the process when its client's connection drops: every
	// survivor costs the whole wait, and there are eight cases.
	left := awaitJailCount(t, rt, cname, marker, 0, 5*time.Second)
	process := "ENDED with its client"
	if left != 0 {
		process = "SURVIVED its client"
		jailKill(rt, cname, marker)
	}
	return fmt.Sprintf("%s MEASURE exec-client-death: %-17s %-7s → client %s; its process %s",
		acKeeperTag, shape, signalName(sig), client, process)
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// Measure 2: does ctrl-p, ctrl-q detach a session's client?
// ─────────────────────────────────────────────────────────────────────────────────────────

// measureDetachSequence is §7 step 4's second measure: whether Apple Container's session client has
// a detach sequence. podman's `exec -it` client detaches on `ctrl-p` then `ctrl-q`, leaving its
// process running with no terminal, and yolo turns that off on podman with `--detach-keys=`
// (JL-D27, JL-D54). Apple Container gets nothing until a probe or its help shows a flag or a
// sequence there, and this is the probe.
//
// The client is the bare runtime CLI, `<rt> exec -i -t <cname> /bin/sh -c '… exec cat -v'`,
// carrying exactly the detach flags yolo's own exec carries on rt (runtime.DetachKeysArgs: none on
// Apple Container), at a real pty. The keys are typed one per write with a pause, as a person types
// them, because podman takes the sequence only when each key arrives in a read of its own. Then
// `hello`. Three answers, each a pass:
//
//   - REACHED: `^Phello` came back and the client is still attached. podman's sequence does not
//     detach this client, and JL-D27 needs no Apple Container half unless the help text below names
//     a sequence of the runtime's own.
//   - DETACHED: the client returned before that. Whether its `cat` runs on headless is read from the
//     jail and recorded; either way JL-D27 owes this backend a half, and the help text below says
//     whether a flag exists to give it.
//   - SWALLOWED: `hello` came back without the `^P`: the client held the first key and did not
//     detach, which is a third behavior to record rather than fold into either.
//
// Red only when the session never started or none of the three happened within the bound.
func measureDetachSequence(t *testing.T, rt, dir, cname string) {
	holder := keeperHolding(t, rt, "HOLDER", dir, "release-holder")
	awaitLaunchLockReleased(t, dir, holder)

	help, _ := exec.Command(rt, "exec", "--help").CombinedOutput()
	var mentions []string
	for _, line := range strings.Split(string(help), "\n") {
		if strings.Contains(strings.ToLower(line), "detach") {
			mentions = append(mentions, strings.TrimSpace(line))
		}
	}
	helpSays := "`" + rt + " exec --help` mentions no detach"
	if len(mentions) > 0 {
		helpSays = "`" + rt + " exec --help` says: " + strings.Join(mentions, " | ")
	}

	answer, evidence := detachSequenceOutcome(t, rt, cname, naming.DetachKeysArgs(rt))
	stepSummary(t, fmt.Sprintf("%s MEASURE detach-sequence (%s): %s; %s",
		acKeeperTag, runtimeVersion(rt), answer, helpSays))
	t.Logf("%s detach-sequence evidence:\n%s", acKeeperTag, lastLines(evidence, 40))

	releaseHolder(t, dir, holder)
}

// detachSequenceOutcome types the sequence at one `<rt> exec -i -t <flags> <cname>` client running
// `cat -v`, and returns which of measureDetachSequence's three answers it gave, with the terminal's
// output as evidence.
func detachSequenceOutcome(t *testing.T, rt, cname string, flags []string) (answer, evidence string) {
	t.Helper()
	const marker = "cat -v"
	master, slave := openTestPty(t)
	argv := append(append([]string{rt, "exec", "-i", "-t"}, flags...), cname,
		"/bin/sh", "-c", `echo DK-READY-$((40+2)); exec cat -v`)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: starting `%s`: %v", acKeeperTag, strings.Join(argv, " "), err)
	}
	_ = slave.Close()
	out := &syncBuffer{}
	go func() {
		buf := make([]byte, 4096)
		for {
			k, err := master.Read(buf)
			if k > 0 {
				_, _ = out.Write(buf[:k])
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		jailKill(rt, cname, marker)
	})

	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(out.String(), "DK-READY-42") {
		select {
		case err := <-done:
			t.Fatalf("%s: the exec client ended (%s) before its command ran, so nothing was measured:\n%s",
				acKeeperTag, exitWord(err), out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the exec's command did not start within %s, so nothing was measured:\n%s",
				acKeeperTag, jailTimeout(), out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, key := range []byte{0x10, 0x11} {
		time.Sleep(300 * time.Millisecond)
		if _, err := master.Write([]byte{key}); err != nil {
			t.Fatalf("%s: typing %#x: %v", acKeeperTag, key, err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := master.Write([]byte("hello\r")); err != nil {
		t.Fatalf("%s: typing hello: %v", acKeeperTag, err)
	}

	// `^P` then `hello` is the line the jail's terminal echoes and `cat -v` prints back. A terminal
	// that does not take ctrl-q as flow control shows it as `^Q` between them, which is still
	// the keys arriving.
	reached := regexp.MustCompile(`\^P(\^Q)?hello`)
	typed := time.Now()
	for {
		select {
		case err := <-done:
			left := mustJailCount(t, rt, cname, marker)
			fate := "its `cat -v` ended with it"
			if left > 0 {
				fate = "its `cat -v` runs on HEADLESS in the jail"
			}
			return fmt.Sprintf("DETACHED — the client returned (%s) after the sequence; %s", exitWord(err), fate), out.String()
		default:
		}
		got := out.String()
		if reached.MatchString(got) {
			return "REACHED — the keys reached the process (`^Phello` came back) and the client stayed attached, " +
				endWithEOF(t, master, done, rt, cname, marker), got
		}
		// Given five seconds to settle, so a `^P` still on its way is not read as missing.
		if strings.Contains(got, "hello") && !strings.Contains(got, "^P") && time.Since(typed) > 5*time.Second {
			return "SWALLOWED — `hello` came back without the `^P`, and the client did not detach, " +
				endWithEOF(t, master, done, rt, cname, marker), got
		}
		if time.Since(typed) > 30*time.Second {
			t.Fatalf("%s: none of the three answers within 30s, so nothing was measured: no `^Phello`, "+
				"no detach, no bare `hello`:\n%s", acKeeperTag, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// endWithEOF ends a still-attached `cat -v` session with ctrl-d and words what followed.
func endWithEOF(t *testing.T, master *os.File, done <-chan error, rt, cname, marker string) string {
	t.Helper()
	if _, err := master.Write([]byte{0x04}); err != nil {
		return "and ctrl-d could not be typed: " + err.Error()
	}
	select {
	case err := <-done:
		left := awaitJailCount(t, rt, cname, marker, 0, 10*time.Second)
		return fmt.Sprintf("and ctrl-d ended it (%s, %d `cat -v` left in the jail)", exitWord(err), left)
	case <-time.After(jailTimeout()):
		return "and ctrl-d did NOT end it within " + jailTimeout().String()
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// The lifecycle
// ─────────────────────────────────────────────────────────────────────────────────────────

// keeperFirstSessionQuitsAlone is TestQuittingTheFirstSessionLeavesTheOthersRunning's claims, §8
// items 1, 2 and 4: the fresh launch discloses its keeper; the first session's quit returns its
// prompt at once with the one line while the jail and the second session run on; a third entry
// attaches beside the second; and the last quit streams the keeper's teardown and leaves no
// container and no keeper.
func keeperFirstSessionQuitsAlone(t *testing.T, rt, dir, cname string) {
	rec := newKeeperRecord(t, "first-session-quits-alone (§8 items 1, 2, 4)")
	first := keeperHolding(t, rt, "FIRST", dir, "release-first")
	awaitLaunchLockReleased(t, dir, first)
	rec.expect(strings.Contains(first.combined(), "keeper: yolo internal daemon jail-keeper will hold") &&
		regexp.MustCompile(`keeper: started, pid \d+`).MatchString(first.combined()),
		"the fresh launch discloses its keeper and names its pid (JL-D21)", first.combined())
	keeper, why := findKeeperPID(dir, 30*time.Second)
	rec.expect(keeper != 0, "the owner-PID file names a live keeper", why)

	second := keeperHolding(t, rt, "SECOND", dir, "release-second")
	mustHaveAttached(t, second)

	writeRelease(t, dir, "release-first")
	quit := time.Now()
	rc := rec.awaitEnd(first)
	took := time.Since(quit)
	rec.expect(rc == 0, fmt.Sprintf("the first session's launcher returns 0 (it returned %d)", rc), first.combined())
	rec.expect(took <= 15*time.Second, fmt.Sprintf("the first launcher gives its prompt back within 15s (it took %s)",
		took.Round(100*time.Millisecond)), first.combined())
	rec.expect(strings.Contains(first.combined(), "Jail "+cname+" stays up for"),
		"the first session's quit says the jail stays up", first.combined())
	rec.expect(runningContainersOn(t, rt, cname) == 1, "the jail runs on after its first session quit", first.combined())
	rec.expect(stillRunning(second), "the second session runs on when the first quits", second.combined())
	if keeper != 0 {
		rec.expect(!processGone(keeper), "the keeper is alive while a session runs", "")
	}

	third := runYolo(t, dir, `echo THIRD-$((40+2))`, keeperEnv(rt))
	rec.expect(third.rc == 0 && strings.Contains(third.stdout, "THIRD-42") &&
		strings.Contains(third.combined(), "Attaching to existing jail"),
		fmt.Sprintf("a third entry attaches beside the second and runs (rc %d)", third.rc), third.combined())

	writeRelease(t, dir, "release-second")
	rc = rec.awaitEnd(second)
	rec.expect(rc == 0 && strings.Contains(second.combined(), "SECOND-OUT-42"),
		fmt.Sprintf("the last session finishes its command and returns 0 (rc %d)", rc), second.combined())
	rec.expect(strings.Contains(second.combined(), "keeper: the last session of "+cname+" left; ending the jail"),
		"the last quit streams the keeper's teardown (JL-D11)", second.combined())
	rec.expect(runningContainersOn(t, rt, cname) == 0, "no container of the name is left after the last quit", "")
	if keeper != 0 {
		rec.expect(awaitProcessGone(keeper, 10*time.Second), "the keeper is gone after the last quit", "")
	}
	rec.verdict()
}

// keeperHangupEndsOneSession is TestAHungUpAttachEndsItsOwnSessionAndNoOther and
// TestAHungUpFirstSessionEndsOnlyItself in one jail (JL-D4, OQ-JL8): a SIGHUP to a session's
// launcher, which is what a closed window or pane sends it, ends that session's own processes in
// the jail and nothing else, and the launcher exits 128+SIGHUP. On a Mac the launcher's arm is
// proxy_other.go's plain spawn, which has never run. An attach is hung up first, then the session
// that started the jail; a third session holds the jail throughout and is the last to quit.
func keeperHangupEndsOneSession(t *testing.T, rt, dir, cname string) {
	rec := newKeeperRecord(t, "hangup-ends-one-session (JL-D4)")
	const firstSleep, attachSleep = "sleep 4321", "sleep 4322"
	first := keeperSession(t, rt, "FIRST", dir, `exec `+firstSleep)
	awaitLaunchLockReleased(t, dir, first)
	attach := keeperSession(t, rt, "ATTACH", dir, `exec `+attachSleep)
	mustHaveAttached(t, attach)
	holder := keeperHolding(t, rt, "HOLDER", dir, "release-holder")
	mustHaveAttached(t, holder)
	for _, s := range []string{firstSleep, attachSleep} {
		if n := awaitJailCount(t, rt, cname, s, 1, 30*time.Second); n != 1 {
			t.Fatalf("%s: `%s` is not running once in the jail (%d), so no hangup can be measured", acKeeperTag, s, n)
		}
	}

	hangUp := func(who *bgRun, mine, other string) {
		t.Helper()
		if err := syscall.Kill(who.pid, syscall.SIGHUP); err != nil {
			t.Fatalf("%s: hanging up %s's launcher: %v", acKeeperTag, who.name, err)
		}
		rc := rec.awaitEnd(who)
		rec.expect(rc == 128+int(syscall.SIGHUP), fmt.Sprintf("the hung-up %s launcher exits %d (it exited %d)",
			who.name, 128+int(syscall.SIGHUP), rc), who.combined())
		rec.expect(awaitJailCount(t, rt, cname, mine, 0, 10*time.Second) == 0,
			fmt.Sprintf("the hung-up %s session's `%s` is gone from the jail", who.name, mine), who.combined())
		if other != "" {
			rec.expect(mustJailCount(t, rt, cname, other) == 1,
				fmt.Sprintf("hanging up %s leaves the other session's `%s` running", who.name, other), "")
		}
		rec.expect(runningContainersOn(t, rt, cname) == 1, fmt.Sprintf("the jail runs on after %s was hung up", who.name), "")
		rec.expect(stillRunning(holder), fmt.Sprintf("the holding session runs on after %s was hung up", who.name),
			holder.combined())
	}
	hangUp(attach, attachSleep, firstSleep)
	hangUp(first, firstSleep, "")

	writeRelease(t, dir, "release-holder")
	rc := rec.awaitEnd(holder)
	rec.expect(rc == 0, fmt.Sprintf("the last session returns 0 (rc %d)", rc), holder.combined())
	rec.expect(runningContainersOn(t, rt, cname) == 0, "no container of the name is left after the last quit", "")
	rec.verdict()
}

// keeperKilledLeavesSessionsRunning is TestAKilledKeeperLeavesItsSessionsAndRefusesArrivals'
// claims, §8 item 7 as OQ-JL7 ruled, which names this backend: `kill -9` of the keeper leaves its
// session running; the next arrival is refused, naming `yolo stop`; and the session, quitting last,
// reaps the jail itself (JL-D30), after which the workspace launches fresh. Off Linux a SIGKILLed
// keeper's children get no death signal (JL-D60); with an empty config it has none to leave.
func keeperKilledLeavesSessionsRunning(t *testing.T, rt, dir, cname string) {
	rec := newKeeperRecord(t, "killed-keeper (§8 item 7, OQ-JL7)")
	first := keeperHolding(t, rt, "FIRST", dir, "release-first")
	awaitLaunchLockReleased(t, dir, first)
	keeper, why := findKeeperPID(dir, 30*time.Second)
	if keeper == 0 {
		t.Fatalf("%s: there is no keeper to kill, so nothing was measured: %s", acKeeperTag, why)
	}

	if err := syscall.Kill(keeper, syscall.SIGKILL); err != nil {
		t.Fatalf("%s: killing the keeper: %v", acKeeperTag, err)
	}
	if !awaitProcessGone(keeper, 10*time.Second) {
		t.Fatalf("%s: the SIGKILLed keeper (pid %d) is still there", acKeeperTag, keeper)
	}
	rec.expect(runningContainersOn(t, rt, cname) == 1, "the jail runs on when its keeper is killed", "")

	arrival := runYolo(t, dir, "echo ENTERED-$((40+2))", keeperEnv(rt))
	rec.expect(arrival.rc != 0 && !strings.Contains(arrival.stdout, "ENTERED-42"),
		fmt.Sprintf("an arrival at the unkept jail is refused (rc %d)", arrival.rc), arrival.combined())
	rec.expect(strings.Contains(arrival.stderr, "Refusing to enter "+cname) && strings.Contains(arrival.stderr, "'yolo stop'"),
		"the refusal names the jail and `yolo stop` (JL-D13)", arrival.combined())
	rec.expect(stillRunning(first), "the session runs on when its keeper is killed", first.combined())

	writeRelease(t, dir, "release-first")
	rc := rec.awaitEnd(first)
	rec.expect(rc == 0 && strings.Contains(first.combined(), "FIRST-OUT-42"),
		fmt.Sprintf("the session finishes its command and returns 0 (rc %d)", rc), first.combined())
	rec.expect(strings.Contains(first.combined(), "This jail's keeper is gone, and this was its last session"),
		"the last session says it reaps the unkept jail (JL-D30)", first.combined())
	rec.expect(runningContainersOn(t, rt, cname) == 0, "no container of the name is left after the unkept jail's last quit", "")
	again := runYolo(t, dir, "echo AGAIN-$((40+2))", keeperEnv(rt))
	rec.expect(again.rc == 0 && strings.Contains(again.stdout, "AGAIN-42"),
		fmt.Sprintf("the workspace launches fresh after the reap (rc %d)", again.rc), again.combined())
	rec.verdict()
}

// keeperMainClientDeath is TestAKilledMainProcessClientLeavesTheJailKept asked as a measure first,
// since the answer is Apple Container's: the keeper's `<rt> run` child, the attached client of the
// jail's main process, is SIGKILLed. On podman the container runs on without it (§9.5 item 3,
// JL-D64), and the keeper keeps the jail. Apple Container's client is a different program over a
// VM, so it is recorded which of two coherent outcomes this backend gives, and each is then checked
// against what the design says follows from it:
//
//   - the container RUNS ON: the keeper keeps it and its services, an arrival attaches, and the
//     last quit ends the jail through the keeper as always (JL-D64);
//   - the container ENDS with its client: the keeper confirms it gone and runs its teardown
//     (observation 3 of §9.5), so the keeper exits, no container is left, and the session returns.
func keeperMainClientDeath(t *testing.T, rt, dir, cname string) {
	rec := newKeeperRecord(t, "main-process-client-killed (§9.5 item 3, JL-D64)")
	first := keeperHolding(t, rt, "FIRST", dir, "release-first")
	awaitLaunchLockReleased(t, dir, first)
	keeper, why := findKeeperPID(dir, 30*time.Second)
	if keeper == 0 {
		t.Fatalf("%s: there is no keeper, so its client cannot be found and nothing was measured: %s", acKeeperTag, why)
	}
	client := findMainProcessClient(keeper)
	if client == 0 {
		t.Fatalf("%s: the keeper (pid %d) has no `%s run` child, so nothing was measured", acKeeperTag, keeper, rt)
	}
	if err := syscall.Kill(client, syscall.SIGKILL); err != nil {
		t.Fatalf("%s: killing the main process's client: %v", acKeeperTag, err)
	}
	if !awaitProcessGone(client, 10*time.Second) {
		t.Fatalf("%s: the killed client (pid %d) is still there", acKeeperTag, client)
	}
	time.Sleep(3 * time.Second)

	if runningContainersOn(t, rt, cname) == 1 {
		stepSummary(t, fmt.Sprintf("%s MEASURE main-client-death (%s): the container RUNS ON when the keeper's `%s run` client is SIGKILLed",
			acKeeperTag, runtimeVersion(rt), rt))
		rec.expect(!processGone(keeper), "the keeper keeps a running jail whose client died", "")
		arrival := runYolo(t, dir, "echo ENTERED-$((40+2))", keeperEnv(rt))
		rec.expect(arrival.rc == 0 && strings.Contains(arrival.stdout, "ENTERED-42") &&
			strings.Contains(arrival.combined(), "Attaching to existing jail"),
			fmt.Sprintf("an arrival attaches to the kept jail (rc %d)", arrival.rc), arrival.combined())
		writeRelease(t, dir, "release-first")
		rc := rec.awaitEnd(first)
		rec.expect(rc == 0, fmt.Sprintf("the session returns 0 (rc %d)", rc), first.combined())
		rec.expect(strings.Contains(first.combined(), "keeper: the last session of "+cname+" left; ending the jail"),
			"the last quit streams the keeper's teardown", first.combined())
	} else {
		stepSummary(t, fmt.Sprintf("%s MEASURE main-client-death (%s): the container ENDS with the keeper's `%s run` client",
			acKeeperTag, runtimeVersion(rt), rt))
		rc := rec.awaitEnd(first)
		rec.expect(rc != 0 && rc != sessionNotEnded,
			fmt.Sprintf("the session whose jail ended under it returns non-zero (rc %d)", rc), first.combined())
	}
	rec.expect(runningContainersOn(t, rt, cname) == 0, "no container of the name is left", "")
	rec.expect(awaitProcessGone(keeper, 30*time.Second), "the keeper is gone once its jail is", "")
	rec.verdict()
}

// keeperSweepSparesAKeptJail is TestAnOrphanSweepSparesAJailWithASessionInIt's claims: the first
// launcher is SIGKILLed while a second terminal is attached; the next launch in ANOTHER workspace,
// sweeping orphans, leaves the jail, whose keeper lives; an entry attaches without being told its
// owner is gone; and once the attached session leaves, the keeper ends the jail. On this backend the
// reaper's keeper-era branch is its own (JL-D7), and has never run.
func keeperSweepSparesAKeptJail(t *testing.T, rt, dir, cname, other string) {
	rec := newKeeperRecord(t, "sweep-spares-a-kept-jail (JL-D7)")
	first := keeperHolding(t, rt, "FIRST", dir, "release-first")
	awaitLaunchLockReleased(t, dir, first)
	keeper, why := findKeeperPID(dir, 30*time.Second)
	if keeper == 0 {
		t.Fatalf("%s: there is no keeper, so nothing was measured: %s", acKeeperTag, why)
	}
	// The attach reads the first session's recorded provisioning outcome as it enters, as its Linux
	// twin's does (JL-D33).
	attach := keeperHoldingAfter(t, rt, "ATTACH", dir, "release-attach",
		`echo "ATTACH-SAW-$((40+2))=$(cat /run/yolo/main/provision.outcome)"; `)
	mustHaveAttached(t, attach)
	awaitOutput(t, attach, regexp.MustCompile(`ATTACH-SAW-42=`))
	rec.expect(strings.Contains(attach.combined(), "ATTACH-SAW-42=done"),
		"the attach entered once provisioning's outcome was recorded (JL-D33)", attach.combined())

	if err := syscall.Kill(first.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("%s: killing the first launcher: %v", acKeeperTag, err)
	}
	if !awaitProcessGone(first.pid, 30*time.Second) {
		t.Fatalf("%s: the SIGKILLed first launcher (pid %d) was never reaped", acKeeperTag, first.pid)
	}

	sweep := runYolo(t, other, "true", keeperEnv(rt))
	if sweep.rc != 0 {
		t.Fatalf("%s: the sweeping launch in another workspace failed (rc %d), so nothing was measured:\n%s",
			acKeeperTag, sweep.rc, lastLines(sweep.combined(), 60))
	}
	rec.expect(!strings.Contains(sweep.combined(), "Reaping orphaned jail "+cname),
		"a sweep from another workspace leaves a jail whose keeper is alive", sweep.combined())
	rec.expect(runningContainersOn(t, rt, cname) == 1, "the jail with a session in it runs on after the sweep", "")

	entry := runYolo(t, dir, "echo ENTERED-$((40+2))", keeperEnv(rt))
	rec.expect(entry.rc == 0 && strings.Contains(entry.stdout, "ENTERED-42"),
		fmt.Sprintf("an entry into the kept jail runs (rc %d)", entry.rc), entry.combined())
	rec.expect(!strings.Contains(entry.stderr, "is gone"), "the entry is not told a live keeper's jail lost its owner", entry.combined())

	writeRelease(t, dir, "release-attach")
	rc := rec.awaitEnd(attach)
	rec.expect(rc == 0 && strings.Contains(attach.combined(), "ATTACH-OUT-42"),
		fmt.Sprintf("the attached session finishes and returns 0 (rc %d)", rc), attach.combined())
	rec.expect(runningContainersOn(t, rt, cname) == 0, "the last session's leaving ends the jail", "")
	rec.expect(awaitProcessGone(keeper, 10*time.Second), "the keeper is gone with its jail", "")
	rec.verdict()
}

// keeperStopSaysWhy is TestAnAttachWhoseJailEndedSaysWhy's claims (JL-D53): a session attached
// when its jail ends prints why, first after `yolo stop`, then after a stop from outside yolo,
// which records nothing. The attach reads why only for an exec status in jailEndStatus (137, 125,
// 255, the statuses podman gives); whether `container exec` gives one of them is unmeasured, so the
// status each stop gave is part of the record.
func keeperStopSaysWhy(t *testing.T, rt string) {
	const release = "release-first"
	start := func(t *testing.T, dir string) (first, attach *bgRun) {
		t.Helper()
		first = keeperHolding(t, rt, "FIRST", dir, release)
		awaitLaunchLockReleased(t, dir, first)
		attach = keeperSession(t, rt, "ATTACH", dir, `exec sleep 600`)
		mustHaveAttached(t, attach)
		return first, attach
	}
	jailEnded := func(rc int) bool { return rc == 137 || rc == 125 || rc == 255 }

	t.Run("yolo stop", func(t *testing.T) {
		rec := newKeeperRecord(t, "stop-says-why/yolo-stop (JL-D53)")
		dir := writeProject(t, `{}`)
		first, attach := start(t, dir)
		stop := runCommand(t, dir, []string{"stop"}, keeperEnv(rt))
		t.Logf("%s `yolo stop` returned %d:\n%s", acKeeperTag, stop.rc, lastLines(stop.combined(), 30))
		rc := rec.awaitEnd(attach)
		stepSummary(t, fmt.Sprintf("%s MEASURE stop-status (%s): an attached `%s exec` %s when `yolo stop` ended its jail",
			acKeeperTag, runtimeVersion(rt), rt, endedWord(rc)))
		rec.expect(jailEnded(rc), fmt.Sprintf("the attached session's status is one jailEndStatus reads (137, 125 or 255; it was %d)", rc),
			attach.combined())
		rec.expect(strings.Contains(attach.combined(), "This session ended because its jail stopped: `yolo stop` (pid "),
			"the attached session is told that `yolo stop` ended its jail", attach.combined())
		frc := rec.awaitEnd(first)
		rec.expect(frc == 143, fmt.Sprintf("the first session ends 143, a stop being recorded (JL-D59; it ended %d)", frc), first.combined())
		rec.expect(runningContainersOn(t, rt, naming.FromWorkspace(dir)) == 0, "no container of the name is left after `yolo stop`", "")
		rec.verdict()
	})

	t.Run("a stop from outside yolo", func(t *testing.T) {
		rec := newKeeperRecord(t, "stop-says-why/outside-stop (JL-D53)")
		dir := writeProject(t, `{}`)
		cname := naming.FromWorkspace(dir)
		first, attach := start(t, dir)
		argv := []string{rt, "stop", cname}
		if rt == "podman" {
			argv = []string{rt, "stop", "-t", "5", cname}
		}
		ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
		defer cancel()
		if out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Logf("%s `%s`: %v\n%s", acKeeperTag, strings.Join(argv, " "), err, out)
		}
		rc := rec.awaitEnd(attach)
		stepSummary(t, fmt.Sprintf("%s MEASURE stop-status (%s): an attached `%s exec` %s when `%s stop` ended its jail",
			acKeeperTag, runtimeVersion(rt), rt, endedWord(rc), rt))
		rec.expect(jailEnded(rc), fmt.Sprintf("the attached session's status is one jailEndStatus reads (137, 125 or 255; it was %d)", rc),
			attach.combined())
		rec.expect(strings.Contains(attach.combined(), "This session ended because its jail stopped, and nothing recorded why"),
			"the attached session is told that nothing recorded why its jail ended", attach.combined())
		frc := rec.awaitEnd(first)
		rec.expect(jailEnded(frc) && strings.Contains(first.combined(), "nothing recorded why"),
			fmt.Sprintf("the first session is told its jail ended (rc %d)", frc), first.combined())
		rec.verdict()
	})
}
