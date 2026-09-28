package entrypoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// The subprocess half of these tests. TestMain (iopriority_main_test.go) dispatches on
// ioTestModeEnv before any test runs, so the test binary can play the entrypoint: apply a
// priority, be re-executed by applyIOPriority's real syscall.Exec, and report what every
// thread of the second image holds.
const (
	ioTestModeEnv   = "YOLO_IOPRIO_TEST_MODE"
	ioTestImagesEnv = "YOLO_IOPRIO_TEST_IMAGES"
	ioReportPrefix  = "IOPRIO-REPORT "
)

// ioChildReport is what the second image saw. The parent test does the asserting, so a
// failure prints the whole picture rather than the first thread that disagreed.
type ioChildReport struct {
	Images      int      `json:"images"`
	Warning     string   `json:"warning"`
	Note        string   `json:"note"`
	MarkerInEnv bool     `json:"marker_in_env"`
	Threads     []int    `json:"threads"`
	Children    []int    `json:"children"`
	ChildEnvs   []string `json:"child_envs"`
}

// runIOPriorityChild is the test binary as entrypoint. The first image starts busy threads
// so the runtime is well past one thread before the set, as the real entrypoint's is; the
// second image then starts more threads, and children from other goroutines, and reads
// every one of them.
func runIOPriorityChild() {
	images, _ := strconv.Atoi(os.Getenv(ioTestImagesEnv))
	images++
	_ = os.Setenv(ioTestImagesEnv, strconv.Itoa(images))
	if images == 1 {
		for i := 0; i < 8; i++ {
			go func() {
				runtime.LockOSThread()
				for {
					time.Sleep(time.Millisecond)
				}
			}()
		}
		time.Sleep(50 * time.Millisecond)
	}
	out := applyIOPriority(os.Args)
	rep := ioChildReport{Images: images, Warning: out.warning, Note: out.note,
		MarkerInEnv: os.Getenv(ioprio.ReexecMarkerEnv) != ""}

	release := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			runtime.LockOSThread()
			<-release
		}()
	}
	var mu sync.Mutex
	var kids []*exec.Cmd
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread() // each child forks from a thread of its own
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), ioTestModeEnv+"=sleep")
			if err := cmd.Start(); err == nil {
				mu.Lock()
				kids = append(kids, cmd)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	entries, _ := os.ReadDir("/proc/self/task")
	for _, ent := range entries {
		tid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		if v, err := ioprio.GetThread(tid); err == nil {
			rep.Threads = append(rep.Threads, v)
		}
	}
	for _, k := range kids {
		if v, err := ioprio.GetThread(k.Process.Pid); err == nil {
			rep.Children = append(rep.Children, v)
		}
		env, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(k.Process.Pid), "environ"))
		rep.ChildEnvs = append(rep.ChildEnvs, string(env))
		_ = k.Process.Kill()
		_ = k.Wait()
	}
	close(release)
	b, _ := json.Marshal(rep)
	os.Stdout.WriteString(ioReportPrefix + string(b) + "\n")
}

