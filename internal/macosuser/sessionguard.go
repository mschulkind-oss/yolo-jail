package macosuser

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cgd"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// sessionguard.go is `resources.memory` on macos-user: a SAMPLED guard, run by the sandbox
// account inside the agent's own Seatbelt sandbox, between the env-file reader and the shell
// that execs the agent (LaunchArgvWithGuard). It is `yolo internal session-guard`, the staged
// yolo the sandbox already runs as its bootstrap.
//
// WHAT IT IS, AND WHAT IT IS NOT. macOS has no cgroups and this backend has no VM to size, so
// nothing can make the kernel refuse an allocation the way a container's memory.max does. The
// guard reads the process table every few seconds (pid, ppid and resident size, from the
// kernel: proctable_darwin.go), sums the resident memory of every process descended from it,
// and, when the sum is over the declared limit, stops the LARGEST of them: SIGTERM, a grace period, then SIGKILL. That is the shape
// of the cgroup OOM killer, which picks the biggest process in the group rather than the
// newest, so a runaway build process goes and the agent that started it usually stays.
//
// ITS HOLES ARE THE LAUNCH'S TO SAY, and it says them (the orchestrator's disclosure line):
//   - it SAMPLES, so an allocation spike between two samples is not seen, and a process that
//     grows fast enough can take the machine's memory first;
//   - the agent runs as the same account in the same sandbox, so it can kill the guard;
//   - a process that leaves the guard's process tree (a daemon that double-forks onto launchd)
//     is no longer counted;
//   - RSS counts a shared page in every process that maps it, so the sum can overstate.
//
// WHY INSIDE THE SANDBOX. The invoking user cannot signal a `_yolojail` process (a different
// uid, and the TTY proxy that might have done it is Linux-only), and a root watchdog is a
// standing privileged process this backend has never had — that one is the maintainer's call
// and is not built. The guard needs no privilege at all: it is the parent of what it stops.
//
// IT STAYS IN THE SESSION'S PROCESS GROUP, deliberately (docs/design/declaration-parity.md
// ledger). A guard that moved the agent into a group of its own and handed it the terminal
// would take job control away from everything upstream: ^Z would stop only the agent, while
// sudo — which waits on the guard, its command — saw nothing stop, and the session would hang
// with the terminal in a stopped group. Staying in the group means ^Z stops the guard with the
// agent and `fg` resumes both, exactly as if the guard were not there. The cost is that the
// terminal's own signals reach the guard too, which is why it catches SIGINT and SIGQUIT and
// drops them (the agent got its own copy), and forwards only the two a terminal does not
// deliver to the whole group when sudo relays them: SIGTERM and SIGHUP.

// SessionGuardVerb is the hidden `yolo internal` verb that runs the guard.
const SessionGuardVerb = "session-guard"

// sessionGuardInterval is how often the guard samples the process table, and
// sessionGuardGrace how long a stopped process gets between SIGTERM and SIGKILL. Both are
// the launch's defaults; the verb takes --interval and --grace only so its run loop can be
// tested in milliseconds.
const (
	sessionGuardInterval = 2 * time.Second
	sessionGuardGrace    = 5 * time.Second
)

// psBin and psArgs are the process-table read OFF macOS (proctable_other.go): every
// process, numeric columns only, no header. Numeric only because a command name can hold
// spaces and a column that can hold anything is a column a parser has to guess at; the guard
// names a process by its pid. ON macOS the guard never runs /bin/ps: ps is setuid root there,
// and Seatbelt refuses a setuid exec inside any sandbox whatever the profile says, so the
// guard read nothing (`fork/exec /bin/ps: operation not permitted`, macos-user CI run
// 37940733418). proctable_darwin.go reads the same three columns from the kernel instead.
const psBin = "/bin/ps"

var psArgs = []string{"-ax", "-o", "pid=,ppid=,rss="}

// SessionGuard is what a launch's session guard enforces. The zero value is no guard, and
// then the launch argv is exactly the argv of a launch that never heard of one.
type SessionGuard struct {
	// MemoryBytes is the declared resources.memory in bytes; 0 means none was declared.
	MemoryBytes int64
}

// Enabled reports whether the launch runs a guard at all.
func (g SessionGuard) Enabled() bool { return g.MemoryBytes > 0 }

