// Package hostprocesses is the allowlisted host-process viewer daemon. It
// answers ps-style requests from the jail against an allowlist yolo resolved at
// launch, via internal/hostservice (the frame-protocol server).
// Frozen contracts: the DEFAULT_FIELDS, the list/tree/pid mode argv + allowlist
// construction, and the exit codes (3 empty-allowlist, 2 bad-mode/bad-pid/
// not-allowlisted). Each holds per ps dialect: the GNU argv below is unchanged, and
// the BSD arm has its own (see "Two ps dialects").
//
// # Two ps dialects, chosen by the host's OS
//
// The daemon runs the HOST's own `ps`, and a Linux host and a Mac have different ones.
// GNU procps, which this daemon was written against, selects by name with `-C`, draws
// a tree with `--forest`, and names a pid in /proc/<pid>/comm. BSD ps on darwin has
// none of the three (the manifest records `ps -C bash` failing with `ps: illegal
// argument: bash` on macOS 25.5, and there is no /proc). So the BSD arm asks ps only
// for what every BSD ps has, `-ax`, `-o` and `-p`, and does the selecting and the
// tree drawing in Go:
//
//   - list: one `ps -ax -o pid=,ucomm=` snapshot, matched against the allowlist here,
//     then `ps -o <fields> -p <pid,…>` through ExecAllowlisted with exactly those
//     pids on the allowlist (handleListBSD).
//   - pid: the name comes from `ps -o ucomm= -p <pid>` instead of /proc/<pid>/comm.
//   - tree: handleTreeBSD.
//
// Every snapshot is read against a name-free `ps -ax -o pid=` taken just before it,
// because BSD ps prints a process's name raw, and a name holding a newline can forge a
// row for another pid (bsdSnapshot).
//
// Which arm runs is hostOS, which is runtime.GOOS unless a test sets it, and
// BuildHandler reads it once. So a Linux test drives the BSD arm against a fake ps,
// and that arm is compiled, vetted and tested on every platform rather than only on
// the one it serves.
//
// # On darwin a `visible` name is matched against `ucomm`
//
// An implementation decision, taken under the maintainer's 2026-10-04 delegation
// ("make them and build it … adjust later"); reversible. A `visible` entry is a comm
// name: on Linux, the kernel's task name, at most 15 bytes, which is what
// /proc/<pid>/comm holds. darwin's counterpart is the process's accounting name,
// `p_comm`, at most 16 bytes (MAXCOMLEN), which BSD ps prints as `ucomm`. BSD `comm`
// has the same keyword but not the same meaning. macOS's ps (adv_cmds, print.c
// `just_command`) prints the process's argv[0], which is a full path whenever the
// process was started by one (`/bin/sleep` where ucomm says `sleep`, which
// bsdps_darwin_test.go asserts), and `(<p_comm>)` when ps may not read the process's
// arguments. Matching on it would make the same program match or miss depending on
// how it was started and by whom.
//
// Each mode compares names the way its GNU twin does. GNU list mode selects with `-C`,
// which matches a name of 15 bytes or more on its first 15 (procps-ng 4.0.7, measured
// 2026-10-04: `-C abcdefghijklmnoZZZ` finds a process whose comm is `abcdefghijklmno`),
// so BSD list mode compares the first 15 bytes of both names (listName). A name written
// for Linux list mode therefore finds the same program on a Mac, and so do the full
// program name and the 16 bytes a Mac's ps shows: `chrome-devtools`,
// `chrome-devtools-` and `chrome-devtools-mcp` all find the program whose ucomm is
// `chrome-devtools-`. Pid and tree mode compare the whole name on both, as Linux
// compares /proc/<pid>/comm, so there a long name is written as the host's kernel cut
// it: 15 bytes on Linux, 16 on a Mac. A program that names itself differently on the
// two (node's Linux task name is `MainThread`) needs both names.
//
// For the same reason a `comm` in `fields` is DISPLAYED as `ucomm` on darwin
// (bsdFields), so the column shows the name the allowlist matched, the short name
// Linux shows, rather than a path. Every other field passes through verbatim: `fields`
// is a list of the host ps's own `-o` keywords, and every default exists in both.
//
// # The allowlist is FROZEN at launch, and that is a deliberate change
//
// This daemon used to open the raw workspace `yolo-jail.jsonc` itself, from an
// inherited cwd, ON EVERY REQUEST — which is the only reason editing
// `host_processes.visible` took effect without a restart. That affordance is real
// and it is indistinguishable from the hole: the same property let an AGENT widen
// its own allowlist mid-session, with no launch and therefore no config-approval
// gate, and the config diff was not in that causal path at all.
//
// It now reads ONE file, ONCE, at startup: the settings file yolo writes after
// validating the values against the loophole manifest's `settings` declarations
// (docs/reference/pack-system.md OQ-K3). Changing what yolo-ps may show requires a
// jail restart, which is exactly where the approval gate lives.
//
// The daemon therefore never parses a config file, never knows where the workspace
// is, and never sees a key it was not handed. What it reads is a flat JSON object
// of already-validated values.
package hostprocesses

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

