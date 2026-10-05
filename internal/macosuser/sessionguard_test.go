package macosuser

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A process table the way `ps -ax -o pid=,ppid=,rss=` prints it: right-aligned columns, the
// guard at pid 100, and two trees that are not the guard's (pid 1's other children, and 900's).
const psFixture = `
    1     0   9000
  100    50   8000
  101   100  10240
  102   101 204800
  103   101  51200
  104   100   1024
  555   100    512
  900     1 999999
  901   900 888888
`

func TestParsePSTableReadsTheNumericColumns(t *testing.T) {
	rows, err := parsePSTable(psFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 || rows[3] != (psRow{pid: 102, ppid: 101, rssKiB: 204800}) {
		t.Errorf("rows = %+v", rows)
	}
	// A process ps may not inspect (outside the sandbox) can render its size as "-"; it is
	// not the session's, so it reads as 0 instead of refusing the whole table.
	rows, err = parsePSTable("  1 0 -\n100 1 2048\n")
	if err != nil || rows[0].rssKiB != 0 || rows[1].rssKiB != 2048 {
		t.Errorf("a \"-\" rss: rows %+v err %v", rows, err)
	}
}

// TestParsePSTableRefusesAMalformedTable: a table the guard half-read is a sum it cannot
// trust, and stopping a process on that sum is the one thing it must not do.
func TestParsePSTableRefusesAMalformedTable(t *testing.T) {
	for _, bad := range []string{
		"",
		"\n\n",
		"100 50",
		"100 50 8000 extra",
		"100 50 8k",
		"PID PPID RSS\n100 50 8000",
		"100 -1 8000",
		"- 1 8000",
		"100 - 8000",
	} {
		if rows, err := parsePSTable(bad); err == nil {
			t.Errorf("parsePSTable(%q) = %+v, want an error", bad, rows)
		}
	}
}

// TestTheSessionTreeIsTheGuardsDescendants: everything below the guard and nothing else —
// not its parent, not a stranger's tree, not the guard itself, not the ps that read the table.
func TestTheSessionTreeIsTheGuardsDescendants(t *testing.T) {
	rows, _ := parsePSTable(psFixture)
	var got []int
	for _, r := range sessionTree(rows, 100, 555) {
		got = append(got, r.pid)
	}
	if fmt.Sprint(got) != "[101 104 102 103]" {
		t.Errorf("session tree = %v, want [101 104 102 103] (breadth first, 555 the reader skipped)", got)
	}
	// A cycle in a table (a racing read can produce one) must not loop the walk.
	cyc := []psRow{{pid: 2, ppid: 3}, {pid: 3, ppid: 2}, {pid: 4, ppid: 4}}
	if tree := sessionTree(cyc, 2, 0); len(tree) != 1 || tree[0].pid != 3 {
		t.Errorf("a cyclic table walked to %+v", tree)
	}
}

// TestJudgeSessionSumsAndPicksTheLargest: the cgroup OOM shape. Under the limit nothing is
// chosen; over it the largest resident process is, wherever in the tree it sits.
func TestJudgeSessionSumsAndPicksTheLargest(t *testing.T) {
	rows, _ := parsePSTable(psFixture)
	const treeKiB = 10240 + 204800 + 51200 + 1024
	under := judgeSession(rows, 100, 555, treeKiB*1024)
	if under.total != treeKiB*1024 || under.over || under.victim.pid != 0 {
		t.Errorf("at exactly the limit: %+v, want the sum %d and no victim", under, treeKiB*1024)
	}
	over := judgeSession(rows, 100, 555, 100<<20)
	if !over.over || over.victim.pid != 102 {
		t.Errorf("over the limit: %+v, want pid 102 (200m), the largest", over)
	}
	// Strangers never count, however large: 900's tree alone is well over the limit.
	if v := judgeSession(rows, 104, 0, 1); v.over || v.total != 0 {
		t.Errorf("a leaf guard with no descendants judged %+v", v)
	}
	// A tie goes to the lowest pid, so one sample's choice is the next one's.
	tie := []psRow{{pid: 10, ppid: 1}, {pid: 12, ppid: 10, rssKiB: 50}, {pid: 11, ppid: 10, rssKiB: 50}}
	if v := judgeSession(tie, 10, 0, 1); v.victim.pid != 11 {
		t.Errorf("tie went to %d, want 11", v.victim.pid)
	}
}

func TestSessionGuardReadsResourcesMemory(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int64
	}{
		{"256m", 256 << 20},
		{"8g", 8 << 30},
		{"8G", 8 << 30},
		{"512k", 512 << 10},
		{"1048576", 1 << 20},
		{"4096b", 4096},
		{nil, 0},
		{"", 0},
		{"lots", 0},
		{"0", 0},
		{8, 0}, // validation's "expected a string"; nothing to guard
	} {
		res := jsonx.NewOrderedMap()
		res.Set("memory", tc.v)
		if got := SessionGuardFor(res).MemoryBytes; got != tc.want {
			t.Errorf("memory %v -> %d bytes, want %d", tc.v, got, tc.want)
		}
	}
	if SessionGuardFor(nil).Enabled() {
		t.Error("no resources block declared a guard")
	}
	if g := (SessionGuard{}); g.Argv("/x/yolo") != nil {
		t.Errorf("a disabled guard renders words: %v", g.Argv("/x/yolo"))
	}
	if got := (SessionGuard{MemoryBytes: 256 << 20}).Argv("/x/yolo"); strings.Join(got, " ") !=
		"/x/yolo internal session-guard --memory 268435456 --" {
		t.Errorf("guard argv = %v", got)
	}
	if formatBytes(256<<20) != "256m" || formatBytes(3<<29) != "1.5g" || formatBytes(2<<30) != "2g" {
		t.Errorf("formatBytes: %s %s %s", formatBytes(256<<20), formatBytes(3<<29), formatBytes(2<<30))
	}
}

