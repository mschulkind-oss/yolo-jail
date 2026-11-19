package mcpauthdaemon

import (
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

type onceWork struct {
	server mcpauth.OnceServer
	done   chan struct{}
	err    error
}

// oncePoll is this launch's existing poll owner, not a request-scoped refresher.
// Each approved record has at most one queued/running job, shared by its waiters.
type oncePoll struct {
	mu      sync.Mutex
	pending map[string]*onceWork
	queue   chan *onceWork
	stopped chan struct{}
}

func newOncePoll(servers int) *oncePoll {
	return &oncePoll{pending: make(map[string]*onceWork), queue: make(chan *onceWork, max(servers, 1)), stopped: make(chan struct{})}
}

func (p *oncePoll) request(server mcpauth.OnceServer) *onceWork {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := server.Name + "|" + server.Identity
	if work := p.pending[key]; work != nil {
		return work
	}
	select {
	case <-p.stopped:
		return nil
	default:
	}
	work := &onceWork{server: server, done: make(chan struct{})}
	p.pending[key] = work
	p.queue <- work // capacity is the frozen approved record count
	return work
}

func (p *oncePoll) finish(work *onceWork, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	work.err = err
	delete(p.pending, work.server.Name+"|"+work.server.Identity)
	close(work.done)
}

func (p *oncePoll) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	close(p.stopped)
	for key, work := range p.pending {
		delete(p.pending, key)
		close(work.done)
	}
}