var DefaultFields = []string{"pid", "comm", "args", "etime", "%cpu", "%mem", "rss"}

// hostOS names the OS whose ps the daemon drives: runtime.GOOS, unless a test sets it
// to drive the other arm. BuildHandler and SelfCheck read it; nothing else may.
var hostOS = runtime.GOOS

// dialect is which ps the daemon speaks.
type dialect int

const (
	gnuPS dialect = iota // GNU procps, on Linux
	bsdPS                // BSD ps, on darwin
)

// dialectFor maps a GOOS to its ps. Only darwin is BSD: yolo runs host daemons on
// Linux and darwin alone, and Linux's is the dialect this daemon was written against.
func dialectFor(goos string) dialect {
	if goos == "darwin" {
		return bsdPS
	}
	return gnuPS
}

// psDeadlineSeconds bounds each ps the BSD arm runs and parses itself: the same 30
// seconds ExecAllowlisted gives the ps whose output it streams.
const psDeadlineSeconds = 30

// bsdListSnapshotArgv is the one question the BSD list mode asks about EVERY process:
// its pid and its name, and nothing else. The name is last because a ucomm may contain
// spaces, so it can only be read as the rest of the line.
var bsdListSnapshotArgv = []string{"ps", "-ax", "-o", "pid=,ucomm="}

// bsdPidListArgv lists every process by its pid ALONE: a column of numbers, which no
// process can write into, so it is the one BSD answer a process name cannot forge. Every
// snapshot is read against it (bsdSnapshot).
var bsdPidListArgv = []string{"ps", "-ax", "-o", "pid="}

// gnuCommMatchLen is how many leading bytes GNU `-C` compares: the 15 a Linux task name
// keeps (TASK_COMM_LEN less its NUL). BSD list mode compares the same (listName).
const gnuCommMatchLen = 15

// listName is a name as list mode compares it, on BSD: its first gnuCommMatchLen bytes,
// which is how GNU `-C` compares a name with a comm (see the package comment). Both the
// allowlisted name and the process's ucomm go through it.
func listName(name string) string {
	if len(name) > gnuCommMatchLen {
		return name[:gnuCommMatchLen]
	}
	return name
}

// Config is the resolved settings this daemon runs on.
type Config struct {
	Visible []string
	Fields  []string
}

// disabled is the fail-closed Config: no allowlist, so every request exits 3.
//
// It is what an absent, unreadable or malformed settings file resolves to, and the
// three cases share an answer on purpose. A daemon that could not read its
// allowlist has no basis for showing anything, and the alternative — refusing to
// start — would turn a transient read failure into a launch that fails with the
// daemon's readiness probe rather than with a sentence naming the file.
func disabled() Config {
	return Config{Visible: []string{}, Fields: append([]string(nil), DefaultFields...)}
}

