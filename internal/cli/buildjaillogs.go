package cli

// buildjaillogs.go keeps a capture or build jail's own diagnostics once its scratch workspace is
// gone (docs/reference/perf-logging.md, "Where a build jail's logs go").
//
// A capture jail and a fork's or a patched extension's build jail each run a whole launch in a
// scratch workspace under the capture store's staging directory, and that launch writes what every
// launch writes into the workspace's .yolo/: the host half of its timing (host-perf.log), the
// entrypoint's boot log, the launch's own teed stream (launch.log), and the jail half's checkpoints
// (home/yolo-perf.log). The workspace is deleted seconds after the build, so all of it went with it,
// and the launch's last line ("timings recorded in <staging>/.yolo/logs/host-perf.log") named a file that
// no longer existed. Every path that deletes such a workspace now hands those files to
// preserveBuildJailLogs first, which appends them to one file per build under yolo's host log
// directory, and the launch's line names that file instead (buildJailLogRewriter).
//
// RETENTION is the two rules the host log directory's other logs already follow: a file past
// treeBuildLogRotateAt is moved to <file>.1 before the next append, as a tree build's log is
// (treepool.go), and only the newest buildJailLogsKept files are kept, the run cap the host perf
// log keeps (perf.MaxRuns), because the build id in the name changes with every build's inputs and
// a per-file bound alone would let the directory grow by one file per build forever. One source
// file is copied at most its last treeBuildLogRotateAt bytes, so one build cannot fill the disk.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// buildJailLogPrefix starts the name of every preserved build-jail log.
const buildJailLogPrefix = "build-jail-"

// buildJailLogsKept is how many preserved build-jail logs survive: the newest, by modification time.
const buildJailLogsKept = perf.MaxRuns

// buildJailLogPath is where the diagnostics of the capture or build jail that ran in workspace are
// kept: <host logs>/build-jail-<staging id>.log. The staging id is the workspace's own name — the
// program's bin for an installer's capture, fork-<build id> for a build, the id the build jail's
// container and keeper log are named by too (jail-keeper-yolo-fork-<build id>-….log).
func buildJailLogPath(workspace string) string {
	return filepath.Join(paths.GlobalStorage(), "logs", buildJailLogPrefix+filepath.Base(workspace)+".log")
}

// buildJailLogSource is one file a build jail's launch leaves in its workspace's .yolo/, relative to
// the workspace, and the label its section in the preserved log carries.
type buildJailLogSource struct{ label, rel string }

// buildJailLogSources are the launch's own diagnostics, in the order a reader wants them: the host
// half's spans, the entrypoint's boot, the jail half's checkpoints, then the whole teed stream.
//
// Each log is taken from the workspace's logs directory and from directly under .yolo, where a
// build jail started by a yolo older than the logs directory writes it; only the one that exists is
// read, and a section's label names the log either way.
func buildJailLogSources(workspace string) []buildJailLogSource {
	both := func(name string) []buildJailLogSource {
		return []buildJailLogSource{{name, paths.WorkspaceLogPath("", name)}, {name, paths.LegacyWorkspaceLogPath("", name)}}
	}
	out := both(run.HostPerfLogName)
	out = append(out, both("boot.log")...)
	out = append(out, buildJailPerfLogSources(workspace)...)
	return append(out, both(run.LaunchLogName)...)
}

// buildJailPerfLogSources are the jail half's checkpoints, yolo-perf.log at the top of the home the
// build jail ran with (docs/design/named-setups.md §9): every built-in setup's home and the legacy
// one, which the rewriter needs before the jail has made any, plus every other home the workspace
// holds now (paths.WorkspaceHomeStates), for a build jail run under a user-defined setup.
func buildJailPerfLogSources(workspace string) []buildJailLogSource {
	var homes []string
	homes = append(homes, paths.LegacyHomeState(""))
	for _, s := range setup.BuiltIns() {
		homes = append(homes, paths.SetupHomeState("", s.Name))
	}
	if workspace != "" {
		for _, h := range paths.WorkspaceHomeStates(workspace) {
			if rel, err := filepath.Rel(workspace, h); err == nil {
				homes = append(homes, rel)
			}
		}
	}
	var out []buildJailLogSource
	seen := map[string]bool{}
	for _, h := range homes {
		rel := filepath.Join(h, "yolo-perf.log")
		if seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, buildJailLogSource{"yolo-perf.log (the jail half)", rel})
	}
	return out
}

