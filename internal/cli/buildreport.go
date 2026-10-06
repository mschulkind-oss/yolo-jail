package cli

// buildreport.go is how a JAIL LAUNCH shows the builds its fork-build slot runs — a plain fork's
// missing build, a patched fork's advance, a patched extension's — on the terminal, with every byte
// the build jail printed kept in the launch's record. A BUILD REPORT (a term coined here) is that
// rendering for one launch; a build's RUN is its share of it. The builds run at once in the slot's
// pool (buildpool.go), under its one progress line.
//
// docs/reference/report-tiers.md is the rule it follows: progress may be compressed to a line, a
// disclosure never is, warnings and refusals (tier 3) stay on the terminal, and "too much on the
// terminal is answered by reading the file". So:
//
//   - THE START LINE prints at once, before the build line runs, whatever the timing: what is built
//     and why, where its log is, and the build's disclosures — the seal it runs under (FP-D9,
//     FP-D13: no credential, no host file, no env_sources, and a bridged network, never the host's,
//     or, for a build launched from inside a jail, that jail's network, which a nested podman is
//     forced onto) and the build line itself, whole, since a payload can sit at its last character
//     (OQ-RO9).
//   - THE POOL'S PROGRESS LINE stands for every build running, redrawn in place on a terminal and a
//     heartbeat every 15 s anywhere else (internal/progress).
//   - THE RESULT LINE is the build's own, among its key's lines in declaration order, with its time:
//     for an admitted build the move line (PF-D8), the disclosure of what the jail now runs, with the
//     store's key, path count and size folded in; for a build that came to nothing a short result,
//     under which the caller prints its failure line and then the build's last lines and its log
//     (failureLines).
//   - THE NESTED LAUNCH'S WARNINGS AND REFUSALS: the build jail's launch prints its own lines on
//     streams of their own (forkbuildchild.go's --launch-fds), and the ones that begin as a warning or
//     a refusal begins are repeated under the result line, whether the build was admitted or came to
//     nothing; a failure's last lines then leave out the ones already repeated. A refusal also ends
//     the build before its build line ran, and its failure line relays it (forkBuildNotStarted).
//
// Everything else the build jail prints — its launch's provenance and progress, its boot, the build
// line's own output — goes to launch.log alone, through the launch stream's log half
// (run.LaunchLogOnly), each line marked with the build's key, and to the build's own log,
// <workspace>/.yolo/build-<slug>.log, rewritten by the first build of the key a launch runs and added
// to by every later one, so a fit's failure that names it still finds its output there once the
// series' base has been built after it (docs/design/patched-forks.md PF-D77, PF-D23).

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// sealDisclosure is what the seal withholds and what crosses, as a build's start line names it
// (FP-D9, FP-D13), for a build jail launched on rt ("" for the runtime its launch resolves). The
// network is what that launch applies: the runtime's bridge, or — launched from inside a jail, where a
// nested podman is forced onto its launcher's namespace — the jail's own network.
func sealDisclosure(rt string) string {
	if sealSharesNetwork(rt) {
		return "sealed: no credential, no host file, no env_sources, and the network of the jail it is launched " +
			"from, never a bridge of its own (a jail nested in a jail shares its network), so what that jail " +
			"reaches, the build reaches"
	}
	return "sealed: no credential, no host file, no env_sources, and a bridged network, never the host's"
}

// sealSharesNetwork is run.SealedBuildSharesLauncherNetwork: a var so a test states either answer.
var sealSharesNetwork = run.SealedBuildSharesLauncherNetwork

const (
	// buildTailLines is how many of a build's last lines a failure prints under its failure line.
	buildTailLines = 20
	// buildWarnLines bounds the nested launch's tier-3 lines a build repeats under its result.
	buildWarnLines = 20
	// buildWarnContinuation bounds the indented lines kept after one of them, its own detail (a
	// refusal's key and `yolo check`).
	buildWarnContinuation = 8
)

// buildReport is one jail launch's build report.
type buildReport struct {
	// log is the launch stream's launch.log alone (run.LaunchLogOnly), io.Discard with no log.
	log io.Writer
	// workspace is the launch's, whose .yolo holds each build's own log; "" for none.
	workspace string
	// rt is the launch's runtime, which its build jails run on too, for the seal's disclosure.
	rt    string
	color bool

	mu sync.Mutex
	// opened is the keys whose own log a build of this launch has opened: the first build of a key
	// rewrites it, and every later one, the series' base after a fit or a re-read's second build,
	// adds to it.
	opened map[string]bool
}

// launchBuildStream is the stream a jail launch's builds print on: the launch's own stderr, teed
// into its launch.log, or the process's when the request carries none.
func launchBuildStream(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stderr
}

