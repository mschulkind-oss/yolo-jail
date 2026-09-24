// Package lingerprobe watches a `podman run` client AFTER its container has
// died, and writes down what the client is blocked on while it lingers.
//
// THE MEASUREMENT IT EXISTS FOR (maintainer's host, 2026-09-24): podman's own
// events put the container's `died` and `remove` 42 ms apart, and the `podman run`
// process that yolo spawned was reaped 12.6 s after the `remove`. The client
// itself lingered, with its container already gone, and nothing on the host
// recorded why. docs/reference/perf-logging.md ("Window A") has the whole story.
//
// # How the death is learned
//
// conmon writes `<exit-dir>/<container id>` the instant the container's process
// is reaped (exitdir.go says where that directory is). An inotify watch on that
// directory is ONE goroutine parked in the netpoller for the jail's lifetime: no
// extra process, no timer, no polling during a normal session. Nothing is sampled
// until the exit file appears. Two alternatives were rejected. The pty going quiet
// is not a signal (the client holds the slave open while it lingers, and an idle
// agent is quiet too). A pidfd on the container's init needs its host pid, which
// takes a `podman inspect`, and podman's own lock is one of the suspects.
//
// # Bounds
//
// Sampling starts Delay (1 s) after the death, repeats at most every Interval
// (500 ms), ends when the client is gone, and is capped in samples and in lines.
// Stop never waits for a sample in flight (a /proc/<pid>/mem read takes the
// target's mmap lock, the one read here that can wait on podman), and once Stop
// returns no further line is ever emitted. The probe holds nothing podman
// needs: it reads /proc and watches a directory.
package lingerprobe

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnsupported is Start's answer off Linux: there is no /proc and no inotify,
// and on macOS the container runtime's client is not a local Linux process anyway.
var ErrUnsupported = errors.New("lingerprobe: unsupported on this platform")

// Note names the probe emits, so the run package and the doc spell them once.
const (
	NoteSample = "shutdown.window_a.sample"
	NoteHost   = "shutdown.window_a.host"
	NotePty    = "shutdown.window_a.pty_mode"
)

// Config is one probe's inputs.
type Config struct {
	PID      int    // the `podman run` client
	CtrID    string // full or short (≥12 chars) container id
	CtrName  string // this jail's container name, for the host context
	ExitDir  string // conmon's exit directory (DetectExitDir)
	ProcRoot string // "" = /proc
	UID      int    // whose podman/conmon processes the host context counts

	Delay      time.Duration // after the death, before the first sample; 0 = 1s
	Interval   time.Duration // between samples; 0 = 500ms
	MaxSamples int           // 0 = 120
	MaxLines   int           // 0 = 80

	// Emit writes one note. Required. Called with the probe's lock held, so a
	// line is never emitted after Stop returns; it must not call back into the probe.
	Emit func(name, detail string)
	// OnDeath runs once, when the exit file appears, under the same rule as Emit.
	OnDeath func(at time.Time)
	// PtyMode, if set, reports the proxy pty's line-discipline mode; it is
	// recorded at the death and appended to every sample.
	PtyMode func() string
}

// Result is what Stop hands back.
type Result struct {
	DeathSeen bool
	DeathAt   time.Time
	Samples   int
	// Dominant completes "podman stayed Ns after …, <Dominant>" — "blocked in
	// read(0 → /dev/pts/5)" — or is "" when nothing was sampled.
	Dominant string
}

// Probe is one running watch-then-sample.
type Probe struct {
	cfg   Config
	s     Sampler
	start uint64
	watch *exitWatch
	stop  chan struct{}

	mu      sync.Mutex
	stopped bool
	lines   int
	capped  bool
	res     Result
	ty      tally
	last    string
	held    int // samples since the last emitted line that matched it
}

// sampleReadHook runs between a sample's /proc read and its tally — nil in
// production; a test holds a sample in flight across Stop with it.
var sampleReadHook func()

// heartbeatEvery is how many identical samples pass between "unchanged" lines:
// a state that holds is written once, then every heartbeat, so the file still
// shows when the client was last seen alive — the only record a SIGKILL leaves.
const heartbeatEvery = 10

