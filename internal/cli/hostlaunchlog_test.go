package cli

// hostlaunchlog_test.go pins what one `yolo host -- <cmd>` leaves on this machine
// (hostLaunchTrace): its line in the machine-wide launches.log, `runtime=host`, and its block in
// host-launch.log holding what yolo itself printed. Both are under GLOBAL_STORAGE/logs and never
// in the directory the command ran in; every cell drives hostMain, so deleting a call site in
// hostExec or hostLaunch fails the cell that reads it.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// readFile is path's content, "" when it does not exist.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// hostLaunchID is how this process's launch blocks name it: on the header, on every body line and
// on every trailer (run.HostLaunchLog), so a block can be told from another launch's interleaved
// with it.
func hostLaunchID() string { return fmt.Sprintf("(pid %d)", os.Getpid()) }

// THE LAUNCH LINE IS ON DISK BEFORE THE HAND-OVER: an exec replaces this process, so `started` is
// written before it or never. The line is the jail launch's own format, with the host's runtime.
// And the block it leaves names the program and never what was typed after it or where: every
// launch below types an argument the machine log may not keep, and the ones that REWRITE the argv
// print it in a disclosure the terminal keeps whole and the log keeps as a count.
func TestAHostLaunchWritesItsLaunchLineBeforeTheExec(t *testing.T) {
	const secret = "do-not-log-this-argument"
	t.Run("a command on PATH", func(t *testing.T) {
		_, cwd := timingHome(t, "")
		var atExec, logAtExec string
		orig := hostSyscallExec
		hostSyscallExec = func(string, []string, []string) error {
			atExec = readFile(t, run.MachineLaunchLogPath())
			logAtExec = readFile(t, run.HostLaunchLogPath())
			return nil
		}
		t.Cleanup(func() { hostSyscallExec = orig })
		var errw bytes.Buffer
		if rc := hostMain([]string{"--", "mytool", secret}, io.Discard, &errw, false, nil); rc != 0 {
			t.Fatalf("rc = %d\n%s", rc, errw.String())
		}
		hash := paths.JailShortHash(runtime.FromWorkspace(cwd))
		want := " launch jail=" + hash + " runtime=host podman_wait=- tries=- outcome=started rc=- after="
		if !strings.Contains(atExec, want) {
			t.Errorf("launches.log at the exec = %q, want a line containing %q", atExec, want)
		}
		if n := strings.Count(readFile(t, run.MachineLaunchLogPath()), "\n"); n != 1 {
			t.Errorf("launches.log holds %d lines after one launch, want 1", n)
		}
		for _, w := range []string{"=== yolo host launch ", " " + hostLaunchID() + " ===\n", "jail=" + hash,
			"program=mytool", hostLaunchID() + " yolo host: starting mytool",
			"=== handed over " + hostLaunchID() + ": exec ==="} {
			if !strings.Contains(logAtExec, w) {
				t.Errorf("host-launch.log at the exec lacks %q:\n%s", w, logAtExec)
			}
		}
		assertMachineLogsNever(t, secret, cwd)
		assertNoWorkspaceState(t, cwd)
	})

	// codex's model menu (hostmodelmenu.go) and the pack flags rewrite the argv and disclose both
	// argvs; the menu is the rewrite every subscription launch of codex makes.
	t.Run("codex's model menu rewrites the argv", func(t *testing.T) {
		l := runHostMenu(t, "", codexOnTheSubscription, nil, nil, "exec", secret)
		if l.rc != 0 || l.argv == nil {
			t.Fatalf("rc=%d, exec reached=%v\n%s", l.rc, l.argv != nil, l.errs)
		}
		cwd, _ := os.Getwd()
		if !strings.Contains(l.errs, "  you asked for: codex exec "+secret+"\n") {
			t.Errorf("the terminal lost the whole disclosure, which is what the user is shown:\n%s", l.errs)
		}
		log := readFile(t, run.HostLaunchLogPath())
		for _, w := range []string{"yolo host: yolo CHANGED the command you asked for:",
			hostLaunchID() + "   you asked for: codex <2 arguments>\n",
			hostLaunchID() + "   yolo will run: codex <4 arguments>\n",
			"  added by pack codex: -c model_catalog_json="} {
			if !strings.Contains(log, w) {
				t.Errorf("host-launch.log lacks %q:\n%s", w, log)
			}
		}
		assertMachineLogsNever(t, secret, cwd)
	})

	// A pack's guarded launch flags (injectHostLaunchFlags), the third rewrite, in the same words.
	t.Run("a pack's launch flags rewrite the argv", func(t *testing.T) {
		rc, argv, errs := hostLaunchFlagsRun(t, twoPostureManifest, secret)
		if rc != 0 || argv == nil {
			t.Fatalf("rc = %d, exec reached = %v\n%s", rc, argv != nil, errs)
		}
		cwd, _ := os.Getwd()
		if !strings.Contains(errs, "  you asked for: tool "+secret+"\n") {
			t.Errorf("the terminal lost the whole disclosure:\n%s", errs)
		}
		log := readFile(t, run.HostLaunchLogPath())
		for _, w := range []string{"  you asked for: tool <1 argument>\n", "  yolo will run: tool <2 arguments>\n",
			"  added by pack local: --ask-first\n"} {
			if !strings.Contains(log, w) {
				t.Errorf("host-launch.log lacks %q:\n%s", w, log)
			}
		}
		assertMachineLogsNever(t, secret, cwd)
	})

	// The managed Codex launch's own rewrite (--no-daemon) is disclosed in the same words.
	t.Run("a managed launch rewrites the argv", func(t *testing.T) {
		_, cwd := timingHome(t, "")
		fake := &argvDisclosingManagedLaunch{}
		orig := prepareOpenAIAuthHost
		prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
		t.Cleanup(func() { prepareOpenAIAuthHost = orig })
		var errw bytes.Buffer
		if rc := hostMain([]string{"--", "mytool", "exec", secret}, io.Discard, &errw, false, nil); !fake.ran {
			t.Fatalf("rc = %d, the managed launch was not reached\n%s", rc, errw.String())
		}
		if !strings.Contains(errw.String(), "  yolo will run: mytool "+fakeManagedArgvFlag+" exec "+secret+"\n") {
			t.Errorf("the terminal lost the whole disclosure:\n%s", errw.String())
		}
		log := readFile(t, run.HostLaunchLogPath())
		for _, w := range []string{"  you asked for: mytool <2 arguments>\n", "  yolo will run: mytool <3 arguments>\n"} {
			if !strings.Contains(log, w) {
				t.Errorf("host-launch.log lacks %q:\n%s", w, log)
			}
		}
		assertMachineLogsNever(t, secret, cwd)
	})

	// A program typed as a path names the directory it was typed in once it is resolved: the
	// terminal's starting line keeps it, the log names the program by its base name, and so does
	// the line an exec that failed prints.
	t.Run("a command given as a path", func(t *testing.T) {
		_, cwd := timingHome(t, "")
		writeFile(t, filepath.Join(cwd, "secretproj", "tool"), "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(cwd, "secretproj", "tool"), 0o755); err != nil {
			t.Fatal(err)
		}
		orig := hostSyscallExec
		hostSyscallExec = func(string, []string, []string) error { return syscall.EACCES }
		t.Cleanup(func() { hostSyscallExec = orig })
		var errw bytes.Buffer
		if rc := hostMain([]string{"--", "./secretproj/tool", "--api-key=" + secret}, io.Discard, &errw, false,
			nil); rc != 126 {
			t.Fatalf("rc = %d, want the failed exec's 126\n%s", rc, errw.String())
		}
		if !strings.Contains(errw.String(), "yolo host: starting ./secretproj/tool (as given, ") {
			t.Errorf("the terminal lost the starting line's path:\n%s", errw.String())
		}
		log := readFile(t, run.HostLaunchLogPath())
		for _, w := range []string{"program=tool", hostLaunchID() + " yolo host: starting tool (as given)\n",
			hostLaunchID() + " yolo host: exec tool: permission denied\n",
			"=== launch done " + hostLaunchID() + ", rc=126 ==="} {
			if !strings.Contains(log, w) {
				t.Errorf("host-launch.log lacks %q:\n%s", w, log)
			}
		}
		assertMachineLogsNever(t, secret, cwd, "secretproj")
	})

	// The directory itself, wherever a line of yolo's names it: here the miss line, which lists
	// the PATH searched, one folder of which is inside the directory the command ran in.
	t.Run("a line naming the directory", func(t *testing.T) {
		_, cwd := timingHome(t, "")
		t.Setenv("PATH", filepath.Join(cwd, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		captureHostExec(t)
		var errw bytes.Buffer
		if rc := hostMain([]string{"--", "nosuchtool-b01"}, io.Discard, &errw, false, nil); rc != 127 {
			t.Fatalf("rc = %d, want the miss's 127\n%s", rc, errw.String())
		}
		if !strings.Contains(errw.String(), filepath.Join(cwd, "bin")) {
			t.Fatalf("the miss line does not name the folder under the directory, so this cell proves "+
				"nothing:\n%s", errw.String())
		}
		if log := readFile(t, run.HostLaunchLogPath()); !strings.Contains(log, "<cwd>"+string(os.PathSeparator)+"bin") {
			t.Errorf("host-launch.log does not name the folder relative to <cwd>:\n%s", log)
		}
		assertMachineLogsNever(t, cwd)
	})
}

// argvDisclosingManagedLaunch is a managed launch whose Argv discloses its rewrite in the words
// openaiauthhost's does: the verdict, both argvs quoted whole, and who added the flag.
type argvDisclosingManagedLaunch struct{ fakeManagedHostLaunch }

func (f *argvDisclosingManagedLaunch) Argv(argv []string) ([]string, []string) {
	out := append([]string{argv[0], fakeManagedArgvFlag}, argv[1:]...)
	return out, []string{"yolo CHANGED the command you asked for:", "  you asked for: " + shquote.Join(argv),
		"  yolo will run: " + shquote.Join(out), "  added by the fake managed launch: " + fakeManagedArgvFlag}
}

// assertMachineLogsNever fails when host-launch.log or launches.log holds any of never.
func assertMachineLogsNever(t *testing.T, never ...string) {
	t.Helper()
	for _, p := range []string{run.HostLaunchLogPath(), run.MachineLaunchLogPath()} {
		got := readFile(t, p)
		for _, n := range never {
			if strings.Contains(got, n) {
				t.Errorf("the machine log %s names %q, which it never may:\n%s", filepath.Base(p), n, got)
			}
		}
	}
}

// A REFUSED LAUNCH LEAVES ITS LINE AND ITS WORDS: `outcome=not-started rc=1`, and the refusal as
// yolo printed it, ANSI-free, followed by the trailer naming the exit code.
func TestARefusedHostLaunchLeavesItsLineAndItsRefusal(t *testing.T) {
	_, cwd := timingHome(t, "")
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error {
		t.Error("a refused launch reached the exec")
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = orig })
	var errw bytes.Buffer
	rc := hostMain([]string{"-p", "nosuchprofile", "--", "mytool"}, io.Discard, &errw, false, nil)
	if rc != 1 {
		t.Fatalf("rc = %d, want the composition's refusal\n%s", rc, errw.String())
	}
	if line := readFile(t, run.MachineLaunchLogPath()); !strings.Contains(line,
		" runtime=host podman_wait=- tries=- outcome=not-started rc=1 ") {
		t.Errorf("launches.log = %q, want the refused launch's not-started line", line)
	}
	log := readFile(t, run.HostLaunchLogPath())
	refusal := strings.SplitN(strings.TrimSpace(errw.String()), "\n", 2)[0]
	if !strings.HasPrefix(refusal, "yolo host: refusing to launch:") || !strings.Contains(log, refusal) {
		t.Errorf("host-launch.log lacks the refusal %q:\n%s", refusal, log)
	}
	if !strings.HasSuffix(log, "=== launch done "+hostLaunchID()+", rc=1 ===\n") {
		t.Errorf("host-launch.log does not end with the trailer:\n%s", log)
	}
	if strings.ContainsRune(log, '\x1b') {
		t.Errorf("host-launch.log holds an escape sequence:\n%q", log)
	}
	assertNoWorkspaceState(t, cwd)
}

// A USAGE ERROR IS NO LAUNCH: an argv the parse refuses (exit 2) writes neither file.
func TestAHostUsageErrorLeavesNoTrace(t *testing.T) {
	timingHome(t, "")
	for _, argv := range [][]string{{"--frob", "--", "mytool"}, {"--"}, {"--network", "none", "--", "mytool"}} {
		var errw bytes.Buffer
		if rc := hostMain(argv, io.Discard, &errw, false, nil); rc != 2 {
			t.Fatalf("`yolo host %s`: rc = %d, want 2\n%s", strings.Join(argv, " "), rc, errw.String())
		}
	}
	for _, p := range []string{run.MachineLaunchLogPath(), run.HostLaunchLogPath()} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a usage error wrote %s:\n%s", p, readFile(t, p))
		}
	}
}