// newBuildReport is the report of a launch on runtime rt whose stream is w.
func newBuildReport(workspace, rt string, w io.Writer, color bool) *buildReport {
	return &buildReport{log: run.LaunchLogOnly(w), workspace: workspace, rt: rt, color: color, opened: map[string]bool{}}
}

// logLine writes a line of key's to launch.log alone, marked as its build jail's lines are: what
// the pool's progress line says in passing, such as a check's wait for a lock, which the log keeps.
func (r *buildReport) logLine(key, line string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.log, "  [%s] %s\n", key, line)
}

// buildStart is what a build's start line says.
type buildStart struct {
	// fork is what is built, which names the line, the progress line and the log.
	fork packload.Fork
	// what is the build's subject: "v1.0.0 (3f2a9c1e) + 1 patch (series ab12cd34)", a plain fork's
	// "<source> at <commit>".
	what string
	// why is the clause that follows it — ", the first build on this machine", " — held at the
	// series' base, …" — "" for none.
	why string
	// wait is a serving advance's clause: how long the launch waits and what a Ctrl-C starts it on.
	wait string
}

// begin starts one build's run for the pool's key it: its log, and its start line and disclosure
// line, printed at once. It never fails: a log that cannot be opened is one fewer place the output
// lands, and the start line says which log there is.
func (r *buildReport) begin(s buildStart, it *poolItem) *buildRun {
	b := &buildRun{r: r, it: it, key: s.fork.Key(), label: s.fork.Label(), started: time.Now()}
	b.openLog(s)
	head := "[bold]build[/bold] " + richtext.Escape(b.label+": "+s.what+s.why)
	if s.wait != "" {
		head += richtext.Escape(" — " + s.wait)
	}
	if where := b.logName(); where != "" {
		head += "[dim]; log: " + richtext.Escape(where) + "[/dim]"
	}
	it.pool.say(head + "\n[dim]  " + richtext.Escape(sealDisclosure(r.rt)+"; "+buildRuns(s.fork)) + "[/dim]")
	return b
}

// buildRuns is the disclosure of what a build runs: its build line, whole, before it runs (OQ-RO9).
// A patched extension's build line is optional (PPX-D3), and its jail copies the checkout either way.
func buildRuns(f packload.Fork) string {
	switch {
	case f.IsTree() && strings.TrimSpace(f.Build) == "":
		return "it runs no build line, and copies the checkout as it is"
	case f.IsTree():
		return "it runs, then copies the checkout: " + f.Build
	}
	return "it runs: " + f.Build
}

// buildRun is one build's share of the report. Every method is safe on a nil *buildRun, which a
// build `yolo capture` runs has, so the build act calls them unconditionally.
type buildRun struct {
	r       *buildReport
	it      *poolItem // the key whose build this is, whose lines take the result
	key     string
	label   string
	started time.Time

	mu sync.Mutex
	// logf is the build's own log, nil when it could not be opened; logPath its path.
	logf    *os.File
	logPath string
	// cur is each stream's partial line (jailTail's order: the jail's stdout and stderr, its launch's),
	// and cr whether a carriage return came last on it, which redraws the line only if more of it
	// follows: a "\r\n" just ends it.
	cur [4][]byte
	cr  [4]bool
	// tail is the last buildTailLines lines of every stream, in the order they came.
	tail []tailLine
	// warn is the nested launch's tier-3 lines, in order; inWarn and cont follow one's continuation on
	// the stream it was printed on.
	warn   []string
	inWarn [4]bool
	cont   [4]int
	// store is the admitted entry, as the result line names it.
	store string
	ended bool
}

// tailLine is one of a build's last lines, and whether it is repeated among the nested launch's
// warnings, which a failure's tail then leaves out.
type tailLine struct {
	text string
	warn bool
}

// buildLogName is the name of the build log of fork key under the workspace's .yolo: readable,
// and unique by a hash of the key (run.PatchedCopySlug), since a key holds a "/".
func buildLogName(key string) string { return "build-" + run.PatchedCopySlug(key) + ".log" }