// LoadSettings reads the flat settings file yolo wrote for this loophole: a JSON
// object of already-validated values, keyed by the names the manifest declares.
//
// It does NOT parse a yolo-jail.jsonc and does not know one exists. Every value here
// was type-checked against the manifest declaration before it was written, so this
// read is defensive rather than validating: anything of the wrong shape falls back
// to the same place an absent key does.
//
// `fields` falls back to DefaultFields when absent OR EMPTY, which is the one place
// an empty list is not taken literally — an empty `ps -o` column list is not a
// narrower view, it is a broken invocation. `visible` empty is taken literally and means the feature is off,
// which is what it has always meant.
func LoadSettings(settingsPath string) Config {
	cfg, _ := loadSettings(settingsPath)
	return cfg
}

// loadSettings is LoadSettings plus the DIAGNOSIS the health check needs and the
// daemon must not have.
//
// The daemon collapses every failure to `disabled()` on purpose: a running daemon
// with no readable allowlist has no basis for showing anything, and branching on why
// would only give it more ways to be wrong. `yolo check` has the opposite need — it
// exists to tell a human what is wrong — and the two cases it must not confuse are
// "no jail has launched this loophole yet", which is the normal state of a fresh
// machine, and "the file is there and does not parse", which is a real fault.
//
// ok is false only for the second. A MISSING file returns ok=true with the
// fail-closed Config, because absence is not a failure of anything.
func loadSettings(settingsPath string) (cfg Config, ok bool) {
	if settingsPath == "" {
		return disabled(), true
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return disabled(), true
	}
	decoded, err := json5.Decode(data)
	if err != nil {
		return disabled(), false
	}
	root, isMap := decoded.(*jsonx.OrderedMap)
	if !isMap {
		return disabled(), false
	}
	return Config{
		Visible: strListOrEmpty(root, "visible"),
		Fields:  strListOrDefault(root, "fields", DefaultFields),
	}, true
}

// strListOrEmpty returns the string elements of m[key], or [] if absent,
// null, or not a list.
func strListOrEmpty(m *jsonx.OrderedMap, key string) []string {
	if m == nil {
		return []string{}
	}
	v, ok := m.Get(key)
	if !ok || v == nil {
		return []string{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// strListOrDefault returns the string elements of m[key], defaulting to def.
// The default applies to the RAW value, so an absent/empty/non-list value →
// def (then filtered, a no-op); a NON-EMPTY list → filtered to its str
// elements (which may be []).
func strListOrDefault(m *jsonx.OrderedMap, key string, def []string) []string {
	var raw []any
	if m != nil {
		if v, ok := m.Get(key); ok && v != nil {
			if arr, ok := v.([]any); ok {
				raw = arr
			}
		}
	}
	// empty/absent/non-list raw -> use the default.
	if len(raw) == 0 {
		raw = make([]any, len(def))
		for i, d := range def {
			raw[i] = d
		}
	}
	out := []string{}
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// BuildHandler returns the hostservice.Handler for an ALREADY-RESOLVED config.
//
// It takes the values, not a path, and that signature is the freeze: there is no
// file for a request to re-read, so the "editing the allowlist takes effect on the
// next request" behaviour is not merely turned off, it is unrepresentable. The
// allowlist is closed over once, at startup, from the settings file yolo wrote at
// launch (see the package comment for what that trades away and why).
func BuildHandler(cfg Config) hostservice.Handler {
	visible := map[string]struct{}{}
	for _, c := range cfg.Visible {
		visible[c] = struct{}{}
	}
	fields := append([]string(nil), cfg.Fields...)
	// Read once, like the allowlist: a handler never changes dialect mid-life.
	d := dialectFor(hostOS)
	return func(s *hostservice.Session) {
		// mode = str(request["mode"] or "list"). A truthy NON-string (e.g. 5,
		// {...}) is stringified and falls through to the unknown-mode exit-2
		// branch — it must NOT silently run list mode. Falsy (absent, "", 0,
		// null, false, []) -> "list".
		mode := pyStrOrList(func() (any, bool) { return s.Get("mode") })

		if len(visible) == 0 {
			// Names the CURRENT spelling, and ONLY it. The old top-level
			// `host_processes.visible` does not work any more — it was honored through the
			// step that moved the keys and REFUSED by the step that deleted it
			// (config.validateHostProcessesRetired), so there is no fold-in left to
			// mention. Naming the retired key here would teach it, and this is the one
			// line a user reads at the moment they are about to go edit a config.
			s.Stderr("loopholes.host-processes.settings.visible is empty — nothing to show. " +
				"Add process names to it and RESTART the jail: the allowlist is resolved " +
				"once at launch, so an edit does not take effect in a running jail.\n")
			s.Exit(3)
			return
		}

		switch mode {
		case "list":
			handleList(s, visible, fields, d)
		case "tree":
			handleTree(s, visible, d)
		case "pid":
			handlePid(s, visible, fields, d)
		default:
			s.Stderr("unknown mode: " + pytext.Repr(mode) + "\n")
			s.Exit(2)
		}
	}
}

// pyStrOrList implements str(mode or "list"): if the value is falsy (absent,
// "", 0, 0.0, false, null, empty list/dict) -> "list"; otherwise str(value).
// For a string that's str(value)==value; for other truthy types we produce the
// str() form so a bogus mode still routes to the unknown-mode exit-2 branch
// (e.g. 5 -> "5", true -> "True").
func pyStrOrList(get func() (any, bool)) string {
	v, ok := get()
	if !ok || !pyTruthy(v) {
		return "list"
	}
	return pyStr(v)
}

func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case []any:
		return len(t) != 0
	case *jsonx.OrderedMap:
		return t.Len() != 0
	default:
		// jsonx integer literal: truthy unless it's zero.
		s, _ := jsonx.DumpsCompact(v)
		s = strings.TrimSpace(s)
		return s != "0" && s != "-0" && s != ""
	}
}

// pyStr renders str(x) for the types a JSON "mode" could decode to.
func pyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	default:
		// int / float literal -> its literal text; containers -> the compact
		// JSON form (close enough to route to unknown-mode; real clients never
		// send these).
		s, _ := jsonx.DumpsCompact(v)
		return s
	}
}

