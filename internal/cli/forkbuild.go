package cli

// forkbuild.go is the BUILD ACT of the fork route (docs/design/forked-programs-as-packs.md §4 steps
// 3–4, FP-D1, FP-D8, FP-D9): build one fork's pinned revision in a sealed capture jail, check that
// it left what the fork declares, and admit the result into the capture store under a `build`
// receipt. It is `yolo capture <forked bin>` (the explicit rebuild) and, from step 6, a launch's
// build of a fork its jail needs.
//
// # The shape, and what is shared with `yolo capture`
//
//	the lock               per BUILD (source, revision, recipe, platform), not per bin: a build of
//	                       a fork of claude and an installer capture of claude never contend
//	a hit, after the lock  another process may have just built it (the launch path's wait, FP-D1)
//	Store.Stage            a scratch workspace inside the store, as a capture's
//	the checkout           the pinned commit, from the pack store's mirror, copied into it
//	the sealed jail        the ordinary run pipeline under the seal (run.Options.Sealed), its pack
//	                       selection narrowed to the fork and its configured base (FP-D9)
//	captureStaged          the capture act's middle, which `yolo capture` runs too: the manifest, an
//	                       empty delta refused, the admit
//	the produces check     before the admit: a build that exits 0 and leaves no program failed
//	the receipt            kind `build`, with the revision, the recipe and the toolchain
//
// THE BUILD NEVER RUNS ON THE HOST (§12): the jail is the ordinary capture jail with the seal on,
// and this file only stages its workspace and reads what it left.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// forkSourceLeaf and forkToolchainLeaf are the build workspace's source checkout and the file the
// build jail writes its image identity to — both siblings of out/, so neither is in the delta nor
// renamed away with the entry.
const (
	forkSourceLeaf    = "src"
	forkToolchainLeaf = "toolchain"
	// jailImageIdentityPath is where the image records its identity (flake.nix, imageIdentity).
	jailImageIdentityPath = "/etc/yolo-jail-image-identity"
)

// forkBuildWaitBound is how long a LAUNCH waits for another process's build of the same key before
// counting it as its own failed build (FP-D1). Longer than a source build of an agent CLI takes
// (dependencies fetched, bundled, installed), so the wait ends with the other build's result rather
// than a timeout, and bounded so a build that hangs cannot hold every later launch of its fork.
const forkBuildWaitBound = 20 * time.Minute

// forkBuild is one build: the fork, the commit it is pinned to, and the platform it is built for.
type forkBuild struct {
	Fork     packload.Fork
	Commit   string
	Platform string
	// Series is a PATCHED fork's series as its act read it, once (docs/design/patched-forks.md
	// §3.2): the bytes the build replays and whose digest its recipe carries. nil for a plain fork.
	Series *packsrc.Series
	// Entry is the walk's list entry a patched build is of (its commit, and the version tag its
	// receipt and the good build record); zero for a plain fork.
	Entry packsrc.ListEntry
}

// recipe is the build's recipe hash: its command, its outputs and the source subdirectory, and for
// a PATCHED fork the series digest too (packdecl.PatchedForkRecipe, PF-D7). A patched fork's is ""
// with no series read, as Install.SourceRecipe is (PF-D31): a plain fork of the same upstream,
// build and produces has ForkSourceRecipe's very hash, so the plain recipe here would serve that
// build — the unpatched upstream — as the patched program. "" matches no build
// (resolveForkBuild) and builds nothing (buildFork).
func (b forkBuild) recipe() string {
	if b.Fork.IsTree() {
		// A PATCHED EXTENSION's recipe is tagged as a tree's (packdecl.TreeRecipe, PPX-D6), so it
		// never equals a program's of the same inputs.
		if b.Series == nil {
			return ""
		}
		return packdecl.TreeSourceRecipe(b.Fork.Source, b.Fork.Build, b.Fork.Produces, b.Series.Digest)
	}
	if b.Fork.Patched() {
		if b.Series == nil {
			return ""
		}
		return packdecl.ForkSourcePatchedRecipe(b.Fork.Source, b.Fork.Build, b.Fork.Produces, b.Series.Digest)
	}
	return packdecl.ForkSourceRecipe(b.Fork.Source, b.Fork.Build, b.Fork.Produces)
}

// buildSource is the source the build's receipt, lock and staging name: the source as written for
// a plain fork, whose selection keys on it; for a PATCHED fork its repository and subdirectory
// alone (patchedBuildSource), since the ref is not part of a patched build's identity (§6.3) — a
// hold moved from `?ref=main` to `?ref=v1.0.1` finds the build already there.
func (b forkBuild) buildSource() string {
	if b.Fork.Patched() {
		return patchedBuildSource(b.Fork.Source)
	}
	return b.Fork.Source
}

// patchedBuildSource is a patched fork's source with no ref: `git+<repository>`, then `//<subdir>`
// for a subdirectory source. It is a record, not an address (packsrc.Parse refuses it, as it
// refuses any git source with no `?ref=`), so no plain fork's query, which is the source as written
// and always carries a ref, can equal it.
func patchedBuildSource(source string) string { return packsrc.BuildSource(source) }

