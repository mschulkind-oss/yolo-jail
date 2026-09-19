package listeners

import (
	"errors"
	"io/fs"
	"strings"
)

// mapSource is a /proc built out of strings: no filesystem, no symlinks on disk,
// identical on every GOOS. A test that parsed the machine's real /proc would not be
// reproducible — the one test that does read it is the live smoke check, which
// creates the socket it looks for.
//
// It counts its directory reads so a test can prove the fd walk did not run.
type mapSource struct {
	files map[string]string
	dirs  map[string][]string
	links map[string]string
	// failFiles/failDirs name entries whose read returns an error, for the
	// "could not ask" half of the tri-state.
	failFiles map[string]bool
	failDirs  map[string]bool

	dirReads  int
	linkReads int
}

func (m *mapSource) ReadFile(name string) ([]byte, error) {
	if m.failFiles[name] {
		return nil, errors.New("read " + name + ": simulated failure")
	}
	s, ok := m.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return []byte(s), nil
}

func (m *mapSource) ReadDirNames(name string) ([]string, error) {
	m.dirReads++
	if m.failDirs[name] {
		return nil, errors.New("open " + name + ": simulated failure")
	}
	names, ok := m.dirs[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return names, nil
}

func (m *mapSource) ReadLink(name string) (string, error) {
	m.linkReads++
	target, ok := m.links[name]
	if !ok {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrNotExist}
	}
	return target, nil
}

// Header lines, verbatim from a live jail's /proc, so the header-skipping is tested
// against the real bytes rather than a paraphrase. The trailing spaces on the tcp
// header are the kernel's.
const (
	tcpHeader  = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                     "
	tcp6Header = "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"
	unixHeader = "Num       RefCount Protocol Flags    Type St Inode Path"
)

func tcpTable(rows ...string) string {
	return tcpHeader + "\n" + strings.Join(rows, "\n") + "\n"
}

func unixTable(rows ...string) string {
	return unixHeader + "\n" + strings.Join(rows, "\n") + "\n"
}

// emptyTables is a source whose three tables exist and contain only their header:
// the "nothing is listening" case, which must not look like the "could not read"
// case.
func emptyTables() *mapSource {
	return &mapSource{files: map[string]string{
		"net/tcp":  tcpHeader + "\n",
		"net/tcp6": tcp6Header + "\n",
		"net/unix": unixHeader + "\n",
	}}
}
