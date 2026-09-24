//go:build linux

package lingerprobe

import (
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// supported: this platform has /proc and inotify.
const supported = true

// exitWatch is an inotify watch on conmon's exit directory. It costs ONE
// goroutine parked in the netpoller for the jail's lifetime — no thread, no
// process, no timer, and nothing at all happens until the directory changes.
//
// The fd is NON-BLOCKING and wrapped by os.NewFile, which registers it with the
// Go netpoller; that is what lets Close interrupt the pending Read. A blocking
// inotify fd closed from another goroutine leaves the reader parked in read(2)
// forever, which on this path would be a goroutine outliving its purpose.
type exitWatch struct {
	f     *os.File
	death chan time.Time
	once  sync.Once
}

func watchExitFile(dir, id string) (*exitWatch, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	// IN_MOVED_TO for conmon's write-temp-then-rename; IN_CLOSE_WRITE for a
	// writer that writes the final name directly.
	if _, err := unix.InotifyAddWatch(fd, dir, unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	w := &exitWatch{f: os.NewFile(uintptr(fd), "inotify:"+dir), death: make(chan time.Time, 1)}
	go w.loop(id)
	// AFTER the watch exists, so there is no window in which a death is in
	// neither the directory listing nor the event stream.
	if exitFilePresent(dir, id) {
		w.fire(time.Now())
	}
	return w, nil
}

func (w *exitWatch) fire(at time.Time) {
	w.once.Do(func() { w.death <- at })
}

func (w *exitWatch) loop(id string) {
	buf := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			return // closed (Stop) or broken: either way, nothing more to watch
		}
		at := time.Now()
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameStart := off + unix.SizeofInotifyEvent
			nameEnd := nameStart + int(ev.Len)
			if nameEnd > n {
				break
			}
			name := string(buf[nameStart:nameEnd])
			for len(name) > 0 && name[len(name)-1] == 0 {
				name = name[:len(name)-1]
			}
			if matchesExitFile(name, id) {
				w.fire(at)
				return
			}
			off = nameEnd
		}
	}
}

func (w *exitWatch) Death() <-chan time.Time { return w.death }

func (w *exitWatch) Close() { _ = w.f.Close() }

// exitFilePresent covers the race the watch cannot: a container that died
// before the watch was added.
func exitFilePresent(dir, id string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if matchesExitFile(e.Name(), id) {
			return true
		}
	}
	return false
}