// id names the build: source, revision, recipe and platform, the key §6 names, and a patched
// fork's key besides, so two forks of one upstream stage apart. The lock and the staging workspace
// are keyed on it, so two builds of one key serialize and anything else does not.
func (b forkBuild) id() string {
	if b.Fork.Patched() {
		return patchedBuildID(b.Fork.Key(), b.buildSource(), b.Commit, b.recipe(), b.Platform)
	}
	return buildID(b.buildSource(), b.Commit, b.recipe(), b.Platform)
}

// buildID is a build's id from its key's parts (forkBuild.id): source, revision, recipe, platform.
func buildID(source, commit, recipe, platform string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + commit + "\x00" + recipe + "\x00" + platform))
	return hex.EncodeToString(sum[:])[:16]
}

// patchedBuildID is a PATCHED build's id, which carries its fork key besides: read back from an
// admitted build's receipt (its fork, source, revision, recipe and platform), it names the lock that
// build was made under, which a move's reap asks about (patchedadvance.go, reapOthers).
func patchedBuildID(fork, source, commit, recipe, platform string) string {
	return buildID(fork+"\x00"+source, commit, recipe, platform)
}

// lockPath is the build's lock, beside the capture locks.
func (b forkBuild) lockPath() string { return forkBuildLockPath(b.id()) }

// forkBuildLockPath is the lock of the build whose id is id.
func forkBuildLockPath(id string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", "fork-build-"+id+".lock")
}

// buildMode is how buildFork behaves toward an existing entry and a build already running.
type buildMode struct {
	// force builds even when the store already holds this build: `yolo capture <forked bin>`, the
	// explicit rebuild (a toolchain moved, OQ-FP2).
	force bool
	// lock is how the build's lock is taken: `yolo capture` refuses on contention, a launch waits,
	// bounded (FP-D1).
	lock pidlock.Mode
	// afterLock, when non-nil, replaces the plain fork's hit check once the lock is held: a
	// PATCHED fork's waiter takes the result of the build it waited for (§6.6), a success as the
	// entry and a failure as the error, and done says to stop there.
	afterLock func() (entry *capture.Entry, err error, done bool)
	// runJail, when non-nil, runs the build jail in place of forkBuildRunJail: a launch's patched
	// advance runs it as a child process, so a Ctrl-C ends the build and not the launch
	// (forkbuildchild.go, PF-D25). s is the jail's four writers, which the act tees to keep the
	// last lines the jail printed (jailTail).
	runJail func(staging string, b forkBuild, s captureStreams) int
	// packs is the pack store a PATCHED build replays its series in (the launch's, under the
	// advance's context); nil reads the machine's with the store's default budget.
	packs *packsrc.Store
	// replaySpent is what the advance's walks have already taken of the replay's bound, which the
	// build's own replay into src/ shares (packsrc.WalkOptions.Spent, PF-D44).
	replaySpent time.Duration
	// settle, when non-nil, is run with the build's result — its entry, or the error that ended it
	// — while the build's lock is still held, and every return from the act after the lock was
	// taken goes through it: a PATCHED fork's advance records a failed build and moves the good
	// build there (§6.6, PF-D17), so a waiter that takes the lock next reads either, and a move's
	// reap sees this build's lock held until the build is in the record.
	settle func(entry *capture.Entry, err error)
}

// errForkBuildLocked is a build refused because another holds its lock.
var errForkBuildLocked = errors.New("another build of this fork is running")

// errForkBuildNotStarted marks a build jail that exited before its build line ran (forkBuildNotStarted
// is the error itself). Not a failed build (§8.1, PF-D21): a patched fork records nothing for it,
// and the candidate stays pending.
var errForkBuildNotStarted = errors.New("the build jail exited before its build line ran")

// forkBuildNotStarted is a build jail that exited before its build line ran, with the last lines it
// printed. Which of three things stopped it — the launch's own refusal at a pre-flight, a runtime
// that would not start the container, or a boot that failed — only the jail knows, and the lines it
// printed last are the ones that say (for a refusal, the refusal itself), so the error relays them
// rather than guessing (PPX-D39: the first launch with patched extensions said "once the runtime
// starts jails again" of a config refusal). It is errForkBuildNotStarted to errors.Is, and its
// jail's exit to errors.As, so `yolo capture` exits with the jail's status, as for any jail that
// failed.
type forkBuildNotStarted struct {
	exit captureJailExit
	// said is the jail's last lines (jailTail.said), "" for none.
	said string
}

func (e forkBuildNotStarted) Error() string {
	if e.said == "" {
		// Nothing came through the jail's writers, which is not to say it printed nothing: a container
		// that started writes to the terminal itself.
		return fmt.Sprintf("the build jail exited %d before its build line ran", e.exit.rc)
	}
	return fmt.Sprintf("the build jail exited %d before its build line ran, saying: %s", e.exit.rc, e.said)
}

func (e forkBuildNotStarted) Is(target error) bool { return target == errForkBuildNotStarted }
func (e forkBuildNotStarted) Unwrap() error        { return e.exit }

// captureStreams are the four writers a capture jail prints on: out and errw take the launch's own
// lines (run.Options.Stdout and Stderr), and jailOut and jailErr the JAIL'S OWN, what its runtime
// client and pid 1 print up to its ready, which the launch relays apart from its own
// (run.Options.JailStdout and JailStderr). A nil jail writer is the process's own stream, where any
// launch relays them. jailReady, when non-nil, is called once the jail's boot is done, after the
// last of its own lines (run.Options.OnJailReady).
type captureStreams struct {
	out, errw, jailOut, jailErr io.Writer
	jailReady                   func()
}

