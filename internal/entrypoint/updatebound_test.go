package entrypoint

// updatebound_test.go is the platform-neutral half of the UPDATE BOUND's tests (program-delivery.md
// §3.5): the stand-in `yolo` the launchers' update path calls, and the probe every cell drives. The
// cells that need a terminal are in updatebound_linux_test.go.
//
// Every cell drives a FAKE program: a shell script at the launcher's REAL_BIN whose update verb
// (or pre-launch refresh) behaves in one of the ways a vendor updater has, and whose bare run
// prints AGENT_RAN. No agent is started and nothing reaches a network (AGENTS.md, "No agent
// tests").

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// asYoloEnv makes this test binary, re-executed through yoloStandIn's script, act as the jail's
// `yolo` for the one verb the update path calls, `internal no-terminal`, which it runs through
// notty.Main exactly as the real binary's dispatch (internal/cli) does. So the cells below run the
// real detach, bound and signal forwarding, not a stub of them.
const asYoloEnv = "YOLO_ENTRYPOINT_TEST_AS_YOLO"

// runAsYolo is the stand-in's whole CLI; any other verb is misuse.
func runAsYolo(args []string) int {
	if len(args) >= 2 && args[0] == "internal" && args[1] == NoTerminalVerb {
		return notty.Main(NoTerminalVerb, args[2:])
	}
	fmt.Fprintf(os.Stderr, "test yolo: no verb %q\n", strings.Join(args, " "))
	return 2
}

