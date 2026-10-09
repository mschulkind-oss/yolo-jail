//go:build linux

package integration

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

const (
	xbThreadHelperEnv  = "YOLO_XB_THREAD_DEATH_HELPER"
	xbThreadHelperMark = "xb-owned-flock-thread-helper-v1"
	xbThreadHelperKey  = "YOLO_XB_THREAD_DEATH_KEY"
)

// This init-only helper is deliberately unreachable from a normal test run. The marker and
// exact short self-exec arguments are both required; a malformed marked invocation exits before
// TestMain can enter the container warmup path.
func init() {
	if os.Getenv(xbThreadHelperEnv) != xbThreadHelperMark {
		return
	}
	if len(os.Args) != 3 || os.Args[1] != "-test.short=true" || os.Args[2] != "-test.run=^TestXBThreadDeathHelper$" {
		os.Exit(125)
	}
	key := os.Getenv(xbThreadHelperKey)
	release := os.NewFile(3, "xb-thread-release")
	report := os.NewFile(4, "xb-thread-report")
	if key == "" || release == nil || report == nil {
		os.Exit(125)
	}
	if _, err := release.Stat(); err != nil {
		os.Exit(125)
	}
	if _, err := report.Stat(); err != nil {
		os.Exit(125)
	}
	lock, err := pidlock.Acquire(key, pidlock.NoWait, nil)
	if err != nil {
		os.Exit(125)
	}
	_ = lock // Its descriptor is process-wide; the pinned worker keeps the flock through leader exit.
	ready := make(chan struct{})
	go func() {
		goruntime.LockOSThread()
		tid := syscall.Gettid()
		if tid == os.Getpid() {
			_, _ = report.Write([]byte("invalid-worker-thread\n"))
			syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 125, 0, 0)
			return
		}
		_, _ = fmt.Fprintf(report, "ready %d %d\n", tid, goruntime.GOMAXPROCS(0))
		close(ready)
		var b [1]byte
		_, _ = release.Read(b[:]) // Parent close/EOF releases the deliberately surviving worker.
		goruntime.KeepAlive(lock) // Retain the flock's os.File until exit_group tears down this TGID.
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 0, 0, 0)
	}()
	<-ready
	// A scheduler-aware Syscall marks the main goroutine as in-syscall before the thread-only
	// exit. The runtime can retake its P so a one-P worker can resume after the parent closes
	// the release pipe; RawSyscall here would strand that P.
	syscall.Syscall(syscall.SYS_EXIT, 0, 0, 0)
	os.Exit(126)
}

// Selected only by the exact self-exec filter above. The work occurs before TestMain, in init.
func TestXBThreadDeathHelper(t *testing.T) {}

func TestBackgroundAdvanceOwnedThreadGroupDeathWait(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "private", "real-flock")
	releaseR, releaseW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reportR, reportW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.short=true", "-test.run=^TestXBThreadDeathHelper$")
	cmd.ExtraFiles = []*os.File{releaseR, reportW}
	cmd.Env = xbThreadHelperEnvironment(key)
	if err := cmd.Start(); err != nil {
		releaseR.Close()
		releaseW.Close()
		reportR.Close()
		reportW.Close()
		t.Fatalf("start isolated thread helper: %v", err)
	}
	_ = releaseR.Close()
	_ = reportW.Close()
	p := xbProcess{pid: cmd.Process.Pid}
	if _, stat, ok := readStat(p.pid); ok && len(stat) >= 20 {
		p.birth = stat[19]
	}
	var waitStarted bool
	waitDone := make(chan error, 1)
	startWait := func() <-chan error {
		if !waitStarted {
			waitStarted = true
			go func() { waitDone <- cmd.Wait() }()
		}
		return waitDone
	}
	cleaned := false
	t.Cleanup(func() {
		_ = releaseW.Close() // EOF also releases a helper whose readiness assertion failed.
		_ = reportR.Close()
		if p.birth == "" {
			if _, stat, ok := readStat(p.pid); ok && len(stat) >= 20 {
				p.birth = stat[19]
			}
		}
		if !cleaned {
			// This Cmd is the test's directly-owned child; if procfs could not supply a
			// birth identity, use that owned Process handle rather than leave it unmanaged.
			if p.birth == "" || !p.ended() {
				_ = cmd.Process.Kill()
			}
			select {
			case <-startWait():
			case <-time.After(5 * time.Second):
				if p.birth == "" || !p.ended() {
					_ = cmd.Process.Kill()
				}
				select {
				case <-waitDone:
				case <-time.After(5 * time.Second):
					t.Errorf("owned helper process %d did not exit and reap after EOF/SIGKILL", p.pid)
				}
			}
		}
	})

	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reportR).ReadString('\n')
		lineCh <- line
	}()
	var line string
	select {
	case line = <-lineCh:
	case <-time.After(5 * time.Second):
		t.Fatal("owned helper did not report its pinned worker")
	}
	var worker, helperPs int
	if n, err := fmt.Sscanf(line, "ready %d %d", &worker, &helperPs); err != nil || n != 2 || worker <= 0 || worker == p.pid || helperPs != 1 {
		t.Fatalf("helper worker report %q (parse %v, fields %d, Ps %d), want one-P helper", line, err, n, helperPs)
	}
	if p.birth == "" {
		t.Fatalf("cannot capture helper birth identity for pid %d", p.pid)
	}
	if p.ended() {
		t.Fatal("owned helper was considered ended before the parent released its worker")
	}
	if !pidlock.Held(key) {
		t.Fatal("real private flock is not held while the owned worker is live")
	}
	if !xbAwaitShort(5*time.Second, func() bool {
		state, found, err := processState(p.pid)
		return err == nil && found && state == "Z"
	}) {
		state, found, err := processState(p.pid)
		t.Fatalf("leader did not become zombie while worker remained: state=%q found=%v err=%v", state, found, err)
	}
	workerState, found, err := processState(worker)
	if err != nil || !found || xbTerminalTaskState(workerState) {
		t.Fatalf("owned pinned worker is not live after leader Z: state=%q found=%v err=%v", workerState, found, err)
	}
	if !p.alive() || p.ended() {
		t.Fatal("owned process predicates did not distinguish a zombie leader from its live task")
	}
	wrongBirth := p
	wrongBirth.birth += "-reused"
	if !wrongBirth.ended() || wrongBirth.alive() {
		t.Fatal("birth mismatch was not treated as a different, ended identity")
	}

	// The exact wait function used by the real SIGTERM/SIGKILL fixture must remain blocked
	// while this matching owned group has a runnable worker and a held kernel lock.
	waitReturned := make(chan struct{})
	go func() {
		xbWaitOwnedProcessEnd(t, "owned thread-group death", p)
		close(waitReturned)
	}()
	select {
	case <-waitReturned:
		t.Fatal("actual owned-process wait returned while the worker still held the flock")
	case <-time.After(200 * time.Millisecond):
	}
	if !pidlock.Held(key) {
		t.Fatal("kernel flock was not held during the actual death wait")
	}
	_ = releaseW.Close()
	select {
	case <-waitReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("actual owned-process wait did not return after the whole thread group exited")
	}
	if !xbAwaitShort(5*time.Second, func() bool { return p.ended() }) {
		t.Fatal("actual owned-process wait returned before the full group became terminal")
	}
	state, found, err := processState(p.pid)
	if err != nil || !found || state != "Z" {
		t.Fatalf("fully-dead group was not still an unreaped zombie control: state=%q found=%v err=%v", state, found, err)
	}
	if p.alive() || !p.ended() {
		t.Fatal("fully dead but unreaped owned group was not accepted as ended")
	}
	// Keep this kernel-level assertion separate and after the completed group-death wait: it
	// cannot be made to pass by merely polling for eventual unlock while the owner is alive.
	if pidlock.Held(key) {
		t.Fatal("fully dead owned group still holds its private kernel flock")
	}
	if err := <-startWait(); err != nil {
		t.Fatalf("reap owned helper: %v", err)
	}
	cleaned = true

	testConservativeXBReadFailures(t, p)
	testXBLeaderZombieTolerance(t)
}

