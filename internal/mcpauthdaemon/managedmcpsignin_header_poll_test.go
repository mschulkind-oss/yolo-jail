package mcpauthdaemon

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

type heldMCPRefresh struct {
	next    http.RoundTripper
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int64
}

func (h *heldMCPRefresh) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, "/token") {
		h.calls.Add(1)
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return h.next.RoundTrip(request)
}

func adoptHeaderFixture(t *testing.T, f *onceDaemonFixture) *mcpauth.CanonicalStore {
	t.Helper()
	once, err := f.b.newOnceBroker()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.b.passAll(context.Background(), once, f.b.onceSources()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.refreshs:
	case <-time.After(30 * time.Second):
		t.Fatal("fixture adoption did not redeem")
	}
	store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func expireHeaderFixture(t *testing.T, store *mcpauth.CanonicalStore) {
	t.Helper()
	if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
		file.Records[0].ExpiresAtMS = time.Now().Add(-time.Minute).UnixMilli()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredHeaderPollCommitsAfterFramedRequesterCancellation(t *testing.T) {
	f := newOnceDaemonFixture(t)
	store := adoptHeaderFixture(t, f)
	f.b.share.Sources = nil // header-only launch maintains a previously adopted record
	passed := make(chan struct{}, 8)
	restore := setOnceSeams(make(chan time.Time), func() { passed <- struct{}{} })
	defer restore()
	f.b.poll = newOncePoll(len(f.b.share.Once))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { f.b.runOnce(stop); close(done) }()
	select {
	case <-passed:
	case <-time.After(30 * time.Second):
		t.Fatal("poll startup pass did not finish")
	}
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("poll did not stop after durable work")
		}
	}()
	expireHeaderFixture(t, store)
	previous := http.DefaultTransport
	held := &heldMCPRefresh{next: previous, entered: make(chan struct{}), release: make(chan struct{})}
	http.DefaultTransport = held
	t.Cleanup(func() { http.DefaultTransport = previous })
	var release sync.Once
	defer release.Do(func() { close(held.release) })
	oldContext := newHeaderContext
	t.Cleanup(func() { newHeaderContext = oldContext })
	canceled := make(chan context.CancelFunc, 2)
	newHeaderContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		canceled <- cancel
		return ctx, cancel
	}
	replies := make(chan headerReply, 1)
	go func() { replies <- askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"}) }()
	var cancel context.CancelFunc
	select {
	case cancel = <-canceled:
	case <-time.After(30 * time.Second):
		t.Fatal("framed requester did not begin")
	}
	select {
	case <-held.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("expired header never woke the existing poll")
	}
	cancel()
	select {
	case reply := <-replies:
		if reply.rc != 1 || reply.stdout != "" || !strings.Contains(reply.stderr, "pending") {
			t.Fatalf("cancelled requester: %+v", reply)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cancelled requester did not return")
	}
	release.Do(func() { close(held.release) })
	select {
	case <-passed:
	case <-time.After(30 * time.Second):
		t.Fatal("poll commit did not finish after requester cancellation")
	}
	file, err := store.ReadStatus()
	if err != nil || len(file.Records) != 1 || file.Records[0].AccessToken != "at-3" {
		t.Fatal("requester cancellation lost durable rotated token")
	}
	newHeaderContext = oldContext
	reply := askBroker(t, f.b, map[string]any{"op": "header", "server": "slack", "format": "json"})
	if reply.rc != 0 || reply.stdout != "{\"Authorization\":\"Bearer at-3\"}\n" || held.calls.Load() != 1 {
		t.Fatalf("next header did not observe committed work: %+v calls=%d", reply, held.calls.Load())
	}
}

func TestUnexpiredHostAndJailHeadersIgnoreRefreshLock(t *testing.T) {
	f := newOnceDaemonFixture(t)
	store := adoptHeaderFixture(t, f)
	release, err := store.LockRefresh(f.b.share.Once[0].Name, f.b.share.Once[0].Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var calls atomic.Int64
	previous := http.DefaultTransport
	http.DefaultTransport = countingMCPTransport{Next: previous, Calls: &calls}
	defer func() { http.DefaultTransport = previous }()
	reply := askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"})
	if reply.rc != 0 || reply.stdout != "Bearer at-2\n" {
		t.Fatalf("jail issuance blocked on refresh lock: %+v", reply)
	}
	token, err := HostHeader(context.Background(), f.b.canonical.Dir, f.b.share, "slack", nil)
	if err != nil || token != "at-2" || calls.Load() != 0 {
		t.Fatal("host issuance waited/refreshed under record lock")
	}
	expireHeaderFixture(t, store)
	if token, err := HostHeader(context.Background(), f.b.canonical.Dir, f.b.share, "slack", nil); err == nil || token != "" || calls.Load() != 0 {
		t.Fatal("expired host issuance redeemed or returned expired token")
	}
}
