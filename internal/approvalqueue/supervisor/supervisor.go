// Package supervisor runs the two yolo children the queue reads: a short "yolo approve --json"
// for each snapshot and a long-lived "yolo approve --watch --json" for events. It runs them with
// an argv and never a shell, parses their output into model inputs and hands those to a sink.
package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
)

// StoreReadFailure is the text yolo's watch prints on stderr when a later store read fails.
const StoreReadFailure = "reading the store failed"

// Config is the supervisor's tunables; zero values take the documented defaults.
type Config struct {
	Yolo              string        // the yolo executable (name on PATH or a path)
	Reconcile         time.Duration // snapshot interval, default 15 s
	SnapshotTimeout   time.Duration // per "--json" run, default 60 s
	BackoffInit       time.Duration // watch restart delay, default 1 s
	BackoffMax        time.Duration // doubled up to this, default 30 s
	BackoffReset      time.Duration // a child that ran this long resets the delay, default 60 s
	MaxLine           int           // longest accepted line or snapshot, default 16 MiB
	StderrKeep        int           // bytes of stderr kept for a failure message, default 4 KiB
	NoInitialSnapshot bool          // the caller already took the first snapshot
	// Sleep waits d or until ctx ends and reports whether the full wait happened; Mono reads a
	// monotonic clock. Both are injectable so the restart schedule is testable; nil means real.
	Sleep func(ctx context.Context, d time.Duration) bool
	Mono  func() time.Time
	Now   func() time.Time
	Logf  func(format string, args ...any) // counts and errors only, never request contents
}

func (c *Config) defaults() {
	if c.Yolo == "" {
		c.Yolo = "yolo"
	}
	if c.Reconcile <= 0 {
		c.Reconcile = 15 * time.Second
	}
	if c.SnapshotTimeout <= 0 {
		c.SnapshotTimeout = 60 * time.Second
	}
	if c.BackoffInit <= 0 {
		c.BackoffInit = time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	if c.BackoffReset <= 0 {
		c.BackoffReset = 60 * time.Second
	}
	if c.MaxLine <= 0 {
		c.MaxLine = 16 << 20
	}
	if c.StderrKeep <= 0 {
		c.StderrKeep = 4 << 10
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Mono == nil {
		c.Mono = time.Now
	}
	if c.Sleep == nil {
		c.Sleep = func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-t.C:
				return true
			}
		}
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
}

// Supervisor runs the children.
type Supervisor struct {
	cfg     Config
	sink    func(model.Input)
	trigger chan struct{}
}

// New makes a supervisor that hands every input to sink.
func New(cfg Config, sink func(model.Input)) *Supervisor {
	cfg.defaults()
	return &Supervisor{cfg: cfg, sink: sink, trigger: make(chan struct{}, 1)}
}

// Trigger asks for a snapshot. At most one "--json" runs at a time and any number of triggers
// that arrive while one runs coalesce into a single follow-up.
func (s *Supervisor) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// Result is the outcome of one snapshot attempt.
type Result struct {
	Input     model.Input // SnapshotOK or SnapshotFail; nil when the attempt was cancelled
	NotFound  bool        // yolo could not be found or run
	Exit      int         // the child's exit code when it ran and failed, else 0
	Message   string
	Cancelled bool
}

// Snapshot runs "yolo approve --json" once and returns the outcome without applying it.
func (s *Supervisor) Snapshot(ctx context.Context) Result {
	start := s.cfg.Now()
	runCtx, cancel := context.WithTimeout(ctx, s.cfg.SnapshotTimeout)
	defer cancel()
	cmd := s.command(runCtx, "approve", "--json")
	out := &capBuf{max: s.cfg.MaxLine}
	errb := &headBuf{max: s.cfg.StderrKeep}
	cmd.Stdout, cmd.Stderr = out, errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{Cancelled: true}
	}
	fail := func(kind model.FailKind, msg string) Result {
		return Result{Input: model.SnapshotFail{At: start, Kind: kind, Message: msg}, Message: msg}
	}
	if err != nil {
		var nf *exec.Error
		var pe *fs.PathError
		if errors.As(err, &nf) || errors.As(err, &pe) {
			r := fail(model.FailExit, "cannot run "+s.cfg.Yolo+": "+err.Error()+"; run `yolo approve --json` to see whether yolo itself works")
			r.NotFound = true
			return r
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return fail(model.FailTimeout, fmt.Sprintf("yolo approve --json timed out after %s", s.cfg.SnapshotTimeout))
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := fmt.Sprintf("yolo approve --json exited %d", ee.ExitCode())
			if t := cleanStderr(errb.String()); t != "" {
				msg += ": " + t
			}
			r := fail(model.FailExit, msg)
			r.Exit = ee.ExitCode()
			return r
		}
		return fail(model.FailExit, err.Error())
	}
	if out.over {
		return fail(model.FailUnsupported, fmt.Sprintf("yolo approve --json printed more than %d bytes", s.cfg.MaxLine))
	}
	st, perr := model.ParseState(out.Bytes())
	if perr != nil {
		var se *model.SchemaError
		if errors.As(perr, &se) {
			return fail(model.FailUnsupported, perr.Error())
		}
		return fail(model.FailUnsupported, "yolo approve --json printed something that is not a schema 1 snapshot ("+perr.Error()+")")
	}
	return Result{Input: model.SnapshotOK{State: st, FetchStart: start}}
}

// cleanStderr drops yolo's one-line banner and trims.
func cleanStderr(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "yolo-jail ") && strings.Contains(l, " | ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// Run starts the snapshot loop and the watch loop and blocks until ctx ends and both children
// are dead. It triggers a first snapshot itself.
func (s *Supervisor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.snapshotLoop(ctx) }()
	go func() { defer wg.Done(); s.watchLoop(ctx) }()
	wg.Wait()
}