// TestIOPriorityReachesEveryThreadAfterOneReexec is the mechanism end to end, with the real
// syscalls and the real exec: every thread of the re-executed image, and every child started
// from another goroutine, holds BE7; there were exactly two images; and the marker reached
// neither the second image's environment nor any child's.
func TestIOPriorityReachesEveryThreadAfterOneReexec(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process I/O priority is a Linux call")
	}
	if _, err := ioprio.GetThread(0); err != nil {
		t.Skipf("ioprio_get is not available here (%v)", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, ioprio.ReexecMarkerEnv+"=") && !strings.HasPrefix(kv, ioprio.EnvVar+"=") &&
			!strings.HasPrefix(kv, ioTestImagesEnv+"=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, ioTestModeEnv+"=child", ioprio.EnvVar+"=low")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the child failed: %v\nstdout:\n%s\nstderr:\n%s", err, &stdout, &stderr)
	}
	line := ""
	for _, l := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(l, ioReportPrefix) {
			line = strings.TrimPrefix(l, ioReportPrefix)
		}
	}
	var rep ioChildReport
	if err := json.Unmarshal([]byte(line), &rep); err != nil {
		t.Fatalf("no report from the child (%v):\n%s\n%s", err, &stdout, &stderr)
	}
	want, _ := ioprio.Low.KernelValue()
	if rep.Images != 2 {
		t.Errorf("the entrypoint ran %d images; exactly one re-exec means 2", rep.Images)
	}
	if rep.Warning != "" || !strings.Contains(rep.Note, "be/7") {
		t.Errorf("outcome warning=%q note=%q, want a log-only note naming be/7", rep.Warning, rep.Note)
	}
	if rep.MarkerInEnv {
		t.Error("the re-exec marker is still in the second image's environment")
	}
	if len(rep.Threads) < 9 {
		t.Errorf("read only %d threads; the child starts at least 9, so this proves little", len(rep.Threads))
	}
	for i, v := range rep.Threads {
		if v != want {
			t.Errorf("thread %d of the re-executed image reads %s, want be/7 (all: %v)", i, ioprio.Describe(v), rep.Threads)
		}
	}
	if len(rep.Children) != 4 {
		t.Fatalf("read %d children, want 4", len(rep.Children))
	}
	for i, v := range rep.Children {
		if v != want {
			t.Errorf("child %d, started from another goroutine, reads %s, want be/7", i, ioprio.Describe(v))
		}
	}
	for i, env := range rep.ChildEnvs {
		if strings.Contains(env, ioprio.ReexecMarkerEnv+"=") {
			t.Errorf("child %d inherited the re-exec marker", i)
		}
	}
}

// fakeIOSyscalls replaces the three seams for an in-process test and records the calls. The
// exec fake is installed even where a test expects no exec: a missed fake would re-execute
// the test binary itself.
type ioCalls struct {
	sets  []int
	execs int
}

func fakeIOSyscalls(t *testing.T, setErr func(v int) error, execErr error, get func() (int, error)) *ioCalls {
	t.Helper()
	calls := &ioCalls{}
	oldSet, oldGet, oldExec := ioSetThread, ioGetThread, ioExecSelf
	ioSetThread = func(v int) error {
		calls.sets = append(calls.sets, v)
		if setErr != nil {
			return setErr(v)
		}
		return nil
	}
	ioGetThread = func(int) (int, error) {
		if get != nil {
			return get()
		}
		return 0, nil
	}
	ioExecSelf = func([]string, []string) error { calls.execs++; return execErr }
	t.Cleanup(func() { ioSetThread, ioGetThread, ioExecSelf = oldSet, oldGet, oldExec })
	t.Setenv(ioprio.ReexecMarkerEnv, "")
	_ = os.Unsetenv(ioprio.ReexecMarkerEnv)
	return calls
}

// TestIOPriorityUndeclaredMakesNoCall: absent and "normal" touch nothing — no set, no exec,
// nothing reported — so a jail that declares nothing keeps its launcher's class.
func TestIOPriorityUndeclaredMakesNoCall(t *testing.T) {
	for _, v := range []string{"", "normal"} {
		calls := fakeIOSyscalls(t, nil, nil, nil)
		t.Setenv(ioprio.EnvVar, v)
		if out := applyIOPriority([]string{"yolo-entrypoint"}); out != (ioPriorityOutcome{}) {
			t.Errorf("%q: outcome %+v, want nothing", v, out)
		}
		if len(calls.sets) != 0 || calls.execs != 0 {
			t.Errorf("%q: %d sets and %d execs, want none", v, len(calls.sets), calls.execs)
		}
	}
}

