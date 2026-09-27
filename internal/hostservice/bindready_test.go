package hostservice

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestASocketPathThatExistsAccepts pins bindUnixSocket's publish order: the path appears
// only once the listener is in LISTEN, so a client that waits for the file and then dials
// is never refused. Before the staged bind, a stat-then-dial loop against a plain
// net.ListenUnix was refused 113 times in 3000 on Linux, and the same gap failed
// check-macos on f937d0fd. The loop here is that measurement, run through bindUnixSocket.
func TestASocketPathThatExistsAccepts(t *testing.T) {
	const rounds = 2000
	refused := 0
	for i := 0; i < rounds; i++ {
		p := filepath.Join(shortDir(t), "d.sock")
		bound := make(chan *net.UnixListener, 1)
		go func() {
			ln, err := bindUnixSocket(p)
			if err != nil {
				t.Error(err)
			}
			bound <- ln
		}()
		for {
			if _, err := os.Stat(p); err == nil {
				break
			}
		}
		c, err := net.Dial("unix", p)
		switch {
		case err == nil:
			_ = c.Close()
		case errors.Is(err, syscall.ECONNREFUSED):
			refused++
		default:
			t.Fatalf("dialing %s: %v", p, err)
		}
		if ln := <-bound; ln != nil {
			_ = ln.Close()
		}
	}
	if refused != 0 {
		t.Errorf("%d of %d dials to a socket path that already existed were refused: the path "+
			"is visible before the listener is in LISTEN", refused, rounds)
	}
}

// TestTheStagedBindLeavesOnlyThe0600Socket: the rename leaves exactly the requested path
// behind, 0600, and no staging sibling; a path at the length the platform allows still
// binds, because the staging name is exactly as long as the real one.
func TestTheStagedBindLeavesOnlyThe0600Socket(t *testing.T) {
	dir := shortDir(t)
	p := filepath.Join(dir, "d.sock")
	ln, err := bindUnixSocket(p)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("%s: mode %v, want a 0600 socket", p, fi.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "d.sock" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the bind left %v in %s, want only d.sock", names, dir)
	}

	// sun_path is 104 bytes on darwin and 108 on Linux, NUL included: fill the smaller.
	longDir := shortDir(t)
	name := strings.Repeat("s", 103-len(longDir)-1)
	long := filepath.Join(longDir, name)
	ln2, err := bindUnixSocket(long)
	if err != nil {
		t.Fatalf("a %d-byte socket path must still bind: %v", len(long), err)
	}
	_ = ln2.Close()
}

// shortDir is a temp dir short enough for AF_UNIX paths on every platform.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}
