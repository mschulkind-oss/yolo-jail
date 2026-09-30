package cli

// forkpin.go is the PIN half of the fork route (docs/design/forked-programs-as-packs.md §4 step 2,
// FP-D7): `yolo pack install` pins every selected fork the fork lock does not already pin for its
// declared source, and `yolo pack update` re-resolves every one. The lock is forks.lock.json beside
// packs.lock.json (packsrc.ForkLock).
//
// RESOLUTION IS AN EXPLICIT ACT, never a launch side effect: a launch only READS the lock
// (run.noteForkPins), and a fork's source never goes through the launch's pack refresh, which
// re-fetches a branch at most hourly — for a fork, the rebuild on a timer §9 forbids.

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
	if config.InJail() {
		if len(forks) > 0 {
			pr.Printf("[dim]Fork pins are recorded on the host (%s) — run `yolo pack install` there.[/dim]",
				packsrc.ForkLockName)
		}
		return 0
	}
	lockPath := forkLockPath()
	store := &packsrc.Store{Dir: paths.PacksDir()}
	rc := 0
	var lines, pruned []string
	err := packsrc.WithForkLock(store.Dir, lockPath, func(line string) { pr.Printf("[dim]%s[/dim]", line) },
		func(l *packsrc.ForkLock) (bool, error) {
			changed := false
			var keep []string
			for _, f := range forks {
				keep = append(keep, f.Key())
				prev, pinned := l.Get(f.Key())
				if pinned && prev.Source == f.Source && prev.Commit != "" && !repin {
					lines = append(lines, fmt.Sprintf("[dim]%s unchanged (%s)[/dim]", f.Key(), shortSHA(prev.Commit)))
					continue
				}
				addr, err := packsrc.Parse(f.Source)
				if err != nil {
					fmt.Fprintf(errw, "yolo pack: fork %s: %v\n", f.Key(), err)
					rc = 1
					continue
				}
				commit, err := forkPinSync(store, addr)
				if err != nil {
					fmt.Fprintf(errw, "yolo pack: fork %s: resolving %s: %v\n", f.Key(), f.Source, err)
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
	if err != nil {
		fmt.Fprintf(errw, "yolo pack: writing %s: %v\n", lockPath, err)
		return 1
	}
	return rc
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
	if len(forks) == 0 {
		return nil, false, nil
	}
	lock, err := packsrc.LoadForkLock(forkLockPath())
	if err != nil {
		return nil, false, err
	}
	for _, p := range packload.ForkPins(forks, lock) {
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
