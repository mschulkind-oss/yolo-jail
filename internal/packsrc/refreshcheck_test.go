package packsrc

// refreshcheck_test.go pins the LAUNCH'S PACK-CHECK PHASE (docs/design/local-pack-clones.md §4.2-§4.3,
// LPC-D3/LPC-D4): mirrors are checked at the same time, under one deadline that covers lock wait, probe
// and fetch; a check that does not finish runs the held copy with the design's warning; a pack with no
// held copy keeps its long first fetch. A git shim blocks each ls-remote on a FIFO the test controls, so
// "in flight at once" and "the deadline fired" are events the test waits on, never elapsed time.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// probeGate is a git whose ls-remote announces itself on started, then waits for one line on release,
// then runs the real git. Every other subcommand runs the real git at once. Both ends are FIFOs the
// test holds open read-write, so neither blocks the test and neither reports end-of-file.
type probeGate struct {
	git     string
	started *bufio.Reader
	release *os.File
}

func newProbeGate(t *testing.T) *probeGate {
	t.Helper()
	dir := t.TempDir()
	mk := func(name string) (string, *os.File) {
		p := filepath.Join(dir, name)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return p, f
	}
	startedPath, startedF := mk("started")
	releasePath, releaseF := mk("release")
	script := "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = ls-remote ]; then\n" +
		"echo started >> " + shquote.Quote(startedPath) + "\nread _ < " + shquote.Quote(releasePath) + "\nbreak\nfi; done\n" +
		"exec " + shquote.Quote(storeGit()) + " \"$@\"\n"
	git := filepath.Join(dir, "git")
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &probeGate{git: git, started: bufio.NewReader(startedF), release: releaseF}
}

// awaitStarted blocks until n more probes have announced themselves. The minute is only a failure guard
// that turns a hang (a check that never started its probes together) into a red test.
func (b *probeGate) awaitStarted(t *testing.T, n int) {
	t.Helper()
	got := make(chan error, 1)
	go func() {
		for i := 0; i < n; i++ {
			if _, err := b.started.ReadString('\n'); err != nil {
				got <- err
				return
			}
		}
		got <- nil
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Minute):
		t.Errorf("fewer than %d probes were in flight at once", n)
	}
}

func (b *probeGate) releaseN(t *testing.T, n int) {
	t.Helper()
	if _, err := b.release.WriteString(strings.Repeat("go\n", n)); err != nil {
		t.Fatal(err)
	}
}

// heldPacks makes n remotes, each its own mirror, and holds a copy of each through the fixture's plain store.
func heldPacks(t *testing.T, f *refreshFixture, n int) []RefreshPack {
	t.Helper()
	var packs []RefreshPack
	for i := 0; i < n; i++ {
		repo := gitRepo(t, map[string]string{"pack.json": fmt.Sprintf(`{"name":"p%d"}`, i)})
		packs = append(packs, RefreshPack{Name: fmt.Sprintf("p%d", i), Source: "git+file://" + repo + "?ref=main"})
	}
	f.refresh(t, false, packs...)
	return packs
}

func checkOf(ctx context.Context) *PackCheck { return &PackCheck{Ctx: ctx, Limit: PackCheckDeadline} }

// heldCommit is the commit the lockfile recorded for name.
func heldCommit(t *testing.T, f *refreshFixture, name string) string {
	t.Helper()
	lk, err := LoadLock(f.lock)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lk.Get(name)
	if !ok {
		t.Fatalf("no lock entry for %s", name)
	}
	return e.Commit
}

// N MIRRORS ARE PROBED AT ONCE: every probe is in flight before any is released, which a sequential
// loop cannot do (it would block on the first), and each then answers unmoved with nothing said.
func TestLaunchCheckProbesEveryMirrorAtTheSameTime(t *testing.T) {
	const n = 4
	f := newRefreshFixture(t)
	packs := heldPacks(t, f, n)
	bg := newProbeGate(t)
	f.store.Git = bg.git

	type result struct {
		outs []Outcome
		err  error
	}
	done := make(chan result, 1)
	go func() {
		outs, err := f.store.Refresh(packs, RefreshOptions{LockPath: f.lock, Check: checkOf(context.Background())})
		done <- result{outs, err}
	}()
	bg.awaitStarted(t, n) // all n probes announced while none has been released
	bg.releaseN(t, n)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, o := range r.outs {
		if o.Err != nil || o.FetchErr != nil || o.Fetched || o.Commit == "" || o.Disclosure() != "" || o.Warning() != "" {
			t.Errorf("unmoved pack %s: %+v, want the held copy and nothing said", o.Name, o)
		}
	}
}

