//go:build linux

package lingerprobe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const testCtrID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The death trigger, against a real inotify watch: a temp file conmon renames
// into place does not fire, the rename does, and another container's exit file
// never does.
func TestExitWatchFiresOnTheRenameIntoPlaceOnly(t *testing.T) {
	dir := t.TempDir()
	w, err := watchExitFile(dir, testCtrID[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	other := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	writeFile(t, filepath.Join(dir, other), "0")
	tmp := filepath.Join(dir, testCtrID+".Xy12Ab")
	writeFile(t, tmp, "137")
	select {
	case <-w.Death():
		t.Fatal("fired on a temp file or another container's exit file")
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.Rename(tmp, filepath.Join(dir, testCtrID)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Death():
	case <-time.After(2 * time.Second):
		t.Fatal("the exit file appeared and the watch never fired")
	}
}

// A container that died before the watch existed is still seen.
func TestExitWatchSeesADeathThatPrecededIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, testCtrID), "0")
	w, err := watchExitFile(dir, testCtrID[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	select {
	case <-w.Death():
	case <-time.After(time.Second):
		t.Fatal("an exit file already present was missed: the listing-after-watch is gone")
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- real blocked processes ----------------------------------------------------

// TestHelperProcess is not a test: it is the body of the child processes the
// real-process tests sample. LINGERPROBE_HELPER picks what it blocks in.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("LINGERPROBE_HELPER") {
	case "read":
		var b [1]byte
		_, _ = syscall.Read(0, b[:]) // a raw read(2) on fd 0, which is a pty slave
		os.Exit(0)
	case "flock":
		f, err := os.Open(os.Getenv("LINGERPROBE_LOCK"))
		if err != nil {
			os.Exit(2)
		}
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX) // blocks: the test holds it
		os.Exit(0)
	}
}

func helper(t *testing.T, mode string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	c.Env = append(append(os.Environ(), "LINGERPROBE_HELPER="+mode), extraEnv...)
	return c
}

func openTestPty(t *testing.T) (master, slave *os.File, name string) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	name = "/dev/pts/" + strconv.Itoa(n)
	s, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	return m, s, name
}

// waitForCall samples until some thread of pid is in want (the child needs a
// moment to reach its syscall), and returns the last line seen.
func waitForCall(t *testing.T, pid int, want string) (Snapshot, string) {
	t.Helper()
	s := Sampler{Root: "/proc", Name: syscallName, Clock: clockNow}
	var snap Snapshot
	var line string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		snap = s.Sample(pid)
		line = snap.Line()
		if strings.Contains(line, want) {
			return snap, line
		}
	}
	t.Fatalf("pid %d never showed %q; last sample:\n%s", pid, want, line)
	return snap, line
}

// THE REAL-PROCESS CHECK: a child blocked in read(0) on a pty, named as such
// from the real /proc — the exact state the stdin hypothesis predicts podman is in.
func TestRealProcessBlockedReadingAPty(t *testing.T) {
	master, slave, ptsName := openTestPty(t)
	defer master.Close()
	c := helper(t, "read")
	c.Stdin = slave
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()

	want := "read(0 → " + ptsName + ")"
	snap, line := waitForCall(t, c.Process.Pid, want)
	t.Logf("sample: %s", line)
	var ty tally
	ty.add(snap)
	if got := ty.dominant(); got != "blocked in "+want {
		t.Errorf("dominant = %q, want %q", got, "blocked in "+want)
	}
}

// And one blocked in flock on a lock this test holds — the containers/storage
// lock shape.
func TestRealProcessBlockedInFlock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "storage.lock")
	writeFile(t, lock, "")
	held, err := os.Open(lock)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	c := helper(t, "flock", "LINGERPROBE_LOCK="+lock)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()

	snap, line := waitForCall(t, c.Process.Pid, "flock(")
	t.Logf("sample: %s", line)
	resolved, _ := filepath.EvalSymlinks(lock)
	if !strings.Contains(line, " → "+lock+")") && !strings.Contains(line, " → "+resolved+")") {
		t.Errorf("flock not attributed to the lock file %s:\n%s", lock, line)
	}
	var ty tally
	ty.add(snap)
	if got := ty.dominant(); !strings.HasPrefix(got, "blocked in flock(") {
		t.Errorf("dominant = %q", got)
	}
}

// ---- the probe end to end ------------------------------------------------------

type noteLog struct {
	mu    sync.Mutex
	lines []string
}

func (n *noteLog) emit(name, detail string) {
	n.mu.Lock()
	n.lines = append(n.lines, name+"  "+detail)
	n.mu.Unlock()
}

