package paths

import (
	"errors"
	"io/fs"
	"os"
)

// rootOpenFile is (*os.Root).OpenFile; a test replaces it to make a lost create race happen.
var rootOpenFile = (*os.Root).OpenFile

// createRetries bounds the repeats of a creating open that reported ENOENT in a live directory.
//
// On macOS 26.5 APFS two threads doing openat(dirfd, name, O_CREAT) for one NEW name make one of
// them fail with ENOENT in nearly every racing pair, although the other has just created the
// file and the directory is intact (docs/lab-reports/2026-11-24-mac-openat-create-race-and-ac-stopped-halves.md).
// Retrying the same openat succeeds, because the name exists by then. The retry applies on every
// unix rather than on darwin alone: on Linux the condition never arises, so it costs nothing, and
// one code path is the one a Linux test can pin.
const createRetries = 8

// OpenRootCreate is r.OpenFile for a caller that may create name while another process or
// goroutine creates the same name. On macOS 26.5 APFS one of two such concurrent creates
// through a directory descriptor, os.Root's included, fails with ENOENT though the directory
// exists and the other has made the file (docs/lab-reports/2026-11-24-mac-openat-create-race-and-ac-stopped-halves.md).
// So a creating open that reports ENOENT is repeated, at most createRetries more times, while
// r still names a directory (a removed one that is still open stats as one, so the bound is what
// ends the retries there; the leaf open in openNoFollowLeaf also checks the link count). Opens that do not
// create are made once. Every other refusal is r.OpenFile's own.
func OpenRootCreate(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
	f, err := rootOpenFile(r, name, flag, perm)
	for tries := 0; err != nil && flag&os.O_CREATE != 0 && errors.Is(err, fs.ErrNotExist) && tries < createRetries; tries++ {
		if fi, serr := r.Stat("."); serr != nil || !fi.IsDir() {
			break
		}
		f, err = rootOpenFile(r, name, flag, perm)
	}
	return f, err
}
