package mcpauthdaemon

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestFramedExpiredHeadersCoalesceInTheExistingPoll(t *testing.T) {
	f := newOnceDaemonFixture(t)
	store := adoptHeaderFixture(t, f)
	f.b.share.Sources = nil
	passed := make(chan struct{}, 8)
	restore := setOnceSeams(make(chan time.Time), func() { passed <- struct{}{} })
	defer restore()
	f.b.poll = newOncePoll(len(f.b.share.Once))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { f.b.runOnce(stop); close(done) }()
	select {
	case <-passed:
	case <-time.After(30 * time.Second):
		t.Fatal("poll startup did not finish")
	}
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("poll did not settle")
		}
	}()
	expireHeaderFixture(t, store)
	previous := http.DefaultTransport
	held := &heldMCPRefresh{next: previous, entered: make(chan struct{}), release: make(chan struct{})}
	http.DefaultTransport = held
	t.Cleanup(func() { http.DefaultTransport = previous })
	var release sync.Once
	defer release.Do(func() { close(held.release) })
	queued := make(chan struct{}, 2)
	previousQueued := afterHeaderWaitQueued
	afterHeaderWaitQueued = func() { queued <- struct{}{} }
	t.Cleanup(func() { afterHeaderWaitQueued = previousQueued })
	replies := make(chan headerReply, 2)
	for range 2 {
		go func() { replies <- askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"}) }()
	}
	for range 2 {
		select {
		case <-queued:
		case <-time.After(30 * time.Second):
			t.Fatal("framed request did not reach poll-owned wait")
		}
	}
	select {
	case <-held.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("queued work did not enter provider")
	}
	f.b.poll.mu.Lock()
	pending := len(f.b.poll.pending)
	f.b.poll.mu.Unlock()
	if pending != 1 || held.calls.Load() != 1 {
		t.Fatal("concurrent waiters created multiple per-record jobs")
	}
	release.Do(func() { close(held.release) })
	for range 2 {
		select {
		case reply := <-replies:
			if reply.rc != 0 || reply.stdout != "Bearer at-3\n" {
				t.Fatalf("coalesced issuance: %+v", reply)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("coalesced waiter did not finish")
		}
	}
	if held.calls.Load() != 1 {
		t.Fatal("coalesced work redeemed more than once")
	}
}
