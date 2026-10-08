package cli

// buildpool.go is a JAIL LAUNCH'S BUILD POOL (docs/design/pi-extension-store-builds.md §5.2, §5.4;
// XB-D10, XB-D56): every key its fork-build slot serves — each plain fork's missing build, each
// patched fork's advance, each patched extension's advance and copy — runs at once, and the launch
// waits for the slowest key rather than the sum. It is run.Options.BuildSlot (runBuildSlot), and
// BuildForks and BuildTrees run their halves through it too.
//
// THE BOUNDS ARE THE POOL'S AND THE KEYS' OWN. Checks, which are network-bound and cheap, run at
// most run.SlotChecks at a time; a key's walk, replay and sealed build at most
// run.SlotBuildJails(rt), one on Apple Container. A key takes a build slot as soon as its own check
// finds something to walk, and never waits for other checks; slots go to the keys waiting for them
// in declaration order (fifoSem), whose lines print first. Each key keeps its own bounds — the
// check's fetch, the replay's, forkBuildWaitBound for its build — and patched-forks.md §6.6's locks,
// in their order: two keys of one repository serialize on its mirror lock and build in parallel
// (PPX-D13).
//
// THE OUTPUT KEEPS THE ORDER THE KEYS ARE DECLARED IN (PPX-D13, PF-D11). Each key's lines are
// buffered (poolItem.buf) and printed once it and every key before it have ended, the forks' before
// the extensions', as the two halves always printed. A build's start line, which carries its
// disclosures, prints at once, because a launch parked in silence reads as a hang; one progress line
// for the whole pool stands for what is running (internal/progress), and each build's result line
// carries its own time.
//
// ONE CTRL-C ENDS EVERY WAIT (PF-D57): the pool runs under one interrupt scope, the act's, whose
// context every key's check, walk, lock wait and build jail runs under. A Ctrl-C cancels them all;
// each key with a good build is handed it, and one with none goes without, said, and the next fresh
// launch builds it — a first advance's included, whose Ctrl-C once ended the whole launch (§7,
// PF-D80). A key that had not begun begins nothing.
//
// EVERY BUILD IS THE fork-build-jail CHILD (forkbuildchild.go). A build jail is a whole launch, and a
// launch run in this process mutates process-wide state — its signal arms (run's armstack.go), its
// pack-record scope — that two at once would share; a child has its own process, and its every
// stream is a pipe this process reads.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// buildPoolLabel is the pool's progress line's label.
const buildPoolLabel = "Waiting for this launch's builds"

// buildPool is one fork-build slot's pool.
type buildPool struct {
	w      io.Writer // the launch stream
	cfg    progress.Config
	color  bool
	report *buildReport
	checks *fifoSem
	builds *fifoSem
	act    *run.ActInterrupt

	mu      sync.Mutex
	ctx     context.Context
	line    *progress.Line
	items   []*poolItem
	flushed int // items[:flushed] are printed
	ended   int
}

// poolItem is one key of the pool: its lines, and what it is doing now, which the pool's line says.
// Every method is safe on a nil *poolItem, an advance outside a pool's, which then runs as an act of
// its own.
type poolItem struct {
	pool  *buildPool
	index int
	label string
	work  func(it *poolItem)

	buf   keyBuffer // the key's lines (out and errw both), printed in declaration order
	phase string    // "checking", "building" or "", under pool.mu
	note  string    // what a building key waits for, which the pool's line adds, under pool.mu
	ended bool      // under pool.mu
}

// newBuildPool is the pool of a launch whose stream is w, running at most maxChecks checks and
// maxBuilds build jails at once (0: run's own bounds for rt, on poolCPUs), under act's interrupt scope.
func newBuildPool(w io.Writer, cfg progress.Config, color bool, workspace, rt string, maxChecks, maxBuilds int,
	act *run.ActInterrupt) *buildPool {
	if maxChecks <= 0 {
		maxChecks = run.SlotChecks
	}
	if maxBuilds <= 0 {
		maxBuilds = buildSlots(rt)
	}
	p := &buildPool{w: w, cfg: cfg, color: color, checks: newFIFOSem(maxChecks), builds: newFIFOSem(maxBuilds),
		act: act, ctx: context.Background()}
	p.report = newBuildReport(workspace, rt, w, color)
	return p
}