// handleList runs `ps -o <fields> -C <comm>...` with an allowlist.
// list branch: argv = ["ps","-o",joined] + ["-C",comm] for each sorted comm;
// allowlist = visible ∪ {"ps","-o","-C",joined}. BSD ps has no -C: handleListBSD.
func handleList(s *hostservice.Session, visible map[string]struct{}, fields []string, d dialect) {
	if d == bsdPS {
		handleListBSD(s, visible, fields)
		return
	}
	joined := strings.Join(fields, ",")
	argv := []string{"ps", "-o", joined}
	comms := sortedKeys(visible)
	for _, comm := range comms {
		argv = append(argv, "-C", comm)
	}
	allow := map[string]struct{}{}
	for c := range visible {
		allow[c] = struct{}{}
	}
	for _, k := range []string{"ps", "-o", "-C", joined} {
		allow[k] = struct{}{}
	}
	s.ExecAllowlisted(func(*jsonx.OrderedMap) []string { return argv }, allow, nil, 30_000_000_000)
}

// handleListBSD is list mode on BSD ps, which cannot select by name: the
// bsdListSnapshotArgv snapshot is matched against the allowlist here, and the matching
// pids, and nothing else, go to `ps -o <fields> -p <pid,…>` through ExecAllowlisted,
// whose allowlist is exactly that argv.
//
// TWO EXECS WHERE GNU NEEDS ONE, so there is a window GNU's `-C` does not have: a pid
// the snapshot matched can exit and be REUSED before the second ps runs. It is the
// window pid mode has always had between reading a name and running its own ps, and on
// darwin it is narrower than it sounds, since pids are handed out in sequence and reuse
// within one request needs the whole pid space to wrap in between.
//
// No match keeps GNU's answer, the column header and exit 1, which is what
// `ps -o … -C <comm>` prints when nothing has that name (bsdHeaderOnly).
func handleListBSD(s *hostservice.Session, visible map[string]struct{}, fields []string) {
	ctx, cancel := context.WithTimeout(context.Background(), psDeadlineSeconds*time.Second)
	defer cancel()
	procs, notListed, err := bsdSnapshot(ctx, psDeadlineSeconds, bsdListSnapshotArgv, false)
	if err != nil {
		s.Stderr("list mode failed: " + err.Error() + "\n" + checkHostPS)
		s.Exit(1)
		return
	}
	if notListed != "" {
		// `ps -ax` lists at least the daemon itself, so an empty answer with a failing
		// status is a ps that could not do the one thing this mode needs. Said here,
		// because the next step would turn it into a bare exit 1 with no output.
		s.Stderr("list mode failed: " + notListed + "\n" + checkHostPS)
		s.Exit(1)
		return
	}
	joined := strings.Join(bsdFields(fields), ",")
	names := map[string]struct{}{}
	for name := range visible {
		names[listName(name)] = struct{}{}
	}
	var pids []string
	for _, p := range procs {
		if _, ok := names[listName(p.comm)]; ok {
			pids = append(pids, strconv.Itoa(p.pid))
		}
	}
	if len(pids) == 0 {
		if header := bsdHeaderOnly(ctx, joined); header != "" {
			s.Stdout(header)
		}
		s.Exit(1)
		return
	}
	pidList := strings.Join(pids, ",")
	argv := []string{"ps", "-o", joined, "-p", pidList}
	allow := map[string]struct{}{"ps": {}, "-o": {}, joined: {}, "-p": {}, pidList: {}}
	s.ExecAllowlisted(func(*jsonx.OrderedMap) []string { return argv }, allow, nil, psDeadlineSeconds*time.Second)
}