// yoloStandIn returns a directory holding a `yolo` that is this test binary in asYoloEnv mode.
func yoloStandIn(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n" + asYoloEnv + "=1 exec " + shellQuoteForTest(exe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// updateBehavior is how a fake vendor program updates: a PRELUDE, run before the program says it
// has started, and an ACTION after it.
type updateBehavior struct{ prelude, action string }

var (
	// sleeps is an update still running (a slow download, a hung request).
	sleeps = updateBehavior{"", "exec sleep 120"}
	// exitsZeroOnInt stops at a Ctrl-C and exits 0, as claude install does (measured 2026-10-01).
	exitsZeroOnInt = updateBehavior{"trap 'kill $! 2>/dev/null; exit 0' INT", "sleep 120 & wait $!"}
)

// boundProbe is one generated launcher and the fake program it manages.
type boundProbe struct {
	home, realBin, script, pidFile, started string
	timeout, grace                          int
}

// boundProbeOpts says which launcher to render and how the fake program updates.
type boundProbeOpts struct {
	npm bool // the npm template; the native one otherwise
	// verb is the update verb; nil declares none.
	verb []string
	// refresh, when set, is a pre-launch refresh, and the program's own update is made not due.
	refresh *packdecl.Refresh
	behave  updateBehavior
	// timeout and grace replace UPDATE_TIMEOUT and UPDATE_GRACE in the rendered launcher.
	timeout, grace int
	// installerURL is the native launcher's installer, which an update re-runs when no verb is
	// declared; empty is a URL nothing fetches.
	installerURL string
}

// newBoundProbe renders the launcher through the production generator and seeds REAL_BIN, so the
// update branch (not the cold install) is what runs.
func newBoundProbe(t *testing.T, o boundProbeOpts) *boundProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	p := &boundProbe{
		home:    home,
		script:  filepath.Join(home, "launch-probetool"),
		pidFile: filepath.Join(home, "update.pid"),
		started: filepath.Join(home, "update.started"),
		timeout: o.timeout,
	}
	stamps := filepath.Join(home, ".cache", "yolo-agent-stamps")
	receipts := filepath.Join(home, "ws", ".yolo", "receipts.jsonl")
	inst := &packdecl.Install{Bin: "probetool", UpdateVerb: o.verb, Refresh: o.refresh}
	var body string
	if o.npm {
		inst.Kind, inst.Package = "npm", "probetool"
		p.realBin = filepath.Join(home, ".npm-global", "bin", "probetool")
		body = npmAgentLauncher("probe", inst, stamps, receipts, true, launcherServers{}, nil)
	} else {
		inst.Kind, inst.InstallerURL = "native", o.installerURL
		if inst.InstallerURL == "" {
			inst.InstallerURL = "https://example.invalid/never-fetched.sh"
		}
		p.realBin = filepath.Join(home, ".local", "bin", "probetool")
		body = nativeAgentLauncher("probe", inst, stamps, receipts, "", true, launcherServers{}, nil)
	}
	if !strings.Contains(body, "\nUPDATE_TIMEOUT=60") {
		t.Fatal("the launcher no longer bakes UPDATE_TIMEOUT=60, so this cell cannot shorten it")
	}
	body = strings.Replace(body, "\nUPDATE_TIMEOUT=60", "\nUPDATE_TIMEOUT="+strconv.Itoa(o.timeout), 1)
	// The grace is patched where the launcher has one; before it had one, there was none.
	if strings.Contains(body, "\nUPDATE_GRACE=5") {
		body = strings.Replace(body, "\nUPDATE_GRACE=5", "\nUPDATE_GRACE="+strconv.Itoa(o.grace), 1)
		p.grace = o.grace
	}
	if err := os.WriteFile(p.script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	q := shellQuoteForTest
	prog := "#!/bin/bash\ncase \"${1:-}\" in\ninstall|refresh)\n" +
		o.behave.prelude + "\n" +
		"    echo \"$$\" > " + q(p.pidFile) + "\n" +
		"    : > " + q(p.started) + "\n" +
		o.behave.action + "\n" +
		"    echo UPDATE_FINISHED >&2\n    exit 0 ;;\nesac\necho AGENT_RAN\n"
	if err := os.MkdirAll(filepath.Dir(p.realBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.realBin, []byte(prog), 0o755); err != nil {
		t.Fatal(err)
	}
	if o.refresh != nil {
		// Only the refresh is due: the program's own stamp is fresh, and the store exists.
		seedFreshStamp(t, stamps, "probetool")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, o.refresh.Lock)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(p.killUpdate)
	return p
}

// killUpdate kills a fake update a failing cell left running, so it does not outlive the test.
func (p *boundProbe) killUpdate() {
	b, err := os.ReadFile(p.pidFile)
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	}
}

// EVERY TEMPLATE CARRIES THE ONE BOUND. The three templates each had their own _bounded, the fork's
// a copy of the others', and a fix landing in two of three is the drift this pins: each rendered
// launcher defines _bounded exactly once, and it is updateBoundShellFn's, with its grace.
func TestEveryLauncherTemplateCarriesTheOneUpdateBound(t *testing.T) {
	refresh := &packdecl.Refresh{Argv: []string{"refresh"}, Lock: ".store/.lock"}
	rendered := map[string]string{
		"npm": npmAgentLauncher("probe", &packdecl.Install{Kind: "npm", Bin: "tool", Package: "tool", Refresh: refresh},
			"/stamps", "/receipts.jsonl", true, launcherServers{}, nil),
		"native": nativeAgentLauncher("probe", &packdecl.Install{Kind: "native", Bin: "tool",
			InstallerURL: "https://x.invalid/i.sh", Refresh: refresh},
			"/stamps", "/receipts.jsonl", "", true, launcherServers{}, nil),
		"source": strings.Join(sourceAgentLauncherSegments(&packdecl.Install{Kind: "source", Bin: "tool",
			Refresh: refresh}, ForkDelivery{Key: "k"}, "/stamps", "/keys", "/receipts.jsonl", "", true,
			launcherServers{}, nil), ""),
	}
	for name, body := range rendered {
		if n := strings.Count(body, "\n_bounded() {"); n != 1 {
			t.Errorf("the %s launcher defines _bounded %d times, want once", name, n)
		}
		if !strings.Contains(body, updateBoundShellFn) {
			t.Errorf("the %s launcher does not carry updateBoundShellFn whole", name)
		}
		if !strings.Contains(body, `_shielded '_stop_refresh_heartbeat; _drop_refresh_lock'`) {
			t.Errorf("the %s launcher's pre-launch refresh is not _shielded", name)
		}
	}
}

// WITH NEITHER yolo's VERB NOR timeout(1), the update still runs, with no stdin, and the launcher
// says it is unbounded rather than leaving a hang unexplained (a stock macOS with no yolo on PATH).
func TestAnUpdateWithNoBoundAvailableRunsAndSaysSo(t *testing.T) {
	p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"},
		behave: updateBehavior{"", "if read -r _x; then echo STDIN_READ >&2; fi"}, timeout: 60, grace: 5})
	cmd := exec.Command(p.script)
	cmd.Dir = p.home
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + pathWithout(t, "yolo", "timeout")}
	cmd.Stdin = strings.NewReader("typed-by-the-user\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, out)
	}
	for _, want := range []string{"it runs with no stdin and no time limit", "UPDATE_FINISHED", "AGENT_RAN"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "STDIN_READ") {
		t.Errorf("the update read the launcher's stdin:\n%s", out)
	}
}

