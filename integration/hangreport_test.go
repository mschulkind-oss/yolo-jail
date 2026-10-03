package integration

// hangreport_test.go is what the harness says about a launch that overran its deadline: where it
// stood, read BEFORE it is killed, because nothing after the kill can still say.
//
// IT EXISTS BECAUSE ONE CI RUN COULD NOT SAY ANYTHING. Run 37146697898 (integration, ubuntu-latest,
// shard 2, 2026-10-03) hit its 120-minute job bound: from one test on, every launch timed out at
// its five-minute bound and the suite's own `nix eval` of the flake was killed at four minutes with
// an empty stderr, and each failure printed one line, `yolo timed out after 5m0s: yolo run …`.
// Nothing named the stage a launch had reached, the process it was waiting on, or what that process
// was waiting for, so the log could not tell a wedged nix from a wedged podman from a lock yolo
// itself held. A hang report answers those three from the one moment they are all still true.
//
// WHAT IT READS. The timed-out process and every descendant it still has, each with its state, its
// age, the kernel function it sleeps in (wchan, per thread on Linux) and the files it holds open;
// then every nix process ELSEWHERE on the machine, since what a stuck nix client waits on is
// usually a nix-daemon worker outside the launch's tree. Then the descendants are killed with it:
// a timed-out launch's children used to run on into the next test.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hangTailLines is how much of each stream a timeout repeats: enough to show the last stage a
// launch announced, short enough that a run with fifteen timeouts stays readable.
const hangTailLines = 60

// hangReport is what armHangReport read when a deadline ended its command. Empty until then.
type hangReport struct {
	tree   []string // the timed-out process and its descendants, root first
	nix    []string // nix processes outside that tree
	killed []int    // the descendants killed with it
}

// armHangReport makes the end of cmd's context describe cmd's process tree and kill its
// descendants before it kills cmd itself. cmd must come from exec.CommandContext and not be
// started yet.
//
// exec.Cmd runs Cancel on the goroutine watching the context and Wait returns only after it, so
// the report is complete by the time Run or Wait returns.
func armHangReport(cmd *exec.Cmd) *hangReport {
	r := &hangReport{}
	cmd.Cancel = func() error {
		tree := processTree(cmd.Process.Pid)
		inTree := map[int]bool{}
		for _, pid := range tree {
			inTree[pid] = true
			r.tree = append(r.tree, processDetail(pid))
		}
		for _, row := range processTable() {
			if !inTree[row.pid] && isNixProcess(row.comm) {
				r.nix = append(r.nix, processDetail(row.pid))
			}
		}
		for _, pid := range tree[1:] {
			if syscall.Kill(pid, syscall.SIGKILL) == nil {
				r.killed = append(r.killed, pid)
			}
		}
		return cmd.Process.Kill()
	}
	return r
}

