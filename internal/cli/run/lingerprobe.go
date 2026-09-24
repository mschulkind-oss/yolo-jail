package run

// lingerprobe.go wires internal/lingerprobe into a launch: it arms the probe
// once the container is visible, feeds it the proxy's pty mode, records the
// stdin chunks forwarded after the death (sizes only), and takes the probe down
// before Window A is recorded, so every sample is in the file before the report
// prints.
//
// WHY THIS EXISTS (docs/reference/perf-logging.md, "Window A"): on the
// maintainer's host on 2026-09-24 podman's own teardown (died → remove) took
// 42 ms, and the `podman run` client stayed alive 12.6 s AFTER its container
// was removed. Only the client can explain that, and only while it is still
// alive, so the probe watches the client from the moment its container dies.
//
// Same gate as every other timing observation: the slot exists only when the
// launch is RECORDING (initPerf), so a launch with timing off arms nothing.

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/lingerprobe"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// quickExitAfterInput is how soon after a forwarded keystroke a client exit
// counts as "freed by the keystroke" in the stderr line.
const quickExitAfterInput = 250 * time.Millisecond

// finalSampleBudget bounds the terminate arm's synchronous last sample.
const finalSampleBudget = 200 * time.Millisecond

// lingerSlot is one launch's probe and the input timing beside it. Created by
// initPerf; a nil slot is "not recording".
type lingerSlot struct {
	mu      sync.Mutex
	probe   *lingerprobe.Probe
	closed  bool   // stopped: a late start (onStarted is a goroutine) must not arm
	off     string // why no probe was armed, as a stable token; "" = armed or n/a
	ptyMode func() string

	deathSeen      atomic.Bool
	lastInput      atomic.Int64 // unix nanos of the last forwarded chunk, 0 = none
	lastInputBytes atomic.Int64
	lastInputCtrlC atomic.Bool
	inputAfterDead atomic.Bool  // the last chunk arrived after the death
	firstCtrlC     atomic.Int64 // unix nanos of the first ^C after the death
	inputNotes     atomic.Int32

	// Test seams. Production leaves them zero.
	procRoot        string
	exitDir         string
	delay, interval time.Duration
}

func newLingerSlot() *lingerSlot { return &lingerSlot{} }

