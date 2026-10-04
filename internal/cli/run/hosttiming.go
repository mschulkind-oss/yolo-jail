package run

// hosttiming.go is the timing surface at the HOST NOTCH (docs/reference/perf-logging.md, The host
// notch; D18): `yolo host -- <cmd>` and `yolo host apply` record spans by the same two gates a jail
// launch records them by, into a machine-wide file, and print the table only when this invocation
// typed a flag that asks for it.
//
// THE GATES ARE THE LAUNCH'S OWN (Options.timingRecording, Options.timingReporting), asked of an
// Options holding only what they read, so the host cannot come to classify an opt-in differently
// from a jail: `--timing` and a typed `--verbose` record and print (D12's explicit flags);
// `perf_logging: true`, `YOLO_TIMING` and `YOLO_VERBOSE` in the environment record silently.
//
// THE FILE IS MACHINE-WIDE, NEVER <cwd>/.yolo/host-perf.log. A host command has no workspace: it
// runs wherever it was typed, the home included, and a `.yolo` minted there hijacks every later
// `yolo config` verb's workspace walk (paths.WorkspaceScopeBreach). So the spans go to
// GLOBAL_STORAGE/logs, beside launches.log, under a header naming the directory by its short code
// (`jail=host:<code>`), never its path — OQ-PR3's ruling for the machine-wide record.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// HostNotchPerfLogName is the host notch's timing file, under GLOBAL_STORAGE/logs.
const HostNotchPerfLogName = "host-notch-perf.log"

// HostNotchPerfLogPath is where a host-notch command's spans land.
func HostNotchPerfLogPath() string {
	return filepath.Join(paths.GlobalStorage(), "logs", HostNotchPerfLogName)
}

// hostNotchLabel is the header's `jail=` value for a command run in workspace: `host:` and the
// directory's short code, the one launches.log and crossings.log key on, so a host command's
// block names which directory it ran in without naming the directory.
func hostNotchLabel(workspace string) string {
	return "host:" + paths.JailShortHash(runtime.FromWorkspace(workspace))
}

// HostNotchTiming is one host-notch command's timing surface. Log is the collector, nil when no
// opt-in asked to record (every method on a nil *perf.Log is a no-op, so call sites span
// unconditionally); a nil *HostNotchTiming is valid too.
type HostNotchTiming struct {
	Log    *perf.Log
	report bool
	stderr io.Writer
	once   sync.Once
}

// HostNotchTimingLog builds the timing surface for one host-notch command. typedTiming is
// `--timing` and typedVerbose the global `--verbose` (cli's explicitVerbose), each as typed on
// THIS invocation; getenv is the process lookup for the two environment opt-ins; stderr takes
// the slow-span notices, the one-time warning when the file cannot be opened, and the report.
//
// The persistent `perf_logging` key is read (config.PerfLoggingEnabled, user scope only) only
// when no flag already answered, as fillDefaults reads it for a jail launch. Best effort, always
// (P2): nothing here can fail the command.
func HostNotchTimingLog(typedTiming, typedVerbose bool, getenv func(string) string, stderr io.Writer) *HostNotchTiming {
	gates := &Options{Timing: typedTiming, Verbose: typedVerbose, Getenv: getenv}
	if !gates.timingReporting() {
		gates.perfLoggingOn = config.PerfLoggingEnabled()
	}
	t := &HostNotchTiming{report: gates.timingReporting(), stderr: stderr}
	if !gates.timingRecording() {
		return t
	}
	ws, err := os.Getwd()
	if err != nil {
		ws = "."
	}
	notice := func(msg string) { fmt.Fprintf(stderr, "yolo: %s\n", msg) }
	t.Log = perf.New(time.Now, hostNotchPerfFileSink(hostNotchLabel(ws), stderr), slowSpanNoticeSink(notice))
	return t
}

// hostNotchPerfFileSink is the machine-wide file sink: the perf package's own (perf.FileSinkTo,
// its trim and its run header), opened under openLockedMachineLog's sibling lock so two host
// commands starting together cannot interleave one's trim with the other's header. A file that
// cannot be opened is said once and the sink stays silent, the perf sink's own contract.
func hostNotchPerfFileSink(label string, stderr io.Writer) perf.Sink {
	var sink perf.Sink
	path := HostNotchPerfLogPath()
	if f := openLockedMachineLog(path, func(f *os.File) {
		sink = perf.FileSinkTo(f, label, time.Now())
	}); f == nil || sink == nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "yolo: timing log unavailable at %s (continuing without it)\n", path)
		}
		return func(perf.Event) {}
	}
	return sink
}

// Recording reports whether this command records spans.
func (t *HostNotchTiming) Recording() bool { return t != nil && t.Log != nil }

// Report ends the timing surface, once, by D12's split: a command whose invocation typed
// `--timing` or `--verbose` prints the table under header; one that recorded on a persistent
// opt-in prints one line naming the file, so a `perf_logging` turned on months ago is still
// discoverable; one that recorded nothing prints nothing. It must run before the command hands
// its process over, which no later line can follow.
func (t *HostNotchTiming) Report(header string) {
	if !t.Recording() {
		return
	}
	t.once.Do(func() {
		if !t.report {
			fmt.Fprintf(t.stderr, "yolo: timings recorded in %s (--timing prints them)\n",
				HostNotchPerfLogPath())
			return
		}
		fmt.Fprintln(t.stderr, header)
		t.Log.Report(t.stderr, time.Now())
		fmt.Fprintf(t.stderr, "  file: %s\n", HostNotchPerfLogPath())
	})
}