// jailSaidWhy reports whether err is a build jail that stopped before its build line ran and relays
// the lines it said why with, so the next step can point at them rather than at the output above.
func jailSaidWhy(err error) bool {
	var ns forkBuildNotStarted
	return errors.As(err, &ns) && ns.said != ""
}

// jailTail keeps the last lines a build jail prints on each of its four streams (writer,
// captureStreams), for forkBuildNotStarted to relay. A jail the runtime refused, or whose boot
// failed, says why on its OWN two streams, and the launch's lines after that are only its keeper's
// account of the end ("keeper: done"); a launch that refused at a pre-flight started no container,
// so its own two streams are all it printed on. Either says so on ONE stream, and can take a few
// lines — the config gate's refusal is its verdict, the key and `yolo check` — while the stream it
// did not use carries what the launch printed before it, such as its flake source. So what it
// relays is the last jailTailLines lines of the stream that printed last, of the jail's own two
// when either printed (said).
//
// NOTHING, ONCE THE JAIL'S BOOT IS DONE (ready). pid 1 prints lines on a boot that succeeds too —
// a shared directory linked, no cgroup delegate — and a jail that stops after it says why in its
// session, whose lines reach the terminal and not this tail; what the launch prints then is its
// keeper's teardown. So a jail that stopped after its boot is relayed with nothing, and the next
// step points at the output above.
//
// Safe for the copy goroutines a child process's pipes run. Color is stripped and a carriage return
// starts a line again, so a styled or redrawn line is kept as a terminal shows it, and a line is cut
// at jailTailMaxLine bytes, since what it keeps ends up in a delivery reason the jail is handed.
type jailTail struct {
	mu      sync.Mutex
	seq     int
	streams [tailStreams]jailTailStream
	booted  bool
}

// The streams a jailTail keeps, one writer each.
const (
	tailLaunchOut = iota // the launch's own stdout (run.Options.Stdout)
	tailLaunchErr        // the launch's own stderr (run.Options.Stderr)
	tailJailOut          // the jail's own stdout: its runtime client's and pid 1's (run.Options.JailStdout)
	tailJailErr          // the jail's own stderr (run.Options.JailStderr)
	tailStreams
)

// jailTailStream is one stream's partial line and last lines, with the order its last line came in.
type jailTailStream struct {
	cur   []byte
	lines []string
	seq   int
}

const (
	// jailTailLines is how many of a stream's last lines jailTail relays.
	jailTailLines = 3
	// jailTailMaxLine bounds one line jailTail keeps.
	jailTailMaxLine = 1024
)

// jailTailANSI matches a CSI sequence, as run's launch log strips them.
var jailTailANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// writer is the writer for stream i, one of the tail* streams.
func (t *jailTail) writer(i int) io.Writer { return jailTailWriter{t: t, i: i} }

// tee is s with every writer teed into t, a nil jail writer standing for the process's own stream:
// the writers the act hands the build jail.
func (t *jailTail) tee(s captureStreams) captureStreams {
	if s.jailOut == nil {
		s.jailOut = os.Stdout
	}
	if s.jailErr == nil {
		s.jailErr = os.Stderr
	}
	return captureStreams{
		out:       io.MultiWriter(s.out, t.writer(tailLaunchOut)),
		errw:      io.MultiWriter(s.errw, t.writer(tailLaunchErr)),
		jailOut:   io.MultiWriter(s.jailOut, t.writer(tailJailOut)),
		jailErr:   io.MultiWriter(s.jailErr, t.writer(tailJailErr)),
		jailReady: t.ready,
	}
}

// ready records that the jail's boot is done: from here said is "".
func (t *jailTail) ready() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.booted = true
}

type jailTailWriter struct {
	t *jailTail
	i int
}

func (w jailTailWriter) Write(p []byte) (int, error) {
	w.t.mu.Lock()
	defer w.t.mu.Unlock()
	s := &w.t.streams[w.i]
	for _, c := range p {
		switch c {
		case '\n':
			w.t.endLine(s)
		case '\r':
			s.cur = s.cur[:0]
		default:
			if len(s.cur) < jailTailMaxLine*2 {
				s.cur = append(s.cur, c)
			}
		}
	}
	return len(p), nil
}

// endLine keeps s's partial line as its newest when it holds anything but space.
func (t *jailTail) endLine(s *jailTailStream) {
	line := strings.TrimSpace(jailTailANSI.ReplaceAllString(string(s.cur), ""))
	s.cur = s.cur[:0]
	if line == "" {
		return
	}
	if len(line) > jailTailMaxLine {
		line = strings.ToValidUTF8(line[:jailTailMaxLine], "") + "…"
	}
	if s.lines = append(s.lines, line); len(s.lines) > jailTailLines {
		s.lines = s.lines[len(s.lines)-jailTailLines:]
	}
	t.seq++
	s.seq = t.seq
}