// Argv is the words LaunchArgvWithGuard puts in front of the inner shell: the staged yolo,
// the verb, the limit, and the `--` that ends the guard's own flags. nil when not Enabled.
func (g SessionGuard) Argv(stagedYolo string) []string {
	if !g.Enabled() {
		return nil
	}
	return []string{stagedYolo, "internal", SessionGuardVerb,
		"--memory", strconv.FormatInt(g.MemoryBytes, 10), "--"}
}

// SessionGuardFor is the guard a `resources` block declares: its memory, parsed by the
// cgroup delegate's own reader (cgd.ParseMemoryValue) so "8g" means one number on every
// backend. An absent, null or unparseable value declares no guard; validation refuses the
// unparseable one long before a launch gets here.
func SessionGuardFor(res *jsonx.OrderedMap) SessionGuard {
	n, ok := memoryLimitBytes(res)
	if !ok {
		return SessionGuard{}
	}
	return SessionGuard{MemoryBytes: n}
}

// memoryLimitBytes reads resources.memory. config's memoryRe admits a trailing "b" (bytes),
// which cgd's reader does not know, so it is dropped first: "512b" is 512.
func memoryLimitBytes(res *jsonx.OrderedMap) (int64, bool) {
	if res == nil {
		return 0, false
	}
	v, _ := res.Get("memory")
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	s = strings.TrimSpace(s)
	if t := strings.TrimRight(s, "bB"); t != s && len(s)-len(t) == 1 {
		s = t
	}
	n, ok := cgd.ParseMemoryValue(s)
	if !ok || n <= 0 {
		return 0, false
	}
	return n, true
}

// formatBytes renders a byte count the way resources.memory is written: "512m", "1.5g".
func formatBytes(n int64) string {
	const mib, gib = 1 << 20, 1 << 30
	switch {
	case n >= gib:
		s := strconv.FormatFloat(float64(n)/gib, 'f', 1, 64)
		return strings.TrimSuffix(s, ".0") + "g"
	case n >= mib:
		return strconv.FormatInt((n+mib/2)/mib, 10) + "m"
	}
	return strconv.FormatInt((n+512)/1024, 10) + "k"
}

// psRow is one line of the process table: its pid, its parent's, and its resident size in
// KiB (ps reports rss in 1024-byte units on macOS and Linux alike).
type psRow struct {
	pid, ppid int
	rssKiB    int64
}

// parsePSTable reads `ps -o pid=,ppid=,rss=` output. Every non-blank line must be exactly
// three non-negative integers; anything else is an error naming the line, because a table
// the guard half-read would be a sum it cannot trust, and stopping a process on that sum is
// the one thing it must not do.
//
// ONE EXCEPTION, in the rss column alone: "-", which is how a ps that may not inspect a
// process can render its size, and the Seatbelt profile denies process-info-pidinfo for any
// process outside the sandbox (seatbelt.go). Such a process is never the session's — the
// guard's descendants share its sandbox, so their sizes are readable — so it reads as 0
// rather than spoiling the table.
func parsePSTable(out string) ([]psRow, error) {
	var rows []psRow
	sc := bufio.NewScanner(strings.NewReader(out))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d of the process table is not pid, ppid and rss: %q", n, line)
		}
		if f[2] == "-" {
			f[2] = "0"
		}
		var nums [3]int64
		for i, s := range f {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil || v < 0 {
				return nil, fmt.Errorf("line %d of the process table has a non-numeric column: %q", n, line)
			}
			nums[i] = v
		}
		rows = append(rows, psRow{pid: int(nums[0]), ppid: int(nums[1]), rssKiB: nums[2]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("the process table is empty")
	}
	return rows, nil
}

// sessionTree is every process descended from root, root itself excluded, and so is skip
// (the ps that produced the table, which is the guard's own child for the instant it runs).
// Anything not reached from root — another session, a process that reparented away — is not
// the session's and is never counted or chosen.
func sessionTree(rows []psRow, root, skip int) []psRow {
	children := map[int][]psRow{}
	for _, r := range rows {
		if r.pid == r.ppid {
			continue // a self-parented row (pid 0 on some systems) would loop the walk
		}
		children[r.ppid] = append(children[r.ppid], r)
	}
	var out []psRow
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if seen[c.pid] {
				continue
			}
			seen[c.pid] = true
			queue = append(queue, c.pid)
			if c.pid != skip {
				out = append(out, c)
			}
		}
	}
	return out
}

// guardVerdict is one sample's judgment: the session's resident total, and when it is over
// the limit, the process to stop.
type guardVerdict struct {
	total  int64 // bytes
	over   bool
	victim psRow
}

