package macosuser

// bootperf.go reads the macos-user bootstrap's step timings back into the launch's collector. The
// bootstrap runs as the sandbox account, in another process, so its steps cannot be spanned here;
// it writes one line per boot step to <workspace>/.yolo/logs/boot-perf.log (entrypoint.BootPerfPath),
// and the launcher records each as a completed interval, macos_user.bootstrap.<step>
// (docs/reference/perf-logging.md). Timing only: nothing here can refuse or change a launch.

import (
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// maxBootPerfSteps caps how many steps one bootstrap's file may add to the host perf log. The file
// is the sandbox account's writing, so the host reads it as untrusted (perf.StepsFromMarks): bounded,
// every name reduced to [a-z0-9_], and a line that does not parse dropped.
const maxBootPerfSteps = perf.MaxForeignSteps

// clearBootPerf removes a previous bootstrap's timings before this one runs, so they are never
// recorded as this launch's, and returns the paths to read afterwards — none when the collector
// records nothing or a stale file could not be removed, in which case nothing is read.
//
// Both the logs directory's file and the one directly under `.yolo` are cleared and read: a
// bootstrap from a yolo older than the logs directory (docs/design/workspace-state-safety.md
// SD-D15) writes the second, and whichever the bootstrap wrote is then the only one there.
func clearBootPerf(deps Deps, workspace string) []string {
	if !deps.Perf.Enabled() || deps.RemoveFile == nil || deps.ReadFile == nil {
		return nil
	}
	ps := []string{entrypoint.BootPerfPath(workspace), paths.LegacyWorkspaceLogPath(workspace, entrypoint.BootPerfName)}
	for _, p := range ps {
		if !deps.RemoveFile(p) {
			return nil
		}
	}
	return ps
}

// recordBootPerf records each step the bootstrap wrote to the first of ps it can read as
// macos_user.bootstrap.<step>, on success and failure alike: a refused bootstrap's steps up to
// the refusal are the ones worth reading. Each step is recorded once whatever the file repeats.
func recordBootPerf(deps Deps, ps []string) {
	var body string
	ok := false
	for _, p := range ps {
		if body, ok = deps.ReadFile(p); ok {
			break
		}
	}
	if !ok {
		return
	}
	for _, s := range parseBootPerf(body) {
		deps.Perf.Record("macos_user.bootstrap."+s.Name, s.Dur)
	}
}

// parseBootPerf reads the bootstrap's `<seconds since start>\t<step>` lines into steps, each
// lasting from the mark before it. A line that does not parse is dropped, and the rest are
// perf.StepsFromMarks's rules.
func parseBootPerf(body string) []perf.ForeignStep {
	var marks []perf.ForeignMark
	for _, line := range strings.Split(body, "\n") {
		secs, label, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		at, err := strconv.ParseFloat(secs, 64)
		if err != nil {
			continue
		}
		marks = append(marks, perf.ForeignMark{At: at, Label: label})
	}
	return perf.StepsFromMarks(marks)
}