// said is the last lines of the stream that printed last, an unterminated line included, joined by
// " / " so they read as one line: of the jail's own two streams when either printed, and of the
// launch's two otherwise; "" when none printed anything, or the jail's boot was done.
func (t *jailTail) said() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.booted {
		return ""
	}
	for i := range t.streams {
		if len(t.streams[i].cur) > 0 {
			t.endLine(&t.streams[i])
		}
	}
	for _, pair := range [][2]int{{tailJailOut, tailJailErr}, {tailLaunchOut, tailLaunchErr}} {
		last := &t.streams[pair[0]]
		if t.streams[pair[1]].seq > last.seq {
			last = &t.streams[pair[1]]
		}
		if last.seq > 0 {
			return strings.Join(last.lines, " / ")
		}
	}
	return ""
}

// forkSourceError is a build that never reached its jail because its source could not be put in
// place: a checkout that failed, or for a patched fork a replay that failed. Nothing about the
// build line is known, so a patched fork records nothing for it (an apply error, §5.2).
type forkSourceError struct{ err error }

func (e forkSourceError) Error() string { return e.err.Error() }
func (e forkSourceError) Unwrap() error { return e.err }

// forkLockTimeout is a launch that waited forkBuildWaitBound for another build of the same key.
type forkLockTimeout struct{ msg string }

func (e forkLockTimeout) Error() string { return e.msg }
func (e forkLockTimeout) Unwrap() error { return pidlock.ErrTimedOut }

// buildFork builds b in a sealed capture jail and returns the admitted entry. A hit returns the
// existing entry and builds nothing, unless the mode forces a build. It says what it does on out,
// and every failure is returned, never fatal to the caller's process.
func buildFork(b forkBuild, mode buildMode, out, errw io.Writer, color bool) (*capture.Entry, error) {
	pr := richtext.Printer{W: out, Color: color}
	store := &capture.Store{Dir: paths.CapturesDir()}
	f := b.Fork
	if b.recipe() == "" {
		// A PATCHED FORK WITH NO SERIES READ, or anything else with no recipe: a build here would
		// be the upstream unpatched, admitted under a recipe no reader asks for.
		return nil, fmt.Errorf("fork %s is a patched fork, and its build was asked for with no series read", f.Key())
	}
	lk, err := pidlock.Acquire(b.lockPath(), mode.lock, func(pid int) {
		pr.Printf("[dim]waiting for pid %d, which is building %s at %s (at most %s)[/dim]",
			pid, f.Bin, shortSHA(b.Commit), forkBuildWaitBound)
	})
	switch {
	case errors.Is(err, pidlock.ErrHeld):
		return nil, fmt.Errorf("%w (%s at %s; lock: %s)", errForkBuildLocked, f.Key(), shortSHA(b.Commit), b.lockPath())
	case errors.Is(err, pidlock.ErrTimedOut):
		return nil, forkLockTimeout{fmt.Sprintf("waited %s for another build of %s at %s, and it has not finished "+
			"(pid %d holds its lock, %s)", forkBuildWaitBound, f.Key(), shortSHA(b.Commit), pidlock.Holder(b.lockPath()),
			b.lockPath())}
	case err != nil:
		return nil, err
	}
	defer lk.Release()
	entry, err := buildForkUnderLock(b, mode, store, pr, out, errw, color)
	if mode.settle != nil {
		mode.settle(entry, err)
	}
	return entry, err
}