// judgeSession sums the session's resident memory and, over the limit, picks the largest
// process (the lowest pid on a tie, so the choice is stable from one sample to the next).
// Under the limit, or with no process in the tree, there is no victim.
func judgeSession(rows []psRow, root, skip int, limit int64) guardVerdict {
	tree := sessionTree(rows, root, skip)
	var v guardVerdict
	for _, r := range tree {
		v.total += r.rssKiB * 1024
	}
	if limit <= 0 || v.total <= limit || len(tree) == 0 {
		return v
	}
	v.over = true
	v.victim = tree[0]
	for _, r := range tree[1:] {
		if r.rssKiB > v.victim.rssKiB || r.rssKiB == v.victim.rssKiB && r.pid < v.victim.pid {
			v.victim = r
		}
	}
	return v
}

// sessionGuardOptions are the verb's parsed flags.
type sessionGuardOptions struct {
	memory   int64
	interval time.Duration
	grace    time.Duration
}

const sessionGuardUsage = "usage: yolo internal " + SessionGuardVerb +
	" --memory <bytes> [--interval <duration>] [--grace <duration>] -- <command> [args...]"

// parseSessionGuardArgs reads `--memory N [--interval D] [--grace D] -- argv...`. Each flag
// also takes the `--flag=value` spelling. The command is required: a guard with nothing to
// run has nothing to guard.
func parseSessionGuardArgs(args []string) (sessionGuardOptions, []string, error) {
	o := sessionGuardOptions{interval: sessionGuardInterval, grace: sessionGuardGrace}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			argv := args[i+1:]
			if len(argv) == 0 {
				return o, nil, errors.New("no command after --")
			}
			if o.memory <= 0 {
				return o, nil, errors.New("--memory <bytes> is required and must be positive")
			}
			return o, argv, nil
		}
		name, val, hasVal := strings.Cut(a, "=")
		if !hasVal {
			if i+1 >= len(args) {
				return o, nil, fmt.Errorf("%s needs a value", a)
			}
			i++
			val = args[i]
		}
		switch name {
		case "--memory":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil || n <= 0 {
				return o, nil, fmt.Errorf("--memory %q is not a positive byte count", val)
			}
			o.memory = n
		case "--interval", "--grace":
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return o, nil, fmt.Errorf("%s %q is not a positive duration", name, val)
			}
			if name == "--interval" {
				o.interval = d
			} else {
				o.grace = d
			}
		default:
			return o, nil, fmt.Errorf("unknown flag %q", a)
		}
	}
	return o, nil, errors.New("no -- before the command")
}

// guardSeams are the run loop's contacts with the process: the child it starts, the process
// table, signals in and out, and where it writes. realGuardSeams is production; a test swaps
// the table and the signal source and keeps everything else real.
type guardSeams struct {
	// start starts argv with the guard's own stdio and returns it running.
	start func(argv []string) (*exec.Cmd, error)
	// ps reads the process table, and returns the pid of the reader process so the guard does
	// not count its own instrument.
	ps func() (out string, readerPid int, err error)
	// kill signals one process.
	kill func(pid int, sig syscall.Signal) error
	// alive reports whether pid still exists.
	alive func(pid int) bool
	// notify, stop and ignore are signal.Notify, signal.Stop and signal.Ignore.
	notify func(c chan<- os.Signal, sig ...os.Signal)
	stop   func(c chan<- os.Signal)
	ignore func(sig ...os.Signal)
	// self is the guard's own pid, the root of the session tree.
	self int
	// stderr is where the guard's lines go, and color whether they are colored.
	stderr io.Writer
	color  bool
}

func realGuardSeams(stderr *os.File) guardSeams {
	return guardSeams{
		start: func(argv []string) (*exec.Cmd, error) {
			c := exec.Command(argv[0], argv[1:]...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c, c.Start()
		},
		ps:     readProcessTable,
		kill:   func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		alive:  func(pid int) bool { return syscall.Kill(pid, 0) == nil },
		notify: signal.Notify,
		stop:   signal.Stop,
		ignore: signal.Ignore,
		self:   os.Getpid(),
		stderr: stderr,
		color:  tty.Color(nil, true, tty.IsTerminalFile(stderr)),
	}
}

// SessionGuardMain is `yolo internal session-guard`: it runs the command after `--` and
// returns its exit status, guarding the session's memory while it runs.
func SessionGuardMain(args []string) int {
	o, argv, err := parseSessionGuardArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo internal "+SessionGuardVerb+": "+err.Error())
		fmt.Fprintln(os.Stderr, sessionGuardUsage)
		return 2
	}
	return runSessionGuard(o, argv, realGuardSeams(os.Stderr))
}

