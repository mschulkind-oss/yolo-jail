package run

// launchlog.go persists what the LAUNCHER says, because until now it said it exactly
// once, to a terminal, and nowhere else.
//
// # The gap it closes
//
// A launch prints from two processes and only one half was ever written down. The
// entrypoint's half is teed into `<workspace>/.yolo/boot.log` (bootlog.go); the host
// launcher's half — the flake source, the nix build, image delivery, the jail line,
// and every pack disclosure — went to the terminal and to nothing else. Checked
// 2026-09-10: no log sink held any of those phrases, and `host-perf.log` records the
// launch SPANS rather than the text. So the diagnoser's evidence for the first half of
// a launch was gone the moment it scrolled, which is worst exactly where it is needed —
// a launch that REFUSED, where there is no jail left to read anything from
// (docs/reference/report-tiers.md's launch stream *Persist the launcher's half*).
//
// # Where, and why here
//
// `<workspace>/.yolo/`, beside `boot.log`, so both halves of one launch sit in one
// directory — decided by precedent (perf-logging.md D2, which put `host-perf.log`
// there), not opened as a question. D2's caveat is inherited whole: the directory is
// inside the live workspace bind, so a jail can write the host's record. Nothing reads
// this file back to make a decision, which is what keeps that a disclosure rather than
// a trust hole; D2's named remediation (a host-global logs dir) is the fix for the day
// it stops being true.
//
// # Retention, and why it is not boot.log's
//
// boot.log rotates one generation aside (`boot.log.prev`) because a jail boots once per
// launch and the question is "did it work last time?". A HOST accumulates launches
// across every workspace, so the shape that fits is the perf log's: one appended run
// block per launch, trimmed to the newest perf.MaxRuns at open. Same directory, same
// bound, one implementation (perf.trimRuns, through perf.TrimRunsInOpenFile).
//
// # Why it is never fatal, and never prints
//
// A logger that can stop a launch is a worse bug than the blindness it fixes, and one
// that ANNOUNCES its own failure adds a line to the launch stream P4 says must stay
// readable. Every failure — no workspace, a read-only mount, a full disk — degrades to
// the plain writers the launch already had, silently. The launch does not learn that
// the log failed, because there is nothing it could usefully do about it.
//
// # What it deliberately does not capture
//
// The version banner, which `internal/cli` prints BEFORE Run is called (the header
// records the version instead, which is the same fact in the one place a reader of this
// file will look for it). The live-overlay refusal, which is Run's first act and
// refuses before any side effect — including this one. And the jailed command's own
// output: the container is spawned with the process's real stdout/stderr (proxy_other.go,
// ttyproxy), never with Options.Stdout, so an interactive session cannot end up in here.
// The macos-user backend draws the same line in the same place — its Deps take the teed
// writer below, while every child it starts keeps the process's own streams
// (internal/macosuser/real.go's runReal) — so "what the launcher said" and "what the
// session did" stay as separate here as they are on the container backends.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// LaunchLogName is the host launcher's half of one launch's record, under
// <workspace>/.yolo/ beside boot.log (the jail's half) and host-perf.log (the spans).
const LaunchLogName = "launch.log"

// launchRunPrefix delimits runs in the file. The trim splits on the same prefix, so the
// delimiter and the splitter are one spelling, here.
const launchRunPrefix = "=== yolo launch"

// launchLog is the open per-launch log. A nil *launchLog is valid and does nothing,
// which is what every failure path returns.
type launchLog struct {
	f *os.File
	// restoreOut un-publishes the macos-user backend's writer (see attachLaunchLog).
	// nil on every path that published nothing.
	restoreOut func()
}

