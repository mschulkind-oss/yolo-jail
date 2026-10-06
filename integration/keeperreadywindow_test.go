package integration

// keeperreadywindow_test.go is the end-to-end pin on a fresh launch interrupted after its keeper
// said the jail is ready and before the launch's signal arm switched to the first session's own
// teardown (docs/design/jail-lifetime-last-session-wins.md JL-D74). The arm still ran the pre-ready
// teardown there, whose lifeline the keeper no longer reads, while the launch's own session count
// kept the keeper from draining, so the launch waited out its whole 20 s bound and the jail ended
// only at its exit. The unit tier drives the same moment against a fake runtime
// (internal/cli/run/keeperreadywindow_test.go).
//
// THE WINDOW IS HELD OPEN BY STOPPING THE LAUNCH: SIGSTOP once its keeper has started, until the
// keeper's log says the jail is ready, then SIGINT and SIGCONT together. On resume the launch's own
// goroutine reads the ready frame and retargets the arm while the arm takes the pending signal; the
// arm won every one of 6 tries in a nested jail before the fix, each exiting at 20.02 s. When the
// goroutine wins instead, the arm runs the session's teardown, which is quick on either build, so
// this test can miss the defect on a run but never fails on a fixed build.
//
// THE SAME WINDOW WITH ANOTHER SESSION IN: the teardown's wait is then a session's quit, which the
// launch said and did not wait out (JL-D74's second half). Without that, the launch still waited
// the whole 20 s bound, 2 of 2 tries in a nested jail, for a keeper that was ending nothing. The
// line saying so ("stays up for") is printed in either order: by the pre-ready teardown when the
// arm wins, and by the session's teardown when the goroutine wins, which says it on a SIGINT or a
// SIGTERM as a session's quit does (JL-D76). So this test requires the line once whichever order
// wins; before JL-D76 only the arm's order printed it, and requiring it failed fixed builds on CI
// runners of both architectures, where the goroutine often wins. The unit tier pins each order on
// its own (internal/cli/run/keeperreadywindow_test.go, internal/cli/run/sessionsignalstaysup_test.go).
//
// THE GOROUTINE'S ORDER, TAKEN TO ITS END: once retargeted, the launch starts the first session's
// exec at once, so the session's teardown can hang that session up before its exec has named itself
// in the jail. The hangup then ended nothing and the session ran on with no terminal (JL-D77). The
// last test here holds that moment open too, by stopping the exec's client and the launch in turn;
// the unit tier pins the jail's half (internal/entrypoint/sessionhangupmark_test.go). A first
// session that ends so abandons the provisioning the other session waits for (JL-D80), so that
// session, which has a terminal as the other terminal it stands for does, runs it and goes on.
//
// EACH TRY IS A WORKSPACE OF ITS OWN. A nested podman sometimes cannot remove the jail's stopped
// container after so quick a stop (the `openByHandleAt` failure the design's §4.4 records, seen in
// 1 of 5 tries in one workspace). The keeper removes it by force since JL-D82, and a try in a
// workspace of its own does not depend on that.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestASIGINTBetweenReadyAndTheFirstSessionEndsTheJailPromptly interrupts a fresh launch in that
// window, three times: each time the launch exits 130 well inside the 20 s bound, its keeper ends
// the jail, and no container of the workspace runs.
func TestASIGINTBetweenReadyAndTheFirstSessionEndsTheJailPromptly(t *testing.T) {
	requireJail(t)
	started := regexp.MustCompile(`keeper: started, pid (\d+)`)
	const bound = 10 * time.Second // half the launch's 20 s unwind bound
	for try := 1; try <= 3; try++ {
		dir := writeProject(t, `{}`)
		cname := naming.FromWorkspace(dir)
		logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
		ready := "holds " + cname + " until its last session leaves"
		run := startYoloBackground(t, "interrupted", dir, `echo SESSION-IN-$((40+2)); sleep 600`)
		var m []string
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(2 * time.Millisecond) {
			if m = started.FindStringSubmatch(run.combined()); m != nil {
				break
			}
			select {
			case err := <-run.done:
				t.Fatalf("try %d: the launch exited (%v) before its keeper started:\n%s", try, err, run.combined())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("try %d: the launch did not start its keeper within %s:\n%s", try, jailTimeout(), run.combined())
			}
		}
		keeper, _ := strconv.Atoi(m[1])
		if err := syscall.Kill(run.pid, syscall.SIGSTOP); err != nil {
			t.Fatalf("stopping the launch: %v", err)
		}
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(20 * time.Millisecond) {
			raw, _ := os.ReadFile(logPath)
			if strings.Contains(string(raw), ready) {
				break
			}
			if time.Now().After(deadline) || syscall.Kill(keeper, 0) != nil {
				_ = syscall.Kill(run.pid, syscall.SIGCONT)
				t.Fatalf("try %d: the keeper never said its jail was ready:\n%s\nthe launch:\n%s", try, raw, run.combined())
			}
		}
		_ = syscall.Kill(run.pid, syscall.SIGINT)
		sent := time.Now()
		if err := syscall.Kill(run.pid, syscall.SIGCONT); err != nil {
			t.Fatalf("resuming the launch: %v", err)
		}
		if rc := run.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
			t.Errorf("try %d: the interrupted launch exited %d, want %d:\n%s", try, rc, 128+int(syscall.SIGINT), run.combined())
		}
		took := time.Since(sent)
		if took >= bound {
			t.Errorf("try %d: the launch took %s to exit after its SIGINT: it waited out its unwind bound for a "+
				"keeper that could not drain while the launch still counted itself in the jail:\n%s",
				try, took.Round(10*time.Millisecond), run.combined())
		}
		if !awaitProcessGone(keeper, 2*time.Minute) {
			t.Fatalf("try %d: the keeper (pid %d) of the interrupted launch is still running:\n%s", try, keeper, run.combined())
		}
		raw, _ := os.ReadFile(logPath)
		t.Logf("try %d: the launch exited %s after its SIGINT, its keeper %s after it; the keeper's log:\n%s", try,
			took.Round(10*time.Millisecond), time.Since(sent).Round(10*time.Millisecond), raw)
		if n := runningContainers(t, cname); n != 0 {
			t.Fatalf("try %d: %d containers named %s are running after the interrupted launch's keeper ended:\n%s",
				try, n, cname, run.combined())
		}
	}
}