// RUN IN THE HOME, A HOST LAUNCH MINTS NO ~/.yolo: its logs are machine-wide.
func TestAHostLaunchInTheHomeCreatesNoWorkspaceState(t *testing.T) {
	home, _ := timingHome(t, "")
	t.Chdir(home)
	captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostMain([]string{"--", "mytool"}, io.Discard, &errw, false, nil); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, errw.String())
	}
	assertNoWorkspaceState(t, home)
	if !strings.Contains(readFile(t, run.HostLaunchLogPath()), "=== handed over "+hostLaunchID()+": exec ===") {
		t.Errorf("the launch from the home left no block")
	}
}

// AN AGENT YOLO STAYS RESIDENT UNDER KEEPS ITS OWN STREAM: on the launch-owned-services path the
// agent is handed the caller's stderr, never the teed one, so what it prints never lands in the
// log, while yolo's own lines, the service line among them, do. Typed as a path inside the
// directory it runs in, the agent is named in the log by its base name: the starting line on this
// path keeps the path off the log too.
func TestAResidentAgentsStderrStaysOutOfTheHostLaunchLog(t *testing.T) {
	for _, tc := range []struct{ name, cmd0, starting string }{
		{"by name", "claude", "yolo host: starting claude (from your PATH, "},
		{"as a path", "./secretproj/claude", hostLaunchID() + " yolo host: starting claude (as given)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, _ := fakeUpstream(t)
			fakeHostBroker(t)
			const marker = "AGENT-STDERR-MARKER-7f3a"
			hostGateHome(t, codexConfig(upstream.URL), wcShell(nil))
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(cwd, "secretproj")
			script := "#!/bin/sh\necho " + marker + " >&2\nexec '" + exe + "' " + testFakeAgentArg + " \"$@\"\n"
			writeFile(t, filepath.Join(bin, "claude"), script)
			if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("YOLO_CLI_TEST_AGENT_DUMP", filepath.Join(t.TempDir(), "agent.json"))
			t.Setenv("YOLO_CLI_TEST_AGENT_MODE", "")
			orig := hostSyscallExec
			hostSyscallExec = func(string, []string, []string) error {
				t.Error("the services path exec'd instead of staying the agent's parent")
				return nil
			}
			t.Cleanup(func() { hostSyscallExec = orig })

			var errw bytes.Buffer
			rc := hostMain([]string{"-p", "codex", "--", tc.cmd0}, io.Discard, &errw, false, nil)
			if rc != 0 {
				t.Fatalf("rc = %d\n%s", rc, errw.String())
			}
			if !strings.Contains(errw.String(), marker) {
				t.Fatalf("the agent's stderr never reached the caller's stream, so this cell proves "+
					"nothing:\n%s", errw.String())
			}
			log := readFile(t, run.HostLaunchLogPath())
			if strings.Contains(log, marker) {
				t.Errorf("the agent's own stderr landed in host-launch.log:\n%s", log)
			}
			for _, w := range []string{`started the "wire-bridge" service`, tc.starting,
				"=== launch done " + hostLaunchID() + ", rc=0 ==="} {
				if !strings.Contains(log, w) {
					t.Errorf("host-launch.log lacks yolo's own %q:\n%s", w, log)
				}
			}
			if line := readFile(t, run.MachineLaunchLogPath()); !strings.Contains(line, " outcome=started rc=- ") {
				t.Errorf("launches.log = %q, want the resident launch's started line", line)
			}
			if tc.cmd0 != "claude" {
				assertMachineLogsNever(t, "secretproj")
			}
		})
	}
}

