package prunecmd

import (
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// pathsGlobalStorage / pathsGlobalHome / pathsGlobalCache are the real storage
// root getters, matching prune_cmd's GLOBAL_STORAGE / GLOBAL_HOME / GLOBAL_CACHE
// imports from cli/paths.py. Wrapped as package funcs so Options can default to
// them yet tests can inject a temp root.
func pathsGlobalStorage() string { return paths.GlobalStorage() }
func pathsGlobalHome() string    { return paths.GlobalHome() }
func pathsGlobalCache() string   { return paths.GlobalCache() }

// killPID sends SIGTERM (or SIGKILL when force) to pid. A missing/dead target
// yields an error the caller ignores (best-effort reap).
func killPID(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}