// openLog opens the build's own log, beneath a root on the jail-writable .yolo, never by path
// (paths.OpenWorkspaceStateFile, which launch.log's open states the reason for), with a header naming
// the build: rewritten by the first build of the key this launch runs, and added to by every later
// one, whose failure lines would otherwise name a log another build had rewritten.
func (b *buildRun) openLog(s buildStart) {
	if b.r.workspace == "" {
		return
	}
	b.r.mu.Lock()
	again := b.r.opened[b.key]
	b.r.mu.Unlock()
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if again {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	name := buildLogName(b.key)
	f, err := paths.OpenWorkspaceStateFile(b.r.workspace, name, flags, 0o644)
	if err != nil {
		return
	}
	b.r.mu.Lock()
	b.r.opened[b.key] = true
	b.r.mu.Unlock()
	b.logf, b.logPath = f, filepath.Join(paths.WorkspaceStateDir(b.r.workspace), name)
	if again {
		fmt.Fprintln(f)
	}
	fmt.Fprintf(f, "=== yolo build of %s, %s ===\n  %s%s\n  %s; %s\n", b.label, b.started.Format("2006-01-02T15:04:05-0700"),
		s.what, s.why, sealDisclosure(b.r.rt), buildRuns(s.fork))
}

// logName is where the start line says the output is: the build's own log, relative to the
// workspace the launch runs in; launch.log when it could not be opened; "" with neither.
func (b *buildRun) logName() string {
	switch {
	case b.logPath != "":
		return filepath.Join(".yolo", filepath.Base(b.logPath))
	case b.r.workspace != "":
		return filepath.Join(".yolo", run.LaunchLogName)
	}
	return ""
}

// streams are the build jail's writers: each line goes to launch.log, marked with the build's key,
// and to the build's own log, and none to the terminal.
func (b *buildRun) streams() jailStreams {
	return jailStreams{out: buildStream{b, 0}, errw: buildStream{b, 1}, launchOut: buildStream{b, 2},
		launchErr: buildStream{b, 3}}
}

// buildStream is one of a run's four streams (jailTail's order: out, errw, the launch's two).
type buildStream struct {
	b *buildRun
	i int
}

func (s buildStream) Write(p []byte) (int, error) {
	s.b.write(s.i, p)
	return len(p), nil
}

// write takes p into stream i, line by line: a carriage return followed by more of the line starts
// it again, as jailTail's does, so a redrawn line is kept as a terminal would show it, and one
// followed by the newline, a CRLF ending, just ends it.
func (b *buildRun) write(i int, p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range p {
		switch c {
		case '\n':
			b.endLine(i)
		case '\r':
			b.cr[i] = true
		default:
			if b.cr[i] {
				b.cur[i], b.cr[i] = b.cur[i][:0], false
			}
			b.cur[i] = append(b.cur[i], c)
		}
	}
}

// endLine files stream i's partial line. Callers hold mu.
func (b *buildRun) endLine(i int) {
	text := strings.TrimRight(jailTailANSI.ReplaceAllString(string(b.cur[i]), ""), " \t")
	b.cur[i], b.cr[i] = b.cur[i][:0], false
	if b.logf != nil {
		fmt.Fprintln(b.logf, text)
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	b.r.logLine(b.key, text)
	warned := false
	if i >= 2 {
		// THE NESTED LAUNCH'S TIER 3: a warning or a refusal, and the indented lines that are its detail.
		switch {
		case tier3Line(text):
			b.inWarn[i], b.cont[i] = true, 0
			warned = b.keepWarn(text)
		case b.inWarn[i] && (strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\t")) &&
			b.cont[i] < buildWarnContinuation:
			b.cont[i]++
			warned = b.keepWarn(text)
		default:
			b.inWarn[i] = false
		}
	}
	if b.tail = append(b.tail, tailLine{text: text, warn: warned}); len(b.tail) > buildTailLines {
		b.tail = b.tail[len(b.tail)-buildTailLines:]
	}
}

// keepWarn keeps one of the nested launch's tier-3 lines, and reports whether it was kept: past
// buildWarnLines it is not, and stays in a failure's tail.
func (b *buildRun) keepWarn(text string) bool {
	if len(b.warn) < buildWarnLines {
		b.warn = append(b.warn, text)
		return true
	}
	return false
}

// tier3Starts are how the run pipeline's warnings and refusals begin, as their lines read with
// color stripped (report-tiers.md's tier 3): "Warning: …", "⚠ …", "Refusing to launch: …", and the
// red lines of a launch that cannot go on.
var tier3Starts = []string{"Warning", "⚠", "Refusing", "Cannot ", "Could not ", "could not ", "Error", "error:",
	"Failed", "launch failed"}

// tier3Line reports whether a line of a nested launch's own is a warning or a refusal.
func tier3Line(text string) bool {
	t := strings.TrimLeft(text, " \t")
	for _, p := range tier3Starts {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// phase is what the build act is doing now, for its log; and a wait for another launch's build of
// the same key, which can last forkBuildWaitBound, for the pool's line too, which otherwise names
// only which keys build.
func (b *buildRun) phase(detail string) {
	if b == nil {
		return
	}
	b.it.setNote(detail, strings.HasPrefix(detail, "waiting"))
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logf != nil {
		fmt.Fprintf(b.logf, "--- %s\n", detail)
	}
}

// admitted records the admitted entry, which the result line names.
func (b *buildRun) admitted(key string, entries int, bytes int64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.store = fmt.Sprintf("store key %s, %d %s, %s", key, entries, plural(entries, "path", "paths"), humanBytes(bytes))
	b.mu.Unlock()
}

// done ends an admitted build: markup is its result line, the move line for a patched build, to
// which the store clause is added; then the nested launch's warnings (printWarnings).
func (b *buildRun) done(markup string) {
	if b == nil {
		return
	}
	b.closeLog() // files every stream's partial line, a last warning's included
	b.mu.Lock()
	if b.ended {
		b.mu.Unlock()
		return
	}
	b.ended = true
	store, warn := b.store, b.warn
	b.mu.Unlock()
	if store != "" {
		markup += "[dim] — " + richtext.Escape(store) + "[/dim]"
	}
	pr := richtext.Printer{W: b.it.stream(), Color: b.r.color}
	pr.Print(markup + "[dim] (" + progress.Elapsed(time.Since(b.started)) + ")[/dim]")
	printWarnings(pr, warn)
}

// printWarnings repeats the nested launch's warnings and refusals under a build's result line
// (report-tiers.md's tier 3, which stays on the terminal).
func printWarnings(pr richtext.Printer, warn []string) {
	for _, w := range warn {
		pr.Print("[yellow]  its build jail: " + richtext.Escape(w) + "[/yellow]")
	}
}

// fail ends a build that came to nothing with a short result ("failed", "did not start"), its
// key's next line, and the nested launch's warnings under it, which came before its last lines as
// often as not; under them the caller prints its failure line and failureLines.
func (b *buildRun) fail(result string) {
	if b == nil {
		return
	}
	b.closeLog() // files every stream's partial line, a last warning's included
	b.mu.Lock()
	if b.ended {
		b.mu.Unlock()
		return
	}
	b.ended = true
	warn := b.warn
	b.mu.Unlock()
	pr := richtext.Printer{W: b.it.stream(), Color: b.r.color}
	pr.Print(richtext.Escape("Building " + b.label + ": " + result + " (" + progress.Elapsed(time.Since(b.started)) + ")"))
	printWarnings(pr, warn)
}

// failureLines are the lines a failed build prints under its failure line: the build's last
// lines, but for the nested launch's warnings its result line repeated already, then where its
// whole output is. Markup, for the caller's printer; nil on a nil run.
func (b *buildRun) failureLines() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	var tail []string
	repeated := false
	for _, l := range b.tail {
		if l.warn {
			repeated = true
			continue
		}
		tail = append(tail, l.text)
	}
	b.mu.Unlock()
	var out []string
	if len(tail) > 0 {
		head := fmt.Sprintf("its last %d %s", len(tail), plural(len(tail), "line", "lines"))
		if repeated {
			head = fmt.Sprintf("its last %d other %s", len(tail), plural(len(tail), "line", "lines"))
		}
		out = append(out, "[dim]  "+head+":[/dim]")
		for _, l := range tail {
			out = append(out, "[dim]    "+richtext.Escape(l)+"[/dim]")
		}
	}
	if where := b.wholeOutput(); where != "" {
		out = append(out, "[dim]  its whole output: "+richtext.Escape(where)+"[/dim]")
	}
	return out
}

// wholeOutput names where a build's output is: its own log and launch.log, "" with neither.
func (b *buildRun) wholeOutput() string {
	if b.r.workspace == "" {
		return ""
	}
	launchLog := filepath.Join(paths.WorkspaceStateDir(b.r.workspace), run.LaunchLogName)
	if b.logPath != "" {
		return b.logPath + ", and in " + launchLog
	}
	return launchLog
}

// closeLog files every stream's partial line and closes the build's log.
func (b *buildRun) closeLog() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cur {
		if len(b.cur[i]) > 0 {
			b.endLine(i)
		}
	}
	if b.logf != nil {
		_ = b.logf.Close()
		b.logf = nil
	}
}

// childJail is the runJail of a build a jail launch runs (buildMode.runJail): the fork-build-jail
// child (forkBuildChild), under ctx and bounded at forkBuildWaitBound, with every stream it prints
// on taken by the act — so the launch can keep them off its terminal, and so no build jail's launch
// runs inside this process's own (run.Run's signal arms and pack-record scope are the process's).
// bound, when non-nil, is set when the bound stopped it.
func childJail(ctx context.Context, color bool, bound *bool) func(string, forkBuild, jailStreams) int {
	return func(staging string, b forkBuild, s jailStreams) int {
		rc, hit := forkBuildChild(ctx, forkBuildWaitBound, staging, b, s, color)
		if bound != nil {
			*bound = hit
		}
		return rc
	}
}
