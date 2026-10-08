package nixroots

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Run watches until ctx ends. The watch is placed BEFORE the first scan, so an entry made
// during the scan is seen as an event; a queue overflow rescans.
func (w *Watcher) Run(ctx context.Context) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return fmt.Errorf("inotify: %w", err)
	}
	defer unix.Close(fd)
	if _, err := unix.InotifyAddWatch(fd, w.AutoDir, unix.IN_CREATE|unix.IN_MOVED_TO); err != nil {
		return fmt.Errorf("watch %s: %w", w.AutoDir, err)
	}
	w.logf("watching %s; scan kept %d", w.AutoDir, w.Scan())
	w.housekeep()
	every := w.Housekeeping
	if every <= 0 {
		every = DefaultHousekeeping
	}
	next := time.Now().Add(every)
	buf := make([]byte, 64*1024)
	for {
		if ctx.Err() != nil {
			return nil
		}
		if time.Now().After(next) {
			w.housekeep()
			next = time.Now().Add(every)
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, 500); err != nil && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("poll: %w", err)
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, err := unix.Read(fd, buf)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read inotify: %w", err)
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameLen := int(ev.Len)
			name := ""
			if nameLen > 0 && off+unix.SizeofInotifyEvent+nameLen <= n {
				name = strings.TrimRight(string(buf[off+unix.SizeofInotifyEvent:off+unix.SizeofInotifyEvent+nameLen]), "\x00")
			}
			off += unix.SizeofInotifyEvent + nameLen
			switch {
			case ev.Mask&unix.IN_Q_OVERFLOW != 0:
				w.logf("event queue overflowed; rescanning (scan kept %d)", w.Scan())
			case ev.Mask&unix.IN_IGNORED != 0:
				return fmt.Errorf("the watch on %s went away", w.AutoDir)
			case name != "":
				w.Consider(name, false)
			}
		}
	}
}

func (w *Watcher) housekeep() {
	released, err := w.Registry.Prune()
	for _, r := range released {
		w.logf("released %s %s: %s", r.Root.ID, r.Root.Source, r.Reason)
	}
	if err != nil {
		w.logf("lifecycle pass failed: %v", err)
	}
}