// stderrMarkingManagedLaunch is a managed launch (a managed Codex login) that stays resident and
// runs its program, standing in for codex: its Run writes managedStderrMarker to the stderr it was
// handed, as the program would to its own, and keeps that writer for the cell to compare.
type stderrMarkingManagedLaunch struct {
	fakeManagedHostLaunch
	stderr io.Writer
}

const managedStderrMarker = "MANAGED-AGENT-STDERR-MARKER-41c9"

func (f *stderrMarkingManagedLaunch) Run(_ string, argv, _ []string, _ io.Reader, _, stderr io.Writer) (int, bool) {
	f.ran, f.argv, f.stderr = true, argv, stderr
	fmt.Fprintln(stderr, managedStderrMarker)
	return 0, true
}

// A MANAGED LAUNCH KEEPS ITS OWN STREAM TOO: the second path yolo stays the parent on, a managed
// Codex login's managed.Run, is handed the caller's stderr itself, never the teed one. A tee there
// would put a pipe on the program's stderr in place of the terminal and copy what it prints into
// the machine log.
func TestAManagedLaunchsStderrStaysOutOfTheHostLaunchLog(t *testing.T) {
	timingHome(t, "")
	fake := &stderrMarkingManagedLaunch{}
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error {
		t.Error("the managed launch exec'd instead of running its program")
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = origExec })

	var errw bytes.Buffer
	if rc := hostMain([]string{"--", "mytool"}, io.Discard, &errw, false, nil); rc != 0 || !fake.ran {
		t.Fatalf("rc = %d, managed launch ran = %v\n%s", rc, fake.ran, errw.String())
	}
	if fake.stderr != io.Writer(&errw) {
		t.Errorf("the managed launch was handed %T for stderr, not the caller's own stream", fake.stderr)
	}
	if !strings.Contains(errw.String(), managedStderrMarker) {
		t.Fatalf("the program's stderr never reached the caller's stream:\n%s", errw.String())
	}
	log := readFile(t, run.HostLaunchLogPath())
	if strings.Contains(log, managedStderrMarker) {
		t.Errorf("the managed program's own stderr landed in host-launch.log:\n%s", log)
	}
	for _, w := range []string{"yolo host: starting mytool", "=== launch done"} {
		if !strings.Contains(log, w) {
			t.Errorf("host-launch.log lacks yolo's own %q:\n%s", w, log)
		}
	}
}