// checkHostPS is the next step after a ps the daemon could not use: the self-check
// asks the same ps the same question (psCheck) and says what to change.
const checkHostPS = "Run `yolo check` on the host: it tests the ps this daemon runs and names the fix.\n"

// bsdHeaderOnly returns the column header BSD ps prints for these fields, or "" when it
// prints none (every field spelled `name=`), for a list that matched nothing.
//
// The header is taken from a query about the daemon's OWN pid, the one process certain
// to exist, and the data row under it is discarded. Only a FIRST line followed by a
// second one is a header: with every header suppressed, the first line is that row,
// which nobody asked to see.
func bsdHeaderOnly(ctx context.Context, joined string) string {
	run, err := runPS(ctx, psDeadlineSeconds, []string{"ps", "-o", joined, "-p", strconv.Itoa(os.Getpid())})
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(run.stdout), "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}
	return lines[0] + "\n"
}

// bsdFields is the `fields` list as BSD ps is asked for it: `comm` becomes `ucomm`, so
// the column shows the name the allowlist matched (see the package comment), and every
// other keyword passes through verbatim. `comm=<header>` keeps its header.
func bsdFields(fields []string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		if f == "comm" || strings.HasPrefix(f, "comm=") {
			f = "u" + f
		}
		out[i] = f
	}
	return out
}

// handlePid runs `ps -o <fields> -p <pid>` after verifying the pid's comm is
// allowlisted. The comm comes from /proc/<pid>/comm under GNU and from
// `ps -o ucomm= -p <pid>` under BSD (commOf).
func handlePid(s *hostservice.Session, visible map[string]struct{}, fields []string, d dialect) {
	pidV, ok := s.Get("pid")
	pid, isInt := asIntStrict(pidV)
	if !ok || !isInt {
		s.Stderr("pid mode requires integer 'pid' in request\n")
		s.Exit(2)
		return
	}
	comm, found, err := commOf(d, pid)
	if err != nil {
		s.Stderr("pid mode failed: " + err.Error() + "\n" + checkHostPS)
		s.Exit(1)
		return
	}
	if !found {
		s.Stderr("pid " + strconv.Itoa(pid) + " not found\n")
		s.Exit(1)
		return
	}
	if _, allowed := visible[comm]; !allowed {
		s.Stderr("pid " + strconv.Itoa(pid) + " has comm=" + pytext.Repr(comm) + " which is not allowlisted\n")
		s.Exit(2)
		return
	}
	if d == bsdPS {
		fields = bsdFields(fields)
	}
	joined := strings.Join(fields, ",")
	pidStr := strconv.Itoa(pid)
	argv := []string{"ps", "-o", joined, "-p", pidStr}
	allow := map[string]struct{}{
		"ps": {}, "-o": {}, joined: {}, "-p": {}, pidStr: {}, comm: {},
	}
	// argv_positions = all positions.
	positions := map[int]struct{}{}
	for i := range argv {
		positions[i] = struct{}{}
	}
	s.ExecAllowlisted(func(*jsonx.OrderedMap) []string { return argv }, allow, positions, 30_000_000_000)
}

