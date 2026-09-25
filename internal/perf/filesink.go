package perf

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxRuns bounds the file sink's history, in runs (a run being one delimited
// block, see runHeader). The entrypoint's perf log uses the same 50-run idiom
// for the same reason: a per-workspace diagnostic file must be self-limiting
// or it quietly becomes a disk-exhaustion bug on a directory the user cannot
// see into from the jail.
const MaxRuns = 50

// runHeader delimits runs in the sink file. The trim below splits on the same
// prefix, so the delimiter and the splitter are one spelling, here.
const runPrefix = "=== YOLO Host Perf"

// FileSink returns a Sink appending one line per event to path.
//
// The file is opened NOW, not lazily on the first event: the run's header line
// is the delimiter the trim idiom needs, and a run that refuses or hangs with
// zero spans is exactly the run whose header (and nothing else) you want on
// disk. Trim happens at open — keep the newest MaxRuns-1 runs, then this run
// appends — so nothing rewrites the file at exit, where the signal arm's
// os.Exit would skip the rewrite anyway (see the package doc).
//
// Best-effort per the package contract: if the file cannot be opened the
// failure is reported ONCE to errw and the returned sink stays installed but
// silent — the in-memory Log and the stderr report still work, and a jail is
// never refused over its timing log. Concurrent launches on one workspace can
// interleave appends: each line is a single O_APPEND write, so lines stay
// whole, and the entrypoint's dump-time trim has the same race with the same
// stakes.
//
// The header carries the jail/container name rather than a PID: the name is
// what every other log in this repo keys on (host-service-*, <cname>-socat),
// and it is stable across the attach sessions that share the workspace.
func FileSink(path, cname string, errw io.Writer, now time.Time) Sink {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		warnOnce(errw, path, err)
		return func(Event) {}
	}
	// Read-write, because FileSinkTo trims through this descriptor.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		warnOnce(errw, path, err)
		return func(Event) {}
	}
	return FileSinkTo(f, cname, now)
}

// FileSinkTo is FileSink for a file the caller has already opened, O_APPEND and read-write:
// it trims the file to the newest MaxRuns-1 runs through that descriptor (TrimRunsInOpenFile),
// then appends this run's header. It is how a caller whose file sits in jail-writable state
// opens it without a path (internal/cli/run's host perf log, which opens it beneath an
// os.Root so a link the jail left at the name is never followed). The sink owns f from here.
func FileSinkTo(f *os.File, cname string, now time.Time) Sink {
	TrimRunsInOpenFile(f, runPrefix, MaxRuns-1)
	fmt.Fprintf(f, "%s (%s) jail=%s ===\n", runPrefix, now.Format("2006-01-02 15:04:05"), cname)

	var mu sync.Mutex // one write per event, under the sink's own lock
	return func(e Event) {
		line := formatLine(e)
		mu.Lock()
		_, _ = f.WriteString(line)
		mu.Unlock()
	}
}

// formatLine renders one event as a self-contained line: a millisecond
// timestamp (so two half-logs and `podman events` can be correlated), the
// kind, the name, and the duration for ends.
func formatLine(e Event) string {
	ts := e.At.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	switch e.Kind {
	case KindStart:
		return fmt.Sprintf("%s start  %s\n", ts, e.Name)
	case KindMark:
		return fmt.Sprintf("%s mark   %s\n", ts, e.Name)
	case KindNote:
		// One line per note, always: a detail that carried a newline would split
		// into a line the reader cannot attribute.
		return fmt.Sprintf("%s note   %s  %s\n", ts, e.Name, strings.ReplaceAll(e.Detail, "\n", " "))
	default:
		return fmt.Sprintf("%s end    %s  dur=%.3fs\n", ts, e.Name, e.Dur.Seconds())
	}
}

// TrimRunsInOpenFile keeps the newest n run blocks of f, a run block being everything from
// one occurrence of prefix to the next. f must be open read-write (O_APPEND is fine): it is
// read from the start and, when it holds more than n runs, truncated and the newest n written
// back, so the file is never reopened by path (the callers' files sit in jail-writable
// `.yolo`, where a second open by path would follow a link the jail left). Exactly once per
// run, at open, never at exit. Failures are silent: the worst case is a file that grows past
// its bound, which the next open trims.
//
// It is EXPORTED because it is the retention idiom every per-workspace diagnostic file in
// <workspace>/.yolo shares, and "the same retention the perf log has" has to mean the same
// code or it means whatever the second copy drifts into. The launch log
// (internal/cli/run/launchlog.go) is the second caller; MaxRuns is the bound both pass.
func TrimRunsInOpenFile(f *os.File, prefix string, n int) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return
	}
	content, err := io.ReadAll(f)
	if err != nil {
		return
	}
	runs := strings.Split(string(content), prefix)
	if len(runs) <= n {
		return
	}
	trimmed := []byte(prefix + strings.Join(runs[len(runs)-n:], prefix))
	if f.Truncate(0) == nil {
		// O_APPEND puts this write at the new end, which is offset 0; without it the
		// offset still sits past what was read, so rewind first either way.
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			_, _ = f.Write(trimmed)
		}
	}
}

// warnOnce is the whole error story for the file sink: say it, then never say
// it again. The sink it guards stays installed and silent.
func warnOnce(errw io.Writer, path string, err error) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "yolo: timing log unavailable at %s: %v (continuing without it)\n", path, err)
}