// startLingerProbe arms the probe for a fresh launch's `podman run` client.
// ctrID is what `podman ps -q` printed while onStarted waited for the container.
// Off Linux, or on any runtime but podman, it does nothing and says nothing.
func (o *Options) startLingerProbe(rt, cname, ctrID string, proc *os.Process) {
	s := o.linger
	if s == nil || rt != "podman" || o.IsMacOS { // parity: NotApplicable — Window A is a podman-only attribution (attributeWindowA), and only podman's client is a local process with a conmon exit file
		return
	}
	setOff := func(token string) {
		s.mu.Lock()
		s.off = token
		s.mu.Unlock()
	}
	if proc == nil {
		setOff("no_pid")
		return
	}
	if ctrID == "" {
		setOff("no_ctr_id")
		return
	}
	exitDir := s.exitDir
	if exitDir == "" {
		d, ok := lingerprobe.DetectExitDir(os.Geteuid(), o.Getenv)
		if !ok {
			setOff("no_exit_dir")
			return
		}
		exitDir = d
	}
	p, err := lingerprobe.Start(lingerprobe.Config{
		PID: proc.Pid, CtrID: ctrID, CtrName: cname, ExitDir: exitDir,
		ProcRoot: s.procRoot, UID: os.Geteuid(),
		Delay: s.delay, Interval: s.interval,
		Emit: func(name, detail string) { o.Perf.Note(name, detail) },
		OnDeath: func(time.Time) {
			s.deathSeen.Store(true)
			o.Perf.Mark("shutdown.window_a.exit_file_seen")
		},
		PtyMode: s.readPtyMode,
	})
	if err == lingerprobe.ErrUnsupported {
		return
	}
	if err != nil {
		setOff("start_failed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		p.Stop()
		return
	}
	s.probe = p
}

func (s *lingerSlot) readPtyMode() string {
	s.mu.Lock()
	m := s.ptyMode
	s.mu.Unlock()
	if m == nil {
		return ""
	}
	return m()
}

// stopLingerProbe takes the probe down and returns what it saw, plus the
// token naming why nothing was sampled when that is the answer. After it
// returns, the probe writes nothing more.
func (o *Options) stopLingerProbe() (lingerprobe.Result, string) {
	s := o.linger
	if s == nil {
		return lingerprobe.Result{}, ""
	}
	s.mu.Lock()
	s.closed = true
	p, off := s.probe, s.off
	s.mu.Unlock()
	if p == nil {
		return lingerprobe.Result{}, off
	}
	res := p.Stop()
	if !res.DeathSeen {
		return res, "death_unseen"
	}
	return res, ""
}

// lingerFinalSample is the terminate arm's last word: one synchronous, bounded
// sample of a client that may be killed together with this process a moment
// from now. The file sink writes it immediately.
func (o *Options) lingerFinalSample(tag string) {
	s := o.linger
	if s == nil {
		return
	}
	s.mu.Lock()
	p := s.probe
	s.mu.Unlock()
	p.FinalSample(tag, finalSampleBudget)
}

// recordInputGap writes the gap from the last forwarded input to the client's
// exit (or the terminate arm's cut). The stdin hypothesis predicts a gap of
// milliseconds after a post-death keystroke, every time.
func (o *Options) recordInputGap(end time.Time) {
	s := o.linger
	if s == nil {
		return
	}
	last := s.lastInput.Load()
	if last == 0 {
		if s.deathSeen.Load() {
			o.Perf.Note("shutdown.window_a.input_to_exit", "no input forwarded this session")
		}
		return
	}
	gap := end.Sub(time.Unix(0, last))
	detail := fmt.Sprintf("gap=%.3fs bytes=%d after_death=%v chunks_after_death=%d", gap.Seconds(),
		s.lastInputBytes.Load(), s.inputAfterDead.Load(), s.inputNotes.Load())
	if s.lastInputCtrlC.Load() {
		detail += " key=" + "ctrl-c"
	}
	o.Perf.Note("shutdown.window_a.input_to_exit", detail)
}

// unsampledReason renders a probe-off token for the stderr line.
func unsampledReason(token string) string {
	switch token {
	case "no_ctr_id":
		return "the container id was never learned"
	case "no_exit_dir":
		return "conmon's exit directory was not found"
	case "no_pid":
		return "the podman client's pid was not known"
	case "start_failed":
		return "the exit-directory watch could not be set up"
	case "death_unseen":
		return "no exit file appeared for this container"
	}
	return token
}

// noteLingeringClient prints the one line that names what a slow client was
// doing, when its post-teardown linger crossed perf.SlowSpanThreshold. It
// rides the RECORDING gate like the `shutdown.window_a took …` notice it sits
// under, so a quiet launch prints it too — that launch is the one whose user
// just waited through the linger.
func (o *Options) noteLingeringClient(res windowAResult, pr lingerprobe.Result, off string, end time.Time) {
	if !res.split || res.clientExit < perf.SlowSpanThreshold {
		return
	}
	after := "after its container was removed"
	if res.teardownStatus != "remove" {
		after = "after podman's last teardown event (" + res.teardownStatus + ")"
	}
	msg := fmt.Sprintf("podman stayed %.1fs %s", res.clientExit.Seconds(), after)
	switch {
	case pr.Dominant != "":
		msg += ", " + pr.Dominant
	case off != "":
		msg += " (not sampled: " + unsampledReason(off) + ")"
	default:
		msg += " (not sampled)"
	}
	if s := o.linger; s != nil {
		if last := s.lastInput.Load(); last != 0 && s.inputAfterDead.Load() {
			if gap := end.Sub(time.Unix(0, last)); gap >= 0 && gap <= quickExitAfterInput {
				msg += fmt.Sprintf("; it exited %s after forwarded input", gap.Round(time.Millisecond))
			}
		}
		if c := s.firstCtrlC.Load(); c != 0 {
			if gap := end.Sub(time.Unix(0, c)); gap > quickExitAfterInput {
				msg += fmt.Sprintf("; a forwarded ^C did not end it (%.1fs before exit)", gap.Seconds())
			}
		}
	}
	o.pr(o.Stderr).printf("[dim]yolo: %s[/dim]", msg)
}
