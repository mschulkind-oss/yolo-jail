package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// rootDirOutside is the directory rel under r as its path names it, not through r's descriptor:
// the root's own name for ".", else r.Lstat(rel), which resolves from r's descriptor and so
// fails once r's directory is gone. A relative root name that the process has since left by
// chdir names another directory or none, which reads as not live and declines the retry.
func rootDirOutside(r *os.Root, rel string) (os.FileInfo, error) {
	if rel == "." {
		return os.Lstat(r.Name())
	}
	return r.Lstat(rel)
}

// rootOpenFile is (*os.Root).OpenFile; a test replaces it to make a lost create race happen.
var rootOpenFile = (*os.Root).OpenFile

// createRetries bounds the repeats of a creating open that reported ENOENT in a live directory.
//
// On macOS 26.5 APFS two threads doing openat(dirfd, name, O_CREAT) for one NEW name make one of
// them fail with ENOENT in nearly every racing pair, although the other has just created the
// file and the directory is intact.
// Retrying the same openat succeeds, because the name exists by then. The retry applies on every
// unix rather than on darwin alone: on Linux the condition never arises, so it costs nothing, and
// one code path is the one a Linux test can pin.
const createRetries = 8

// OpenRootCreate is r.OpenFile for a caller that may create name while another process or
// goroutine creates the same name. On macOS 26.5 APFS one of two such concurrent creates
// through a directory descriptor, os.Root's included, fails with ENOENT though the directory
// exists and the other has made the file. So a creating open that reports ENOENT is repeated,
// at most createRetries more times, while the directory that would hold name is still the one at
// its name: the open descriptor and the name resolved from outside it (rootDirOutside) are the
// same file. A removed directory fails that test on Linux and on macOS alike, where its
// descriptor still reports a directory mode and nlink=2. Opens that do not create are made once.
// Every other refusal is r.OpenFile's own.
func OpenRootCreate(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
	f, err := rootOpenFile(r, name, flag, perm)
	for tries := 0; err != nil && flag&os.O_CREATE != 0 && errors.Is(err, fs.ErrNotExist) && tries < createRetries; tries++ {
		rel := filepath.Dir(name)
		in, ierr := r.Stat(rel)
		out, oerr := rootDirOutside(r, rel)
		if ierr != nil || oerr != nil || !in.IsDir() || !os.SameFile(in, out) {
			break
		}
		f, err = rootOpenFile(r, name, flag, perm)
	}
	return f, err
}