// readyWindowWithAnother is a fresh launch held stopped once its keeper said the jail is ready,
// with a second session already in that jail, waiting there for the first session's provisioning,
// which the held launch has not begun.
type readyWindowWithAnother struct {
	cname        string
	first, other *bgRun
	keeper       int
}

// startYoloAtTerminal is startYoloBackground with a terminal of its own: a pty on the launch's
// stdin, stdout and stderr, whose output combined() returns.
func startYoloAtTerminal(t *testing.T, name, dir, script string) *bgRun {
	t.Helper()
	master, slave := openTestPty(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, yoloBin, append(jailRunArgs(), "--", "bash", "-lc", script)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)
	cmd.Env = append(cmd.Env, autoCaptureEnvForSuite()...)
	cmd.Env = append(cmd.Env, readinessEnvForSuite()...)
	awaitDetachedWriters(t, dir, launchHome(cmd.Env))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("%s: starting yolo: %v", name, err)
	}
	// The child holds its own copies; the parent's would keep the master from ever seeing EOF.
	_ = slave.Close()
	out := &syncBuffer{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				_, _ = out.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	r := &bgRun{name: name, pid: cmd.Process.Pid, out: out, done: make(chan error, 1), exited: make(chan struct{})}
	go func() {
		r.done <- cmd.Wait()
		close(r.exited)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.exited:
		case <-time.After(30 * time.Second):
		}
	})
	return r
}