// buildForkUnderLock is buildFork's act once the build's lock is held: the hit after the lock, the
// staged workspace and its source, the sealed jail, the admit and the receipt. The workspace is
// cleaned up when it returns, before the caller's settle.
func buildForkUnderLock(b forkBuild, mode buildMode, store *capture.Store, pr richtext.Printer, out, errw io.Writer,
	color bool) (*capture.Entry, error) {
	f := b.Fork
	// A HIT AFTER THE LOCK: a launch that waited finds the entry the winner just admitted and uses
	// it, as the host floor re-checks after its wait, rather than building the same bytes twice. A
	// patched fork's waiter takes the winner's result, a failure included (afterLock, §6.6).
	switch {
	case mode.afterLock != nil:
		if entry, err, done := mode.afterLock(); done {
			return entry, err
		}
	case !mode.force:
		if entry, _, err := resolveForkBuild(store, f.Bin, b.Platform, f.Source, b.Commit, b.recipe()); err == nil {
			return entry, nil
		}
	}
	staging, err := store.Stage("fork-" + b.id())
	if err != nil {
		return nil, err
	}
	cname := runtime.FromWorkspace(staging)
	defer cleanupCaptureWorkspace(staging, cname)
	src := filepath.Join(staging, forkSourceLeaf)
	tree := ""
	if b.Series != nil {
		// A PATCHED FORK: the series replayed onto the commit on the host, in a scratch repository
		// outside this workspace, and the patched subdirectory copied into src/ (§5.1).
		if tree, err = replayIntoSource(mode.packs, b, src, mode.replaySpent); err != nil {
			return nil, forkSourceError{fmt.Errorf("replaying the series onto %s: %w", b.Entry.Label(), err)}
		}
		pr.Printf("[bold]build[/bold] [cyan]%s[/cyan]  [dim]%s at %s + %d %s (series %s), in a sealed jail[/dim]",
			f.Key(), patchedBuildSource(f.Source), b.Entry.Label(), b.Series.Len(),
			plural(b.Series.Len(), "patch", "patches"), b.Series.ShortDigest())
	} else {
		if err := checkOutForkSource(f.Source, b.Commit, src); err != nil {
			return nil, forkSourceError{fmt.Errorf("checking out %s at %s: %w", f.Source, shortSHA(b.Commit), err)}
		}
		pr.Printf("[bold]build[/bold] [cyan]%s[/cyan]  [dim]%s at %s, in a sealed jail[/dim]", f.Key(), f.Source, b.Commit)
	}
	runJail := mode.runJail
	if runJail == nil {
		runJail = func(staging string, b forkBuild, s captureStreams) int {
			return forkBuildRunJail(staging, b, s, color)
		}
	}
	// THE JAIL'S LAST LINES, kept as they stream past, the launch's and the jail's own apart: a jail
	// that stops before its build line ran is relayed with them (forkBuildNotStarted). The jail's own
	// go where any launch relays them, the process's own streams.
	tail := &jailTail{}
	streams := tail.tee(captureStreams{out: out, errw: errw})
	entry, m, err := captureStaged(store, staging,
		func() int { return runJail(staging, b, streams) },
		func(m *capture.Manifest) string {
			if f.IsTree() {
				return fmt.Sprintf("%s's build left no tree at ~/%s", f.Label(), packdecl.TreeReservedDir(f.Bin))
			}
			return fmt.Sprintf("%s's build left nothing in the program surfaces (%s)", f.Key(),
				strings.Join(m.Surfaces, ", "))
		},
		func(m *capture.Manifest) string {
			if f.IsTree() {
				return treeAdmitProblem(m, f)
			}
			if why := missingProduces(m, f.Produces); why != "" {
				return why
			}
			return linksIntoTheBuild(m)
		})
	toolchain, terr := os.ReadFile(filepath.Join(staging, forkToolchainLeaf))
	var exit captureJailExit
	if err != nil && errors.As(err, &exit) && terr != nil {
		// THE BUILD LINE NEVER RAN: the jail's script writes the toolchain record first, so a jail
		// that exited without one stopped before it — a launch pre-flight that refused, a runtime
		// that would not start it, or a boot that failed — and never a build that failed, which a
		// PATCHED fork's record keeps apart (§8.1, PF-D21). Which it was, the jail said last.
		return nil, forkBuildNotStarted{exit: exit, said: tail.said()}
	}
	if err != nil {
		return nil, err
	}
	receipt := entrypoint.BuildReceipt{
		Bin: f.Bin, Source: b.buildSource(), Key: entry.Key, Digest: capture.DigestHash(entry.Digest),
		Bytes: m.TotalBytes(), Path: entry.Root, Platform: m.Platform, Revision: b.Commit,
		Recipe: b.recipe(), Toolchain: strings.TrimSpace(string(toolchain)),
		Act: entrypoint.ReceiptActRecord, Time: time.Now(),
	}
	if b.Series != nil {
		receipt.Fork, receipt.Series, receipt.Tree = f.Key(), b.Series.Digest, tree
		receipt.Tag, receipt.Version = b.Entry.Tag, b.Entry.Version
	}
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), receipt.Line()); err != nil {
		return nil, fmt.Errorf("writing the build receipt: %w", err)
	}
	pr.Printf("[green]built[/green] %s  [cyan]%s[/cyan]  %d paths, %s  [dim]%s[/dim]",
		f.Key(), entry.Key, len(m.Entries), humanBytes(m.TotalBytes()), entry.Root)
	return entry, nil
}

