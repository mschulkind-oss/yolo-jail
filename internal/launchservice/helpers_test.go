package launchservice

import (
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

func signalIgnore() { signal.Ignore(syscall.SIGTERM) }

func notifyTerm(c chan os.Signal) { signal.Notify(c, syscall.SIGTERM) }

// lockedBuilder is a strings.Builder safe for the watcher goroutine and the test to share.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
