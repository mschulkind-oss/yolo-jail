// Package sigyield lets a handler that catches a signal on purpose tell the launch's terminal arm to
// leave that signal to it, and lets a handler that ends the process on one run the arm's last words
// first.
//
// The TERMINAL ARM (internal/cli/run's ArmTerminalRestore) is the process's outermost signal arm: from
// the moment `yolo run` marks the terminal (the kitty tab's title and colors, the tmux pane) until the
// launch returns, a signal nothing else takes puts the terminal back and ends the process. Most of a
// launch has a handler of its own for some signals: the host floor's refresh records a Ctrl-C so the
// launch can decide what it means, the nix-children arm stops nix and exits, the macos-user arm works
// by phase, the TTY proxy runs its own teardown. Two handlers acting on one signal race to the exit, so
// while one of those is installed it HOLDS its signals here and the terminal arm stands back for them.
// A holder that then ends the process calls LastWords on its way out, so the terminal goes back
// regardless of which handler ended the launch.
//
// The package keeps no state but a count per signal and one function, so any package can import it.
package sigyield

import (
	"os"
	"sync"
)

var state struct {
	mu    sync.Mutex
	held  map[os.Signal]int
	words func()
}

// Hold marks sigs as taken by the caller's own handler until the returned release runs. Holds nest
// and count; release is idempotent.
func Hold(sigs ...os.Signal) (release func()) {
	state.mu.Lock()
	if state.held == nil {
		state.held = map[os.Signal]int{}
	}
	for _, s := range sigs {
		state.held[s]++
	}
	state.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			state.mu.Lock()
			defer state.mu.Unlock()
			for _, s := range sigs {
				if state.held[s]--; state.held[s] <= 0 {
					delete(state.held, s)
				}
			}
		})
	}
}

// Held reports whether some handler holds s.
func Held(s os.Signal) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.held[s] > 0
}

// SetLastWords registers f as what LastWords runs, until the returned clear. f must be safe to call
// more than once and from any goroutine.
func SetLastWords(f func()) (clear func()) {
	state.mu.Lock()
	state.words = f
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		state.words = nil
		state.mu.Unlock()
	}
}

// LastWords runs the registered last words, if any. A holder calls it just before it ends the process
// on a signal it held.
func LastWords() {
	state.mu.Lock()
	f := state.words
	state.mu.Unlock()
	if f != nil {
		f()
	}
}
