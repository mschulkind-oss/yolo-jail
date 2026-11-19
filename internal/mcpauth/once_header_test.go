package mcpauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The header route (docs/design/mcp-auth-sharing.md §9.6, phase D of the plan): an agent that
// sends the token as a header asks for the current unexpired access token, with
// a `header` audit event and no provider or source-store work.
func TestOnceHeaderHandsOverTheCurrentToken(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker) // adopts: rt-1 redeemed, at-2 issued for 60 minutes
	token, err := f.broker.Header(context.Background(), f.server, []OnceSource{f.src})
	if err != nil || token != "at-2" {
		t.Fatalf("Header = %q, %v; want at-2", token, err)
	}
	if f.as.callCount() != 1 {
		t.Fatalf("refreshed a token with an hour left: %v", f.as.calls)
	}
	if f.actions() != "adopt,header" {
		t.Fatalf("audit = %s, want adopt,header", f.actions())
	}
}

// Even a token close to expiry is issued as-is; refresh belongs to the poll.
func TestOnceHeaderDoesNotRefreshATokenAboutToExpire(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.clock.now = f.clock.now.Add(56 * time.Minute)
	token, err := f.broker.Header(context.Background(), f.server, []OnceSource{f.src})
	if err != nil || token != "at-2" {
		t.Fatalf("Header = %q, %v; want the still-unexpired at-2", token, err)
	}
	if f.as.callCount() != 1 {
		t.Fatal("issuance redeemed a refresh token")
	}
	if e := f.entry(); e["accessToken"] != "at-2" {
		t.Fatal("issuance wrote to the source")
	}
	if f.actions() != "adopt,header" {
		t.Fatalf("audit = %s", f.actions())
	}
}

// The poll can maintain a canonical record with no local source, without writeback.
func TestOncePollMaintainsWithNoSource(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.clock.now = f.clock.now.Add(56 * time.Minute)
	if err := f.broker.Maintain(context.Background(), f.server, nil, false); err != nil {
		t.Fatal(err)
	}
	token, err := f.broker.Header(context.Background(), f.server, nil)
	if err != nil || token != "at-3" {
		t.Fatalf("Header = %q, %v; want at-3", token, err)
	}
	if e := f.entry(); e["accessToken"] != "at-2" {
		t.Fatalf("a header-only broker wrote to a source: %v", e)
	}
}

func TestOnceHeaderRefusesAServerNotSignedIn(t *testing.T) {
	f := newOnceFixture(t)
	if _, err := f.broker.Header(context.Background(), f.server, []OnceSource{f.src}); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
	if f.actions() != "" {
		t.Fatalf("audit = %s, want nothing for a refusal", f.actions())
	}
}

// A refresh that fails transiently leaves the token that is still valid in use, and once that
// one has expired the request is refused rather than handed a dead token.
func TestOnceHeaderTransientFailureKeepsAValidTokenAndRefusesAnExpiredOne(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.as.fail, f.as.status = "temporarily_unavailable", 503
	f.clock.now = f.clock.now.Add(56 * time.Minute)
	token, err := f.broker.Header(context.Background(), f.server, []OnceSource{f.src})
	if err != nil || token != "at-2" {
		t.Fatalf("Header = %q, %v; want the still-valid at-2", token, err)
	}
	f.clock.now = f.clock.now.Add(5 * time.Minute)
	if _, err := f.broker.Header(context.Background(), f.server, []OnceSource{f.src}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}
