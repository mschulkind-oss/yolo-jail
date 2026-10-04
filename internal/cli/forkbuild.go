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
	"strings"
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
func patchedBuildSource(source string) string {
	a, err := packsrc.Parse(source)
	if err != nil {
		return source
	}
	out := "git+" + a.Repo
	if a.Path != "" {
		out += "//" + a.Path
	}
	return out
}

// id names the build: source, revision, recipe and platform, the key §6 names, and a patched
// fork's key besides, so two forks of one upstream stage apart. The lock and the staging workspace
// are keyed on it, so two builds of one key serialize and anything else does not.
func (b forkBuild) id() string {
	key := b.buildSource()
	if b.Fork.Patched() {
		key = b.Fork.Key() + "\x00" + key
	}
	sum := sha256.Sum256([]byte(key + "\x00" + b.Commit + "\x00" + b.recipe() + "\x00" + b.Platform))
	return hex.EncodeToString(sum[:])[:16]
}

// lockPath is the build's lock, beside the capture locks.
func (b forkBuild) lockPath() string {
	return filepath.Join(paths.GlobalStorage(), "locks", "fork-build-"+b.id()+".lock")
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
	// (forkbuildchild.go, PF-D25).
	runJail func(staging string, b forkBuild) int
	// packs is the pack store a PATCHED build replays its series in (the launch's, under the
	// advance's context); nil reads the machine's with the store's default budget.
	packs *packsrc.Store
}

// errForkBuildLocked is a build refused because another holds its lock.
var errForkBuildLocked = errors.New("another build of this fork is running")

// errForkBuildNotStarted marks a build jail that exited before its build line ran: the runtime
// would not start it, or its boot failed. Not a failed build (§8.1, PF-D21): a patched fork records
// nothing for it, and the candidate stays pending.
var errForkBuildNotStarted = errors.New("the build jail never ran the build line")

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
		if tree, err = replayIntoSource(mode.packs, b, src); err != nil {
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
		runJail = func(staging string, b forkBuild) int { return forkBuildRunJail(staging, b, out, errw, color) }
	}
	entry, m, err := captureStaged(store, staging,
		func() int { return runJail(staging, b) },
		func(m *capture.Manifest) string {
			return fmt.Sprintf("%s's build left nothing in the program surfaces (%s)", f.Key(),
				strings.Join(m.Surfaces, ", "))
		},
		func(m *capture.Manifest) string {
			if why := missingProduces(m, f.Produces); why != "" {
				return why
			}
			return linksIntoTheBuild(m)
		})
	toolchain, terr := os.ReadFile(filepath.Join(staging, forkToolchainLeaf))
	var exit captureJailExit
	if err != nil && b.Series != nil && errors.As(err, &exit) && terr != nil {
		// THE BUILD LINE NEVER RAN: the jail's script writes the toolchain record first, so a jail
		// that exited without one is a runtime that would not start it or a boot that failed,
		// never a build that failed — which a PATCHED fork's record keeps apart (§8.1, PF-D21).
		return nil, fmt.Errorf("%w (%v)", errForkBuildNotStarted, err)
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
			workspace: req.Workspace, out: out, errw: errw, color: color, launch: true, hand: req.Hand}).delivery
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
// narrowed to the fork and its base (FP-D9).
func forkBuildRunJail(workspace string, b forkBuild, out, errw io.Writer, color bool) int {
	return runCaptureJail(workspace, b.Fork.Bin, forkBuildJailArgv(b.Fork.Build),
		&captureSeal{only: []string{b.Fork.Pack, b.Fork.Base}}, out, errw, color)
}

// captureSeal is what makes a capture jail a fork build's: the seal, and the packs entries the
// selection is narrowed to. nil for `yolo capture` of an installer, which keeps today's jail.
type captureSeal struct {
	only []string
}
