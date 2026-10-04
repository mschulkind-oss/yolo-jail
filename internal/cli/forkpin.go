package cli

// forkpin.go is the verbs' half of the fork PIN (docs/design/forked-programs-as-packs.md §4 step 2,
// FP-D7, FP-D18): `yolo pack install` pins every selected fork the fork lock does not already pin
// for its declared source, and `yolo pack update` re-resolves every one. The lock is forks.lock.json
// beside packs.lock.json (packsrc.ForkLock).
//
// INSTALL IS NO LONGER REQUIRED (FP-D18, applying the maintainer's OQ-PF1): a launch pins an unpinned
// fork itself (run.PinLaunchForks), and so does `yolo host -- <bin>`. What stays explicit is MOVING
// a pin, which only `yolo pack update` does: a launch leaves a standing pin where it is, and a fork's
// source never goes through the launch's pack refresh, which re-fetches a branch at most hourly —
// for a fork, the rebuild on a timer §9 forbids.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// forkPinSync resolves a fork's source to a commit: fetch its mirror, then resolve the ref. A
// package var so a test can stand a fixture repository in for the network, and so the tests that
// drive `yolo pack install` for its other halves never reach a remote.
var forkPinSync = func(store *packsrc.Store, a packsrc.Addr) (string, error) { return store.Sync(a) }

// forkLockPath is the fork lock this process reads and writes.
func forkLockPath() string { return packsrc.ForkLockPath(paths.UserConfigPath()) }

// pinForks pins the selected packs' forks. repin re-resolves every fork (`yolo pack update`);
// without it only a fork with no pin for its declared source is resolved (`yolo pack install`), so
// install leaves a pinned fork alone when its branch moves. It prunes the pins of forks that left
// the selection, saying so. Returns the verb's exit status.
func pinForks(pr richtext.Printer, errw io.Writer, repin bool) int {
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		// The pack half above has already reported an unreadable config.
		return 0
	}
	forks := packload.Forks(sel.packs)
	trees := packload.PatchedTrees(sel.packs)
	if config.InJail() {
		if len(forks) > 0 {
			pr.Printf("[dim]Fork pins are recorded on the host (%s): the next launch there pins a fork "+
				"that has none, and `yolo pack update` there moves one.[/dim]", packsrc.ForkLockName)
		}
		if len(trees) > 0 {
			pr.Printf("[dim]Patched extensions are checked, replayed and built on the host: `yolo pack update` " +
				"there checks them.[/dim]")
		}
		return 0
	}
	lockPath := forkLockPath()
	store := &packsrc.Store{Dir: paths.PacksDir()}
	rc := 0
	var lines, pruned, migrated []string
	err := packsrc.WithForkLock(store.Dir, lockPath, func(line string) { pr.Printf("[dim]%s[/dim]", line) },
		func(l *packsrc.ForkLock) (bool, error) {
			changed := false
			var keep []string
			for _, f := range forks {
				if f.Patched() {
					// A PATCHED FORK HAS NO PIN (PF-D16): a plain fork's entry left under its key from
					// before a migration is dropped, and said, apart from the forks that left.
					if _, pinned := l.Get(f.Key()); pinned {
						delete(l.Forks, f.Key())
						migrated = append(migrated, f.Key())
						changed = true
					}
					continue
				}
				keep = append(keep, f.Key())
				prev, pinned := l.Get(f.Key())
				addr, err := packsrc.Parse(f.Source)
				if err != nil {
					fmt.Fprintf(errw, "yolo pack: fork %s: %v\n", f.Key(), err)
					rc = 1
					continue
				}
				if pinned && prev.Source == f.Source && prev.Commit != "" && !repin {
					// THE PIN STANDS, and install makes it BUILDABLE HERE now: a lock that arrived
					// with the config names a commit this machine's pack store may never have
					// fetched (a launch's build would fetch it too, by the commit: FP-D18).
					if err := ensureForkCheckout(store, addr, prev.Commit); err != nil {
						fmt.Fprintf(errw, "yolo pack: fork %s: its pinned commit %s cannot be checked out "+
							"from %s (%v) — `yolo pack update` pins what %s names now\n",
							f.Key(), shortSHA(prev.Commit), f.Source, err, addr.Ref)
						rc = 1
						continue
					}
					lines = append(lines, fmt.Sprintf("[dim]%s unchanged (%s)[/dim]", f.Key(), shortSHA(prev.Commit)))
					continue
				}
				commit, err := forkPinSync(store, addr)
				if err != nil {
					fmt.Fprintf(errw, "yolo pack: fork %s: resolving %s: %v\n", f.Key(), f.Source, err)
					rc = 1
					continue
				}
				// Checked out now, while this act has the network, so the launch that builds it
				// reads only the pack store.
				if err := ensureForkCheckout(store, addr, commit); err != nil {
					fmt.Fprintf(errw, "yolo pack: fork %s: checking out %s: %v\n", f.Key(), shortSHA(commit), err)
					rc = 1
					continue
				}
				switch {
				case !pinned || prev.Source != f.Source:
					lines = append(lines, fmt.Sprintf("[green]%s[/green] %s → %s (fork of %s)",
						f.Key(), addr.Ref, shortSHA(commit), f.Base))
				case prev.Commit != commit:
					lines = append(lines, fmt.Sprintf("[yellow]%s[/yellow] %s: %s → %s — the next launch "+
						"builds the new revision", f.Key(), addr.Ref, shortSHA(prev.Commit), shortSHA(commit)))
				default:
					lines = append(lines, fmt.Sprintf("[dim]%s unchanged (%s)[/dim]", f.Key(), shortSHA(commit)))
				}
				next := packsrc.ForkLockEntry{Key: f.Key(), Source: f.Source, Ref: addr.Ref, Commit: commit}
				if prev != next {
					l.Set(next)
					changed = true
				}
			}
			if pruned = l.Prune(keep); len(pruned) > 0 {
				changed = true
			}
			return changed, nil
		})
	for _, line := range lines {
		pr.Printf("%s", line)
	}
	for _, gone := range pruned {
		pr.Printf("[dim]fork %s left the selection — dropped from %s[/dim]", gone, packsrc.ForkLockName)
	}
	for _, key := range migrated {
		pr.Printf("[dim]fork %s is a patched fork now; its plain-fork pin is dropped from %s[/dim]",
			key, packsrc.ForkLockName)
	}
	if err != nil {
		fmt.Fprintf(errw, "yolo pack: writing %s: %v\n", lockPath, err)
		return 1
	}
	// THE PATCHED FORKS (docs/design/patched-forks.md §8.3), after the fork lock is released — the
	// fork lock comes before a check record's lock, and nothing here holds both: update checks and
	// replays every one, install those with no good build.
	if n := checkPatchedForks(pr, errw, forks, repin); n != 0 {
		rc = n
	}
	// And the PATCHED EXTENSIONS, through the same check and walk by their extension keys
	// (docs/design/patched-extensions.md §6.1, PF-D12 generalized): no pin, no fork lock.
	if n := checkPatchedForks(pr, errw, trees, repin); n != 0 {
		rc = n
	}
	return rc
}