// runSessionGuard starts argv, forwards SIGTERM and SIGHUP to it, drops SIGINT and SIGQUIT
// (the agent, in the same process group, got the terminal's own copy), samples the session's
// memory every interval, and returns the child's exit status — 128+N when a signal ended it,
// the shell's convention, so `sudo` and the launcher above see what they would have seen
// without the guard in between.
func runSessionGuard(o sessionGuardOptions, argv []string, s guardSeams) int {
	// Signals are CAUGHT, never ignored: an ignored disposition survives exec, so a guard that
	// ignored SIGINT before starting the agent would hand it an agent deaf to ^C. Caught ones
	// reset to the default in the child.
	sigs := make(chan os.Signal, 8)
	s.notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	defer s.stop(sigs)

	child, err := s.start(argv)
	if err != nil {
		// The next step is the launch argv that named this command, and the launch without the
		// guard in it: the same command failing there too says the guard is not the cause.
		s.say("[bold red]yolo: the memory guard could not start %s (%s). `yolo run --dry-run` "+
			"prints the launch argv it was given; to launch without the guard, remove "+
			"resources.memory from yolo-jail.jsonc.[/bold red]",
			richtext.Escape(argv[0]), richtext.Escape(err.Error()))
		return 127
	}
	// The terminal may be handed to a job-control shell the agent runs, leaving the guard in a
	// background group; a write from there under `stty tostop` would stop it with SIGTTOU, and
	// a stopped guard guards nothing. Ignored only now, after the exec, so the agent does not
	// inherit it.
	s.ignore(syscall.SIGTTOU, syscall.SIGTTIN)

	done := make(chan error, 1)
	go func() { done <- child.Wait() }()

	tick := time.NewTicker(o.interval)
	defer tick.Stop()
	var (
		victim     int
		graceTimer <-chan time.Time
		psFailing  bool
	)
	for {
		select {
		case err := <-done:
			return childStatus(err)
		case sig := <-sigs:
			if sig == syscall.SIGTERM || sig == syscall.SIGHUP {
				_ = s.kill(child.Process.Pid, sig.(syscall.Signal))
			}
		case <-graceTimer:
			graceTimer = nil
			if s.alive(victim) {
				_ = s.kill(victim, syscall.SIGKILL)
				s.say("[bold red]yolo: pid %d was still running %s after SIGTERM; sent SIGKILL.[/bold red]",
					victim, o.grace)
			}
			victim = 0
		case <-tick.C:
			if victim != 0 {
				continue // one process at a time: the last one's grace has not run out
			}
			out, readerPid, err := s.ps()
			var rows []psRow
			if err == nil {
				rows, err = parsePSTable(out)
			}
			if err != nil {
				if !psFailing {
					s.say("[yellow]yolo: the memory guard cannot read the process table (%s), so "+
						"resources.memory is not being checked; it will keep trying. Remove "+
						"resources.memory from yolo-jail.jsonc to silence this.[/yellow]",
						richtext.Escape(err.Error()))
				}
				psFailing = true
				continue
			}
			psFailing = false
			v := judgeSession(rows, s.self, readerPid, o.memory)
			if !v.over {
				continue
			}
			s.say("[bold red]yolo: this session holds %s resident, over resources.memory (%s); "+
				"stopping pid %d, its largest process (%s). Raise resources.memory in "+
				"yolo-jail.jsonc, or remove it, if the session needs more.[/bold red]",
				formatBytes(v.total), formatBytes(o.memory), v.victim.pid, formatBytes(v.victim.rssKiB*1024))
			_ = s.kill(v.victim.pid, syscall.SIGTERM)
			victim = v.victim.pid
			graceTimer = time.After(o.grace)
		}
	}
}

// say writes one guard line. "\r\n", because the agent may hold the terminal in raw mode,
// where a bare newline moves down without returning to the first column.
func (s guardSeams) say(format string, a ...any) {
	fmt.Fprint(s.stderr, richtext.Render(fmt.Sprintf(format, a...), s.color)+"\r\n")
}

// childStatus is the exit status the guard reports for its child.
func childStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
	}
	return 1
}