// preserveBuildJailLogs appends what the build jail that ran in workspace left of its diagnostics
// to buildJailLogPath(workspace), each under a header naming the file, and removes each source it
// copied, so a second cleanup of the same workspace copies nothing twice. Best-effort throughout,
// as the cleanup it precedes is: a log that cannot be kept never fails the capture or the build.
//
// THE READ IS CONFINED TO THE WORKSPACE (os.Root) and takes regular files only: the workspace is
// the build jail's to write, and a link it left at .yolo/logs/boot.log must not copy a host file into a
// log directory a jail may be shown (a context mount of it is ordinary).
func preserveBuildJailLogs(workspace string) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return
	}
	defer root.Close()
	dest := buildJailLogPath(workspace)
	var out *os.File
	defer func() {
		if out != nil {
			_ = out.Close()
			pruneBuildJailLogs(filepath.Dir(dest), buildJailLogsKept)
		}
	}()
	for _, src := range buildJailLogSources(workspace) {
		data, truncated, ok := readBuildJailLog(root, src.rel)
		if !ok {
			continue
		}
		if out == nil {
			if out = openBuildJailLog(dest); out == nil {
				return
			}
		}
		header := fmt.Sprintf("=== %s of %s, kept %s by yolo pid %d", src.label, workspace,
			time.Now().Format(time.RFC3339), os.Getpid())
		if truncated {
			header += fmt.Sprintf(" (its last %d bytes)", len(data))
		}
		if _, err := io.WriteString(out, header+" ===\n"); err != nil {
			return
		}
		if _, err := out.Write(data); err != nil {
			return
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			_, _ = io.WriteString(out, "\n")
		}
		_ = root.Remove(src.rel)
	}
}

// readBuildJailLog reads rel under root when it is a non-empty regular file: at most its last
// treeBuildLogRotateAt bytes, and whether that cut anything.
func readBuildJailLog(root *os.Root, rel string) (data []byte, truncated, ok bool) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, false, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return nil, false, false
	}
	size := fi.Size()
	if size > treeBuildLogRotateAt {
		if _, err := f.Seek(size-treeBuildLogRotateAt, io.SeekStart); err != nil {
			return nil, false, false
		}
		truncated = true
	}
	data, err = io.ReadAll(io.LimitReader(f, treeBuildLogRotateAt))
	if err != nil {
		return nil, false, false
	}
	return data, truncated, true
}

// openBuildJailLog opens dest for appending, rotated first to dest.1 when past treeBuildLogRotateAt
// (the tree build log's rule, treepool.go); nil when it cannot be made.
func openBuildJailLog(dest string) *os.File {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return nil
	}
	if fi, err := os.Stat(dest); err == nil && fi.Size() > treeBuildLogRotateAt {
		_ = os.Rename(dest, dest+".1")
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// pruneBuildJailLogs removes every preserved build-jail log in dir past the newest keep, each with
// its rotated .1.
func pruneBuildJailLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type logFile struct {
		name string
		mod  time.Time
	}
	var logs []logFile
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, buildJailLogPrefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logFile{name, fi.ModTime()})
	}
	if len(logs) <= keep {
		return
	}
	slices.SortFunc(logs, func(a, b logFile) int { return b.mod.Compare(a.mod) })
	for _, l := range logs[keep:] {
		_ = os.Remove(filepath.Join(dir, l.name))
		_ = os.Remove(filepath.Join(dir, l.name+".1"))
	}
}