func xbThreadHelperEnvironment(key string) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, xbThreadHelperEnv+"=") || strings.HasPrefix(value, xbThreadHelperKey+"=") || strings.HasPrefix(value, "GOMAXPROCS=") {
			continue
		}
		env = append(env, value)
	}
	return append(env, xbThreadHelperEnv+"="+xbThreadHelperMark, xbThreadHelperKey+"="+key, "GOMAXPROCS=1")
}

func xbAwaitShort(timeout time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ok()
}

func testConservativeXBReadFailures(t *testing.T, p xbProcess) {
	t.Helper()
	readLeader := func(int) (xbProcStat, bool, error) {
		return xbProcStat{state: "Z", birth: p.birth}, false, nil
	}
	readTasks := func(int) ([]xbTaskState, bool, error) {
		return nil, false, errors.New("fixture proc read denied")
	}
	alive, ended := xbOwnedThreadGroup(p, readLeader, readTasks)
	if alive || ended {
		t.Fatalf("unreadable task snapshot was not conservative: alive=%v ended=%v", alive, ended)
	}
	if _, err := xbParseProcStat([]byte("malformed")); err == nil {
		t.Fatal("malformed proc stat unexpectedly parsed as a terminal task")
	}
}

func testXBLeaderZombieTolerance(t *testing.T) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start single-thread zombie control: %v", err)
	}
	p := xbProcess{pid: cmd.Process.Pid}
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	p = xbRemember(t, p.pid)
	if err := syscall.Kill(p.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill single-thread control: %v", err)
	}
	if !xbAwaitShort(5*time.Second, func() bool {
		state, found, err := processState(p.pid)
		return err == nil && found && state == "Z"
	}) {
		t.Fatal("single-thread control did not remain as an unreaped zombie")
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", p.pid))
	if err != nil || len(entries) != 1 {
		t.Fatalf("single-thread zombie task snapshot = %d, %v", len(entries), err)
	}
	if !p.ended() || p.alive() {
		t.Fatal("terminal single-thread zombie was not accepted as ended")
	}
	if err := cmd.Wait(); err != nil {
		// SIGKILL normally yields an ExitError; the process was still correctly reaped.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("reap single-thread control: %v", err)
		}
	}
	reaped = true
}

func TestXBThreadDeathWaitIsUsedByTheSignalFixture(t *testing.T) {
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate Linux regression source")
	}
	source := filepath.Join(filepath.Dir(thisFile), "backgroundadvance_linux_test.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, source, nil, 0)
	if err != nil {
		t.Fatalf("parse actual fixture source: %v", err)
	}
	calls := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestBackgroundAdvanceSignalsFenceAndRecoverItsRealBuildWorkspace" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.Ident)
			if !ok || selector.Name != "xbWaitOwnedProcessEnd" || len(call.Args) != 3 {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			what, err := strconv.Unquote(literal.Value)
			if err == nil && what == "signalled background owner exit" {
				if ident, ok := call.Args[2].(*ast.Ident); ok && ident.Name == "bg" {
					calls++
				}
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("signal fixture must call the exercised owned-thread death wait exactly once; found %d", calls)
	}
}