// Start arms the probe: the exit-directory watch now, sampling only after the
// death.
func Start(cfg Config) (*Probe, error) {
	if !supported {
		return nil, ErrUnsupported
	}
	if cfg.PID <= 0 {
		return nil, errors.New("lingerprobe: no client pid")
	}
	if len(cfg.CtrID) < 12 {
		return nil, errors.New("lingerprobe: no container id")
	}
	if cfg.Emit == nil {
		return nil, errors.New("lingerprobe: no Emit")
	}
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = "/proc"
	}
	if cfg.Delay <= 0 {
		cfg.Delay = time.Second
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.MaxSamples <= 0 {
		cfg.MaxSamples = 120
	}
	if cfg.MaxLines <= 0 {
		cfg.MaxLines = 80
	}
	s := Sampler{Root: cfg.ProcRoot, Name: syscallName, Clock: clockNow}
	st, ok := s.Stat(cfg.PID)
	if !ok {
		return nil, fmt.Errorf("lingerprobe: pid %d unreadable", cfg.PID)
	}
	w, err := watchExitFile(cfg.ExitDir, cfg.CtrID)
	if err != nil {
		return nil, err
	}
	p := &Probe{cfg: cfg, s: s, start: st.StartTime, watch: w, stop: make(chan struct{})}
	go p.run()
	return p, nil
}

func (p *Probe) run() {
	var at time.Time
	select {
	case at = <-p.watch.Death():
	case <-p.stop:
		return
	}
	p.watch.Close()
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.res.DeathSeen, p.res.DeathAt = true, at
	if p.cfg.OnDeath != nil {
		p.cfg.OnDeath(at)
	}
	p.mu.Unlock()

	p.emit(NoteHost, p.s.ReadHostContext(p.cfg.UID, p.cfg.CtrName).Line())
	if p.cfg.PtyMode != nil {
		if m := p.cfg.PtyMode(); m != "" {
			p.emit(NotePty, m)
		}
	}
	if !p.wait(p.cfg.Delay) {
		return
	}
	for i := 0; i < p.cfg.MaxSamples; i++ {
		if !p.s.Alive(p.cfg.PID, p.start) {
			p.mu.Lock()
			held := p.held
			p.mu.Unlock()
			p.emit(NoteSample, fmt.Sprintf("%s client gone (last state held ×%d)", p.offset(), held))
			return
		}
		p.sampleOnce("")
		if !p.wait(p.cfg.Interval) {
			return
		}
	}
	p.emit(NoteSample, fmt.Sprintf("%s sampling capped at %d samples", p.offset(), p.cfg.MaxSamples))
}

func (p *Probe) wait(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.stop:
		return false
	case <-t.C:
		return true
	}
}

func (p *Probe) offset() string {
	return fmt.Sprintf("%+.3fs", time.Since(p.res.DeathAt).Seconds())
}

// sampleOnce reads the tree (lock NOT held — the reads can wait on podman),
// then tallies and emits under the lock.
func (p *Probe) sampleOnce(tag string) {
	snap := p.s.Sample(p.cfg.PID)
	if sampleReadHook != nil {
		sampleReadHook()
	}
	line := snap.Line()
	if p.cfg.PtyMode != nil {
		if m := p.cfg.PtyMode(); m != "" {
			line += "; pty " + m
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.ty.add(snap)
	p.res.Samples++
	detail := p.offset() + " " + line
	switch {
	case tag != "":
		detail = p.offset() + " " + tag + ": " + line
	case line == p.last:
		p.held++
		if p.held%heartbeatEvery != 0 {
			return
		}
		detail = fmt.Sprintf("%s unchanged ×%d", p.offset(), p.held)
	default:
		if p.held > 0 {
			detail = fmt.Sprintf("%s (previous held ×%d) %s", p.offset(), p.held, line)
		}
		p.held = 0
	}
	p.last = line
	p.emitLocked(NoteSample, detail)
}

func (p *Probe) emit(name, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopped {
		p.emitLocked(name, detail)
	}
}

func (p *Probe) emitLocked(name, detail string) {
	if p.lines >= p.cfg.MaxLines {
		if !p.capped {
			p.capped = true
			p.cfg.Emit(NoteSample, fmt.Sprintf("line cap (%d) reached; tallying continues", p.cfg.MaxLines))
		}
		return
	}
	p.lines++
	p.cfg.Emit(name, detail)
}

// FinalSample takes one sample NOW, tagged, if the death has been seen and the
// client is still alive — the signal arm's last word, since a client killed
// with its launcher leaves nothing after this. Bounded by budget: a sample that
// cannot finish in time is abandoned, and its line is dropped once Stop runs.
func (p *Probe) FinalSample(tag string, budget time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	ready := p.res.DeathSeen && !p.stopped
	p.mu.Unlock()
	if !ready {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if p.s.Alive(p.cfg.PID, p.start) {
			p.sampleOnce(tag)
		}
	}()
	t := time.NewTimer(budget)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// Stop ends the probe and returns what it saw. Idempotent, nil-safe, and it
// never waits for a sample in flight: after it returns, no line is emitted.
func (p *Probe) Stop() Result {
	if p == nil {
		return Result{}
	}
	p.mu.Lock()
	if !p.stopped {
		p.stopped = true
		close(p.stop)
	}
	res := p.res
	res.Dominant = p.ty.dominant()
	p.mu.Unlock()
	p.watch.Close()
	return res
}