// TestIOPriorityFailedSetSkipsTheReexec: an LSM denial or an unknown syscall leaves nothing
// set anywhere, so there is nothing to carry and nothing to reset (IO-D4).
func TestIOPriorityFailedSetSkipsTheReexec(t *testing.T) {
	calls := fakeIOSyscalls(t, func(int) error { return syscall.EPERM }, nil, nil)
	t.Setenv(ioprio.EnvVar, "idle")
	out := applyIOPriority([]string{"yolo-entrypoint"})
	if calls.execs != 0 || len(calls.sets) != 1 {
		t.Errorf("sets %v, execs %d; a failed set must not re-exec", calls.sets, calls.execs)
	}
	if !strings.Contains(out.warning, "could not be set") || !strings.Contains(out.warning, "default disk priority") {
		t.Errorf("warning %q must say the set failed and what the jail runs at", out.warning)
	}
}

// TestIOPriorityFailedExecResetsTheThread: the pinned thread is reset to unset, so the boot
// goes on with every thread unset; a reset that fails too is named, because that thread's
// children would inherit the class.
func TestIOPriorityFailedExecResetsTheThread(t *testing.T) {
	low, _ := ioprio.Low.KernelValue()
	calls := fakeIOSyscalls(t, nil, errors.New("exec format error"), nil)
	t.Setenv(ioprio.EnvVar, "low")
	out := applyIOPriority([]string{"yolo-entrypoint"})
	if calls.execs != 1 || len(calls.sets) != 2 || calls.sets[0] != low || calls.sets[1] != 0 {
		t.Fatalf("sets %v, execs %d; want the class, one exec, then a reset to 0", calls.sets, calls.execs)
	}
	if !strings.Contains(out.warning, "re-exec") || !strings.Contains(out.warning, "It was reset") {
		t.Errorf("warning %q must name the failed re-exec and the reset", out.warning)
	}

	calls = fakeIOSyscalls(t, func(v int) error {
		if v == 0 {
			return syscall.EPERM
		}
		return nil
	}, errors.New("exec format error"), nil)
	t.Setenv(ioprio.EnvVar, "low")
	out = applyIOPriority([]string{"yolo-entrypoint"})
	if len(calls.sets) != 2 || calls.sets[1] != 0 {
		t.Errorf("sets %v; the reset must still be attempted", calls.sets)
	}
	if !strings.Contains(out.warning, "Resetting that thread failed too") {
		t.Errorf("a failed reset must be named: %q", out.warning)
	}
}

// TestIOPriorityUnrecognizedValueIsWarnedNotApplied: only a launcher/entrypoint skew can
// produce one, and the warning says so.
func TestIOPriorityUnrecognizedValueIsWarnedNotApplied(t *testing.T) {
	calls := fakeIOSyscalls(t, nil, nil, nil)
	t.Setenv(ioprio.EnvVar, "realtime")
	out := applyIOPriority([]string{"yolo-entrypoint"})
	if len(calls.sets) != 0 || calls.execs != 0 {
		t.Errorf("an unrecognized value made %d sets and %d execs", len(calls.sets), calls.execs)
	}
	if !strings.Contains(out.warning, `"realtime"`) || !strings.Contains(out.warning, "different yolo versions") {
		t.Errorf("warning %q", out.warning)
	}
}

