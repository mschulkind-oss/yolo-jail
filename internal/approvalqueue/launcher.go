package approvalqueue

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// The browser is opened on a small launcher file, never on the URL itself: the one-time code
// is in the URL, and a process's argv can be read by every other process on a machine where a
// separate account runs the agent (a macOS agent account can read every process's argv). So
// the opener's argv carries only the launcher's path. The file sits at mode 0600 in a fresh
// 0700 directory, redirects the browser to the URL, and is deleted after the exchange, after
// the code's lifetime, or at exit, whichever comes first.

// launchDir makes the fresh 0700 directory the launcher files live in. root, when set, is where
// to make it (tests); otherwise Linux uses $XDG_RUNTIME_DIR when it is an absolute path, and
// everything else, and a Linux without one, uses the system temporary directory. os.MkdirTemp
// creates the directory at mode 0700.
func launchDir(root, goos string, getenv func(string) string) (string, error) {
	if root == "" && goos == "linux" {
		if x := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(x) {
			root = x
		}
	}
	return os.MkdirTemp(root, "yolo-approve-web-")
}

// launcherBody is the launcher page. It is a file: page, not the served one, so its meta
// refresh is not subject to the served page's CSP. The URL holds only base64url characters.
func launcherBody(url string) string {
	return `<!doctype html>
<meta charset="utf-8">
<meta http-equiv="refresh" content="0;url=` + url + `">
<title>yolo approval queue</title>
<p>Opening the yolo approval queue. If nothing happens, <a href="` + url + `">follow this link</a>.</p>
`
}

// openerFor is the program that opens a file in the person's browser, or "" where yolo knows
// none.
func openerFor(goos string) string {
	switch goos {
	case "linux":
		return "xdg-open"
	case "darwin":
		return "open"
	}
	return ""
}

// systemOpen runs the platform's opener on path. Its argv is the opener and the path only.
func systemOpen(path string) error {
	name := openerFor(runtime.GOOS)
	if name == "" {
		return fmt.Errorf("no browser opener is known for %s", runtime.GOOS)
	}
	cmd := exec.Command(name, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it; its result changes nothing, the URL is on the terminal as well
	return nil
}

// launcher writes launcher files and opens them.
type launcher struct {
	dir   string
	open  func(path string) error                       // nil: do not open (--no-open)
	after func(d time.Duration, f func()) (stop func()) // time.AfterFunc in production
	logf  func(format string, a ...any)

	mu   sync.Mutex
	path string // the live launcher file, "" when none
	stop func() // cancels its expiry
}

func newLauncher(dir string, open func(string) error, after func(time.Duration, func()) func(), logf func(string, ...any)) *launcher {
	if after == nil {
		after = func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		}
	}
	return &launcher{dir: dir, open: open, after: after, logf: logf}
}

// Launch replaces any earlier launcher file with one for url, schedules its deletion after
// ttl, and opens it.
func (l *launcher) Launch(url string, ttl time.Duration) {
	l.Remove()
	if l.open == nil { // --no-open: the printed link is the only way in
		return
	}
	f, err := os.CreateTemp(l.dir, "open-*.html") // mode 0600
	if err != nil {
		l.logf("cannot write the launcher file: %v", err)
		return
	}
	path := f.Name()
	_, werr := f.WriteString(launcherBody(url))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		l.logf("cannot write the launcher file: %v", werr)
		return
	}
	l.mu.Lock()
	l.path = path
	l.stop = l.after(ttl, func() { l.removePath(path) })
	l.mu.Unlock()
	if err := l.open(path); err != nil {
		l.logf("could not open a browser (%v); use the link printed above", err)
	}
}

// Remove deletes the live launcher file, if any.
func (l *launcher) Remove() {
	l.mu.Lock()
	path, stop := l.path, l.stop
	l.path, l.stop = "", nil
	l.mu.Unlock()
	if stop != nil {
		stop()
	}
	if path != "" {
		os.Remove(path)
	}
}

func (l *launcher) removePath(path string) {
	l.mu.Lock()
	if l.path == path {
		l.path, l.stop = "", nil
	}
	l.mu.Unlock()
	os.Remove(path)
}

// Close deletes the live launcher file and the directory.
func (l *launcher) Close() {
	l.Remove()
	if l.dir != "" {
		os.RemoveAll(l.dir)
	}
}
