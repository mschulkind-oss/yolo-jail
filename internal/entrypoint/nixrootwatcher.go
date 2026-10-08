package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// nixrootwatcher.go starts the ROOT WATCHER (docs/design/in-jail-nix-roots.md §5, option C;
// internal/nixroots/watch.go) as a detached child of the boot: `yolo-jaild nix-roots`.
//
// NOT THROUGH THE SUPERVISOR, by implementation decision NR-D6: YOLO_JAIL_DAEMONS is the
// host's one composed payload of loophole and pack-service daemons, with the readiness,
// orphan and disclosure machinery those carry, and the watcher is none of them — it serves no
// endpoint, needs no credential, and a jail without it is exactly the jail it was before. Its
// singleton is its own lock (nixroots.WatchLock), so a re-run boot or an attach starts one
// that exits at once. A watcher that dies stays dead until the next boot; the boot's scan
// then admits what the gap's builds asked for, and `yolo nix-roots keep` is the hand-run form.
//
// Started only when the launch bound the host's auto dir and stated a map; anything else is
// a jail the watcher has no work in, and nothing is said.

// nixRootAutoDir, lookJaild and startNixRootWatcherFn are seams for tests.
var (
	nixRootAutoDir = nixroots.HostAutoDir
	lookJaild      = exec.LookPath
)

// startNixRootWatcherFn spawns the watcher detached, its output appended to logPath.
var startNixRootWatcherFn = func(bin, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(bin, "nix-roots")
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = log, log
	// Its own process group, so a signal meant for the jail's foreground (a Ctrl-C) is not.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// REAPED, not released: the watcher exits at once in a jail with nothing to watch (a
	// workspace the host cannot see, a watcher already running), and a released child of the
	// jail's main process stays a zombie for the jail's life. A zombie has dropped its I/O
	// context, so it also read as an unset `resources.io.priority` to every probe of the
	// jail. Only the jail's own boot runs this step (notSessionPass), and that process
	// outlives its watcher, so the Wait is always there to reap it.
	go func() { _ = cmd.Wait() }()
	return nil
}

// startNixRootWatcher is the boot step. It never fails the boot.
func startNixRootWatcher(e *Env) {
	if fi, err := os.Stat(nixRootAutoDir); err != nil || !fi.IsDir() {
		return
	}
	m, err := nixroots.ParseHostMap(e.Getenv(nixroots.MapEnv))
	if err != nil || !m.Translates() {
		return
	}
	bin, err := lookJaild("yolo-jaild")
	if err != nil {
		e.warn("yolo: the nix root watcher was not started (yolo-jaild is not on PATH); " +
			"links nix makes here are not kept for the host. `yolo nix-roots keep <link>` keeps one by hand.")
		return
	}
	logPath := filepath.Join(e.Home, filepath.FromSlash(paths.JailDaemonLogsRel()), "nix-roots.log")
	if err := startNixRootWatcherFn(bin, logPath); err != nil {
		e.warn("yolo: the nix root watcher could not be started (" + err.Error() + "); " +
			"`yolo nix-roots keep <link>` keeps a link by hand.")
	}
}