func TestParseSessionGuardArgs(t *testing.T) {
	o, argv, err := parseSessionGuardArgs([]string{"--memory", "100", "--interval=10ms", "--grace", "1s", "--", "/bin/zsh", "-c", "x"})
	if err != nil || o.memory != 100 || o.interval != 10*time.Millisecond || o.grace != time.Second ||
		strings.Join(argv, " ") != "/bin/zsh -c x" {
		t.Errorf("parsed %+v %v %v", o, argv, err)
	}
	if o, _, _ := parseSessionGuardArgs([]string{"--memory=5", "--", "a"}); o.interval != sessionGuardInterval || o.grace != sessionGuardGrace {
		t.Errorf("defaults not applied: %+v", o)
	}
	for _, bad := range [][]string{
		{"--", "a"},
		{"--memory", "0", "--", "a"},
		{"--memory", "x", "--", "a"},
		{"--memory", "5", "--"},
		{"--memory", "5", "a"},
		{"--memory"},
		{"--interval", "-1s", "--memory", "5", "--", "a"},
		{"--bogus", "1", "--memory", "5", "--", "a"},
	} {
		if _, _, err := parseSessionGuardArgs(bad); err == nil {
			t.Errorf("parseSessionGuardArgs(%q) accepted", bad)
		}
	}
}

// syncBuf is a bytes.Buffer two goroutines may share: the child's output is copied in by
// os/exec while the test reads it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// guardRun is one run of the real loop: a real child process under /bin/sh, a fake process
// table built from that child's real pid, and a fake signal source the test writes to.
type guardRun struct {
	mu      sync.Mutex
	sigs    chan<- os.Signal
	child   int
	kills   []string
	rssKiB  int64
	psCalls int
	psErr   error
	stdout  syncBuf
	stderr  syncBuf
}

