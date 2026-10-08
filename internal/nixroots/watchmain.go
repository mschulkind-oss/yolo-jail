package nixroots

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// WatchLock is the root watcher's singleton lock: a second watcher (a re-run boot, an
// attach) finds it held and exits, leaving the first in charge.
const WatchLock = "/tmp/yolo-nix-roots.lock"

// WatchMain is `yolo-jaild nix-roots`: the root watcher for this jail's workspace. It exits 0
// without watching when there is nothing to do — no auto dir bound, no map, or a watcher
// already running — because each is a jail the watcher has no work in, not a failure.
func WatchMain(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo-jaild nix-roots")
		return 2
	}
	if fi, err := os.Stat(HostAutoDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "nix-roots: %s is not bound into this jail; nothing to watch\n", HostAutoDir)
		return 0
	}
	m, err := ParseHostMap(os.Getenv(MapEnv))
	if err != nil || !m.Translates() {
		fmt.Fprintln(os.Stderr, "nix-roots: this jail's launcher stated no host path map; nothing can be kept")
		return 0
	}
	lock, err := os.OpenFile(WatchLock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nix-roots: %v\n", err)
		return 1
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 0
	}
	ws := os.Getenv("YOLO_WORKSPACE")
	if ws == "" {
		ws = "/workspace"
	}
	w := newWatcher(HostAutoDir, ws, m, os.Getenv)
	w.Log = os.Stderr
	if _, ok := m.Translate(w.resolvedRegistryDir()); !ok {
		fmt.Fprintf(os.Stderr, "nix-roots: %s is under no mount the host can see (a workspace under "+
			"/tmp, or a read-only one), so no managed root can be made here; nothing to watch\n", w.Registry.Dir())
		return 0
	}
	// SIGINT is ignored, a second guard behind the process group of its own the boot starts it
	// in: a Ctrl-C typed at the jail's shell is not meant for it. SIGTERM, which the jail's
	// stop sends, ends it.
	signal.Ignore(syscall.SIGINT)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := w.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "nix-roots: %v\n", err)
		return 1
	}
	return 0
}

// newWatcher is the watcher WatchMain runs: the workspace registry, registering through a
// Registrar that the watcher also PINS through, so every admission is fenced (NR-D7).
func newWatcher(autoDir, workspace string, m HostMap, getenv func(string) string) *Watcher {
	registrar := &Registrar{Map: m, Socket: getenv("NIX_DAEMON_SOCKET_PATH"), StoreDir: getenv("NIX_STORE_DIR")}
	reg := &Registry{Workspace: workspace, StoreDir: getenv("NIX_STORE_DIR"), Register: registrar.Register}
	return &Watcher{AutoDir: autoDir, Map: m, Registry: reg, Registrar: registrar}
}