// commOf names a pid the way the allowlist matches it. found is false when no such
// process exists. err is set only when the BSD lookup's ps could not run or overran its
// deadline, which is a broken daemon rather than an absent pid.
func commOf(d dialect, pid int) (comm string, found bool, err error) {
	if d == gnuPS {
		comm, found = linuxComm(strconv.Itoa(pid))
		return comm, found, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), psDeadlineSeconds*time.Second)
	defer cancel()
	run, err := runPS(ctx, psDeadlineSeconds, []string{"ps", "-o", "ucomm=", "-p", strconv.Itoa(pid)})
	if err != nil {
		return "", false, err
	}
	// A pid BSD ps does not know prints nothing and exits 1, which is the not-found
	// answer, so the status is not consulted: an empty name is.
	//
	// The name is ALL of the output, not its first line. BSD ps prints ucomm raw, and a
	// file name may hold a newline, so the first line of `sway\nx` is a name the process
	// does not have; whole, it matches nothing, as exactly as Linux compares
	// /proc/<pid>/comm.
	comm = strings.TrimSpace(string(run.stdout))
	return comm, comm != "", nil
}

// linuxComm reads /proc/<pid>/comm, the kernel's own name for a process. pid must be
// decimal, so a malformed one can name no other file.
func linuxComm(pid string) (string, bool) {
	if _, err := strconv.Atoi(pid); err != nil {
		return "", false
	}
	b, err := os.ReadFile("/proc/" + pid + "/comm")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// psRun is what one ps the daemon parses itself produced.
type psRun struct {
	stdout, stderr []byte
	rc             int // ps's exit status; 0 when it succeeded
}

// psTimeoutError is the deadline passing, worded as tree mode has always worded it:
// "Command '<argv list repr>' timed out after N seconds" (TestTreeTimeoutStderrGolden).
type psTimeoutError struct {
	argv []string
	secs int
}

func (e *psTimeoutError) Error() string {
	return "Command '" + pyReprStrList(e.argv) + "' timed out after " + strconv.Itoa(e.secs) + " seconds"
}

// runPS runs one ps whose output the daemon reads rather than streams. A ps that ran
// and exited non-zero is NOT an error: its stdout is returned regardless, with the
// status in rc, which is how tree mode has always read ps. err is set only when ps
// could not be started, or when ctx's deadline (secs, for the message) passed.
func runPS(ctx context.Context, secs int, argv []string) (psRun, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return psRun{}, &psTimeoutError{argv: argv, secs: secs}
	}
	run := psRun{stdout: out, stderr: []byte(stderr.String())}
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return psRun{}, err
		}
		run.rc = ee.ExitCode()
	}
	return run, nil
}

// bsdProc is one row of a BSD snapshot.
type bsdProc struct {
	pid, ppid int
	comm      string // ucomm
}

