package entrypoint

import (
	"os"
	"path/filepath"
)

// staleGeneratedClients are files an OLDER entrypoint wrote into ~/.local/bin
// that the image now provides as real binaries.
//
// UNLINKING THEM IS NOT TIDINESS — IT IS THE CUTOVER. The jail's PATH is
//
//	$HOME/.yolo-shims:$HOME/.local/bin:…:$GOPATH/bin:/bin:/usr/bin:$HOME/.yolo-launchers
//
// so ~/.local/bin PRECEDES /bin. The jail home persists across launches and
// across image upgrades, which means a script written by a previous boot keeps
// SHADOWING the baked binary of the same name forever. For yolo-cglimit and
// yolo-journalctl that is worse than a stale copy: the retired scripts speak the
// plain AF_UNIX transport, so once the run pipeline publishes an endpoint file
// instead, a surviving script reports "not available" in a jail where the
// loophole is running fine.
//
// yolo and yolo-ps are here for the same reason, from their own earlier ports.
var staleGeneratedClients = []string{
	"yolo",            // was a shell wrapper
	"yolo-ps",         // was a Python client
	"yolo-cglimit",    // was a Python client (docs/design/loophole-transport.md §8.4)
	"yolo-journalctl", // was a Python client (docs/design/loophole-transport.md §8.4)
}

// staleShimFiles are leftovers from the pre-Go bootstrap, in the shim dir.
var staleShimFiles = []string{"_yolo_bootstrap.py", "_yolo_python", "yolo", "yolo-go"}

// RemoveStaleGeneratedClients unlinks the in-jail clients older entrypoints
// generated, so the baked binaries the image ships are what resolve.
//
// It only ever removes REGULAR files it recognizes by name, never a directory
// and never the anchor dirs themselves (both ~/.local/bin's neighbours and the
// shim dir are bind-mount anchors elsewhere in the boot path).
func RemoveStaleGeneratedClients(e *Env) error {
	for _, name := range staleGeneratedClients {
		stale := filepath.Join(e.LocalBin(), name)
		if fi, err := os.Lstat(stale); err == nil && fi.Mode().IsRegular() {
			_ = os.Remove(stale)
		}
	}
	for _, name := range staleShimFiles {
		_ = os.Remove(filepath.Join(e.ShimDir(), name))
	}
	return nil
}