// TestIOPrioritySecondImageRemovesTheMarkerAndVerifies: the re-executed image removes the
// marker before anything else reads the environment, never re-executes, and records what
// its thread actually holds — a mismatch is a warning, not a silent success.
func TestIOPrioritySecondImageRemovesTheMarkerAndVerifies(t *testing.T) {
	low, _ := ioprio.Low.KernelValue()
	calls := fakeIOSyscalls(t, nil, nil, func() (int, error) { return low, nil })
	t.Setenv(ioprio.EnvVar, "low")
	t.Setenv(ioprio.ReexecMarkerEnv, "1")
	out := applyIOPriority([]string{"yolo-entrypoint"})
	if _, present := os.LookupEnv(ioprio.ReexecMarkerEnv); present {
		t.Error("the second image left the marker in its environment")
	}
	if calls.execs != 0 || len(calls.sets) != 0 {
		t.Errorf("the second image made %d sets and %d execs, want none", len(calls.sets), calls.execs)
	}
	if out.warning != "" || !strings.Contains(out.note, "be/7") {
		t.Errorf("outcome %+v, want a note naming be/7", out)
	}

	fakeIOSyscalls(t, nil, nil, func() (int, error) { return 0, nil })
	t.Setenv(ioprio.EnvVar, "low")
	t.Setenv(ioprio.ReexecMarkerEnv, "1")
	if out := applyIOPriority(nil); !strings.Contains(out.warning, "none/0") {
		t.Errorf("a second image whose thread reads unset must warn: %+v", out)
	}
}

// TestReportIOPrioritySplitsTheTwoSinks: a failure is on the boot stream and names the key;
// a success is in boot.log alone.
func TestReportIOPrioritySplitsTheTwoSinks(t *testing.T) {
	var stderr, logOnly bytes.Buffer
	e := &Env{Stderr: &stderr, LogOnly: &logOnly}
	reportIOPriority(e, ioPriorityOutcome{note: "applied"})
	if stderr.Len() != 0 || !strings.Contains(logOnly.String(), "applied") {
		t.Errorf("a success reached stderr %q / log %q", &stderr, &logOnly)
	}
	reportIOPriority(e, ioPriorityOutcome{warning: "it failed"})
	if !strings.Contains(stderr.String(), "Warning: resources.io.priority: it failed") {
		t.Errorf("a failure is not on the boot stream naming the key: %q", &stderr)
	}
}

// TestMainAppliesTheIOPriorityFirstAndReportsItAfterTheLog pins the CALL SITES, which the
// tests above cannot: Main runs the whole boot for real, so its source is what is checked,
// as TestMainHoldsInsideTheGeneratorRefusalAndStillReturnsTheError does. applyIOPriority
// must be Main's first statement, take os.Args, and come before EnvFromOS; its report must
// follow attachBootLog. Deleting either call fails this.
func TestMainAppliesTheIOPriorityFirstAndReportsItAfterTheLog(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "boot.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var main *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Main" && fn.Recv == nil {
			main = fn
		}
	}
	if main == nil || len(main.Body.List) == 0 {
		t.Fatal("boot.go has no Main")
	}
	if !callsFunc(main.Body.List[0], "applyIOPriority") {
		t.Fatal("Main's first statement is not the applyIOPriority call. Anything before it " +
			"runs twice (the first image re-executes), and a re-exec after the boot log " +
			"rotates boot.log twice.")
	}
	first := main.Body.List[0].(*ast.AssignStmt).Rhs[0].(*ast.CallExpr)
	if sel, ok := first.Args[0].(*ast.SelectorExpr); !ok || sel.Sel.Name != "Args" {
		t.Error("applyIOPriority must re-execute with exactly os.Args")
	}
	pos := map[string]token.Pos{}
	ast.Inspect(main.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				if _, seen := pos[id.Name]; !seen {
					pos[id.Name] = call.Pos()
				}
			}
		}
		return true
	})
	for _, name := range []string{"EnvFromOS", "attachBootLog", "reportIOPriority"} {
		if _, ok := pos[name]; !ok {
			t.Fatalf("Main no longer calls %s", name)
		}
	}
	if !(pos["applyIOPriority"] < pos["EnvFromOS"] && pos["attachBootLog"] < pos["reportIOPriority"]) {
		t.Errorf("order in Main: applyIOPriority %v, EnvFromOS %v, attachBootLog %v, reportIOPriority %v; "+
			"the apply must precede EnvFromOS and the report must follow the log", pos["applyIOPriority"],
			pos["EnvFromOS"], pos["attachBootLog"], pos["reportIOPriority"])
	}
}
