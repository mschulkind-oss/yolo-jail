// Package hub owns the one live model. Every input reaches it through Apply, which runs the
// pure reducer under one lock, so inputs are applied one at a time in arrival order and the
// reducer is the only writer. Readers get copies.
package hub

import (
	"context"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
)

// Hub holds the model and tells readers when it changes.
type Hub struct {
	mu      sync.Mutex
	m       model.Model
	changed chan struct{}
	opts    model.Options
	now     func() time.Time
}

// New makes a hub for a queue that started at now().
func New(opts model.Options, now func() time.Time) *Hub {
	return &Hub{m: model.New(now()), changed: make(chan struct{}), opts: opts, now: now}
}

// Apply feeds one input to the reducer and wakes everyone waiting for a change.
func (h *Hub) Apply(in model.Input) {
	h.mu.Lock()
	h.m = model.Reduce(h.m, in, h.now())
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
}

// Model returns the current model (a value; its slices are never mutated by the reducer).
func (h *Hub) Model() model.Model {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m
}

// Changed returns a channel closed at the next Apply.
func (h *Hub) Changed() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changed
}

// Published renders the current model as the browser receives it, at the hub's clock.
func (h *Hub) Published() model.Published {
	h.mu.Lock()
	m := h.m
	h.mu.Unlock()
	return model.Publish(m, h.now(), h.opts)
}

// Now is the hub's clock.
func (h *Hub) Now() time.Time { return h.now() }

// WaitFor blocks until pred holds for the model, or ctx ends. It re-checks on every change.
func (h *Hub) WaitFor(ctx context.Context, pred func(model.Model) bool) error {
	for {
		ch := h.Changed()
		if pred(h.Model()) {
			return nil
		}
		select {
		case <-ch:
		case <-time.After(20 * time.Millisecond): // predicates may read state outside the model
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