// newGuardRun returns the seams for one run, the child at rssKiB resident in the fake table
// for as long as it is alive. The table also holds the guard (this test process) and the
// reader, which is the largest row of all and which the guard must never count or choose.
func newGuardRun(t *testing.T, rssKiB int64) (*guardRun, guardSeams) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	g := &guardRun{rssKiB: rssKiB}
	s := guardSeams{
		start: func(argv []string) (*exec.Cmd, error) {
			c := exec.Command(argv[0], argv[1:]...)
			c.Stdout = &g.stdout
			err := c.Start()
			if err == nil {
				g.mu.Lock()
				g.child = c.Process.Pid
				g.mu.Unlock()
			}
			return c, err
		},
		ps: func() (string, int, error) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.psCalls++
			if g.psErr != nil {
				return "", 0, g.psErr
			}
			table := fmt.Sprintf("%d 1 4000\n77777 %d 999999999\n", os.Getpid(), os.Getpid())
			if g.child != 0 && syscall.Kill(g.child, 0) == nil {
				table += fmt.Sprintf("%d %d %d\n", g.child, os.Getpid(), g.rssKiB)
			}
			return table, 77777, nil
		},
		kill: func(pid int, sig syscall.Signal) error {
			g.mu.Lock()
			g.kills = append(g.kills, fmt.Sprintf("%d:%s", pid, sig))
			g.mu.Unlock()
			return syscall.Kill(pid, sig)
		},
		alive: func(pid int) bool { return syscall.Kill(pid, 0) == nil },
		notify: func(c chan<- os.Signal, _ ...os.Signal) {
			g.mu.Lock()
			g.sigs = c
			g.mu.Unlock()
		},
		stop:   func(chan<- os.Signal) {},
		ignore: func(...os.Signal) {},
		self:   os.Getpid(),
		stderr: &g.stderr,
	}
	return g, s
}

func (g *guardRun) killLog() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.Join(g.kills, " ")
}

func (g *guardRun) childPid() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.child
}

func (g *guardRun) signals() chan<- os.Signal {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sigs
}

// runGuard runs the loop in the background and returns its status channel.
func runGuard(o sessionGuardOptions, argv []string, s guardSeams) <-chan int {
	rc := make(chan int, 1)
	go func() { rc <- runSessionGuard(o, argv, s) }()
	return rc
}

func waitRC(t *testing.T, rc <-chan int) int {
	t.Helper()
	select {
	case n := <-rc:
		return n
	case <-time.After(30 * time.Second):
		t.Fatal("the guard did not return within 30s")
	}
	return -1
}

var fastGuard = sessionGuardOptions{memory: 100 << 20, interval: 20 * time.Millisecond, grace: 300 * time.Millisecond}

// TestTheGuardReturnsTheChildsStatus: the guard is transparent to `sudo` and the launcher
// above it — the child's exit code, or 128+N when a signal ended it.
func TestTheGuardReturnsTheChildsStatus(t *testing.T) {
	g, s := newGuardRun(t, 1)
	if rc := waitRC(t, runGuard(fastGuard, []string{"sh", "-c", "echo hi; exit 7"}, s)); rc != 7 {
		t.Errorf("rc = %d, want the child's 7", rc)
	}
	if g.stdout.String() != "hi\n" || g.stderr.String() != "" || g.killLog() != "" {
		t.Errorf("an under-limit run said or did something: stdout %q stderr %q kills %q",
			g.stdout.String(), g.stderr.String(), g.killLog())
	}
	_, s = newGuardRun(t, 1)
	if rc := waitRC(t, runGuard(fastGuard, []string{"sh", "-c", "kill -USR1 $$"}, s)); rc != 128+int(syscall.SIGUSR1) {
		t.Errorf("rc = %d, want 128+SIGUSR1 for a child a signal ended", rc)
	}
	_, s = newGuardRun(t, 1)
	if rc := waitRC(t, runGuard(fastGuard, []string{"/nonexistent/yolo-test-binary"}, s)); rc != 127 {
		t.Errorf("rc = %d, want 127 when the command cannot start", rc)
	}
}

// TestTheGuardForwardsTermAndHupAndDropsInt: SIGTERM and SIGHUP reach the child (sudo relays
// them to its command, which is the guard), and SIGINT does not — the agent, in the same
// process group, got the terminal's own copy, and a second one would be a double ^C.
func TestTheGuardForwardsTermAndHupAndDropsInt(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		name := strings.TrimPrefix(sigName(sig), "SIG")
		g, s := newGuardRun(t, 1)
		script := `trap 'echo GOT-INT' INT; trap 'echo GOT-` + name + `; exit 42' ` + name +
			`; echo up; while :; do sleep 0.02; done`
		rc := runGuard(fastGuard, []string{"sh", "-c", script}, s)
		waitFor(t, func() bool { return g.signals() != nil && strings.Contains(g.stdout.String(), "up") })
		g.signals() <- syscall.SIGINT
		time.Sleep(200 * time.Millisecond)
		g.signals() <- sig
		if n := waitRC(t, rc); n != 42 {
			t.Errorf("%s: rc = %d, want 42 from the child's trap", name, n)
		}
		out := g.stdout.String()
		if !strings.Contains(out, "GOT-"+name) || strings.Contains(out, "GOT-INT") {
			t.Errorf("%s: child saw %q; want the forwarded signal and never SIGINT", name, out)
		}
	}
}