// attachLaunchLog opens the log and INSTALLS the tee on o.Stdout and o.Stderr.
//
// It mutates Options rather than returning writers for the caller to install, for the
// reason attachBootLog states: the returned-writer shape has a silent failure mode with
// no test that can catch it — a caller that opens the log and forgets to wire it
// produces a complete, correct-looking, permanently EMPTY log. Both writers are ALWAYS
// left usable: on any failure they stay exactly what they were and the returned
// *launchLog is nil, which every method accepts.
//
// BOTH streams, because the launch's own split is not a rule the reader can rely on:
// the notices go to stderr and the nix build, the image delivery, the config diff and
// its prompt go to stdout (§2.4). A log of one of them would be a log of half a launch,
// and which half would depend on the line.
//
// AND IT PUBLISHES THE TEED STDOUT TO THE macos-user BACKEND, which is the one backend
// the tee could not reach: it composes its own writer three packages away, in
// internal/cli/commands.go out of macosuser.RealDeps, and that writer was os.Stdout — so
// a macos-user launch left a log holding this header and the trailer with the whole
// launch missing between them (docs/plans/handoff-macos-user-open-threads.md §7). A
// PUBLISHED writer rather than an argument because the same RealDeps builds the deps for
// `yolo macos-setup` and its three siblings, which have no pipeline to take one from and
// must keep printing to the process; nothing publishes on their path. The undo rides on
// the returned *launchLog, because finish closes the file this writer tees into.
func attachLaunchLog(o *Options) *launchLog {
	if o.Workspace == "" {
		return nil
	}
	// EnsureWorkspaceStateDir (inside OpenWorkspaceStateFile) rather than a bare MkdirAll:
	// this is the EARLIEST thing in a launch that creates <workspace>/.yolo — before
	// staging, before the config load, in every backend and on a launch that goes on to
	// refuse — so it is where the directory gets the .gitignore that keeps the secrets below
	// out of the user's next commit.
	//
	// And it opens the file BENEATH a root on `.yolo` (paths.OpenWorkspaceStateFile), never
	// by path: `.yolo` is jail-writable, and a link the last jail left at launch.log took this
	// launch's every line, which quote the jail-writable workspace config, to the host file it
	// named.
	f, err := paths.OpenWorkspaceStateFile(o.Workspace, LaunchLogName, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil
	}
	// Trim at open, never at exit: a launch that hangs or is killed still leaves its
	// lines on disk, and the signal teardown's os.Exit would skip a rewrite anyway.
	perf.TrimRunsInOpenFile(f, launchRunPrefix, perf.MaxRuns-1)
	l := &launchLog{f: f}
	l.writeHeader(o)
	o.Stdout = teeLog{w: o.Stdout, log: f}
	o.Stderr = teeLog{w: o.Stderr, log: f}
	l.restoreOut = macosuser.SetLaunchWriter(o.Stdout)
	return l
}

// writeHeader records what the launch WAS, so a run block read later is self-describing.
//
// The version answers the first question anyone asks of a launch that misbehaved — "is
// this even the code I think it is?" — and it is recorded here because it is the one
// launch line the tee cannot see: `internal/cli` prints the banner before Run exists.
// version.Get("") is the ldflags stamp or "unknown"; it deliberately does not resolve a
// repo root, because the flake source gets its own line inside the block below.
func (l *launchLog) writeHeader(o *Options) {
	if l == nil {
		return
	}
	fmt.Fprintf(l.f, "%s %s ===\n", launchRunPrefix, time.Now().Format("2006-01-02T15:04:05-0700"))
	fmt.Fprintf(l.f, "  yolo=%s  workspace=%s\n", version.Get(""), o.Workspace)
	var absent []string
	for _, k := range launchLogFacts {
		if v := o.Getenv(k); v != "" {
			fmt.Fprintf(l.f, "  %s=%s\n", k, v)
			continue
		}
		absent = append(absent, k)
	}
	// Name the absent ones too, for bootlog.go's reason: an omitted line reads as "not
	// recorded" rather than "empty", and every variable below changes how a later line
	// in this same block should be read.
	if len(absent) > 0 {
		sort.Strings(absent)
		fmt.Fprintf(l.f, "  (unset: %s)\n", strings.Join(absent, " "))
	}
}

// launchLogFacts are the environment opt-ins and hatches that change what a launch DOES
// without appearing in any line it prints. YOLO_VERSION is here as the nested-launch
// marker: set means this launcher is itself running inside a jail, which is the fact
// that explains --net=host, --userns=host and every carve-out that follows from them.
var launchLogFacts = []string{
	"YOLO_VERSION",
	"YOLO_REPO_ROOT",
	"YOLO_RUNTIME",
	"YOLO_ALLOW_STALE_IMAGE",
	"YOLO_ALLOW_SOURCE_SKEW",
	"YOLO_NO_HOST_LOOPBACK",
	"YOLO_STORE_PACKAGES",
	"YOLO_ALLOW_UNREACHABLE_SERVICES",
	paths.HoldOnRefusalEnv,
}