// THE LOG KEEPS THE NEWEST perf.MaxRuns BLOCKS: a launch over a full log trims it at open, keeping
// the newest and adding its own.
func TestTheHostLaunchLogKeepsTheNewestRuns(t *testing.T) {
	timingHome(t, "")
	path := run.HostLaunchLogPath()
	var seed strings.Builder
	for i := 0; i < perf.MaxRuns+10; i++ {
		fmt.Fprintf(&seed, "=== yolo host launch old-%03d ===\n  yolo=x  jail=y  program=z\n=== launch done, rc=0 ===\n", i)
	}
	writeFile(t, path, seed.String())
	captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostMain([]string{"--", "mytool"}, io.Discard, &errw, false, nil); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, errw.String())
	}
	log := readFile(t, path)
	if n := strings.Count(log, "=== yolo host launch "); n != perf.MaxRuns {
		t.Errorf("the log holds %d blocks, want %d", n, perf.MaxRuns)
	}
	if strings.Contains(log, "old-010 ") || !strings.Contains(log, "old-011 ") {
		t.Errorf("the trim kept the wrong blocks:\n%s", log[:200])
	}
	if !strings.Contains(log, "program=mytool") {
		t.Errorf("this launch's block is missing")
	}
}

// THE TRIM AND THE HEADER RUN UNDER THE SIBLING LOCK: while another launch holds
// host-launch.log.lock, opening the log waits, and it writes its header once the lock is free.
// Host wrappers make concurrent host launches ordinary, and an unlocked trim interleaved with
// another's header loses blocks.
func TestTheHostLaunchLogTrimsUnderTheSiblingLock(t *testing.T) {
	timingHome(t, "")
	path := run.HostLaunchLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan *run.HostLaunchLog)
	ws := t.TempDir()
	go func() { done <- run.OpenHostLaunchLog(ws, "mytool") }()
	select {
	case <-done:
		t.Fatal("the log opened while another launch held its lock")
	case <-time.After(150 * time.Millisecond):
	}
	if strings.Contains(readFile(t, path), "program=mytool") {
		t.Fatal("the header was written while another launch held the lock")
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	select {
	case l := <-done:
		l.Done(0)
	case <-time.After(5 * time.Second):
		t.Fatal("the log never opened once the lock was free")
	}
	if !strings.Contains(readFile(t, path), "program=mytool") {
		t.Error("no header once the lock was free")
	}
}