// buildForksForLaunch is run.Options.BuildForks: the launch's builds (OQ-FP4). For each pinned fork
// it answers with the store key of its build at the pin — a hit when the store holds it, a build
// in a sealed jail when it does not — or with why there is none. It waits, bounded, for a build of
// the same key another launch is running, and then uses that build (FP-D1). It never fails the
// launch: a failed build is its fork's reason, printed by the fork's launcher in the jail (§9).
//
// A PATCHED fork gets its ADVANCE instead (patchedadvance.go; docs/design/patched-forks.md §6,
// §7): its upstream checked, its series replayed and the newest fit built, the good build moved
// once that build is admitted, and the good build handed, which the request's Hand records.
func buildForksForLaunch(req run.ForkBuildRequest, out, errw io.Writer, color bool) map[string]entrypoint.ForkDelivery {
	pr := richtext.Printer{W: out, Color: color}
	store := &capture.Store{Dir: paths.CapturesDir()}
	got := map[string]entrypoint.ForkDelivery{}
	platform := req.Platform
	var plain []packload.ForkPin
	for _, p := range req.Pins {
		if !p.Fork.Patched() {
			plain = append(plain, p)
			continue
		}
		got[p.Fork.Bin] = advancePatchedFork(p.Fork, advanceOptions{platform: platform, runtime: req.Runtime,
			workspace: req.Workspace, out: out, errw: errw, color: color, launch: true, hand: req.Hand,
			act: req.Interrupt}).delivery
	}
	var missing []forkBuild
	for _, p := range plain {
		b := forkBuild{Fork: p.Fork, Commit: p.Commit, Platform: platform}
		if entry, _, err := resolveForkBuild(store, p.Fork.Bin, platform, p.Fork.Source, p.Commit, b.recipe()); err == nil {
			got[p.Fork.Bin] = entrypoint.ForkDelivery{Key: entry.Key}
			continue
		}
		missing = append(missing, b)
	}
	if len(missing) == 0 {
		return got
	}
	if req.Interrupt.Interrupted() {
		// A CTRL-C ENDED THIS LAUNCH'S WAIT in a patched fork's advance above (PF-D57): no build is
		// begun, since each would be a new wait the user had just declined.
		for _, b := range missing {
			fmt.Fprintf(errw, "Warning: %s was not built at %s — a Ctrl-C ended this launch's wait for its fork "+
				"builds, and this launch continues without %s. The next launch builds it.\n", b.Fork.Key(),
				shortSHA(b.Commit), b.Fork.Bin)
			got[b.Fork.Bin] = entrypoint.ForkDelivery{Reason: "fork " + b.Fork.Pack + "'s build of commit " +
				shortSHA(b.Commit) + " was not started: a Ctrl-C ended the launch's wait for its fork builds — " +
				"the next launch builds it"}
		}
		return got
	}
	// THE COST IS STATED WHERE IT IS PAID, as auto-capture states its: a source build fetches its
	// dependencies and compiles, once per commit per machine.
	pr.Printf("[bold]fork builds[/bold]  %d %s never built at %s on this machine",
		len(missing), plural(len(missing), "fork", "forks"), plural(len(missing), "its pin", "their pins"))
	pr.Printf("[dim]  Each is built once now, from its pinned commit, in a sealed jail of its own " +
		"that gets no credential and no host file; every later launch materializes it.[/dim]")
	for i, b := range missing {
		pr.Printf("[dim]  [%d/%d][/dim] %s at %s", i+1, len(missing), b.Fork.Key(), shortSHA(b.Commit))
		entry, err := buildFork(b, buildMode{lock: pidlock.Mode{Wait: true, Bound: forkBuildWaitBound}}, out, errw, color)
		if err != nil {
			fmt.Fprintf(errw, "Warning: could not build %s (%v) — nothing was stored, and this launch "+
				"continues without %s. The next launch builds it again.\n", b.Fork.Key(), err, b.Fork.Bin)
			got[b.Fork.Bin] = entrypoint.ForkDelivery{Reason: "fork " + b.Fork.Pack + "'s build of commit " +
				shortSHA(b.Commit) + " failed on the host (" + err.Error() + ")"}
			continue
		}
		got[b.Fork.Bin] = entrypoint.ForkDelivery{Key: entry.Key}
	}
	return got
}

// missingProduces names the `produces` paths the build's result lacks, or "" when it has them all.
// A build that exits 0 without its program is a FAILED build (§9), and admitting it would file an
// entry that materializes no program and satisfies every later lookup.
func missingProduces(m *capture.Manifest, produces []string) string {
	have := map[string]bool{}
	for _, e := range m.Entries {
		have[e.Path] = true
	}
	var missing []string
	for _, p := range produces {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "the build exited 0 but left none of " + strings.Join(missing, ", ") +
		" — the paths its pack declares under `produces`"
}

// linksIntoTheBuild names a link the build left pointing into its OWN WORKSPACE — the checkout it
// ran in, which is deleted when the build ends — or "" when there is none. Admitted, such a link is
// dangling in every jail that materializes the entry: the program is "there" and cannot start.
//
// `npm install -g .` is the common cause, and the reason this is a refusal rather than a lint:
// npm installs a FOLDER as a link to it, so the most natural build line for a Node fork produces
// exactly this, and the produces check alone passes it (the path exists — as a link). npm writes
// that link RELATIVE, so a relative target is resolved from where the link sat in the build jail's
// home (the manifest's Home) before it is compared.
func linksIntoTheBuild(m *capture.Manifest) string {
	for _, e := range m.Entries {
		if e.Kind != capture.KindSymlink {
			continue
		}
		resolved := e.Target
		if !path.IsAbs(resolved) {
			resolved = path.Join(m.Home, path.Dir(e.Path), resolved)
		}
		if resolved == containerWorkspace || strings.HasPrefix(resolved, containerWorkspace+"/") {
			return fmt.Sprintf("the build left %s as a link into its own workspace (%s), which is "+
				"deleted when the build ends — install a copy instead (for an npm package, "+
				"`npm install -g \"$(npm pack --silent)\"` rather than `npm install -g .`)", e.Path, e.Target)
		}
	}
	return ""
}

// captureStaged is THE CAPTURE ACT'S MIDDLE, shared by `yolo capture` of an installer and a fork's
// build: run the jail in the staged workspace, read the manifest it left beside the tree, refuse a
// failed jail, an empty delta and whatever check adds, and admit the entry. empty and check return
// the refusal's text, check "" for none.
func captureStaged(store *capture.Store, staging string, runJail func() int,
	empty func(*capture.Manifest) string, check func(*capture.Manifest) string) (*capture.Entry, *capture.Manifest, error) {
	if rc := runJail(); rc != 0 {
		return nil, nil, captureJailExit{rc: rc}
	}
	outDir := filepath.Join(staging, captureOutLeaf)
	m, err := capture.ReadManifest(outDir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the capture manifest: %w", err)
	}
	if len(m.Entries) == 0 {
		return nil, nil, fmt.Errorf("%s — nothing was stored", empty(m))
	}
	if check != nil {
		if why := check(m); why != "" {
			return nil, nil, fmt.Errorf("%s — nothing was stored", why)
		}
	}
	entry, err := store.AdmitEntry(outDir)
	if err != nil {
		return nil, nil, err
	}
	return entry, m, nil
}

// captureJailExit is a capture jail that exited non-zero, carrying its status so `yolo capture`
// exits with it, as it did before the act's middle was shared.
type captureJailExit struct{ rc int }

func (e captureJailExit) Error() string {
	return fmt.Sprintf("the capture jail exited %d — nothing was stored", e.rc)
}

// checkOutForkSource copies the pinned commit of source into dst: through the pack store, which
// checks the commit out once into a tree nothing edits again, then a copy, because that tree is
// shared and a build writes into its source directory.
//
// A COMMIT THIS MACHINE'S PACK STORE DOES NOT HOLD is fetched first, by the commit itself
// (forkFetchCommit; FP-D18): a fork lock that arrived with the config from the machine that pinned
// it names a commit this one never fetched, and before, every build here failed its checkout until a
// `yolo pack install`. The fetch moves no pin and no tag.
//
// THE COPY NEVER FOLLOWS A LINK: a symlink in the fork's repository is copied as a link, so it
// resolves inside the build jail, never on the host that copies it.
func checkOutForkSource(source, commit, dst string) error {
	a, err := packsrc.Parse(source)
	if err != nil {
		return err
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	res, err := forkCheckout(store, a, commit)
	if err != nil {
		if ferr := forkFetchCommit(a, commit); ferr != nil {
			return fmt.Errorf("%w (fetching it: %v)", err, ferr)
		}
		if res, err = forkCheckout(store, a, commit); err != nil {
			return err
		}
	}
	return copySourceTree(res, dst)
}

// forkFetchCommit fetches commit of a into the pack store through the launch's store (its fetch
// budget, and no controlling terminal for git), since a build is a launch's act. A package var so a
// test can stand in for the network.
var forkFetchCommit = func(a packsrc.Addr, commit string) error {
	return packsrc.LaunchStore(paths.PacksDir()).FetchForkCommit(a, commit, nil)
}

// forkCheckout materializes a's commit and returns the directory to copy from. A package var so a
// test can stand a fixture tree in for the pack store.
var forkCheckout = func(store *packsrc.Store, a packsrc.Addr, commit string) (string, error) {
	r, err := store.Materialize(a, commit)
	if err != nil {
		return "", err
	}
	return r.Root, nil
}

// copySourceTree copies src to dst: directories, regular files with their permission bits, and
// symlinks as links. The pack store's completion marker at the tree's root is not the fork's.
func copySourceTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == ".yolo-pack-complete" {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm()|0o600)
		}
		return nil // a device, socket or pipe in a repository is not source
	})
}

