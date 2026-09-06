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
	trimToLastRuns(path, MaxRuns-1)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		warnOnce(errw, path, err)
		return func(Event) {}
	}
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
	default:
		return fmt.Sprintf("%s end    %s  dur=%.3fs\n", ts, e.Name, e.Dur.Seconds())
	}
}

// trimToLastRuns keeps the newest n run blocks of path. Read-modify-rewrite,
// exactly once per run, at open — never at exit. Failures are silent: the
// worst case is a file that grows past MaxRuns, which the next open trims.
func trimToLastRuns(path string, n int) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	runs := strings.Split(string(content), runPrefix)
	if len(runs) <= n {
		return
	}
	trimmed := runPrefix + strings.Join(runs[len(runs)-n:], runPrefix)
	_ = os.WriteFile(path, []byte(trimmed), 0o644)
}

// warnOnce is the whole error story for the file sink: say it, then never say
// it again. The sink it guards stays installed and silent.
func warnOnce(errw io.Writer, path string, err error) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "yolo: timing log unavailable at %s: %v (continuing without it)\n", path, err)
}
