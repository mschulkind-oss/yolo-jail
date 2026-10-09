package cli

// treepool.go is THE PARALLEL ADVANCE of a launch's built trees (docs/design/pi-extension-store-builds.md
// §5, XB-D10): every extension a fresh jail launch or a host act delivers runs its own pipeline —
// check, build, admit, copy — and the pipelines run together, where they ran one key after another.
//
//   - TWO BOUNDS, one per kind of work: at most treeCheckSlots checks at once (network-bound and
//     cheap), and at most buildSlots builds (CPU-bound): min(4, max(1, CPUs/2)), and 1 on a backend
//     that cannot start a second capture jail beside a running one (run.BuildJailsSideBySide). A
//     key's build starts as soon as its own check finds a candidate; it waits for no other check.
//   - THE LOCKS ARE THE KEYS' OWN: a build's lock per build identity, a record's per owner key, a
//     mirror's per repository (patched-forks.md §6.6), so two keys of one repository serialize on its
//     mirror for the fetch and the replay and build apart. Nothing here takes a lock: no machine-wide
//     lock is in the path. Records and trees are published by rename and read without a lock.
//   - LINES PRINT IN DECLARATION ORDER (orderedOutput): a key's lines pass straight through while
//     every key before it has ended, and are held until then otherwise; a build that starts while
//     held says so at once on the real error stream, naming its log, because a launch parked in
//     silence reads as a hang.
//   - ONE CTRL-C ENDS EVERY WAIT (PF-D57): the pool runs under ONE interrupt scope, so the signal
//     cancels every key's check, lock wait and build at once — never only the innermost of several
//     scopes, which is what one scope per concurrent advance would do. Each key with a good build is
//     handed it; a key with none takes its fallback or its owner's stop. Every build runs as a child
//     process (forkbuildchild.go), since two in-process launches would share this process's signal
//     arms.
//
// Patched forks keep their own advance, one after another, in the fork slot before this one
// (XB-D35): a launch carries one or two of them, and its extensions are the many.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// treeCheckSlots is how many checks a pool runs at once (XB-D10).
const treeCheckSlots = 8

// buildSlots is how many build jails a pool runs at once on runtime: min(4, max(1, CPUs/2)), 4 being
// pi's own GIT_UPDATE_CONCURRENCY, and 1 where a capture jail cannot start beside a running one.
// It is run.SlotBuildJails' rule on poolCPUs, and the bound of every pool this package makes: a host
// act's (newAdvancePool) and a launch's whose request names none (newBuildPool).
func buildSlots(runtime string) int {
	return run.SlotBuildJailsOn(runtime, poolCPUs())
}

// poolCPUs is this host's CPU count: a var so a test can stand a machine in, since a pool's bound is
// one build on a machine of fewer than four CPUs (a 3-CPU CI runner among them).
var poolCPUs = goruntime.NumCPU

// advancePool bounds a pool's concurrent checks and builds. A nil pool bounds nothing.
type advancePool struct {
	checks, builds chan struct{}
}

func newAdvancePool(runtime string) *advancePool {
	return &advancePool{checks: make(chan struct{}, treeCheckSlots), builds: make(chan struct{}, buildSlots(runtime))}
}

// slot runs fn holding one of sem's slots, and reports whether it ran: false when ctx ended first.
func slot(ctx context.Context, sem chan struct{}, fn func()) bool {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	defer func() { <-sem }()
	if ctx.Err() != nil {
		return false
	}
	fn()
	return true
}

// check runs fn under a check slot; with no pool, at once.
func (p *advancePool) check(ctx context.Context, fn func()) bool {
	if p == nil {
		fn()
		return true
	}
	return slot(ctx, p.checks, fn)
}

// build runs fn under a build slot; with no pool, at once.
func (p *advancePool) build(ctx context.Context, fn func()) bool {
	if p == nil {
		fn()
		return true
	}
	return slot(ctx, p.builds, fn)
}

// treeLane is what one key's pipeline in a pool runs with: its own writers, the pool, the pool's one
// interrupt scope's context, and the start line its build says at once.
type treeLane struct {
	out, errw io.Writer
	pool      *advancePool
	ctx       context.Context
	started   func()
}

// options are a's lane fields set on o.
func (l treeLane) options(o advanceOptions) advanceOptions {
	o.out, o.errw, o.pool, o.ctx, o.started = l.out, l.errw, l.pool, l.ctx, l.started
	return o
}

// runTreesInParallel runs fn for every tree at once, each on its own lane, under one interrupt scope
// of act's, and returns once every one has; the lanes' lines reach out and errw in the trees' order.
func runTreesInParallel(trees []packload.Fork, runtime string, act *run.ActInterrupt, out, errw io.Writer,
	fn func(i int, f packload.Fork, lane treeLane)) {
	if len(trees) == 0 {
		return
	}
	act.Scope(func(ctx context.Context) { runTreesUnder(ctx, trees, runtime, out, errw, fn) })
}

