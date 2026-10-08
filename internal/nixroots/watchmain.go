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
	registrar := &Registrar{Map: m, Socket: os.Getenv("NIX_DAEMON_SOCKET_PATH"),
		StoreDir: os.Getenv("NIX_STORE_DIR")}
	reg := &Registry{Workspace: ws, StoreDir: os.Getenv("NIX_STORE_DIR"), Register: registrar.Register}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	w := &Watcher{AutoDir: HostAutoDir, Map: m, Registry: reg, Registrar: registrar, Log: os.Stderr}
	if err := w.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "nix-roots: %v\n", err)
		return 1
	}
	return 0
}