// forkBuildJailArgv is the build jail's command: capture-run around the fork's build command line,
// with capture's --out, --surface-root and full reference scan, as captureJailArgv has them. The
// build is ONE command line (packdecl), run by bash in the checkout, after the image's identity is
// written beside out/ — the toolchain record (OQ-FP2), which never goes in the manifest.
func forkBuildJailArgv(build string) []string {
	script := "cat " + jailImageIdentityPath + " > " +
		shquote.Quote(path.Join(containerWorkspace, forkToolchainLeaf)) + " 2>/dev/null || true\n" +
		"cd " + shquote.Quote(path.Join(containerWorkspace, forkSourceLeaf)) + " && " + build
	return []string{
		"yolo", "internal", "capture-run",
		"--out=" + path.Join(containerWorkspace, captureOutLeaf),
		"--surface-root=" + paths.WorkspaceHomeState(containerWorkspace),
		"--scan-content-refs",
		"--", "env", "YOLO_BYPASS_SHIMS=1", "bash", "-c", script,
	}
}

// forkBuildRunJail runs b's build in the capture jail UNDER THE SEAL, with the pack selection
// narrowed to the fork and its base (FP-D9) — or, for a PATCHED EXTENSION, to the contributing pack
// alone (PPX-D5): its toolchain is the image's, so no agent pack has anything to add to it.
func forkBuildRunJail(workspace string, b forkBuild, s captureStreams, color bool) int {
	return runCaptureJail(workspace, b.Fork.Bin, buildJailArgv(b.Fork), &captureSeal{only: sealPacks(b.Fork), tree: sealTree(b.Fork)},
		s, color)
}