// add appends a key, in declaration order; work runs on a goroutine of its own once the pool runs.
func (p *buildPool) add(label string, work func(it *poolItem)) {
	p.report.order[label] = len(p.items)
	p.items = append(p.items, &poolItem{pool: p, index: len(p.items), label: label, work: work})
}

// say prints a line on the launch stream above the pool's line, before or while it runs.
func (p *buildPool) say(markup string) {
	pr := richtext.Printer{W: p.writer(), Color: p.color}
	pr.Print(markup)
}

// writer is where a line the pool prints at once goes: through its progress line while it runs, so
// the live line is never torn, and to the stream before.
func (p *buildPool) writer() io.Writer {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.line != nil {
		return p.line
	}
	return p.w
}

// run runs every key at once under the act's one interrupt scope and returns once each has ended
// and its lines are printed. A pool with no keys starts nothing.
func (p *buildPool) run() {
	if len(p.items) == 0 {
		return
	}
	scope := func(ctx context.Context) {
		p.mu.Lock()
		p.ctx = ctx
		p.line = p.cfg.Start(p.w, buildPoolLabel)
		p.line.Set(p.renderLocked())
		p.mu.Unlock()
		var wg sync.WaitGroup
		for _, it := range p.items {
			wg.Add(1)
			go func(it *poolItem) {
				defer wg.Done()
				defer it.end()
				it.work(it)
			}(it)
		}
		wg.Wait()
		p.mu.Lock()
		line := p.line
		p.line = nil
		p.mu.Unlock()
		line.Set("") // every key's own result line said how it ended
		line.Done("")
	}
	if p.act == nil {
		run.InterruptScope(scope)
		return
	}
	p.act.Scope(scope)
}

// renderLocked is the pool's line's detail: what is building, how many are checking — naming a check
// that waits for another's lock — and how many keys have ended. Callers hold mu.
func (p *buildPool) renderLocked() string {
	var building, waiting []string
	checking := 0
	for _, it := range p.items {
		switch it.phase {
		case "building":
			if it.note != "" {
				building = append(building, it.label+" ("+it.note+")")
				continue
			}
			building = append(building, it.label)
		case "checking":
			checking++
			if it.note != "" {
				waiting = append(waiting, it.label+" ("+it.note+")")
			}
		}
	}
	var parts []string
	if len(building) > 0 {
		parts = append(parts, "building "+strings.Join(building, ", "))
	}
	if checking > 0 {
		c := fmt.Sprintf("checking %d", checking)
		if len(waiting) > 0 {
			c += ": " + strings.Join(waiting, ", ")
		}
		parts = append(parts, c)
	}
	return strings.Join(append(parts, fmt.Sprintf("%d of %d done", p.ended, len(p.items))), "; ")
}

// setPhase records what it is doing and redraws the pool's line.
func (it *poolItem) setPhase(phase string) {
	p := it.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	it.phase = phase
	if phase == "" {
		it.note = ""
	}
	p.line.Set(p.renderLocked())
}

// setNote records what the key waits for, building or checking, when shown is set, and clears it
// otherwise.
func (it *poolItem) setNote(note string, shown bool) {
	if it == nil {
		return
	}
	if !shown {
		note = ""
	}
	p := it.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	it.note = note
	p.line.Set(p.renderLocked())
}

// end marks it ended and prints, in declaration order, every key's lines that may print now.
func (it *poolItem) end() {
	p := it.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	it.ended, it.phase = true, ""
	p.ended++
	for p.flushed < len(p.items) && p.items[p.flushed].ended {
		if data := p.items[p.flushed].buf.Bytes(); len(data) > 0 {
			_, _ = p.line.Write(data)
		}
		p.flushed++
	}
	p.line.Set(p.renderLocked())
}

