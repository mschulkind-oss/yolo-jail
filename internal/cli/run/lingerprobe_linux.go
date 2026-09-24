//go:build linux

package run

// The halves of the Window A probe's wiring that only the Linux tty proxy can
// reach: it is the only proxy with a pty and a stdin loop to observe
// (proxy_linux.go). The non-Linux fallback runs a plain foreground exec.

import (
	"fmt"
	"time"
)

// maxInputNotes caps the forwarded-input notes one launch writes after the
// death. A user waiting out a hang may mash keys; the first few dozen say
// everything the timing can.
const maxInputNotes = 40

func (s *lingerSlot) setPtyMode(m func() string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.ptyMode = m
	s.mu.Unlock()
}

// noteForwardedInput is the proxy's Observer.Input. Every chunk updates the
// last-input clock (two atomic stores); only chunks after the death are
// written down, as a SIZE and at most the name "ctrl-c" — never content.
func (o *Options) noteForwardedInput(n int, key string) {
	s := o.linger
	if s == nil {
		return
	}
	now := time.Now()
	dead := s.deathSeen.Load()
	s.lastInput.Store(now.UnixNano())
	s.lastInputBytes.Store(int64(n))
	s.lastInputCtrlC.Store(key != "")
	s.inputAfterDead.Store(dead)
	if !dead {
		return
	}
	if key != "" {
		s.firstCtrlC.CompareAndSwap(0, now.UnixNano())
	}
	if s.inputNotes.Add(1) > maxInputNotes {
		return
	}
	detail := fmt.Sprintf("bytes=%d", n)
	if key != "" {
		detail += " key=" + key
	}
	o.Perf.Note("child.input", detail)
}