// A PROBE THAT HAS NOT ANSWERED WHEN THE DEADLINE FIRES RUNS THE HELD COPY, never refuses, and warns
// once with the design's wording and its next step.
func TestLaunchCheckDeadlineRunsTheHeldCopyAndWarns(t *testing.T) {
	f := newRefreshFixture(t)
	packs := heldPacks(t, f, 1)
	held := heldCommit(t, f, "p0")
	bg := newProbeGate(t)
	f.store.Git = bg.git

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { bg.awaitStarted(t, 1); cancel() }() // the deadline fires once the probe is hung
	outs, err := f.store.Refresh(packs, RefreshOptions{LockPath: f.lock, Check: checkOf(ctx)})
	if err != nil {
		t.Fatal(err)
	}
	o := outs[0]
	if o.Err != nil || o.Commit != held || o.Fetched || o.FetchErr == nil {
		t.Fatalf("hung probe: %+v, want the held %s with the failure recorded", o, held)
	}
	want := "pack p0: could not check origin main (ls-remote timed out after 5s) — running the held " +
		held[:8] + ". Nothing to do until the network is back; `yolo pack update` checks on demand."
	if got := o.Warning(); got != want {
		t.Errorf("warning:\n got %q\nwant %q", got, want)
	}
}

// A MIRROR LOCKED PAST THE DEADLINE RUNS THE HELD COPY TOO: the wait is under the same deadline.
func TestLaunchCheckLockWaitIsUnderTheDeadline(t *testing.T) {
	f := newRefreshFixture(t)
	packs := heldPacks(t, f, 1)
	a, err := Parse(packs[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := f.store.lockMirror(a.Repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outs, err := f.store.Refresh(packs, RefreshOptions{
		LockPath: f.lock, Check: checkOf(ctx),
		Waiting: func(string) { cancel() }, // the deadline fires once the check is really waiting
	})
	if err != nil {
		t.Fatal(err)
	}
	o := outs[0]
	if o.Err != nil || o.Commit == "" || o.FetchErr == nil || !strings.Contains(o.Warning(), "waiting for the mirror lock timed out after 5s") {
		t.Fatalf("locked mirror: %+v, warning %q", o, o.Warning())
	}
}

// A PACK WITH NO HELD COPY KEEPS ITS LONG FIRST FETCH: the phase's deadline has already fired and the
// pack is fetched all the same, while a held pack beside it, whose probe cannot run, runs its held copy.
func TestLaunchCheckNeverFetchedPackKeepsItsOwnBudget(t *testing.T) {
	f := newRefreshFixture(t)
	packs := heldPacks(t, f, 1)
	fresh := gitRepo(t, map[string]string{"pack.json": `{"name":"fresh"}`})
	both := append(packs, RefreshPack{Name: "fresh", Source: "git+file://" + fresh + "?ref=main"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the phase's deadline has fired before anything starts
	outs, err := f.store.Refresh(both, RefreshOptions{LockPath: f.lock, Check: checkOf(ctx)})
	if err != nil {
		t.Fatal(err)
	}
	if o := outs[1]; o.Err != nil || o.FetchErr != nil || !o.Fetched || o.Commit == "" || !strings.HasPrefix(o.Disclosure(), "Fetched pack fresh") {
		t.Errorf("never-fetched pack: %+v, want its first fetch to run", o)
	}
	if o := outs[0]; o.Err != nil || o.Commit == "" || o.FetchErr == nil {
		t.Errorf("held pack: %+v, want the held copy and the failure recorded", o)
	}
}

// Only a launch check carries the §4.3 text; an explicit act keeps its own wording.
func TestExplicitRefreshKeepsItsOwnWarning(t *testing.T) {
	o := Outcome{Name: "p", Ref: "main", Commit: strings.Repeat("a", 40), FetchErr: fmt.Errorf("boom")}
	if w := o.Warning(); !strings.Contains(w, "using the cached") {
		t.Errorf("warning %q", w)
	}
}