// context is the pool's interrupt scope's context, which a Ctrl-C cancels; Background outside one.
func (it *poolItem) context() context.Context {
	if it == nil {
		return context.Background()
	}
	it.pool.mu.Lock()
	defer it.pool.mu.Unlock()
	return it.pool.ctx
}

// stopped reports whether a Ctrl-C has ended the act's wait: this pool's, or an earlier scope's of
// the same act.
func (it *poolItem) stopped() bool {
	if it == nil {
		return false
	}
	return it.context().Err() != nil || it.pool.act.Interrupted()
}

// stream is where the key's lines go: its buffer, printed in declaration order.
func (it *poolItem) stream() io.Writer { return &it.buf }

// check runs fn, the key's check, once a check slot is free, and reports whether it ran: false when
// a Ctrl-C ended the wait for the slot first. Outside a pool it runs fn at once.
func (it *poolItem) check(fn func()) bool {
	if it == nil {
		fn()
		return true
	}
	if !it.pool.checks.acquire(it.context(), it.index) {
		return false
	}
	defer it.pool.checks.release()
	it.setPhase("checking")
	defer it.setPhase("")
	fn()
	return true
}

// build takes a build slot for the key's walk, replay and build, waiting for one, and returns its
// release; ok is false when a Ctrl-C ended the wait first. Outside a pool it takes nothing.
func (it *poolItem) build() (release func(), ok bool) {
	if it == nil {
		return func() {}, true
	}
	if !it.pool.builds.acquire(it.context(), it.index) {
		return func() {}, false
	}
	it.setPhase("building")
	return func() {
		it.setPhase("")
		it.pool.builds.release()
	}, true
}

// keyBuffer is a bytes.Buffer safe for the writers of one key and the pool's flush.
type keyBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *keyBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// Bytes is a copy of what was written.
func (s *keyBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}

func (s *keyBuffer) String() string { return string(s.Bytes()) }

// fifoSem is a counting semaphore whose free slots go to the waiters in the order of their index,
// the keys' declaration order, so with one slot the keys run in turn, as the slot always ran them.
type fifoSem struct {
	mu      sync.Mutex
	free    int
	waiters []*fifoWaiter // by index
}

type fifoWaiter struct {
	index int
	ready chan struct{}
}

func newFIFOSem(n int) *fifoSem { return &fifoSem{free: max(1, n)} }

// acquire takes a slot, waiting for one, and reports false when ctx ended before it did.
func (s *fifoSem) acquire(ctx context.Context, index int) bool {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	if s.free > 0 && len(s.waiters) == 0 {
		s.free--
		s.mu.Unlock()
		return true
	}
	w := &fifoWaiter{index: index, ready: make(chan struct{})}
	at, _ := slices.BinarySearchFunc(s.waiters, index, func(w *fifoWaiter, i int) int { return w.index - i })
	s.waiters = slices.Insert(s.waiters, at, w)
	s.mu.Unlock()
	select {
	case <-w.ready:
		return true
	case <-ctx.Done():
	}
	s.mu.Lock()
	if i := slices.Index(s.waiters, w); i >= 0 {
		s.waiters = slices.Delete(s.waiters, i, i+1)
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	s.release() // handed a slot as ctx ended: give it on
	return false
}

// release frees a slot, to the first waiter when there is one.
func (s *fifoSem) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiters) > 0 {
		w := s.waiters[0]
		s.waiters = s.waiters[1:]
		close(w.ready)
		return
	}
	s.free++
}