func (s *Supervisor) snapshotLoop(ctx context.Context) {
	tick := time.NewTicker(s.cfg.Reconcile)
	defer tick.Stop()
	if !s.cfg.NoInitialSnapshot {
		s.Trigger()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.trigger:
		}
		res := s.Snapshot(ctx)
		if res.Cancelled {
			return
		}
		if f, ok := res.Input.(model.SnapshotFail); ok {
			s.cfg.Logf("snapshot failed: %s", f.Message)
		}
		s.sink(res.Input)
	}
}

func (s *Supervisor) watchLoop(ctx context.Context) {
	delay := s.cfg.BackoffInit
	for ctx.Err() == nil {
		began := s.cfg.Mono()
		msg := s.watchOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if s.cfg.Mono().Sub(began) >= s.cfg.BackoffReset {
			delay = s.cfg.BackoffInit
		}
		s.cfg.Logf("watch ended (%s); restarting in %s", msg, delay)
		s.sink(model.WatchExited{At: s.cfg.Now(), Message: msg})
		s.Trigger()
		if !s.cfg.Sleep(ctx, delay) {
			return
		}
		if delay *= 2; delay > s.cfg.BackoffMax {
			delay = s.cfg.BackoffMax
		}
	}
}

// watchOnce runs one watch child to its end and returns why it ended.
func (s *Supervisor) watchOnce(ctx context.Context) string {
	started := s.cfg.Now()
	s.sink(model.WatchStarted{At: started})
	cmd := s.command(ctx, "approve", "--watch", "--json")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err.Error()
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err.Error()
	}
	if err := cmd.Start(); err != nil {
		return "cannot start: " + err.Error()
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			if line := sc.Text(); strings.Contains(line, StoreReadFailure) {
				s.sink(model.WatchReadFailed{At: s.cfg.Now(), Message: cleanStderr(line)})
			}
		}
		io.Copy(io.Discard, stderr)
	}()
	r := bufio.NewReaderSize(stdout, 64<<10)
	first := true
	for {
		line, over, rerr := readLine(r, s.cfg.MaxLine)
		if len(line) > 0 || over {
			s.handleLine(line, over, started, first)
			first = false
		}
		if rerr != nil {
			break
		}
	}
	wg.Wait()
	werr := cmd.Wait()
	if werr != nil {
		return werr.Error()
	}
	return "exit status 0"
}

func (s *Supervisor) handleLine(line []byte, over bool, started time.Time, first bool) {
	bad := func(why string) {
		s.cfg.Logf("watch line discarded: %s", why)
		s.sink(model.Malformed{At: s.cfg.Now(), Message: why})
		s.Trigger()
	}
	if over {
		bad(fmt.Sprintf("line over %d bytes", s.cfg.MaxLine))
		return
	}
	var head struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		bad("not JSON")
		return
	}
	if head.Event == model.EvState {
		st, err := model.ParseState(line)
		if err != nil {
			var se *model.SchemaError
			if errors.As(err, &se) {
				s.sink(model.SnapshotFail{At: s.cfg.Now(), Kind: model.FailUnsupported, Message: err.Error()})
				bad("unsupported schema")
				return
			}
			bad("bad state line")
			return
		}
		fetch := started
		if !first {
			fetch = s.cfg.Now()
		}
		s.sink(model.SnapshotOK{State: st, FetchStart: fetch, FromWatch: true})
		return
	}
	ev, err := model.ParseEvent(line)
	if err != nil {
		var se *model.SchemaError
		if errors.As(err, &se) {
			s.sink(model.SnapshotFail{At: s.cfg.Now(), Kind: model.FailUnsupported, Message: err.Error()})
		}
		bad("bad event line")
		return
	}
	s.sink(model.EventIn{Event: ev})
}

// readLine reads one line of at most max bytes. A longer line is consumed to its newline and
// reported with over set and no content.
func readLine(r *bufio.Reader, max int) (line []byte, over bool, err error) {
	var acc []byte
	for {
		chunk, e := r.ReadSlice('\n')
		if !over {
			if len(acc)+len(chunk) > max+1 {
				over, acc = true, nil
			} else {
				acc = append(acc, chunk...)
			}
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		if over {
			return nil, true, e
		}
		return bytes.TrimRight(acc, "\r\n"), false, e
	}
}

// capBuf keeps up to max bytes and notes when more arrived.
type capBuf struct {
	b    bytes.Buffer
	max  int
	over bool
}

func (c *capBuf) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); len(p) > room {
		c.over = true
		if room > 0 {
			c.b.Write(p[:room])
		}
		return len(p), nil
	}
	return c.b.Write(p)
}
func (c *capBuf) Bytes() []byte { return c.b.Bytes() }

// headBuf keeps the first max bytes.
type headBuf struct {
	b   bytes.Buffer
	max int
}

func (h *headBuf) Write(p []byte) (int, error) {
	if room := h.max - h.b.Len(); room > 0 {
		if len(p) > room {
			h.b.Write(p[:room])
		} else {
			h.b.Write(p)
		}
	}
	return len(p), nil
}
func (h *headBuf) String() string { return h.b.String() }

// childEnv is the queue's environment minus the variable that would make yolo label a
// decision as the on_request hook's. The queue never holds a token or code in its environment.
func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "YOLO_REQUEST_ID=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func (s *Supervisor) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.cfg.Yolo, args...)
	cmd.Env = childEnv()
	confine(cmd)
	return cmd
}