// pathFor is the launcher's PATH: the stand-in `yolo` first when detached, and no `yolo` at all
// otherwise, so the cell never depends on which yolo (if any) the machine running it has.
func pathFor(t *testing.T, detached bool) string {
	t.Helper()
	path := pathWithout(t, "yolo")
	if detached {
		return yoloStandIn(t) + string(os.PathListSeparator) + path
	}
	return path
}

// boundModes are the two ways a launcher can run an update: through yolo's detached, bounded verb,
// and, where no yolo has it, under GNU timeout(1).
func boundModes(t *testing.T) []struct {
	name     string
	detached bool
} {
	t.Helper()
	modes := []struct {
		name     string
		detached bool
	}{{"detached", true}}
	if _, err := exec.LookPath("timeout"); err == nil {
		modes = append(modes, struct {
			name     string
			detached bool
		}{"timeout-fallback", false})
	}
	return modes
}

// waitForPath polls for path to exist, for up to limit.
func waitForPath(t *testing.T, path string, limit time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// syncBuffer is a bytes.Buffer a cell may read while the launcher still writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// groupLaunch is one launcher run in a process group of its own, as a terminal runs its
// foreground job, so a signal sent to the group arrives as a Ctrl-C (SIGINT) or a closed terminal
// (SIGHUP) does. It needs no pty, so these cells run on macOS too, where the launcher is
// /bin/bash 3.2 and the verb is darwin's.
type groupLaunch struct {
	cmd  *exec.Cmd
	out  *syncBuffer
	done chan error
}

func startInGroup(t *testing.T, p *boundProbe, path string, env ...string) *groupLaunch {
	t.Helper()
	g := &groupLaunch{out: &syncBuffer{}, done: make(chan error, 1)}
	g.cmd = exec.Command(p.script)
	g.cmd.Dir = p.home
	g.cmd.Env = append([]string{"HOME=" + p.home, "PATH=" + path}, env...)
	g.cmd.Stdout, g.cmd.Stderr = g.out, g.out
	g.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := g.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { g.done <- g.cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	})
	return g
}

// signalWhen sends sig to the launcher's group once path exists, failing the cell if it never does.
func (g *groupLaunch) signalWhen(t *testing.T, path string, sig syscall.Signal) {
	t.Helper()
	if !waitForPath(t, path, 15*time.Second) {
		t.Fatalf("%s never appeared. The launcher printed:\n%s", path, g.out.String())
	}
	if err := syscall.Kill(-g.cmd.Process.Pid, sig); err != nil {
		t.Fatal(err)
	}
}

// wait returns the launcher's exit, failing the cell when it has not exited within limit.
func (g *groupLaunch) wait(t *testing.T, limit time.Duration) error {
	t.Helper()
	select {
	case err := <-g.done:
		return err
	case <-time.After(limit):
		t.Fatalf("the launcher was still running after %s. It printed:\n%s", limit, g.out.String())
		return nil
	}
}

// lockGone fails the cell when the install-prefix lock of p's launcher is still there.
func lockGone(t *testing.T, p *boundProbe, what string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(p.realBin)), ".yolo-update.lock")); !os.IsNotExist(err) {
		t.Errorf("%s must release the install-prefix lock (err=%v)", what, err)
	}
}

// A HANGUP OR A TERMINATE DURING AN UPDATE ENDS THE LAUNCHER AND RELEASES ITS LOCK. A closed
// terminal sends SIGHUP to its foreground process group, the launcher's, and a stopped jail sends
// SIGTERM. Before, the launcher died at once and left the install-prefix lock behind, so the next
// ten minutes of launches said "another update is in progress" and the next after that broke the
// lock and ran the update again.
func TestASignalDuringAnUpdateReleasesTheLockAndEndsTheLauncher(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		for _, mode := range boundModes(t) {
			t.Run(sig.String()+"/"+mode.name, func(t *testing.T) {
				p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"}, behave: sleeps, timeout: 30, grace: 2})
				g := startInGroup(t, p, pathFor(t, mode.detached))
				if !waitForPath(t, p.started, 15*time.Second) {
					t.Fatalf("the update never started:\n%s", g.out.String())
				}
				if _, err := os.Stat(filepath.Join(p.home, ".local", ".yolo-update.lock")); err != nil {
					t.Fatalf("the update runs without the install-prefix lock: %v", err)
				}
				g.signalWhen(t, p.started, sig)
				err := g.wait(t, 20*time.Second)
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("the launcher must end by the %s, got %v:\n%s", sig, err, g.out.String())
				}
				if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != sig {
					t.Errorf("the launcher must die of %s itself, got %v", sig, err)
				}
				if strings.Contains(g.out.String(), "AGENT_RAN") {
					t.Errorf("a launcher ended by %s must not start the program:\n%s", sig, g.out.String())
				}
				lockGone(t, p, "a "+sig.String())
			})
		}
	}
}