// runBuildSlot is the fork-build slot's act (run.Options.BuildSlot): every key of req's two halves
// in one pool, printed on stream, and each half's answers.
func runBuildSlot(req run.BuildSlotRequest, stream io.Writer, color bool) (map[string]entrypoint.ForkDelivery,
	map[string]run.TreeDelivery) {
	var workspace, rt string
	var cfg progress.Config
	var act *run.ActInterrupt
	switch {
	case req.Forks != nil:
		workspace, rt, cfg, act = req.Forks.Workspace, req.Forks.Runtime, req.Forks.Progress, req.Forks.Interrupt
	case req.Trees != nil:
		workspace, rt, cfg, act = req.Trees.Workspace, req.Trees.Runtime, req.Trees.Progress, req.Trees.Interrupt
	}
	pool := newBuildPool(stream, cfg, color, workspace, rt, req.MaxChecks, req.MaxBuilds, act)
	var mu sync.Mutex
	forks := map[string]entrypoint.ForkDelivery{}
	trees := map[string]run.TreeDelivery{}
	if fr := req.Forks; fr != nil {
		addForkKeys(pool, *fr, color, func(bin string, d entrypoint.ForkDelivery) {
			mu.Lock()
			defer mu.Unlock()
			forks[bin] = d
		})
	}
	var later []bool
	if tr := req.Trees; tr != nil {
		later = make([]bool, len(tr.Trees))
		for i, f := range tr.Trees {
			pool.add(f.Label(), func(it *poolItem) {
				d, l := deliverTree(f, *tr, pool.report, it, color)
				mu.Lock()
				defer mu.Unlock()
				trees[f.Key()], later[i] = d, l
			})
		}
	}
	pool.run()
	// A cause that held a good build is said once, when every key has printed (buildcauses.go).
	pool.report.flush()
	if tr := req.Trees; tr != nil {
		var background []packload.Fork
		for i, f := range tr.Trees {
			if later[i] {
				background = append(background, f)
			}
		}
		if len(background) > 0 {
			backgroundTreeAdvance(background, *tr, stream, color)
		}
	}
	return forks, trees
}

// addForkKeys adds the fork builds' keys to pool, in the order the request names them: each patched
// fork's advance, and each plain fork the store holds no build of at its pin, whose cost the launch
// states once, as auto-capture states its (a source build fetches its dependencies and compiles, once
// per commit per machine). A plain fork the store holds a build of is answered at once.
func addForkKeys(pool *buildPool, req run.ForkBuildRequest, color bool, answer func(string, entrypoint.ForkDelivery)) {
	store := &capture.Store{Dir: paths.CapturesDir()}
	missing := 0
	for _, p := range req.Pins {
		if p.Fork.Patched() {
			pool.add(p.Fork.Label(), func(it *poolItem) {
				answer(p.Fork.Bin, advancePatchedFork(p.Fork, advanceOptions{platform: req.Platform, runtime: req.Runtime,
					workspace: req.Workspace, out: it.stream(), errw: it.stream(), color: color, launch: true,
					hand: req.Hand, act: req.Interrupt, report: pool.report, slot: it}).delivery)
			})
			continue
		}
		b := forkBuild{Fork: p.Fork, Commit: p.Commit, Platform: req.Platform}
		if entry, _, err := resolveForkBuild(store, p.Fork.Bin, req.Platform, p.Fork.Source, p.Commit, b.recipe()); err == nil {
			answer(p.Fork.Bin, entrypoint.ForkDelivery{Key: entry.Key})
			continue
		}
		missing++
		pool.add(p.Fork.Label(), func(it *poolItem) {
			answer(p.Fork.Bin, buildPlainForkForLaunch(b, req.Runtime, pool.report, it, color))
		})
	}
	if missing > 0 && !req.Interrupt.Interrupted() {
		pool.say(fmt.Sprintf("[bold]fork builds[/bold]  %d %s never built at %s on this machine[dim] — each is built "+
			"once now, from its pinned commit, and every later launch materializes it[/dim]", missing,
			plural(missing, "fork", "forks"), plural(missing, "its pin", "their pins")))
	}
}