// String is the report as a timeout's message carries it.
func (r *hangReport) String() string {
	if r == nil || len(r.tree) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- the timed-out process and its descendants, read before they were killed ---\n")
	for i, line := range r.tree {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(line + "\n")
	}
	if len(r.killed) > 0 {
		fmt.Fprintf(&b, "(killed with it: %v)\n", r.killed)
	}
	if len(r.nix) == 0 {
		b.WriteString("--- no other nix process is visible from here ---\n")
		return b.String()
	}
	b.WriteString("--- every other nix process visible from here (a stuck nix client usually waits on a nix-daemon worker) ---\n")
	for _, line := range r.nix {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// timeoutDetail is what a timed-out launch's failure carries under its one-line summary: the tail
// of each stream, then the hang report.
func timeoutDetail(stdout, stderr string, r *hangReport) string {
	var b strings.Builder
	for _, s := range []struct{ name, text string }{{"stderr", stderr}, {"stdout", stdout}} {
		text := strings.TrimRight(s.text, "\n")
		total := strings.Count(text, "\n") + 1
		switch {
		case text == "":
			fmt.Fprintf(&b, "--- %s: nothing was printed ---\n", s.name)
		case total > hangTailLines:
			fmt.Fprintf(&b, "--- %s, the last %d of %d lines ---\n%s\n", s.name, hangTailLines, total,
				lastLines(text, hangTailLines))
		default:
			fmt.Fprintf(&b, "--- %s, all %d lines ---\n%s\n", s.name, total, text)
		}
	}
	b.WriteString(r.String())
	return b.String()
}

// processTree is root followed by every process descended from it, from one process listing.
func processTree(root int) []int {
	children := map[int][]int{}
	for _, row := range processTable() {
		children[row.ppid] = append(children[row.ppid], row.pid)
	}
	tree := []int{root}
	for i := 0; i < len(tree); i++ {
		kids := children[tree[i]]
		sort.Ints(kids)
		tree = append(tree, kids...)
	}
	return tree
}

// isNixProcess reports whether a command name is one of nix's own programs: nix, nix-daemon,
// nix-store and the rest of the nix-* family.
func isNixProcess(comm string) bool {
	base := filepath.Base(comm)
	return base == "nix" || strings.HasPrefix(base, "nix-")
}

// procRow is one process of a listing.
type procRow struct {
	pid, ppid int
	comm      string
}

// TestATimedOutLaunchSaysWhereItStood drives runLaunch, the body of every run* helper, over a fake
// launcher that announces a hundred stages and then never ends, with a child of its own. The
// timeout's message must carry the last lines the launch printed and name the processes it left
// running, and that child must not outlive the launch. No container: it runs under -short.
func TestATimedOutLaunchSaysWhereItStood(t *testing.T) {
	dir := resolvedTempDir(t)
	pidFile := filepath.Join(dir, "child.pid")
	fake := filepath.Join(dir, "fake-yolo")
	// The child's streams go to /dev/null so that it holds none of the launch's pipes: what is
	// pinned here is that the timeout reaps it, not how long Wait would block on it.
	script := "#!/bin/sh\n" +
		"i=1\n" +
		"while [ $i -le 100 ]; do echo \"launch stage $i\" >&2; i=$((i+1)); done\n" +
		"echo 'the one stdout line'\n" +
		"sleep 7351 </dev/null >/dev/null 2>&1 &\n" +
		"echo $! > " + pidFile + "\n" +
		"exec sleep 7352\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := yoloBin
	yoloBin = fake
	t.Cleanup(func() { yoloBin = saved })
	t.Cleanup(func() {
		if pid := readPid(pidFile); pid > 0 && !processGone(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	start := time.Now()
	_, timedOut := runLaunch(t, dir, []string{"run", "--", "true"}, withTimeout(2*time.Second))
	if timedOut == nil {
		t.Fatal("the fake launcher never ends, but runLaunch did not report a timeout")
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("runLaunch took %s to return from a 2s deadline", took)
	}
	msg := timedOut.String()
	for _, want := range []string{
		"yolo timed out after 2s: yolo run -- true",
		// The last 60 of the 100 stages, so the stage a launch stood at is in the log.
		"the last 60 of 100 lines",
		"launch stage 41\n",
		"launch stage 100\n",
		"the one stdout line",
		// The processes, read before the kill: the launcher (exec'd into sleep 7352) and its child.
		"sleep 7352",
		"sleep 7351",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the timeout's message does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "launch stage 40\n") {
		t.Errorf("the timeout's message repeats more than the last 60 lines:\n%s", msg)
	}

	pid := readPid(pidFile)
	if pid <= 0 {
		t.Fatalf("the fake launcher never recorded its child's pid in %s", pidFile)
	}
	if !awaitProcessGone(pid, 5*time.Second) {
		t.Errorf("the timed-out launch's child (pid %d, sleep 7351) outlived it: a timed-out "+
			"launch's processes run on into the next test", pid)
	}
}

// readPid is the pid written in path, or 0.
func readPid(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

// TestANixEvalThatOverrunsSaysWhatItWaitedOn is the same pin on the suite's own `nix eval`
// (nixEvalDrvPath), over a `nix` on PATH that never answers. Its timeout must name the stuck
// process rather than hand back nix's empty stderr.
func TestANixEvalThatOverrunsSaysWhatItWaitedOn(t *testing.T) {
	bin := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\nexec sleep 7353\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	savedRoot, savedTimeout := repoRoot, nixEvalTimeout
	repoRoot, nixEvalTimeout = bin, time.Second
	t.Cleanup(func() { repoRoot, nixEvalTimeout = savedRoot, savedTimeout })

	_, stderr, err := nixEvalDrvPath(t, "imageClosureRoot", `[]`)
	if err == nil {
		t.Fatal("a nix that never answers did not fail the eval")
	}
	for _, want := range []string{"nix eval timed out", "the timed-out process", "sleep 7353"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the overrun eval's stderr does not say %q:\n%s", want, stderr)
		}
	}
}
