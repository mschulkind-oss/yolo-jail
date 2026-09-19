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
//	$HOME/.yolo/bin/block:$HOME/.local/bin:…:$GOPATH/bin:/bin:/usr/bin:$HOME/.yolo/bin/launch
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
	"yolo-cglimit",    // was a Python client (docs/reference/loophole-transport.md §8.4)
	"yolo-journalctl", // was a Python client (docs/reference/loophole-transport.md §8.4)
}

// staleShimFiles are leftovers from the pre-Go bootstrap, in the shim dir.
var staleShimFiles = []string{"_yolo_bootstrap.py", "_yolo_python", "yolo", "yolo-go"}

// RemoveStaleGeneratedClients unlinks the in-jail clients older entrypoints
// generated, so the baked binaries the image ships are what resolve.
//
// It only ever removes REGULAR files it recognizes by name, never a directory
// and never the anchor dirs themselves (both ~/.local/bin's neighbours and the
// shim dir are bind-mount anchors elsewhere in the boot path).
// A FAILED UNLINK IS REPORTED, and the paragraph above is the reason: this is the
// CUTOVER, not tidiness. A surviving script keeps shadowing the baked binary on every
// future launch, and for yolo-cglimit / yolo-journalctl the symptom is the exact
// inverse of the truth — the client reports "not available" in a jail where the
// loophole is running fine. Discarding the error made that permanent AND unattributed.
//
// It still returns nil, deliberately: this runs through genStep, so returning the
// error would refuse the boot over a stale file in a home yolo may not be able to
// write (a :ro home in a skewed launch is the likely cause). A shadowed client is a
// degraded jail; a refused launch is no jail.
func RemoveStaleGeneratedClients(e *Env) error {
	for _, name := range staleGeneratedClients {
		stale := filepath.Join(e.LocalBin(), name)
		if fi, err := os.Lstat(stale); err == nil && fi.Mode().IsRegular() {
			if err := os.Remove(stale); err != nil {
				e.warn("Warning: could not unlink the stale generated client " + stale +
					": " + err.Error() + "; it precedes /bin on PATH, so it will keep " +
					"shadowing the baked " + name + " binary — a retired client can " +
					"report a loophole as unavailable in a jail where it is running fine")
			}
		}
	}
	for _, name := range staleShimFiles {
		// Absence is the normal case (these are pre-Go bootstrap leftovers), so only a
		// real removal failure is worth a line — but it IS worth one: the block dir is
		// FIRST on PATH, so a leftover named after a real tool intercepts it.
		path := filepath.Join(e.BlockDir(), name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			e.warn("Warning: could not unlink the retired bootstrap file " + path +
				": " + err.Error() + "; it sits in the FIRST directory on PATH")
		}
	}
	removeRetiredGeneratedDirs(e)
	return nil
}

// retiredGeneratedDirs are the home-relative names the two generated-script dirs used
// before they were gathered under ~/.yolo/bin (host-agent-environment.md OQ-6, 2026-08-30).
var retiredGeneratedDirs = []string{".yolo-shims", ".yolo-launchers"}

// removeRetiredGeneratedDirs clears out the pre-rename dirs.
//
// After the rename they are on no PATH, so their contents are inert — but they are
// EXECUTABLES named after real tools sitting in a user's home, which is exactly the shape
// that becomes confusing the first time someone puts one of those directories back on
// PATH by hand or copies a home between machines. They also still hold a `grep` blocker
// that would silently start intercepting again.
//
// Contents-only for the retired dirs too: on an existing host they are still bind-mount
// ANCHORS from a launcher that has not been upgraded yet, and removing the directory
// itself would fail EROFS against the :ro /home/agent (or, worse, succeed and detach a
// live mount). Emptying them is enough and is safe in both skew directions.
//
// config.reservedHomeDirRoots keeps both names reserved for one release so a user config
// cannot claim a path yolo is still cleaning up.
func removeRetiredGeneratedDirs(e *Env) {
	for _, name := range retiredGeneratedDirs {
		dir := filepath.Join(e.Home, name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // absent is the normal case on a fresh home
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue // never recurse: these dirs only ever held flat scripts
			}
			// Reported for the reason stated above — one of these leftovers is a `grep`
			// blocker that starts intercepting again the moment its directory is back on
			// a PATH — and because the failure is the interesting half: reaching here at
			// all means the retired dir still exists, so the sweep is live and the only
			// question left is whether it worked.
			path := filepath.Join(dir, entry.Name())
			if err := os.Remove(path); err != nil {
				e.warn("Warning: could not empty the retired generated-script dir: " +
					path + ": " + err.Error() + "; it is an executable named after a real " +
					"tool and will intercept again if this directory is ever back on PATH")
			}
		}
	}
}