// TestTheGuardStopsTheLargestProcessWithTermThenKill is the breach: one red line naming
// resources.memory, SIGTERM to the largest process, and SIGKILL only once the grace runs out.
func TestTheGuardStopsTheLargestProcessWithTermThenKill(t *testing.T) {
	// A child that honors SIGTERM goes on the first signal. The grace is long, so a loaded
	// machine that is slow to reap the child cannot turn this into the SIGKILL case.
	patient := fastGuard
	patient.grace = 20 * time.Second
	g, s := newGuardRun(t, 200<<10) // 200m resident against 100m
	rc := waitRC(t, runGuard(patient, []string{"sh", "-c", "while :; do sleep 0.02; done"}, s))
	if rc != 128+int(syscall.SIGTERM) {
		t.Errorf("rc = %d, want 128+SIGTERM", rc)
	}
	pid := g.childPid()
	if g.killLog() != fmt.Sprintf("%d:terminated", pid) {
		t.Errorf("kills = %q, want one SIGTERM to the child %d", g.killLog(), pid)
	}
	errText := g.stderr.String()
	for _, want := range []string{"resources.memory (100m)", "200m", fmt.Sprintf("stopping pid %d", pid),
		"Raise resources.memory"} {
		if !strings.Contains(errText, want) {
			t.Errorf("the breach line lacks %q:\n%s", want, errText)
		}
	}
	if strings.Contains(errText, "SIGKILL") || strings.Contains(errText, "77777") {
		t.Errorf("a process that left on SIGTERM got a SIGKILL line, or the reader was chosen:\n%s", errText)
	}

	// A child that ignores SIGTERM gets SIGKILL after the grace.
	g, s = newGuardRun(t, 200<<10)
	if n := waitRC(t, runGuard(fastGuard, []string{"sh", "-c", "trap '' TERM; while :; do sleep 0.02; done"}, s)); n != 128+int(syscall.SIGKILL) {
		t.Errorf("rc = %d, want 128+SIGKILL", n)
	}
	pid = g.childPid()
	if !strings.HasPrefix(g.killLog(), fmt.Sprintf("%d:terminated %d:killed", pid, pid)) {
		t.Errorf("kills = %q, want SIGTERM then SIGKILL to %d", g.killLog(), pid)
	}
	if !strings.Contains(g.stderr.String(), fmt.Sprintf("pid %d was still running", pid)) {
		t.Errorf("no SIGKILL line:\n%s", g.stderr.String())
	}
}

// TestTheGuardSaysOnceWhenItCannotReadTheTable: an unreadable table is said, once, and the
// guard keeps the session running — it never stops a process on a sum it does not have.
func TestTheGuardSaysOnceWhenItCannotReadTheTable(t *testing.T) {
	g, s := newGuardRun(t, 200<<10)
	g.psErr = fmt.Errorf("ps: operation not permitted")
	rc := runGuard(fastGuard, []string{"sh", "-c", "sleep 0.3; exit 3"}, s)
	if n := waitRC(t, rc); n != 3 {
		t.Errorf("rc = %d, want 3", n)
	}
	if c := strings.Count(g.stderr.String(), "cannot read the process table"); c != 1 {
		t.Errorf("the table warning printed %d times, want once:\n%s", c, g.stderr.String())
	}
	if g.killLog() != "" || g.psCalls < 2 {
		t.Errorf("kills %q after %d reads; want none, and the guard to keep trying", g.killLog(), g.psCalls)
	}
}

func sigName(s syscall.Signal) string {
	switch s {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	}
	return s.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 20s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
