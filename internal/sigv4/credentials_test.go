package sigv4

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// credServer plays aws-auth's container-credentials adapter.
type credServer struct {
	srv      *httptest.Server
	hits     atomic.Int64
	mu       sync.Mutex
	expires  time.Time
	status   int
	body     string
	gotAuth  string
	delay    time.Duration
	keyIndex int
}

func newCredServer(t *testing.T, expires time.Time) *credServer {
	t.Helper()
	cs := &credServer{expires: expires, status: http.StatusOK}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.hits.Add(1)
		cs.mu.Lock()
		cs.gotAuth = r.Header.Get("Authorization")
		status, body, delay := cs.status, cs.body, cs.delay
		cs.keyIndex++
		idx := cs.keyIndex
		exp := cs.expires
		cs.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		fmt.Fprintf(w, `{"AccessKeyId":"ASIA%d","SecretAccessKey":"secret-%d","Token":"token-%d","Expiration":%q}`,
			idx, idx, idx, exp.UTC().Format(time.RFC3339))
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

func (cs *credServer) set(f func(*credServer)) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	f(cs)
}

func TestChainOrderIsStaticThenContainerThenBearer(t *testing.T) {
	cs := newCredServer(t, time.Now().Add(time.Hour))
	full := Env{AccessKeyID: "AKIDSTATIC", SecretAccessKey: "s", ContainerURI: cs.srv.URL, Bearer: "b"}

	got, err := (&Chain{Env: full}).Resolve(context.Background())
	if err != nil || got.Creds.AccessKeyID != "AKIDSTATIC" || got.Bearer != "" {
		t.Fatalf("static pair must win: %v %v", got, err)
	}
	full.AccessKeyID, full.SecretAccessKey = "", ""
	got, err = (&Chain{Env: full}).Resolve(context.Background())
	if err != nil || got.Creds.AccessKeyID != "ASIA1" || got.Bearer != "" {
		t.Fatalf("the container endpoint must come before the bearer: %v %v", got, err)
	}
	full.ContainerURI = ""
	got, err = (&Chain{Env: full}).Resolve(context.Background())
	if err != nil || got.Bearer != "b" || got.Creds.valid() {
		t.Fatalf("the bearer is last, and alone: %v %v", got, err)
	}
	_, err = (&Chain{}).Resolve(context.Background())
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("no source must be ErrNoCredential, got %v", err)
	}
}

func TestChainResolvesLazilyAndSingleFlight(t *testing.T) {
	cs := newCredServer(t, time.Now().Add(time.Hour))
	cs.set(func(c *credServer) { c.delay = 50 * time.Millisecond })
	chain := &Chain{Env: Env{ContainerURI: cs.srv.URL}}
	if n := cs.hits.Load(); n != 0 {
		t.Fatalf("constructing a chain fetched %d times; resolution is lazy", n)
	}
	var wg sync.WaitGroup
	ids := make([]string, 16)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := chain.Resolve(context.Background())
			if err != nil {
				t.Errorf("resolve: %v", err)
				return
			}
			ids[i] = r.Creds.AccessKeyID
		}()
	}
	wg.Wait()
	if n := cs.hits.Load(); n != 1 {
		t.Fatalf("16 concurrent requests made %d fetches; want exactly one", n)
	}
	for _, id := range ids {
		if id != "ASIA1" {
			t.Fatalf("a waiter got %q, not the one fetched set", id)
		}
	}
}

func TestChainRefreshesBeforeExpiration(t *testing.T) {
	now := time.Now()
	clock := now
	cs := newCredServer(t, now.Add(time.Hour))
	chain := &Chain{Env: Env{ContainerURI: cs.srv.URL}, Now: func() time.Time { return clock }}
	first, _ := chain.Resolve(context.Background())
	clock = now.Add(50 * time.Minute) // outside the refresh window
	again, _ := chain.Resolve(context.Background())
	if cs.hits.Load() != 1 || again.Creds != first.Creds {
		t.Fatalf("a set 10 minutes from expiry must be served from cache (hits %d)", cs.hits.Load())
	}
	clock = now.Add(56 * time.Minute) // inside RefreshBefore
	cs.set(func(c *credServer) { c.expires = now.Add(2 * time.Hour) })
	refreshed, err := chain.Resolve(context.Background())
	if err != nil || cs.hits.Load() != 2 || refreshed.Creds.AccessKeyID != "ASIA2" {
		t.Fatalf("inside five minutes of Expiration the chain must fetch a new set: %v hits=%d %v",
			refreshed, cs.hits.Load(), err)
	}
}