func (n *noteLog) snapshot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.lines...)
}

// The whole probe against a real lingering process: nothing is sampled before
// the exit file appears, the death fires OnDeath and the host note, samples
// follow at the interval naming the blocked call, and after Stop not one more
// line is emitted.
func TestProbeSamplesOnlyAfterTheDeathAndGoesSilentAtStop(t *testing.T) {
	master, slave, ptsName := openTestPty(t)
	defer master.Close()
	c := helper(t, "read")
	c.Stdin = slave
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()

	exits := t.TempDir()
	var notes noteLog
	var deathAt time.Time
	p, err := Start(Config{
		PID: c.Process.Pid, CtrID: testCtrID[:12], CtrName: "yolo-ws-test", ExitDir: exits,
		UID: os.Getuid(), Delay: 50 * time.Millisecond, Interval: 40 * time.Millisecond,
		Emit:    notes.emit,
		OnDeath: func(at time.Time) { deathAt = at },
		PtyMode: func() string { return "icanon=off isig=off echo=off" },
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := notes.snapshot(); len(got) != 0 {
		t.Fatalf("the probe emitted before any death — a normal session must cost nothing:\n%v", got)
	}

	writeFile(t, filepath.Join(exits, testCtrID), "0")
	want := "read(0 → " + ptsName + ")"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(notes.snapshot(), "\n"), want) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	res := p.Stop()
	after := len(notes.snapshot())
	time.Sleep(200 * time.Millisecond)
	got := notes.snapshot()
	joined := strings.Join(got, "\n")

	if !res.DeathSeen || deathAt.IsZero() {
		t.Errorf("death not seen: %+v", res)
	}
	if !strings.Contains(joined, NoteHost+"  podman_procs=") {
		t.Errorf("no host-context note at the death:\n%s", joined)
	}
	if !strings.Contains(joined, NotePty+"  icanon=off isig=off echo=off") {
		t.Errorf("no pty-mode note at the death:\n%s", joined)
	}
	if !strings.Contains(joined, NoteSample+"  +") || !strings.Contains(joined, want) {
		t.Errorf("no sample naming %s:\n%s", want, joined)
	}
	if res.Samples == 0 || res.Dominant != "blocked in "+want {
		t.Errorf("result = %+v, want the read named as dominant", res)
	}
	if len(got) != after {
		t.Errorf("%d lines landed AFTER Stop returned — the report would print before the sampler "+
			"finished:\n%s", len(got)-after, joined)
	}
}

// The terminate arm's last sample: taken synchronously, tagged, and a probe
// that never saw a death takes none.
func TestFinalSampleIsTaggedAndNeedsADeath(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	exits := t.TempDir()
	var notes noteLog
	p, err := Start(Config{PID: c.Process.Pid, CtrID: testCtrID, ExitDir: exits,
		Delay: time.Hour, Emit: notes.emit})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	p.FinalSample("final (SIGTERM)", time.Second)
	if len(notes.snapshot()) != 0 {
		t.Fatal("a final sample was taken with no death seen")
	}
	writeFile(t, filepath.Join(exits, testCtrID), "0")
	for i := 0; i < 100 && len(notes.snapshot()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	p.FinalSample("final (SIGTERM)", time.Second)
	if joined := strings.Join(notes.snapshot(), "\n"); !strings.Contains(joined, "final (SIGTERM): pid ") {
		t.Errorf("no tagged final sample:\n%s", joined)
	}
}

// A sample IN FLIGHT when Stop runs — its /proc reads done, its line not yet
// written — must be dropped: Stop's promise is that nothing is written after it
// returns, which is what lets the report print right behind it.
func TestASampleInFlightAtStopIsDropped(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	inFlight, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	sampleReadHook = func() {
		once.Do(func() { close(inFlight); <-release })
	}
	defer func() { sampleReadHook = nil }()

	exits := t.TempDir()
	var notes noteLog
	p, err := Start(Config{PID: c.Process.Pid, CtrID: testCtrID, ExitDir: exits,
		Delay: time.Millisecond, Interval: time.Hour, Emit: notes.emit})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(exits, testCtrID), "0")
	select {
	case <-inFlight:
	case <-time.After(3 * time.Second):
		t.Fatal("no sample started")
	}
	before := len(notes.snapshot())
	p.Stop()
	close(release)
	time.Sleep(100 * time.Millisecond)
	if got := notes.snapshot(); len(got) != before {
		t.Errorf("the in-flight sample was written after Stop: %v", got[before:])
	}
}