// holdAtReadyWithAnother starts a fresh launch of firstScript in a workspace of its own, stops it
// (SIGSTOP) once its keeper has started, waits for that keeper to say the jail is ready, and attaches
// a second session. The launch is left stopped, for the caller to resume.
//
// THE SECOND SESSION HAS A TERMINAL, as the other terminal it stands for does. A session that
// waits for the first session's provisioning takes the run over when the first session abandons it,
// which the session's teardown order can do: hung up before it named itself (JL-D77), or as it
// began the run. One with no terminal cannot take it over and is refused instead (JL-D33), and
// since JL-D80 it is refused at once rather than after the two minutes it used to wait, which this
// test, ending the second session within them, never saw.
func holdAtReadyWithAnother(t *testing.T, try int, firstScript string) *readyWindowWithAnother {
	t.Helper()
	started := regexp.MustCompile(`keeper: started, pid (\d+)`)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)
	logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
	ready := "holds " + cname + " until its last session leaves"
	first := startYoloBackground(t, "interrupted", dir, firstScript)
	var m []string
	for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(2 * time.Millisecond) {
		if m = started.FindStringSubmatch(first.combined()); m != nil {
			break
		}
		select {
		case err := <-first.done:
			t.Fatalf("try %d: the launch exited (%v) before its keeper started:\n%s", try, err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("try %d: the launch did not start its keeper within %s:\n%s", try, jailTimeout(), first.combined())
		}
	}
	keeper, _ := strconv.Atoi(m[1])
	if err := syscall.Kill(first.pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stopping the launch: %v", err)
	}
	for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(20 * time.Millisecond) {
		raw, _ := os.ReadFile(logPath)
		if strings.Contains(string(raw), ready) {
			break
		}
		if time.Now().After(deadline) || syscall.Kill(keeper, 0) != nil {
			_ = syscall.Kill(first.pid, syscall.SIGCONT)
			t.Fatalf("try %d: the keeper never said its jail was ready:\n%s\nthe launch:\n%s", try, raw, first.combined())
		}
	}
	// The second session attaches to the ready jail and waits there for the first session's
	// provisioning, which the held launch has not begun.
	other := startYoloAtTerminal(t, "other", dir, `echo OTHER-IN-$((1+1)); sleep 600`)
	for deadline := time.Now().Add(jailTimeout()); !strings.Contains(other.combined(), "first session to begin provisioning"); time.Sleep(20 * time.Millisecond) {
		select {
		case err := <-other.done:
			_ = syscall.Kill(first.pid, syscall.SIGCONT)
			t.Fatalf("try %d: the second session exited (%v) before it was in the jail:\n%s", try, err, other.combined())
		default:
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(first.pid, syscall.SIGCONT)
			t.Fatalf("try %d: the second session never got into the jail:\n%s", try, other.combined())
		}
	}
	return &readyWindowWithAnother{cname: cname, first: first, other: other, keeper: keeper}
}

// endTheOther quits the second session and requires the keeper to end the jail after it.
func (w *readyWindowWithAnother) endTheOther(t *testing.T, try int) {
	t.Helper()
	_ = syscall.Kill(w.other.pid, syscall.SIGINT)
	_ = w.other.wait(t, jailTimeout())
	if !awaitProcessGone(w.keeper, 2*time.Minute) {
		t.Fatalf("try %d: the keeper (pid %d) outlived the jail's last session:\n%s", try, w.keeper, w.other.combined())
	}
	if n := runningContainers(t, w.cname); n != 0 {
		t.Fatalf("try %d: %d containers named %s are running after the last session left", try, n, w.cname)
	}
}

// TestASIGINTInTheReadyWindowLeavesTheJailUpForAnotherSession is the same window with a second
// session already in the jail, attached while the first launch was held stopped after its keeper's
// ready. The interrupted launch is then one session leaving a jail another is in: it must exit well
// inside its bound, the other session must go on in the running jail, and once that session quits
// the keeper ends the jail. It says the jail stays up for the other session exactly once, whichever
// of its two teardowns took the signal, which is a race (the file's header).
func TestASIGINTInTheReadyWindowLeavesTheJailUpForAnotherSession(t *testing.T) {
	requireJail(t)
	const bound = 10 * time.Second // half the launch's 20 s unwind bound
	for try := 1; try <= 2; try++ {
		w := holdAtReadyWithAnother(t, try, `echo SESSION-IN-$((40+2)); sleep 600`)
		first, other, keeper, cname := w.first, w.other, w.keeper, w.cname
		_ = syscall.Kill(first.pid, syscall.SIGINT)
		sent := time.Now()
		if err := syscall.Kill(first.pid, syscall.SIGCONT); err != nil {
			t.Fatalf("resuming the launch: %v", err)
		}
		if rc := first.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
			t.Errorf("try %d: the interrupted launch exited %d, want %d:\n%s", try, rc, 128+int(syscall.SIGINT), first.combined())
		}
		took := time.Since(sent)
		t.Logf("try %d: the launch exited %s after its SIGINT", try, took.Round(10*time.Millisecond))
		if took >= bound {
			t.Errorf("try %d: the launch took %s to exit after its SIGINT: it waited out its unwind bound for a "+
				"keeper that keeps the jail up for its other session:\n%s", try, took.Round(10*time.Millisecond), first.combined())
		}
		if processGone(other.pid) {
			t.Fatalf("try %d: the other session ended with the interrupted launch:\n%s", try, other.combined())
		}
		if processGone(keeper) {
			t.Fatalf("try %d: the keeper ended a jail its other session is still in:\n%s", try, other.combined())
		}
		if n := runningContainers(t, cname); n != 1 {
			t.Fatalf("try %d: %d containers named %s run with a session still in the jail", try, n, cname)
		}
		staysUp, said := "Jail "+cname+" stays up for", first.combined()
		if n := strings.Count(said, staysUp); n != 1 {
			t.Errorf("try %d: the interrupted launch said %d times that the jail stays up for its other "+
				"session, want once, whichever of its teardowns took the signal:\n%s", try, n, said)
		} else {
			line, _, _ := strings.Cut(said[strings.Index(said, staysUp):], "\n")
			t.Logf("try %d: %s", try, line)
		}
		w.endTheOther(t, try)
	}
}

