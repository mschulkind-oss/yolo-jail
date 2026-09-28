package integration

// iopriority_test.go is the end-to-end acceptance of `resources.io.priority`
// (docs/design/io-priority.md §8, "Done looks like" 1 and 3). The unit tier pins each piece
// — the pinned-thread re-exec in a subprocess (internal/entrypoint), the argv and briefing
// per backend and the disclosure over fake sysfs trees (internal/cli/run), the check's
// grading (internal/cli/check). Only a real jail proves the chain: the launcher's value
// crossing into the container environment, the mounted entrypoint re-executing as PID 2,
// every process of the jail inheriting the class, and an attach doing the same for its own
// shell.
//
// The priority is read per THREAD by a static probe this test builds and drops into the
// workspace, through ioprio_get: `ionice` is not in the image, and `ionice -p` reads one
// thread; /proc/<pid>/io is I/O accounting, not priority.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// ioprioProbeSource prints one line per thread of every process it can see:
// "IOPRIO <pid> <tid> <raw value> <comm>", plus "SELF <pid>".
const ioprioProbeSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	fmt.Printf("SELF %d\n", os.Getpid())
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		comm, _ := os.ReadFile("/proc/" + p.Name() + "/comm")
		tasks, err := os.ReadDir("/proc/" + p.Name() + "/task")
		if err != nil {
			continue
		}
		for _, tk := range tasks {
			tid, err := strconv.Atoi(tk.Name())
			if err != nil {
				continue
			}
			r, _, e := syscall.RawSyscall(syscall.SYS_IOPRIO_GET, 1, uintptr(tid), 0)
			if e != 0 {
				continue
			}
			fmt.Printf("IOPRIO %d %d %d %s\n", pid, tid, r, strings.TrimSpace(string(comm)))
		}
	}
}
`

// buildIOPrioProbe builds the probe as a static Linux binary at <dir>/.ioprio-probe, which
// the jail sees at /workspace/.ioprio-probe.
func buildIOPrioProbe(t *testing.T, dir string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module ioprioprobe\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(ioprioProbeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, ".ioprio-probe"), ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH,
		"GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the ioprio probe: %v\n%s", err, out)
	}
}

type ioprioThread struct {
	pid, tid, value int
	comm            string
}

// parseIOPrioProbe reads the probe's lines out of a session's output.
func parseIOPrioProbe(t *testing.T, out string) (self int, threads []ioprioThread) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		switch {
		case len(f) == 2 && f[0] == "SELF":
			self, _ = strconv.Atoi(f[1])
		case len(f) >= 4 && f[0] == "IOPRIO":
			pid, _ := strconv.Atoi(f[1])
			tid, _ := strconv.Atoi(f[2])
			v, _ := strconv.Atoi(f[3])
			comm := ""
			if len(f) > 4 {
				comm = strings.Join(f[4:], " ")
			}
			threads = append(threads, ioprioThread{pid, tid, v, comm})
		}
	}
	if self == 0 || len(threads) == 0 {
		t.Fatalf("the probe reported nothing:\n%s", out)
	}
	return self, threads
}

// assertEveryThreadIsLow: every thread of every process but PID 1 (podman-init, which the
// runtime starts before any entrypoint exists) reads BE7, and the probe itself was seen.
func assertEveryThreadIsLow(t *testing.T, session, out string) {
	t.Helper()
	want, _ := ioprio.Low.KernelValue()
	self, threads := parseIOPrioProbe(t, out)
	sawSelf := false
	for _, th := range threads {
		if th.pid == self {
			sawSelf = true
		}
		if th.pid == 1 {
			continue
		}
		if th.value != want {
			t.Errorf("%s: pid %d thread %d (%s) reads %s, want be/7", session, th.pid, th.tid, th.comm, ioprio.Describe(th.value))
		}
	}
	if !sawSelf {
		t.Errorf("%s: the probe did not see its own process, so it read the wrong /proc:\n%s", session, out)
	}
}

// holdIOPrioJail launches a jail in the background that runs script, says so, and then stays
// up until the test ends (or writes <dir>/<release>), so the test can attach into it. It
// returns once the script has run and the launch has released the workspace lock.
func holdIOPrioJail(t *testing.T, dir, release, script string) *bgRun {
	t.Helper()
	first := startYoloBackground(t, "first", dir,
		script+`; echo FIRST-RAN-$((40+2)); `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644) })
	ran := regexp.MustCompile(`FIRST-RAN-42`)
	deadline := time.Now().Add(jailTimeout())
	for !ran.MatchString(first.combined()) {
		select {
		case err := <-first.done:
			t.Fatalf("the first launch exited (%v) before its script ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first session never ran its script within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, dir, first)
	return first
}

// TestIOPriorityReachesEveryProcessOfTheJail: a jail declaring "low" runs every process at
// BE7 — the boot's children, the shell and its descendants — and an attach into it does the
// same for its own shell. The launch and the attach each print the disclosure line exactly
// when the disk under the workspace ignores the value, graded by the resolver the launcher
// uses, and `yolo check` prints a row for it. The boot log records the application.
func TestIOPriorityReachesEveryProcessOfTheJail(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"resources": {"io": "low"}}`)

	const release = "release-ioprio"
	if runtime.GOOS != "linux" {
		// Apple Container and podman on macOS never pass the value: the Warned line is the
		// whole behavior there, on the launch and on an attach into it (IO-D10).
		const warned = `resources.io.priority "low" is NOT applied on`
		first := holdIOPrioJail(t, dir, release, `true`)
		if !strings.Contains(first.combined(), warned) {
			t.Errorf("a macOS launch must print the Warned line:\n%s", first.combined())
		}
		attach := runYolo(t, dir, "true")
		if attach.rc != 0 || !strings.Contains(attach.combined(), "Attaching to existing jail") {
			t.Fatalf("the second run did not attach (rc %d):\n%s", attach.rc, attach.combined())
		}
		if !strings.Contains(attach.stderr, warned) {
			t.Errorf("a macOS attach must print the Warned line too:\n%s", attach.combined())
		}
		return
	}
	buildIOPrioProbe(t, dir)

	first := holdIOPrioJail(t, dir, release, `/workspace/.ioprio-probe > /workspace/.ioprio-first.txt`)
	firstOut, err := os.ReadFile(filepath.Join(dir, ".ioprio-first.txt"))
	if err != nil {
		t.Fatalf("the first session's probe wrote nothing: %v\n%s", err, first.combined())
	}
	assertEveryThreadIsLow(t, "fresh launch", string(firstOut))

	attach := runCommand(t, dir, append(jailRunArgs(), "--", "/workspace/.ioprio-probe"))
	if attach.rc != 0 || !strings.Contains(attach.combined(), "Attaching to existing jail") {
		t.Fatalf("the second run did not attach (rc %d):\n%s", attach.rc, attach.combined())
	}
	assertEveryThreadIsLow(t, "attach", attach.stdout)

	// The disclosure, iff the launcher's own grading says this disk ignores "low".
	ignored := ioprio.NoEffect(ioprio.Low, ioprio.Resolve("/", dir))
	const disclosure = `resources.io.priority "low" has no effect on`
	for _, s := range []struct{ name, out string }{{"fresh launch", first.combined()}, {"attach", attach.stderr}} {
		if got := strings.Contains(s.out, disclosure); got != (len(ignored) > 0) {
			t.Errorf("%s: disclosure printed=%v, but the disk under %s is graded %+v:\n%s",
				s.name, got, dir, ignored, s.out)
		}
	}

	// The attach's boot rotated the first boot's log aside ONCE: each of the two logs holds
	// its own boot's record, which a double rotation would have lost.
	for _, name := range []string{"boot.log", "boot.log.prev"} {
		b, err := os.ReadFile(filepath.Join(dir, ".yolo", name))
		if err != nil || !strings.Contains(string(b), `resources.io.priority: "low" (be/7) on every thread`) {
			t.Errorf(".yolo/%s does not record the applied priority (%v):\n%s", name, err, b)
		}
	}

	check := runYoloCLI(t, dir, "check", "--no-build")
	if !strings.Contains(check.stdout, "Disk I/O priority") || !strings.Contains(check.stdout, `resources.io.priority "low"`) {
		t.Errorf("`yolo check` printed no I/O priority row for a declared priority:\n%s", check.combined())
	}
}