func TestChainNeverServesAnExpiredSet(t *testing.T) {
	now := time.Now()
	clock := now
	cs := newCredServer(t, now.Add(time.Hour))
	chain := &Chain{Env: Env{ContainerURI: cs.srv.URL}, Now: func() time.Time { return clock }}
	if _, err := chain.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs.set(func(c *credServer) {
		c.status = http.StatusBadRequest
		c.body = `{"Code":"SessionExpired","Message":"run aws sso login --profile work"}`
	})

	clock = now.Add(57 * time.Minute) // refresh fails, but the set has three minutes left
	if r, err := chain.Resolve(context.Background()); err != nil || r.Creds.AccessKeyID != "ASIA1" {
		t.Fatalf("a failed refresh inside the window may keep signing with an unexpired set: %v %v", r, err)
	}
	clock = now.Add(59*time.Minute + 45*time.Second) // refresh fails and the set is about to expire
	_, err := chain.Resolve(context.Background())
	var ee *EndpointError
	if !errors.As(err, &ee) || !strings.Contains(ee.Message, "aws sso login --profile work") {
		t.Fatalf("an expiring set must not be served; want aws-auth's own refusal, got %v", err)
	}

	cs.set(func(c *credServer) {
		c.status, c.body = http.StatusOK, ""
		c.expires = clock.Add(-time.Minute)
	})
	if _, err := chain.Resolve(context.Background()); err == nil {
		t.Fatal("a set that arrives already expired must be refused")
	}
}

func TestChainInvalidateForcesAFetch(t *testing.T) {
	cs := newCredServer(t, time.Now().Add(time.Hour))
	chain := &Chain{Env: Env{ContainerURI: cs.srv.URL}}
	_, _ = chain.Resolve(context.Background())
	chain.Invalidate()
	if r, _ := chain.Resolve(context.Background()); cs.hits.Load() != 2 || r.Creds.AccessKeyID != "ASIA2" {
		t.Fatalf("Invalidate must force the next Resolve to fetch (hits %d)", cs.hits.Load())
	}
}

func TestChainSendsTheContainerAuthorizationToken(t *testing.T) {
	cs := newCredServer(t, time.Now().Add(time.Hour))
	_, err := (&Chain{Env: Env{ContainerURI: cs.srv.URL, ContainerToken: "tok-1"}}).Resolve(context.Background())
	if err != nil || cs.gotAuth != "tok-1" {
		t.Fatalf("AWS_CONTAINER_AUTHORIZATION_TOKEN must ride as Authorization: %q %v", cs.gotAuth, err)
	}
}

func TestChainUnreachableEndpointIsAnUnreachableError(t *testing.T) {
	cs := newCredServer(t, time.Now())
	uri := cs.srv.URL
	cs.srv.Close()
	_, err := (&Chain{Env: Env{ContainerURI: uri}}).Resolve(context.Background())
	var ue *UnreachableError
	if !errors.As(err, &ue) {
		t.Fatalf("a dead adapter must be UnreachableError, got %T %v", err, err)
	}
}

func TestChainRefusesANonLoopbackHTTPEndpoint(t *testing.T) {
	var hit atomic.Bool
	_, err := (&Chain{Env: Env{ContainerURI: "http://192.0.2.10/credentials", ContainerToken: "tok"},
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			hit.Store(true)
			return nil, errors.New("dialed")
		})}}).Resolve(context.Background())
	if err == nil || hit.Load() {
		t.Fatalf("an http URI off loopback must be refused before any request (err %v, dialed %v)", err, hit.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestChainErrorsNeverCarryACredential(t *testing.T) {
	cs := newCredServer(t, time.Now().Add(time.Hour))
	cs.set(func(c *credServer) { c.status = http.StatusForbidden; c.body = `{"Code":"Denied","Message":"no"}` })
	_, err := (&Chain{Env: Env{ContainerURI: cs.srv.URL, ContainerToken: "the-container-token"}}).Resolve(context.Background())
	if err == nil || strings.Contains(err.Error(), "the-container-token") {
		t.Fatalf("error leaks the container token or is nil: %v", err)
	}
}