// A CTRL-C ENDS THE UPDATE, NOT THE LAUNCH, on every platform. The pty cells type a real Ctrl-C on
// Linux; this one sends the SIGINT a terminal sends to its foreground group, which macOS's
// check runs too, so bash 3.2's trap timing is exercised: a program that exits 0 on the Ctrl-C,
// as claude install does, is still an interrupted update only if the trap has run by the time
// _bounded reads the flag.
func TestAnInterruptAtTheLaunchersGroupEndsTheUpdateNotTheLaunch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		npm    bool
		behave updateBehavior
	}{
		{"native/dies-of-it", false, sleeps},
		{"native/exits-0", false, exitsZeroOnInt},
		{"npm/exits-0", true, exitsZeroOnInt},
	} {
		for _, mode := range boundModes(t) {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				p := newBoundProbe(t, boundProbeOpts{npm: tc.npm, verb: []string{"install"},
					behave: tc.behave, timeout: 30, grace: 2})
				g := startInGroup(t, p, pathFor(t, mode.detached))
				g.signalWhen(t, p.started, syscall.SIGINT)
				if err := g.wait(t, 20*time.Second); err != nil {
					t.Fatalf("the launcher must go on to the program, got %v:\n%s", err, g.out.String())
				}
				out := g.out.String()
				if !strings.Contains(out, "update interrupted") || !strings.Contains(out, "AGENT_RAN") {
					t.Errorf("an interrupted update must be said, and the installed version run:\n%s", out)
				}
				if strings.Contains(out, "UPDATE_FINISHED") {
					t.Errorf("the update ran on past the Ctrl-C:\n%s", out)
				}
				lockGone(t, p, "an interrupted update")
			})
		}
	}
}