// finish records how the launch ENDED. Without it the block's last line is ambiguous in
// the case that matters most: a refused launch and one killed mid-build both end with
// whatever happened to be printed last.
//
// A teardown that exits through a signal handler's os.Exit never reaches this, which is
// why nothing above waits for it — the header is written at open and every line lands as
// it happens.
func (l *launchLog) finish(rc int) {
	if l == nil {
		return
	}
	fmt.Fprintf(l.f, "=== launch done, rc=%d ===\n", rc)
	// Before the close, so nothing can be handed a writer whose log half is a closed
	// file — a launch is one process, but a test binary is many launches.
	if l.restoreOut != nil {
		l.restoreOut()
	}
	_ = l.f.Close()
}

// teeLog passes a launch line through to the terminal UNCHANGED and appends an
// ANSI-free copy to the log.
//
// THE TERMINAL COPY IS THE ORIGINAL BYTES, deliberately. The config-change prompt writes
// its `[y/N] ` through Options.Stdout without a newline and without the printer
// (preflight.go), so anything that reformatted, buffered or line-split what passes
// through here would change what a human is asked. The tee writes the terminal first,
// reports the terminal's own count, and treats the log as best-effort after it.
//
// THE LOG COPY IS STRIPPED, equally deliberately: color is resolved against the real
// terminal (Options.pr consults IsTTYStdout, not the writer), so on an interactive
// launch every styled line arrives here already rendered to escape sequences. A log full
// of them is a log nobody greps twice.
type teeLog struct {
	w   io.Writer
	log io.Writer
}

func (t teeLog) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		_, _ = t.log.Write(stripANSI(p[:n]))
	}
	return n, err
}

// ansiEscape matches a CSI sequence — the colors richtext.ToANSI emits, and any cursor
// motion a subprocess digest happens to carry through.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// WriteTransient is the terminal half alone, for a live progress line's redraws
// (internal/progress): a frame redrawn in place every second is not a line of the
// launch, and a log holding them is carriage-return spam. The step's persistent
// start and result lines still arrive through Write, so the log records every step
// that was shown.
func (t teeLog) WriteTransient(p []byte) (int, error) { return t.w.Write(p) }

// WriteRedacted is a line the terminal gets whole and the log gets as logCopy, for a line whose
// words a machine log may not keep: the host launch log's argv disclosures and starting line
// (HostLaunchLog), which name what the user typed after the program.
func (t teeLog) WriteRedacted(p, logCopy []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		_, _ = t.log.Write(stripANSI(logCopy))
	}
	return n, err
}

func stripANSI(p []byte) []byte { return ansiEscape.ReplaceAll(p, nil) }

// HostLaunchLogName is the HOST NOTCH's launch log, under GLOBAL_STORAGE/logs beside
// launches.log: what `yolo host -- <cmd>` said before it handed over, which until 2026-10-04 went
// to the terminal and nowhere else (report-tiers.md's launch stream, *The launcher persists its
// half*).
//
// NOT <cwd>/.yolo/launch.log, and the difference is the point. A host launch has no workspace: it
// runs wherever its command was typed, the home included, and a wrapper launch from a desktop
// launcher runs it from wherever that launcher's cwd is. Writing beside the jail's launch.log would
// mint a .yolo marker in every such directory (paths.WorkspaceScopeBreach's hazard, in the home),
// so the host's record is machine-wide, keyed by the directory's short code like launches.log.
const HostLaunchLogName = "host-launch.log"

// hostLaunchRunPrefix delimits a host launch's block, and the trim splits on it.
const hostLaunchRunPrefix = "=== yolo host launch"

// HostLaunchLogPath is where host launch blocks land.
func HostLaunchLogPath() string {
	return filepath.Join(paths.GlobalStorage(), "logs", HostLaunchLogName)
}

