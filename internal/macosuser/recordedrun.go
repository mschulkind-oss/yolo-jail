package macosuser

// recordedrun.go keeps a child's own output where the act's reader can find it: Deps.RunRecorded,
// and the capture bootstrap's use of it (runCaptureSteps).
//
// WHY THE BOOTSTRAP NEEDS IT. Every other crossing inherits this process's stdout and stderr
// (runReal says why). A build jail's log keeps what the act writes to Out, not what a child
// writes to the inherited descriptors, so a capture bootstrap that failed on a Mac left one line
// in the build log, "the staging home has no generated launcher", and nothing of the bootstrap's
// own reason (the 2026-10-23 Mac suite run). The bootstrap is
// non-interactive (its crossing is `sudo -n`, or `/usr/bin/env` as you), so a pipe changes no
// prompt; its output is copied to Out, bounded, and its refusal carries its exit status and the
// tail of its stderr.

import (
	"strconv"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// bootstrapOutputKept is how much of the capture bootstrap's output the act keeps: its last 8 KiB,
// enough for the boot steps' lines and a failure's reason, and a bound on what one bootstrap can
// put in a build log.
const bootstrapOutputKept = 8 << 10

// bootstrapStderrLines is how many of the kept stderr's last lines the refusal repeats.
const bootstrapStderrLines = 20

// RecordedRun is what Deps.RunRecorded kept of one child: the tail of its stdout and stderr
// together and of its stderr alone, its exit status, and whether either tail was cut.
type RecordedRun struct {
	Output string
	Stderr string
	Status int
	Cut    bool
}

// runRecorded runs argv through deps.RunRecorded, keeping keep bytes, or through deps.Run when the
// seam is nil (a test faking Run alone), which records nothing.
func runRecorded(deps Deps, argv []string, keep int) RecordedRun {
	if deps.RunRecorded == nil {
		return RecordedRun{Status: deps.Run(argv)}
	}
	return deps.RunRecorded(argv, keep)
}

// printRecordedOutput copies what was kept of what's output to out, escaped so a bracket the child
// printed is not read as markup, under a header naming it; nothing when it printed nothing.
func printRecordedOutput(out printer, what string, r RecordedRun) {
	body := strings.TrimRight(r.Output, "\n")
	if body == "" {
		return
	}
	head := "[dim]--- the " + what + "'s output"
	if r.Cut {
		head += ", its last " + byteCount(bootstrapOutputKept)
	}
	out.print(head + " ---[/dim]")
	out.print(richtext.Escape(body))
	out.print("[dim]--- end of the " + what + "'s output ---[/dim]")
}

// refuseCaptureBootstrap is the stop for a capture bootstrap that exited non-zero: its exit status,
// the last lines of its stderr, and the next step.
func refuseCaptureBootstrap(out printer, boot RecordedRun) {
	out.printf("[bold red]capture bootstrap failed (exit %d)[/bold red] — the staging home has no "+
		"generated launcher, so there is nothing for the capture to run. Aborting.", boot.Status)
	if lines := lastLines(boot.Stderr, bootstrapStderrLines); len(lines) > 0 {
		out.print("  The end of its stderr:")
		for _, ln := range lines {
			out.print("    " + richtext.Escape(ln))
		}
	} else {
		out.print("  It wrote nothing on stderr.")
	}
	out.print("  Fix what its output names, then run the build or capture again. If it names nothing " +
		"you can fix, report this output, with `yolo --version` and your macos_user_mode, to yolo's maintainers.")
}

// lastLines is the last n lines of s, without its trailing newline.
func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// byteCount spells n bytes as KiB when it is a whole number of them.
func byteCount(n int) string {
	if n >= 1024 && n%1024 == 0 {
		return strconv.Itoa(n/1024) + " KiB"
	}
	return strconv.Itoa(n) + " bytes"
}

// tailBuffer is an io.Writer keeping the last max bytes written to it, safe for the two goroutines
// os/exec copies a child's stdout and stderr with.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
	cut bool
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.cut = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Cut reports whether anything written was dropped.
func (t *tailBuffer) Cut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cut
}