// ensureForkCheckout makes commit of a checked out in the pack store, the tree a build copies
// (checkOutForkSource): at once when it already is, and otherwise after a fetch of a's mirror,
// which this machine may never have made — the lock can arrive with the config from another one.
func ensureForkCheckout(store *packsrc.Store, a packsrc.Addr, commit string) error {
	if _, err := forkCheckout(store, a, commit); err == nil {
		return nil
	}
	if _, err := forkPinSync(store, a); err != nil {
		return err
	}
	_, err := forkCheckout(store, a, commit)
	return err
}

// forkStatusLines is `yolo pack status`'s fork section: each selected fork's pin, or why it has
// none. It returns the lines and whether any fork's pin was made for a source the fork no longer
// declares — DRIFT, which fails the verb as a pack's drift does. A fork never pinned is reported
// like a pack never installed, and does not fail it.
func forkStatusLines() (lines []string, drift bool, err error) {
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return nil, false, nil
	}
	forks := packload.Forks(sel.packs)
	trees := packload.PatchedTrees(sel.packs)
	if len(forks) == 0 && len(trees) == 0 {
		return nil, false, nil
	}
	// IN A JAIL the lock is not here: it is beside the host's user config, which no jail reads, so
	// an absent file would read as every fork being unpinned (pinForks' in-jail line, the twin).
	if config.InJail() {
		if len(forks) > 0 {
			lines = append(lines, fmt.Sprintf("[dim]%d %s: the pins are recorded on the host (%s) — run "+
				"`yolo pack status` there[/dim]", len(forks), plural(len(forks), "fork", "forks"),
				packsrc.ForkLockName))
		}
		if len(trees) > 0 {
			lines = append(lines, fmt.Sprintf("[dim]%d patched %s: checked and built on the host — run "+
				"`yolo pack status` there[/dim]", len(trees), plural(len(trees), "extension", "extensions")))
		}
		return lines, false, nil
	}
	// A PATCHED EXTENSION's state is its check record's, as a patched fork's is (patchedfork.go).
	defer func() {
		for _, f := range trees {
			lines = append(lines, patchedForkStatusLines(f)...)
		}
	}()
	lock, err := packsrc.LoadForkLock(forkLockPath())
	if err != nil {
		return nil, false, err
	}
	for _, p := range packload.ForkPins(forks, lock) {
		if p.Fork.Patched() {
			// No pin by design (PF-D16): its state is its check record's (patchedfork.go).
			lines = append(lines, patchedForkStatusLines(p.Fork)...)
			continue
		}
		if p.Commit == "" {
			drift = drift || p.LockedSource != ""
			lines = append(lines, "[yellow]⚠ "+p.Line()+"[/yellow]")
			continue
		}
		lines = append(lines, fmt.Sprintf("%-20s %s [dim]%s — fork of %s's %s, from %s; %s[/dim]",
			p.Fork.Key(), shortSHA(p.Commit), p.Ref, p.Fork.Base, p.Fork.Bin, p.Fork.Source, forkBuiltState(p)))
	}
	return lines, drift, nil
}

// forkBuiltState says whether the capture store holds this pin's build for a container jail on
// this machine, read offline through the one fork query (resolveForkBuild).
func forkBuiltState(p packload.ForkPin) string {
	b := forkBuild{Fork: p.Fork, Commit: p.Commit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	if entry, _, err := resolveForkBuild(store, p.Fork.Bin, b.Platform, p.Fork.Source, p.Commit, b.recipe()); err == nil {
		return "built (" + entry.Key + ", " + b.Platform + ")"
	}
	return "not built yet for " + b.Platform + " — the next launch builds it"
}
