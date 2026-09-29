package ghbroker

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// A broker stopped during a long read. Every run dir holds a copy of the host's hosts.yml,
// so a broker that dies before its deferred cleanup must not leave it behind for good, and
// the gh it was running must not outlive it. No test here reaches GitHub: gh is a fake that
// sleeps.

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func gone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// deadPID is the pid of a process that has exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("/bin/sh", "-c", "exit 0")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

// Shutdown ends a gh in flight, so the call returns at once instead of when gh would have.
func TestShutdownEndsTheGHInFlight(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", nil)
	done := make(chan RunResult, 1)
	var s sinks
	start := time.Now()
	go func() { done <- f.r.Run([]string{"sleep", "30"}, nil, s.stdout, s.stderr) }()
	waitFor(t, "gh to start", 10*time.Second, func() bool {
		entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls"))
		return len(entries) == 1
	})
	f.r.Shutdown()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not return after Shutdown")
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	var s2 sinks
	if res := f.r.Run([]string{"pr", "view"}, nil, s2.stdout, s2.stderr); res.Exit != ExitUnavailable ||
		!strings.Contains(s2.errOut.String(), "stopping") {
		t.Fatalf("a call after Shutdown: %+v %q", res, s2.errOut.String())
	}
}

// A run dir a killed owner left is collected by the next broker and the next self-check to
// start, by liveness: a live owner's is kept, and so is one whose name carries no pid.
func TestTheNextStartCollectsARunDirADeadOwnerLeft(t *testing.T) {
	for _, via := range []string{"broker", "self-check"} {
		t.Run(via, func(t *testing.T) {
			t.Setenv("HOME", resolvedDir(t))
			t.Setenv("PATH", resolvedDir(t)) // no gh: nothing runs
			dead := filepath.Join(runRoot(), strconv.Itoa(deadPID(t))+"-aaaa")
			live := filepath.Join(runRoot(), strconv.Itoa(os.Getpid())+"-bbbb")
			unnamed := filepath.Join(runRoot(), "0123456789abcdef")
			for _, d := range []string{dead, live, unnamed} {
				if err := os.MkdirAll(filepath.Join(d, "config"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(d, "config", "hosts.yml"), []byte("oauth_token: x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if via == "broker" {
				_, cleanup := newBroker(brokerscope.File{Workspace: "/w"}, "/w", &bytes.Buffer{})
				cleanup()
			} else {
				SelfCheck(&bytes.Buffer{})
			}
			if _, err := os.Stat(dead); !os.IsNotExist(err) {
				t.Fatal("a dead broker's hosts.yml copy was not collected")
			}
			for _, keep := range []string{live, unnamed} {
				if _, err := os.Stat(keep); err != nil {
					t.Fatalf("%s was collected: %v", keep, err)
				}
			}
		})
	}
}

// TestBrokerMainHelper is the broker's own process for the tests below: `yolo internal
// daemon github-broker` is Main, and this runs Main with the argv after "--".
func TestBrokerMainHelper(t *testing.T) {
	if os.Getenv("GHBROKER_MAIN_HELPER") != "1" {
		t.Skip("run as a helper process only")
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Exit(Main(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

// brokerProcess is a real broker, Main in a process of its own in its own session (as the
// launcher spawns it), behind yolo's real front, over a gh that sleeps in `run watch` and
// writes its pid first.
type brokerProcess struct {
	cmd      *exec.Cmd
	done     chan struct{} // closed once the broker process is reaped
	endpoint string
	ghPID    string
	stopFrnt chan struct{}
}

func startBrokerProcess(t *testing.T) *brokerProcess {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("the fake gh needs /bin/sh")
	}
	root := resolvedDir(t)
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	fake := filepath.Join(root, "fake")
	if err := os.MkdirAll(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &brokerProcess{ghPID: filepath.Join(root, "gh.pid"), stopFrnt: make(chan struct{}),
		done: make(chan struct{})}
	script := "#!/bin/sh\ncase \"$1\" in\n  --version) echo 'gh version 2.101.0 (fake)'; exit 0 ;;\n" +
		"  auth) echo '" + fakeToken + "'; exit 0 ;;\nesac\n" +
		"echo $$ > '" + p.ghPID + "'\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(fake, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "ghcfg")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "hosts.yml"), []byte("github.com:\n    oauth_token: "+fakeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "ws")
	sf, err := brokerscope.Write(brokerscope.File{Source: Source, LaunchID: "cafe", PID: os.Getpid(),
		Workspace: ws, Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("/tmp", "ghb-") // AF_UNIX paths are short on darwin
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "d.sock")
	p.cmd = exec.Command(os.Args[0], "-test.run=^TestBrokerMainHelper$", "-test.count=1", "--",
		"--socket", sock, "--scope-file", sf)
	p.cmd.Env = append(os.Environ(), "GHBROKER_MAIN_HELPER=1", "HOME="+home, "GH_CONFIG_DIR="+cfg,
		"PATH="+fake+":/usr/bin:/bin")
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var logs bytes.Buffer
	p.cmd.Stdout, p.cmd.Stderr = &logs, &logs
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
		if b, err := os.ReadFile(p.ghPID); err == nil {
			if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		close(p.stopFrnt)
		if t.Failed() {
			t.Logf("broker output:\n%s", logs.String())
		}
	})
	waitFor(t, "the broker's socket", 20*time.Second, func() bool { _, err := os.Stat(sock); return err == nil })
	epDir := resolvedDir(t)
	if err := os.Chmod(epDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p.endpoint = filepath.Join(epDir, "github-broker.endpoint")
	go func() { _ = svcendpoint.ServeFront(p.endpoint, "127.0.0.1", sock, p.stopFrnt) }()
	waitFor(t, "the front", 10*time.Second, func() bool { return svcendpoint.Probe(p.endpoint) })
	return p
}

// forwardLongRead sends `gh run watch 1 -R o/r`, a standing read, and waits until the host
// gh runs it. It returns the gh's pid and the broker's run dir.
func (p *brokerProcess) forwardLongRead(t *testing.T) (ghPID int, runDir string) {
	t.Helper()
	go func() {
		env := func(k string) string {
			if k == EndpointEnv {
				return p.endpoint
			}
			return ""
		}
		Forward([]string{"run", "watch", "1", "-R", "o/r"}, ForwardEnv{Getenv: env, Stdin: strings.NewReader(""),
			Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, OriginRepo: func() string { return "o/r" }})
	}()
	waitFor(t, "the host gh to start", 20*time.Second, func() bool {
		b, err := os.ReadFile(p.ghPID)
		ghPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && ghPID > 0
	})
	dirs, _ := filepath.Glob(filepath.Join(runRoot(), strconv.Itoa(p.cmd.Process.Pid)+"-*"))
	if len(dirs) != 1 {
		t.Fatalf("the broker's run dir: %v", dirs)
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "config", "hosts.yml")); err != nil {
		t.Fatalf("no hosts.yml copy in the run dir: %v", err)
	}
	return ghPID, dirs[0]
}

func (p *brokerProcess) exited(d time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(d):
		return false
	}
}

// Stopped the way the launcher stops it — SIGTERM to its process group, SIGKILL after a
// 5 s grace — during a long read, the broker ends its gh, returns inside the grace, and its
// cleanup removes the hosts.yml copy.
func TestAStoppedBrokerEndsItsGHAndRemovesItsCopy(t *testing.T) {
	p := startBrokerProcess(t)
	ghPID, runDir := p.forwardLongRead(t)
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if !p.exited(4 * time.Second) {
		t.Fatal("the broker was still running 4 s after SIGTERM; the launcher would have SIGKILLed it")
	}
	waitFor(t, "the host gh to end", 5*time.Second, func() bool { return gone(ghPID) })
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("the run dir, and its hosts.yml copy, outlived the broker: %v", err)
	}
}

// SIGKILLed outright (the launcher's straggler kill, an OOM), the broker runs no cleanup.
// On Linux its gh dies with it; everywhere, the next broker to start collects the copy.
func TestAKilledBrokersCopyIsCollectedByTheNextStart(t *testing.T) {
	p := startBrokerProcess(t)
	ghPID, runDir := p.forwardLongRead(t)
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !p.exited(5 * time.Second) {
		t.Fatal("SIGKILL did not end the broker")
	}
	if runtime.GOOS == "linux" {
		waitFor(t, "the host gh to die with its broker", 5*time.Second, func() bool { return gone(ghPID) })
	}
	if _, err := os.Stat(runDir); err != nil {
		t.Fatalf("expected the killed broker's run dir to remain until the next start: %v", err)
	}
	t.Setenv("PATH", resolvedDir(t))
	_, cleanup := newBroker(brokerscope.File{Workspace: "/w"}, "/w", &bytes.Buffer{})
	cleanup()
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("the next broker did not collect the killed one's hosts.yml copy: %v", err)
	}
}