// yoloStandInWithSlowProbe is yoloStandIn, except that the launcher's probe for the verb (its
// `-- true` run) marks marker and then takes five seconds: the window a Ctrl-C can land in.
func yoloStandInWithSlowProbe(t *testing.T, marker string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := "#!/bin/sh\ncase \"$*\" in *' -- true') : > " + shellQuoteForTest(marker) + "; sleep 5; exit 0 ;; esac\n" +
		asYoloEnv + "=1 exec " + shellQuoteForTest(exe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A CTRL-C WHILE THE LAUNCHER PROBES FOR THE VERB STARTS NO UPDATE. The probe dies of it, which
// reads as "this yolo lacks the verb", and the update would then start in a fallback, attached to
// the terminal, after the user asked it to stop (unbounded on a Mac with no timeout(1)). Both
// probes: the update verb's (_bounded) and the installer re-run's (_run_without_terminal).
func TestAnInterruptDuringTheProbeStartsNoUpdate(t *testing.T) {
	installer := "#!/bin/bash\n: > \"$HOME/update.started\"\nexec sleep 120\n"
	for _, tc := range []struct {
		name string
		verb []string
	}{{"update-verb", []string{"install"}}, {"installer", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			o := boundProbeOpts{verb: tc.verb, behave: sleeps, timeout: 30, grace: 2}
			if tc.verb == nil {
				if _, err := exec.LookPath("curl"); err != nil {
					t.Skip("curl not found")
				}
				o.installerURL = serveBody(t, 200, "application/x-sh", installer)
			}
			p := newBoundProbe(t, o)
			probing := filepath.Join(p.home, "probing")
			g := startInGroup(t, p, yoloStandInWithSlowProbe(t, probing)+string(os.PathListSeparator)+pathWithout(t, "yolo"))
			g.signalWhen(t, probing, syscall.SIGINT)
			if err := g.wait(t, 20*time.Second); err != nil {
				t.Fatalf("the launcher must go on to the program, got %v:\n%s", err, g.out.String())
			}
			out := g.out.String()
			if _, err := os.Stat(p.started); err == nil {
				t.Errorf("the update started after a Ctrl-C at its probe:\n%s", out)
			}
			if !strings.Contains(out, "update interrupted") || !strings.Contains(out, "AGENT_RAN") {
				t.Errorf("an update interrupted at its probe must be said, and the installed version run:\n%s", out)
			}
			lockGone(t, p, "an update interrupted at its probe")
		})
	}
}

// AN NPM PACKAGE'S OWN UPDATE IS SHIELDED TOO. With no verb, the npm launcher's update is
// "npm install -g", which runs in the terminal's foreground group (no bound, no detach: npm takes
// no terminal input), so the Ctrl-C reaches it directly. It still ends the update, not the launch,
// and the launcher says so, since npm's own output does not say the launch goes on.
func TestAnInterruptDuringAnNpmInstallUpdateRunsTheInstalledVersion(t *testing.T) {
	p := newBoundProbe(t, boundProbeOpts{npm: true, behave: sleeps, timeout: 30, grace: 2})
	fake := t.TempDir()
	npm := "#!/bin/sh\ncase \"$1\" in\nview) echo 9.9.9 ;;\ninstall) : > " + shellQuoteForTest(p.started) +
		"; exec sleep 120 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(fake, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}
	g := startInGroup(t, p, fake+string(os.PathListSeparator)+pathWithout(t, "yolo", "npm"))
	g.signalWhen(t, p.started, syscall.SIGINT)
	if err := g.wait(t, 20*time.Second); err != nil {
		t.Fatalf("the launcher must go on to the program, got %v:\n%s", err, g.out.String())
	}
	out := g.out.String()
	if !strings.Contains(out, "update interrupted") || !strings.Contains(out, "AGENT_RAN") {
		t.Errorf("an interrupted npm update must be said, and the installed version run:\n%s", out)
	}
	lockGone(t, p, "an interrupted npm update")
}

// "yolo pack update" STOPPED BY CTRL-C LEAVES NO LOCK. Update mode exits rather than running the
// program, with a failure, and the native launcher holds the install-prefix lock across the act
// there too, so the Ctrl-C must release it: before, the launcher died of it with the lock held.
func TestAnInterruptDuringPackUpdateReleasesTheLock(t *testing.T) {
	for _, mode := range boundModes(t) {
		t.Run(mode.name, func(t *testing.T) {
			p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"}, behave: sleeps, timeout: 30, grace: 2})
			g := startInGroup(t, p, pathFor(t, mode.detached), "YOLO_PACK_UPDATE=1")
			g.signalWhen(t, p.started, syscall.SIGINT)
			err := g.wait(t, 20*time.Second)
			if err == nil {
				t.Errorf("an interrupted pack update must not report success:\n%s", g.out.String())
			}
			if strings.Contains(g.out.String(), "AGENT_RAN") {
				t.Errorf("update mode must not run the program:\n%s", g.out.String())
			}
			lockGone(t, p, "an interrupted pack update")
		})
	}
}

// A SECOND CTRL-C WHILE THE LAUNCHER RELEASES ITS LOCK LEAVES NO LOCK. Measured 2026-10-01 in a
// nested jail: a Ctrl-C typed twice, 150 ms apart, at claude's update. The first ended the update;
// the second landed after the shield came down and before the lock's rmdir had run, killed the
// launcher (and the rmdir) with the install-prefix lock still held, and the next ten minutes of
// launches skipped their update. A slow stand-in rmdir holds that window open here.
func TestASecondCtrlCWhileTheLockIsReleasedLeavesNoLock(t *testing.T) {
	realRmdir, err := exec.LookPath("rmdir")
	if err != nil {
		t.Skip("rmdir not found")
	}
	for _, mode := range boundModes(t) {
		t.Run(mode.name, func(t *testing.T) {
			p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"}, behave: sleeps, timeout: 30, grace: 2})
			releasing := filepath.Join(p.home, "releasing")
			fake := t.TempDir()
			body := "#!/bin/sh\n: > " + shellQuoteForTest(releasing) + "\nsleep 1\nexec " + shellQuoteForTest(realRmdir) + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(fake, "rmdir"), []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			g := startInGroup(t, p, fake+string(os.PathListSeparator)+pathFor(t, mode.detached))
			g.signalWhen(t, p.started, syscall.SIGINT)
			g.signalWhen(t, releasing, syscall.SIGINT)
			err := g.wait(t, 20*time.Second)
			out := g.out.String()
			lockGone(t, p, "a second Ctrl-C during the release")
			if err != nil || !strings.Contains(out, "AGENT_RAN") {
				t.Errorf("a Ctrl-C while the lock is released must not end the launch (err=%v):\n%s", err, out)
			}
		})
	}
}
