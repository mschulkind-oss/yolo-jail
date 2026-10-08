package nixroots

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// watch.go is the ROOT WATCHER (a term coined in docs/design/in-jail-nix-roots.md §5, option
// C): an in-jail daemon watching a read-only bind of the host's /nix/var/nix/gcroots/auto.
// Every indirect root any client asks the host's daemon for appears there as an entry whose
// target is the path STRING the client sent. For an entry naming a link that exists in THIS
// jail, points straight into the store and lies under a mount the host can see, the watcher
// admits a managed root (registry.go), so the host keeps what the link points at.
//
// It watches the daemon call's result rather than any tool's argv, so it covers every
// root-making client at once (nix build, nix-build, nix profile, nix develop --profile,
// nix-direnv), and it sits in no nix command's path: when it is down, a jail is exactly as it
// was without it.
//
// WHAT IT CANNOT CLOSE, accepted as implementation decision NR-D5: the window between the
// producing client's exit (its temp root ends) and the managed root's registration, a few
// milliseconds of a GC that starts in exactly that gap; and an entry a GC or root query
// deletes before the watcher reads it. Each loses one root that the next rebuild or
// `yolo nix-roots keep` restores — the state every jail is in today.

// HostAutoDir is where a launch binds the host's gcroots/auto, read-only, in a jail.
const HostAutoDir = "/run/yolo/nix-gcroots-auto"

// HostAutoSource is the host directory bound there.
const HostAutoSource = "/nix/var/nix/gcroots/auto"

// DefaultHousekeeping is how often a running watcher applies the lifecycle (Registry.Prune).
const DefaultHousekeeping = time.Hour

// Watcher admits managed roots for the entries a jail's links make in the host's auto dir.
type Watcher struct {
	AutoDir  string
	Map      HostMap
	Registry *Registry
	// Registrar, when set, pins each target the moment the watcher learns of it and
	// registers the managed link on the pin's connection (Pin). Nil registers through
	// Registry.Register with no pin, which only a test wants.
	Registrar *Registrar
	Log       io.Writer
	// Housekeeping is DefaultHousekeeping when zero.
	Housekeeping time.Duration
}

func (w *Watcher) logf(format string, a ...any) {
	if w.Log != nil {
		fmt.Fprintf(w.Log, time.Now().UTC().Format(time.RFC3339)+" "+format+"\n", a...)
	}
}

// Consider admits the managed root the auto entry name asks for, if it is one of this
// jail's. scan says the entry was found by a scan rather than a fresh event: a scan admits a
// root that is missing but renews none, since seeing an old entry is not a new request.
// It reports whether a root was admitted or renewed.
func (w *Watcher) Consider(name string, scan bool) bool {
	x, err := os.Readlink(filepath.Join(w.AutoDir, name))
	if err != nil || !filepath.IsAbs(x) {
		return false
	}
	// Never our own managed links, whichever spelling the entry carries.
	if within(filepath.Clean(x), w.Registry.Dir()) {
		return false
	}
	if hostDir, ok := w.Map.Translate(w.resolvedRegistryDir()); ok && within(filepath.Clean(x), hostDir) {
		return false
	}
	fi, err := os.Lstat(x)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(x)
	if err != nil || !w.Registry.isStorePath(target) {
		return false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(x))
	if err != nil {
		return false
	}
	host, ok := w.Map.Translate(filepath.Join(dir, filepath.Base(x)))
	if !ok || host == x {
		return false
	}
	if scan {
		if roots, err := w.Registry.List(); err == nil {
			for _, r := range roots {
				if r.Source == x && r.Target == target {
					return false
				}
			}
		}
	}
	// PIN FIRST (NR-D7): the target is held from here until the managed root is registered
	// and fenced, so the only unguarded interval left is the one before this line.
	reg := w.Registry
	if w.Registrar != nil {
		pin, err := w.Registrar.Pin(target)
		if err != nil {
			w.logf("could not pin %s for %s: %v", target, x, err)
			return false
		}
		defer pin.Close()
		pinned := *w.Registry
		pinned.Register = pin.Register
		reg = &pinned
	}
	if _, err := os.Lstat(target); err != nil {
		w.logf("%s -> %s: the store path is already gone; a rebuild re-roots it", x, target)
		return false
	}
	adm, err := reg.Admit(x, host, target, ByWatch)
	for _, r := range adm.Released {
		w.logf("released %s %s: %s", r.Root.ID, r.Root.Source, r.Reason)
	}
	if err != nil {
		w.logf("could not keep %s -> %s: %v", x, target, err)
		return false
	}
	verb := "renewed"
	if adm.New {
		verb = "kept"
	}
	w.logf("%s %s -> %s (id %s)", verb, x, target, adm.Root.ID)
	return true
}

func (w *Watcher) resolvedRegistryDir() string {
	if d, err := filepath.EvalSymlinks(w.Registry.Dir()); err == nil {
		return d
	}
	return w.Registry.Dir()
}

// Scan considers every entry in the auto dir.
func (w *Watcher) Scan() int {
	ents, err := os.ReadDir(w.AutoDir)
	if err != nil {
		w.logf("cannot read %s: %v", w.AutoDir, err)
		return 0
	}
	n := 0
	for _, e := range ents {
		if w.Consider(e.Name(), true) {
			n++
		}
	}
	return n
}

func (w *Watcher) housekeep() {
	released, err := w.Registry.Prune()
	for _, r := range released {
		w.logf("released %s %s: %s", r.Root.ID, r.Root.Source, r.Reason)
	}
	if err != nil {
		w.logf("lifecycle pass failed: %v", err)
	}
}
