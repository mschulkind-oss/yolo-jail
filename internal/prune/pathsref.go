package prune

import (
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// pathsGlobalStorage / pathsGlobalHome / pathsGlobalCache are the real storage
// root getters, wrapped as package funcs so Options can default to them yet
// tests can inject a temp root.
func pathsGlobalStorage() string { return paths.GlobalStorage() }
func pathsGlobalHome() string    { return paths.GlobalHome() }
func pathsGlobalCache() string   { return paths.GlobalCache() }
func pathsBuildDir() string      { return paths.BuildDir() }
func pathsAgentsDir() string     { return paths.AgentsDir() }
func pathsContainerDir() string  { return paths.ContainerDir() }

// embeddedPacksLeaf is the state-dir child holding the embedded-pack cache trees — read off
// paths.EmbeddedPacksDirUnder rather than retyped, so the sweep cannot drift from the
// directory packload writes (TestEmbeddedPacksDirDefaultIsThePathsLocation pins the whole
// path).
var embeddedPacksLeaf = filepath.Base(paths.EmbeddedPacksDirUnder(string(filepath.Separator)))

// imageDeliveryLeaf is the state-dir child an archive delivery works in, read off
// paths.ImageDeliveryDirUnder for the same reason as embeddedPacksLeaf.
var imageDeliveryLeaf = filepath.Base(paths.ImageDeliveryDirUnder(string(filepath.Separator)))

// killPID sends SIGTERM (or SIGKILL when force) to pid. A missing/dead target
// yields an error the caller ignores (best-effort reap).
func killPID(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}