// bsdSnapshot runs a BSD snapshot (bsdListSnapshotArgv or bsdTreeSnapshotArgv) and
// returns the rows it can believe, sorted by pid.
//
// A PROCESS NAME CAN WRITE ROWS INTO A BSD SNAPSHOT. BSD ps prints ucomm raw (adv_cmds
// print.c, ucomm(): a bare printf of p_comm, where args and comm go through strvis),
// and darwin copies p_comm from the executable's file name, which may hold a newline. A
// process run from a file named "a\n600 sway" therefore prints its own row, `500 a`
// when its pid is 500, and then a line claiming that pid 600 is called sway. On macos-user the agent's own processes are in
// the host's `ps -ax`, so a believed forged row would have the host show it any
// process's command line, the very thing Seatbelt denies it. GNU procps escapes control
// characters, so the GNU arm has no such row.
//
// Two rules leave a forged row nothing to select:
//
//   - a pid on two rows is dropped, both rows (parseBSDSnapshot): the kernel holds a pid
//     once, so one row is forged, and nothing tells which.
//   - a pid the name-free listing (bsdPidListArgv), taken just BEFORE the snapshot, did
//     not hold is dropped. Otherwise a forged row could name a pid nobody holds, and
//     select whatever process is born there before the next ps runs. Since darwin hands
//     out pids in sequence, a pid freed between the two listings comes back only after
//     the whole pid space wraps.
//
// A process born between the two listings is missed by this request; the next one sees
// it. The process that forged the rows keeps its own first line, a name it chose, which
// shows nothing it could not show by naming a file `sway`.
//
// err is runPS's: a ps that could not start or overran the deadline (secs names it).
// notListed is set when a ps listed no process and exited non-zero, and says which and
// how; procs is then empty.
func bsdSnapshot(ctx context.Context, secs int, argv []string, withPPID bool) (procs []bsdProc, notListed string, err error) {
	listing, err := runPS(ctx, secs, bsdPidListArgv)
	if err != nil {
		return nil, "", err
	}
	listed := map[int]bool{}
	for _, line := range strings.Split(string(listing.stdout), "\n") {
		pidTok, _ := cutField(line)
		if pid, err := strconv.Atoi(pidTok); err == nil {
			listed[pid] = true
		}
	}
	if len(listed) == 0 && listing.rc != 0 {
		return nil, notListing(bsdPidListArgv, listing), nil
	}
	snap, err := runPS(ctx, secs, argv)
	if err != nil {
		return nil, "", err
	}
	rows := parseBSDSnapshot(snap.stdout, withPPID)
	if len(rows) == 0 && snap.rc != 0 {
		return nil, notListing(argv, snap), nil
	}
	for _, p := range rows {
		if listed[p.pid] {
			procs = append(procs, p)
		}
	}
	return procs, "", nil
}

// notListing says that the ps argv ran, exited non-zero and listed no process.
func notListing(argv []string, run psRun) string {
	return pyReprStrList(argv) + " exited " + strconv.Itoa(run.rc) + " without listing a process: " +
		strings.TrimSpace(string(run.stderr))
}

// parseBSDSnapshot reads the rows of bsdListSnapshotArgv (withPPID false) or
// bsdTreeSnapshotArgv (withPPID true), sorted by pid. The name is the REST of each
// line, trimmed, because it is the last column and may contain spaces. A row whose
// numbers do not parse is skipped, never guessed at, and a pid on two rows is dropped
// with both of them: one is a row a process name forged (bsdSnapshot says how).
func parseBSDSnapshot(out []byte, withPPID bool) []bsdProc {
	var procs []bsdProc
	for _, line := range strings.Split(string(out), "\n") {
		pidTok, rest := cutField(line)
		pid, err := strconv.Atoi(pidTok)
		if err != nil {
			continue
		}
		p := bsdProc{pid: pid}
		if withPPID {
			var ppidTok string
			ppidTok, rest = cutField(rest)
			if p.ppid, err = strconv.Atoi(ppidTok); err != nil {
				continue
			}
		}
		p.comm = strings.TrimSpace(rest)
		procs = append(procs, p)
	}
	rows := map[int]int{}
	for _, p := range procs {
		rows[p.pid]++
	}
	var unique []bsdProc
	for _, p := range procs {
		if rows[p.pid] == 1 {
			unique = append(unique, p)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].pid < unique[j].pid })
	return unique
}

// cutField returns the first whitespace-separated field of s and everything after the
// whitespace that ends it.
func cutField(s string) (field, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// asIntStrict accepts only an actual JSON integer (a float like 42.0 or a
// string "42" does NOT count).
func asIntStrict(v any) (int, bool) {
	// jsonx decodes JSON integers to its internal integer type (re-encodes with
	// no "."); a float decodes to float64. Distinguish by re-encoding.
	if v == nil {
		return 0, false
	}
	if _, isFloat := v.(float64); isFloat {
		return 0, false // 42.0 is a float, not an int
	}
	if _, isStr := v.(string); isStr {
		return 0, false
	}
	if _, isBool := v.(bool); isBool {
		return 0, false
	}
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return n, true
}