// firstSessionMark is in the command line of every process of the first session's exec below, and
// of no other process in its jail: the session's entrypoint carries the command as its payload, and
// the shells it runs and the `sleep` the command ends in carry it after.
const firstSessionMark = "43217"

// inJailFirstSession is the first session's entrypoint in the jail, read from this host's /proc,
// which shows the processes of the jail's pid namespace too: the pid of a process whose argv[0] is
// the jail's entrypoint and whose form is the first session's, or 0. The exec's client on this host
// has the same arguments after `<rt> exec`, so argv[0] is what tells the two apart.
func inJailFirstSession() int {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		args, err := processArgs(pid)
		if err == nil && len(args) > 1 && args[0] == "/opt/yolo-jail/bin/yolo-entrypoint" &&
			args[1] == "--yolo-first-session" {
			return pid
		}
	}
	return 0
}

// awaitChild polls for a running child of parent whose command line holds arg, within bound: its
// pid, or 0.
func awaitChild(parent int, arg string, bound time.Duration) int {
	for deadline := time.Now().Add(bound); time.Now().Before(deadline); {
		for _, pid := range processChildren(parent) {
			if processHasArgs(pid, arg) {
				return pid
			}
		}
	}
	return 0
}

// TestASIGINTBeforeTheFirstSessionNamesItselfLeavesNothingOfItRunning is the ready window's other
// order taken to its end (docs/design/jail-lifetime-last-session-wins.md JL-D77): the launch's own
// goroutine has retargeted the arm to the session's teardown and started the first session's exec
// client, and the teardown's hangup reaches the jail before that exec has named itself there. The
// hangup then found no session and ended nothing, and the exec, which killing its client does not
// end (conmon keeps it, §9.7), ran on in the jail with no terminal for as long as the other session
// kept the jail up: it took provisioning and went on to its command.
//
// THE WINDOW IS HELD OPEN BY STOPPING PROCESSES, as above. The first session's exec client is
// stopped the moment it appears, before it has made its exec, and the SIGINT then takes the
// session's teardown, since the retarget comes before that client starts. The launch is stopped
// while its hangup's client runs, so that it cannot kill the exec client before the exec is made,
// which the exec client is let do once the hangup has run. A try in which either stop came too late
// is not counted, and the test needs two that are.
func TestASIGINTBeforeTheFirstSessionNamesItselfLeavesNothingOfItRunning(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" {
		t.Skip("holds the window open by reading the jail's processes from this host's /proc, which a Mac's VM keeps to itself")
	}
	rt := detectRuntime()
	// The mark is split in the probe's own text, so the probe never counts itself.
	probe := `n=0; for p in /proc/[0-9]*; do c=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); ` +
		`case "$c" in *"` + firstSessionMark[:3] + `""` + firstSessionMark[3:] + `"*) n=$((n+1)); echo "LEFT: $c";; esac; ` +
		`done; echo "COUNT=$n"`
	counted := 0
	for try := 1; try <= 5 && counted < 2; try++ {
		w := holdAtReadyWithAnother(t, try, `echo SESSION-IN-$((40+2)); exec sleep `+firstSessionMark)
		first := w.first
		if err := syscall.Kill(first.pid, syscall.SIGCONT); err != nil {
			t.Fatalf("resuming the launch: %v", err)
		}
		client := awaitChild(first.pid, "--yolo-first-session", jailTimeout())
		if client == 0 {
			t.Fatalf("try %d: the launch never started its first session's exec:\n%s", try, first.combined())
		}
		_ = syscall.Kill(client, syscall.SIGSTOP)
		held := inJailFirstSession() == 0
		_ = syscall.Kill(first.pid, syscall.SIGINT)
		hangup := awaitChild(first.pid, "--yolo-hangup-session", 10*time.Second)
		if hangup == 0 {
			_ = syscall.Kill(client, syscall.SIGCONT)
			t.Fatalf("try %d: the SIGINT ran no hangup of the first session:\n%s", try, first.combined())
		}
		_ = syscall.Kill(first.pid, syscall.SIGSTOP)
		for deadline := time.Now().Add(1500 * time.Millisecond); !processGone(hangup) && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		held = held && processGone(hangup) && !processGone(client) && inJailFirstSession() == 0
		// The hangup has run in the jail. Now the exec is made, and the launch, still stopped, cannot
		// kill its client first. It has run once the session's entrypoint is in the jail, or once its
		// client has ended.
		_ = syscall.Kill(client, syscall.SIGCONT)
		ran := false
		for deadline := time.Now().Add(1200 * time.Millisecond); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			if inJailFirstSession() != 0 {
				ran = true
				time.Sleep(300 * time.Millisecond) // it names itself first thing; let it get past that
				break
			}
			if processGone(client) {
				ran = true
				break
			}
		}
		_ = syscall.Kill(first.pid, syscall.SIGCONT)
		if rc := first.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
			t.Errorf("try %d: the interrupted launch exited %d, want %d:\n%s", try, rc, 128+int(syscall.SIGINT), first.combined())
		}
		exited := time.Now()
		if !held || !ran {
			t.Logf("try %d: not counted, a stop came too late to hold the window (held %v, the exec ran %v)", try, held, ran)
			w.endTheOther(t, try)
			continue
		}
		counted++
		for _, line := range strings.Split(first.combined(), "\n") {
			if strings.Contains(line, "before the session began") || strings.Contains(line, "stays up") {
				t.Logf("try %d: the launch printed: %s", try, strings.TrimSpace(line))
			}
		}
		// What the hangup reached had its signal before the launch exited, and a session that ends
		// itself on finding it was hung up does so as it starts. After that nothing of it may run.
		time.Sleep(time.Second)
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			out, err := jailExec(ctx, rt, w.cname, probe).CombinedOutput()
			cancel()
			if !strings.Contains(string(out), "COUNT=") {
				t.Fatalf("try %d: counting the first session's processes in the jail failed (%v):\n%s", try, err, out)
			}
			if !strings.Contains(string(out), "COUNT=0") {
				t.Errorf("try %d: the interrupted first session runs on in the jail after its launcher exited, its "+
					"hangup having reached the jail before the session named itself:\n%s\nthe launch:\n%s",
					try, out, first.combined())
				break
			}
		}
		if processGone(w.other.pid) {
			t.Fatalf("try %d: the other session ended with the interrupted launch:\n%s", try, w.other.combined())
		}
		// The first session, ending as it named itself, abandoned the provisioning the other session
		// waits for (JL-D80), so the other runs it on its terminal and goes on to its command, rather
		// than wait out the two minutes the jail gives a first session that has not arrived.
		const began = "OTHER-IN-2"
		for !strings.Contains(w.other.combined(), began) {
			if processGone(w.other.pid) {
				t.Fatalf("try %d: the other session ended instead of running the abandoned provisioning:\n%s",
					try, w.other.combined())
			}
			if time.Since(exited) > 30*time.Second {
				t.Fatalf("try %d: the other session had not begun %s after the first session ended before it "+
					"began, so it is still waiting for that session to begin provisioning:\n%s",
					try, time.Since(exited).Round(time.Second), w.other.combined())
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("try %d: the other session had begun its command when looked at, %s after the interrupted "+
			"launch exited", try, time.Since(exited).Round(10*time.Millisecond))
		w.endTheOther(t, try)
	}
	if counted < 2 {
		t.Fatalf("only %d of the tries held the window open, so the test measured nothing", counted)
	}
}