// buildJailLogRewriter is the stream a capture or build jail's launch writes its own lines to, with
// every path of the diagnostics preserveBuildJailLogs keeps replaced by the file it keeps them in:
// the launch's "timings recorded in <workspace>/.yolo/logs/host-perf.log" line, and its report's "host
// file:" and "jail half:" lines, would otherwise name files deleted seconds later. The launch writes
// each line in one Write (richtext.Printer), so a write is rewritten whole and never held back.
type buildJailLogRewriter struct {
	w     io.Writer
	olds  [][]byte
	saved []byte
}

// newBuildJailLogRewriter is w with workspace's diagnostics' paths rewritten; nil stays nil. The
// rewriter is a launch stream as w is: a progress redraw stays transient (WriteTransient), and when
// w is teed into a launch log, a write for that log alone still reaches it (run.LogWriter).
func newBuildJailLogRewriter(w io.Writer, workspace string) io.Writer {
	if w == nil {
		return nil
	}
	r := &buildJailLogRewriter{w: w, saved: []byte(buildJailLogPath(workspace))}
	for _, src := range buildJailLogSources(workspace) {
		r.olds = append(r.olds, []byte(filepath.Join(workspace, src.rel)))
	}
	if lw, ok := w.(run.LogWriter); ok {
		return buildJailLogRewriterToLog{r, lw}
	}
	return r
}

func (r *buildJailLogRewriter) rewrite(p []byte) []byte {
	q := p
	for _, old := range r.olds {
		if bytes.Contains(q, old) {
			q = bytes.ReplaceAll(q, old, r.saved)
		}
	}
	return q
}

func (r *buildJailLogRewriter) Write(p []byte) (int, error) {
	if _, err := r.w.Write(r.rewrite(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteTransient is a redraw, passed on as one (progress.TransientWriter).
func (r *buildJailLogRewriter) WriteTransient(p []byte) (int, error) {
	if tw, ok := r.w.(interface{ WriteTransient([]byte) (int, error) }); ok {
		return tw.WriteTransient(p)
	}
	return r.w.Write(p)
}

// buildJailLogRewriterToLog is the rewriter of a stream teed into a launch log.
type buildJailLogRewriterToLog struct {
	*buildJailLogRewriter
	lw run.LogWriter
}

func (r buildJailLogRewriterToLog) WriteLog(p []byte) (int, error) {
	if _, err := r.lw.WriteLog(r.rewrite(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// buildJailOutput is a host build's jail output, kept as it streams (buildMode.keepJailOutput): in
// the build's log, buildJailLogPath of its staging workspace, the file its diagnostics are appended
// to once the workspace goes, so one file holds the whole build. ANSI-free, as every log yolo keeps
// is; one write at a time, the jail's stdout and stderr being copied on goroutines of their own.
type buildJailOutput struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

// openBuildJailOutput opens the build's log for staging, under a header naming what is built; nil
// when it cannot be made, and the build streams as it did.
func openBuildJailOutput(staging, what string) *buildJailOutput {
	path := buildJailLogPath(staging)
	f := openBuildJailLog(path)
	if f == nil {
		return nil
	}
	fmt.Fprintf(f, "=== build jail output of %s in %s, started %s by yolo pid %d ===\n", what, staging,
		time.Now().Format(time.RFC3339), os.Getpid())
	return &buildJailOutput{path: path, f: f}
}

// tee is w, or def when w is nil (the process's own stream, as a nil jail writer is), with every
// write also kept.
func (o *buildJailOutput) tee(w, def io.Writer) io.Writer {
	if w == nil {
		w = def
	}
	return buildJailOutputWriter{o: o, w: w}
}

func (o *buildJailOutput) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.f != nil {
		_ = o.f.Close()
		o.f = nil
	}
}

type buildJailOutputWriter struct {
	o *buildJailOutput
	w io.Writer
}

func (w buildJailOutputWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.o.mu.Lock()
	if w.o.f != nil {
		_, _ = w.o.f.Write(jailTailANSI.ReplaceAll(p, nil))
	}
	w.o.mu.Unlock()
	return n, err
}