// sealPacks are the packs a build jail's selection is narrowed to: a fork and its base, or a
// patched extension's contributing pack — and the base of every other fork that pack declares
// (Fork.PackBases), without which the narrowed selection is refused for a fork whose base it lacks
// (PPX-D39). Every other gate that asks whether another pack provides what one names is skipped for
// a narrowed selection (run's selectionNarrowed), so nothing more is carried for those.
func sealPacks(f packload.Fork) []string {
	out := []string{f.Pack}
	if !f.IsTree() {
		out = append(out, f.Base)
	}
	for _, b := range f.PackBases {
		if !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	return out
}

// sealTree is the name a patched extension's build jail is told it builds, "" for a fork's.
func sealTree(f packload.Fork) string {
	if f.IsTree() {
		return f.Bin
	}
	return ""
}

// buildJailArgv is f's build jail command: a fork's (forkBuildJailArgv) or a tree's (treeBuildJailArgv).
func buildJailArgv(f packload.Fork) []string {
	if f.IsTree() {
		return treeBuildJailArgv(f.Build, f.Bin)
	}
	return forkBuildJailArgv(f.Build)
}

// treeBuildJailArgv is a PATCHED EXTENSION's build jail command (docs/design/patched-extensions.md
// §7.1, PPX-D5): capture-run, as a fork's, around a script that writes the toolchain record first —
// the image's identity and the version of the image's own node and npm, which the build runs with
// (PF-D39 reads the record's absence as a jail that never ran its build line) — then runs the
// optional build line in the checkout with the image's /bin first on PATH, and ends in ONE FIXED
// STEP: the checkout, links kept as links, copied into the reserved directory under ~/.local
// (packdecl.TreeReservedDir), which the capture driver already walks.
//
// The build line runs in a subshell, so a `cd` inside it cannot move the copy's source, and on lines
// of its own inside it: a build line ending in a `# comment`, which a fork's build takes, would
// otherwise comment out the subshell's close and the final copy, and bash would refuse the script.
// A build line holds no newline (packdecl refuses one), so it cannot close the subshell early.
func treeBuildJailArgv(build, name string) []string {
	src := path.Join(containerWorkspace, forkSourceLeaf)
	toolchain := shquote.Quote(path.Join(containerWorkspace, forkToolchainLeaf))
	script := "{ cat " + jailImageIdentityPath + " 2>/dev/null; printf ' node %s npm %s' " +
		"\"$(/bin/node --version 2>/dev/null)\" \"$(/bin/npm --version 2>/dev/null)\"; } > " + toolchain + " || true\n" +
		"export PATH=/bin:/usr/bin:\"$PATH\"\n" +
		"cd " + shquote.Quote(src)
	if strings.TrimSpace(build) != "" {
		script += " && (\n" + build + "\n)"
	}
	script += " && mkdir -p \"$HOME\"/" + shquote.Quote(packdecl.TreeReservedRoot) +
		" && cp -a " + shquote.Quote(src) + " \"$HOME\"/" + shquote.Quote(packdecl.TreeReservedDir(name))
	return []string{
		"yolo", "internal", "capture-run",
		"--out=" + path.Join(containerWorkspace, captureOutLeaf),
		"--surface-root=" + paths.WorkspaceHomeState(containerWorkspace),
		"--scan-content-refs",
		"--", "env", "YOLO_BYPASS_SHIMS=1", "bash", "-c", script,
	}
}

// treeAdmitProblem is a PATCHED EXTENSION's admit (docs/design/patched-extensions.md §7.1, PPX-D5),
// "" when the build may be admitted. Beside the empty-delta refusal captureStaged makes, and the link
// check every build gets (linksIntoTheBuild), three checks, each one a failed build:
//
//   - nothing in the delta lies outside the reserved directory: the tree would not carry it;
//   - every `produces` path exists in the tree, joined onto the reserved directory, since a tree's
//     produces are tree-relative where a program's are home-relative;
//   - the full content scan finds no reference to the build jail's home: the tree lands at another
//     path in every jail and at the host, so the relocation a program gets cannot rescue it (it
//     rewrites one home prefix into another, and a tree also moves within the home).
func treeAdmitProblem(m *capture.Manifest, f packload.Fork) string {
	reserved := packdecl.TreeReservedDir(f.Bin)
	have := map[string]bool{}
	var strays []string
	for _, e := range m.Entries {
		have[e.Path] = true
		if e.Path == reserved || strings.HasPrefix(e.Path, reserved+"/") || strings.HasPrefix(reserved, e.Path+"/") {
			continue
		}
		strays = append(strays, e.Path)
	}
	if len(strays) > 0 {
		return fmt.Sprintf("the build left %d %s outside the tree it delivers (%s), which the tree would "+
			"not carry — point the build line's tools at the checkout (a cache or a store under ./), "+
			"so nothing of the build lands in the home", len(strays), plural(len(strays), "path", "paths"),
			strings.Join(sampleOf(strays, 3), ", "))
	}
	if !have[reserved] {
		return fmt.Sprintf("the build left no tree at ~/%s", reserved)
	}
	var missing []string
	for _, p := range f.Produces {
		if !have[reserved+"/"+p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return "the build exited 0 but its tree has none of " + strings.Join(missing, ", ") +
			" — the paths its pack declares under `produces`, relative to the tree"
	}
	var refs []string
	seen := map[string]bool{}
	for _, r := range m.AbsoluteRefs {
		rel := strings.TrimPrefix(strings.TrimPrefix(r.Path, reserved), "/")
		if !seen[rel] {
			seen[rel] = true
			refs = append(refs, rel)
		}
	}
	refs = append(refs, m.NotRelocatable...)
	if len(refs) > 0 || m.RefScan != capture.RefScanFull {
		if m.RefScan != capture.RefScanFull {
			refs = append(refs, "(the full content scan did not run)")
		}
		return fmt.Sprintf("the tree names the build jail's home %s in %s — the tree lands at another "+
			"path in every jail and at the host, where such a reference breaks; have the build leave "+
			"relative paths", m.Home, strings.Join(sampleOf(refs, 3), ", "))
	}
	return linksIntoTheBuild(m)
}

// captureSeal is what makes a capture jail a fork build's: the seal, and the packs entries the
// selection is narrowed to — and, for a patched extension's build, the extension's name (tree),
// which the jail is told (run.Options.SealedTree). nil for `yolo capture` of an installer, which
// keeps today's jail.
type captureSeal struct {
	only []string
	tree string
}