// HostLaunchLog is one `yolo host -- <cmd>` launch's block in HostLaunchLogPath: a header, every
// line yolo itself printed to its stderr through Writer (ANSI-stripped, progress redraws kept
// off, as teeLog does for a jail's launch), and a trailer saying how it ended. A nil
// *HostLaunchLog does nothing, which is what every failure to open returns: like the jail's
// launch log it is never fatal and never prints.
//
// IT HOLDS ONLY YOLO'S OWN LINES. The command's output never passes through it: the exec replaces
// this process, and an agent yolo stays the parent of (launch-owned services, a managed Codex
// login) is handed the process's own stderr, so it keeps its terminal and nothing it prints lands
// here.
//
// AND NOTHING TYPED AFTER THE PROGRAM, NOR WHERE IT WAS TYPED. The file is machine-wide, kept for
// perf.MaxRuns launches, and its directory is one a jail may mount, so it keeps OQ-PR3's rule for
// the machine-wide record (a directory by its short code, never its path): an argument can be a
// secret, or a prompt, and the directory names a project. The header names the program by its
// base name. A line whose words carry the argv, an argv disclosure or the starting line, reaches
// the log through teeLog.WriteRedacted, in words the caller chose (cli's printHostLinesLogged).
// And every line is redacted on its way in (hostLaunchRedactor): a program typed as a path is
// named by its base name, and the directory the command ran in is written `<cwd>`.
//
// EVERY LINE NAMES ITS LAUNCH (`(pid <n>)`), because the blocks interleave. Host wrappers make
// concurrent host launches ordinary, and a launch yolo stays resident under writes its last lines
// and its trailer when its agent exits, hours later and below other launches' blocks; a trailer
// read by where it sits would then report another launch's outcome. A trailer is matched to its
// header by that id. Within one launch's life no other process holds its pid, so the id is unique
// among the blocks that can interleave with it.
type HostLaunchLog struct {
	mu     sync.Mutex
	f      *os.File
	id     string
	redact func(string) string
	// midLine is whether the last body write ended without a newline, so the next one continues
	// that line rather than starting a new one with the id.
	midLine bool
	ended   bool
}

// hostLaunchPID is the process id a launch's lines are named by; a var so a test can open two
// launches as two processes would.
var hostLaunchPID = os.Getpid

// OpenHostLaunchLog opens the host launch log, trims it to the newest perf.MaxRuns blocks and
// writes this launch's header, all under the sibling lock (openLockedMachineLog), so two host
// launches starting together cannot interleave a trim with a header. workspace is the directory
// the command ran in and program the command as typed; neither is written whole.
func OpenHostLaunchLog(workspace, program string) *HostLaunchLog {
	id := fmt.Sprintf("(pid %d)", hostLaunchPID())
	f := openLockedMachineLog(HostLaunchLogPath(), func(f *os.File) {
		perf.TrimRunsInOpenFile(f, hostLaunchRunPrefix, perf.MaxRuns-1)
		fmt.Fprintf(f, "%s %s %s ===\n", hostLaunchRunPrefix, time.Now().Format("2006-01-02T15:04:05-0700"), id)
		fmt.Fprintf(f, "  yolo=%s  jail=%s  program=%s\n", version.Get(""),
			paths.JailShortHash(runtime.FromWorkspace(workspace)), filepath.Base(program))
	})
	if f == nil {
		return nil
	}
	return &HostLaunchLog{f: f, id: id, redact: hostLaunchRedactor(workspace, program, paths.Home())}
}

// Writer is errw with this log teed beneath it, or errw untouched when the log did not open (a
// nil log): the caller always gets a stream to print to.
func (l *HostLaunchLog) Writer(errw io.Writer) io.Writer {
	if l == nil {
		return errw
	}
	return teeLog{w: errw, log: hostLaunchBody{l}}
}

// hostLaunchBody is the log half of the tee: each line redacted, and named by the launch's id.
type hostLaunchBody struct{ l *HostLaunchLog }

func (b hostLaunchBody) Write(p []byte) (int, error) {
	l := b.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return len(p), nil
	}
	var out strings.Builder
	for _, piece := range strings.SplitAfter(string(p), "\n") {
		if piece == "" {
			continue
		}
		if !l.midLine {
			out.WriteString(l.id + " ")
		}
		out.WriteString(l.redact(piece))
		l.midLine = !strings.HasSuffix(piece, "\n")
	}
	_, _ = l.f.WriteString(out.String())
	return len(p), nil
}