// runTreesUnder is runTreesInParallel under ctx, which ends every key's wait when it is cancelled:
// an interrupt scope's, or the background advance's signal context (backgroundadvance.go), which must
// not take a scope, since a scope re-raises the signal it caught.
func runTreesUnder(ctx context.Context, trees []packload.Fork, runtime string, out, errw io.Writer,
	fn func(i int, f packload.Fork, lane treeLane)) {
	if len(trees) == 0 {
		return
	}
	pool := newAdvancePool(runtime)
	ord := newOrderedOutput(out, errw, len(trees))
	var wg sync.WaitGroup
	for i, f := range trees {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lo, le := ord.writers(i)
			log := treeBuildLog(f)
			lane := treeLane{out: lo, errw: le, pool: pool, ctx: ctx}
			lane.started = func() {
				// The log opens HERE, under the build's own lock and slot, and is appended to:
				// a launch that builds nothing of the key never touches it (XB-D50).
				ord.logTo(i, log, fmt.Sprintf("=== %s: build started %s (yolo pid %d)\n", f.Label(),
					time.Now().Format(time.RFC3339), os.Getpid()))
				ord.startLine(i, fmt.Sprintf("%s: its build has started — its lines follow once every extension "+
					"listed before it has ended, and its output is in %s as it runs", f.Label(), log))
			}
			defer ord.end(i)
			fn(i, f, lane)
		}()
	}
	wg.Wait()
}

// treeBuildLog is where a key's lines are written as they come once its build starts, for a key
// whose lines the launch holds: one file per key under yolo's host log directory, APPENDED to by
// every build of the key, each opening with a header line (XB-D50). One file per key keeps it the
// one path a start line can name; appending, and opening it only once a build starts, means no
// launch truncates the log another launch's start line pointed its user at.
func treeBuildLog(f packload.Fork) string {
	return filepath.Join(paths.GlobalStorage(), "logs", "tree-build-"+run.PatchedCopySlug(f.Key())+".log")
}

// treeBuildLogRotateAt is the size past which a build's log is moved to <log>.1 before a new build
// appends to it, so the file stays bounded. A rename, never a truncation: a build still writing to
// the old file keeps writing to it under its new name.
const treeBuildLogRotateAt = 1 << 20

// orderedOutput gives each of n keys its own writers for the two streams and writes what they
// receive to the real ones in key order: the HEAD — the first key that has not ended — writes
// straight through, and every later key's writes are held, in the order they came across both
// streams, until every key before it has ended (XB-D10's "lines print in declaration order").
type orderedOutput struct {
	mu        sync.Mutex
	out, errw io.Writer
	slots     []*orderedSlot
	head      int
}

type orderedSlot struct {
	held []heldWrite
	done bool
	// log, when set, receives every write of the slot as it comes (logTo), until end closes it.
	log io.WriteCloser
}

type heldWrite struct {
	toErr bool
	p     []byte
}

func newOrderedOutput(out, errw io.Writer, n int) *orderedOutput {
	o := &orderedOutput{out: out, errw: errw, slots: make([]*orderedSlot, n)}
	for i := range o.slots {
		o.slots[i] = &orderedSlot{}
	}
	return o
}

// writers are key i's two streams.
func (o *orderedOutput) writers(i int) (io.Writer, io.Writer) {
	return orderedWriter{o: o, i: i}, orderedWriter{o: o, i: i, toErr: true}
}

type orderedWriter struct {
	o     *orderedOutput
	i     int
	toErr bool
}

func (w orderedWriter) Write(p []byte) (int, error) {
	o := w.o
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.slots[w.i]
	if s.log != nil {
		_, _ = s.log.Write(p)
	}
	if w.i == o.head {
		return o.stream(w.toErr).Write(p)
	}
	s.held = append(s.held, heldWrite{toErr: w.toErr, p: append([]byte(nil), p...)})
	return len(p), nil
}

func (o *orderedOutput) stream(toErr bool) io.Writer {
	if toErr {
		return o.errw
	}
	return o.out
}

// startLine says line for key i at once on the real error stream when i's lines are held, and as one
// of its own lines when they are not: the head's lines are live already.
func (o *orderedOutput) startLine(i int, line string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if i == o.head {
		return
	}
	fmt.Fprintln(o.errw, line)
}

// end marks key i ended, and when it was the head, flushes every key after it in turn: each key's
// held writes, and the next one not ended becomes the head, whose writes go straight through.
func (o *orderedOutput) end(i int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.slots[i].done = true
	if l := o.slots[i].log; l != nil {
		_ = l.Close()
		o.slots[i].log = nil
	}
	for o.head < len(o.slots) && o.slots[o.head].done {
		o.head++
		if o.head < len(o.slots) {
			next := o.slots[o.head]
			for _, h := range next.held {
				_, _ = o.stream(h.toErr).Write(h.p)
			}
			next.held = nil
		}
	}
}

// logTo tees key i's writes into the file at path from now on, until end, appending to it after
// header; a second call for the same key (a build of the series' base after the first) keeps the
// file already open. A file that cannot be made logs nothing: the launch's own lines are
// unaffected.
func (o *orderedOutput) logTo(i int, path, header string) {
	o.mu.Lock()
	open := o.slots[i].log != nil
	o.mu.Unlock()
	if open {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > treeBuildLogRotateAt {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = io.WriteString(f, header)
	o.mu.Lock()
	o.slots[i].log = f
	o.mu.Unlock()
}