// event writes one of the block's own `=== … ===` lines, ending any line a body write left open.
func (l *HostLaunchLog) event(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.midLine {
		_, _ = l.f.WriteString("\n")
		l.midLine = false
	}
	_, _ = l.f.WriteString(line + "\n")
}

// HandedOver records that the launch handed the process over (how: "exec"), the last line a
// launch that replaces itself can write. The file stays open: the exec closes it (Go opens every
// file close-on-exec), and an exec that fails returns here, to Done.
func (l *HostLaunchLog) HandedOver(how string) {
	if l == nil {
		return
	}
	l.event(fmt.Sprintf("=== handed over %s: %s ===", l.id, how))
}

// Done records how a launch that returned ended, and closes the log. Idempotent.
func (l *HostLaunchLog) Done(rc int) {
	if l == nil || l.ended {
		return
	}
	l.event(fmt.Sprintf("=== launch done %s, rc=%d ===", l.id, rc))
	l.mu.Lock()
	l.ended = true
	_ = l.f.Close()
	l.mu.Unlock()
}

// hostLaunchRedactor is what every line of a host launch's block passes through on its way into the
// log, for the rule HostLaunchLog states: a program typed as a path (program, holding a separator)
// becomes its base name, in each spelling a line may give it (as typed, absolute, and under home
// as `~/…`); and the directory the command ran in becomes `<cwd>`, where a line names it or a path
// under it. The directory is left alone when it is the root, the home or above the home, where it
// names no project and would rewrite every path under the home a line names.
func hostLaunchRedactor(workspace, program, home string) func(string) string {
	tilde := func(p string) string {
		if home != "" && home != "/" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
			return "~" + strings.TrimPrefix(p, home)
		}
		return p
	}
	var pairs []string
	if strings.ContainsRune(program, os.PathSeparator) {
		abs := program
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(workspace, program)
		}
		base := filepath.Base(program)
		// Longest first: a replacer tries its pairs in order at each position.
		for _, spelling := range []string{abs, tilde(abs), program} {
			if spelling != base {
				pairs = append(pairs, spelling, base)
			}
		}
	}
	typed := strings.NewReplacer(pairs...)
	var dir *regexp.Regexp
	ws := filepath.Clean(workspace)
	namesAProject := filepath.IsAbs(ws) && ws != string(os.PathSeparator) && ws != home &&
		!strings.HasPrefix(home, ws+string(os.PathSeparator))
	if namesAProject {
		// A whole path: the directory followed by a separator, the end, or a character no path
		// component continues with, so a sibling named with it as a prefix is left alone.
		dir = regexp.MustCompile("(?:" + regexp.QuoteMeta(ws) + "|" + regexp.QuoteMeta(tilde(ws)) + ")" +
			`(/|$|[^A-Za-z0-9._-])`)
	}
	return func(line string) string {
		line = typed.Replace(line)
		if dir != nil {
			line = dir.ReplaceAllString(line, "<cwd>${1}")
		}
		return line
	}
}

// openLockedMachineLog opens a machine-wide log under GLOBAL_STORAGE/logs for appending
// (read-write, for the trim) and runs init on it holding an exclusive flock on a SIBLING
// `<path>.lock`, then releases the lock and returns the file; nil when any step fails.
//
// THE LOCK IS FOR THE TRIM. perf.TrimRunsInOpenFile reads the file and rewrites it, unlocked,
// which in a per-workspace file is safe enough (one workspace's launches rarely start together),
// and in a machine-wide one is not: host wrappers make concurrent host launches the ordinary case,
// and two trims interleaved with a header lose blocks. A sibling for appendLaunchLine's reason:
// a lock on the log's own descriptor locks the inode the trim rewrites. The event lines written
// after init are single O_APPEND writes, outside the lock, as the perf sink's always were.
func openLockedMachineLog(path string, init func(*os.File)) *os.File {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	defer lock.Close()
	fd := int(lock.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return nil
	}
	defer func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil
	}
	init(f)
	return f
}
