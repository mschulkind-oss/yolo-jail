package openaiauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refreshFunc func(context.Context, string) (Tokens, error)

func (f refreshFunc) Refresh(ctx context.Context, token string) (Tokens, error) {
	return f(ctx, token)
}

func testBroker(t *testing.T, refresher Refresher) (Broker, time.Time) {
	t.Helper()
	now := time.Unix(2_000_000_000, 0)
	dir := t.TempDir()
	b := Broker{
		StatePath: filepath.Join(dir, "credentials.json"),
		LockPath:  filepath.Join(dir, "refresh.lock"),
		Refresher: refresher,
		Now:       func() time.Time { return now },
	}
	return b, now
}

func initialState(now time.Time) State {
	return State{
		Version:      stateVersion,
		AccessToken:  "access-1",
		IDToken:      "id-1",
		RefreshToken: "refresh-1",
		ExpiresAtMS:  now.Add(time.Minute).UnixMilli(),
		AccountID:    "account-1",
		Generation:   1,
	}
}

func TestConcurrentCallersRedeemExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	now := time.Unix(2_000_000_000, 0)
	b, _ := testBroker(t, refreshFunc(func(_ context.Context, token string) (Tokens, error) {
		if token != "refresh-1" {
			t.Errorf("upstream token = %q, want refresh-1", token)
		}
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return Tokens{AccessToken: "access-2", IDToken: "id-2", RefreshToken: "refresh-2", ExpiresAt: now.Add(time.Hour)}, nil
	}))
	if err := writeState(b.StatePath, initialState(now)); err != nil {
		t.Fatal(err)
	}

	const callers = 12
	results := make(chan Result, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := b.Refresh(context.Background(), Request{CallerGeneration: 1})
			results <- result
			errs <- err
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result.State.RefreshToken != "refresh-2" || result.State.Generation != 2 {
			t.Fatalf("result state = %#v, want generation 2", result.State)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream refresh calls = %d, want 1", got)
	}
}

func TestStaleCallerNeverRedeemsEvenWhenCurrentStateIsDue(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) {
		calls.Add(1)
		return Tokens{}, errors.New("must not run")
	}))
	state := initialState(now)
	state.RefreshToken = "refresh-current"
	if err := writeState(b.StatePath, state); err != nil {
		t.Fatal(err)
	}
	state.Generation = 2
	if err := writeState(b.StatePath, state); err != nil {
		t.Fatal(err)
	}
	result, err := b.Refresh(context.Background(), Request{CallerGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionStale || result.State.RefreshToken != "refresh-current" {
		t.Fatalf("result = %#v", result)
	}
	if calls.Load() != 0 {
		t.Fatal("stale caller reached upstream")
	}
}

func TestPiStyleRequestRefreshesWithoutReceivingRefreshToken(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	b, _ := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) {
		return Tokens{AccessToken: "access-2", IDToken: "id-2", RefreshToken: "refresh-2", ExpiresAt: now.Add(time.Hour)}, nil
	}))
	if err := writeState(b.StatePath, initialState(now)); err != nil {
		t.Fatal(err)
	}
	result, err := b.Refresh(context.Background(), Request{Caller: "pi/workspace-a"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionRefreshed || result.State.AccessToken != "access-2" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRefreshPreservesRotatingFieldsOmittedByUpstream(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	b, _ := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) {
		return Tokens{AccessToken: "access-2", ExpiresAt: now.Add(time.Hour)}, nil
	}))
	if err := writeState(b.StatePath, initialState(now)); err != nil {
		t.Fatal(err)
	}
	result, err := b.Refresh(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if result.State.IDToken != "id-1" || result.State.RefreshToken != "refresh-1" {
		t.Fatalf("refresh discarded omitted rotating fields: %#v", result.State)
	}
	current, err := b.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current != result.State {
		t.Fatalf("Current() = %#v, want %#v", current, result.State)
	}
}

func TestRefreshContinuesAfterCallerCancellation(t *testing.T) {
	called := make(chan struct{})
	now := time.Unix(2_000_000_000, 0)
	b, _ := testBroker(t, refreshFunc(func(ctx context.Context, _ string) (Tokens, error) {
		if err := ctx.Err(); err != nil {
			t.Fatalf("refresh inherited caller cancellation: %v", err)
		}
		close(called)
		return Tokens{AccessToken: "access-2", IDToken: "id-2", RefreshToken: "refresh-2", ExpiresAt: now.Add(time.Hour)}, nil
	}))
	if err := writeState(b.StatePath, initialState(now)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Refresh(ctx, Request{}); err != nil {
		t.Fatal(err)
	}
	<-called
}

func TestReplaceAndLogoutUseCanonicalGeneration(t *testing.T) {
	b, now := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) { return Tokens{}, nil }))
	first, err := b.Replace(Tokens{
		AccessToken: "access-1", IDToken: "id-1", RefreshToken: "refresh-1", ExpiresAt: now.Add(time.Hour), AccountID: "account-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Replace(Tokens{
		AccessToken: "access-2", IDToken: "id-2", RefreshToken: "refresh-2", ExpiresAt: now.Add(2 * time.Hour), AccountID: "account-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || second.Generation != 2 {
		t.Fatalf("replacement generations = %d, %d; want 1, 2", first.Generation, second.Generation)
	}
	if err := b.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(b.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadState after logout error = %v, want file not found", err)
	}
	if err := b.Logout(); err != nil {
		t.Fatalf("second Logout = %v, want idempotent success", err)
	}
}

func TestReplaceRepairsCorruptCanonicalState(t *testing.T) {
	b, now := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) { return Tokens{}, nil }))
	if err := os.WriteFile(b.StatePath, []byte(`{"refresh_token":"truncated`), 0o600); err != nil {
		t.Fatal(err)
	}
	repaired, err := b.Replace(Tokens{
		AccessToken: "access-new", IDToken: "id-new", RefreshToken: "refresh-new",
		ExpiresAt: now.Add(time.Hour), AccountID: "account-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Generation != 1 || repaired.RefreshToken != "refresh-new" {
		t.Fatalf("repaired state = %#v", repaired)
	}
	current, err := b.Current()
	if err != nil || current != repaired {
		t.Fatalf("Current after repair = %#v, %v", current, err)
	}
}

func TestPermanentFailureMarksLoginRequiredWithoutDeletingCredential(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) {
		calls.Add(1)
		return Tokens{}, &RefreshError{Kind: ErrorPermanent, Code: "invalid_grant", Err: errors.New("redemption refused")}
	}))
	want := initialState(now)
	if err := writeState(b.StatePath, want); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Refresh(context.Background(), Request{}); err == nil {
		t.Fatal("Refresh succeeded, want permanent error")
	}
	got, err := loadState(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LoginRequired || got.LastErrorCode != "invalid_grant" {
		t.Fatalf("state = %#v, want inspectable login-required marker", got)
	}
	if got.RefreshToken != want.RefreshToken || got.AccessToken != want.AccessToken || got.Generation != want.Generation {
		t.Fatalf("credentials changed on permanent failure: %#v", got)
	}
	if b.RefreshDue() {
		t.Fatal("login-required state remains due for proactive refresh")
	}
	if _, err := b.Refresh(context.Background(), Request{}); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("second Refresh error = %v, want ErrLoginRequired", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("permanent credential was redeemed %d times, want 1", calls.Load())
	}
	result, err := b.Refresh(context.Background(), Request{CallerGeneration: got.Generation + 1})
	if err != nil || result.Decision != DecisionStale {
		t.Fatalf("stale repair = %#v, %v; want stale generation", result, err)
	}
}

func TestWriteStateIsPrivateAndDiagnosticsContainOnlyFingerprints(t *testing.T) {
	var got Event
	b, now := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) {
		return Tokens{}, errors.New("must not run")
	}))
	b.Observe = func(event Event) { got = event }
	state := initialState(now)
	state.ExpiresAtMS = now.Add(time.Hour).UnixMilli()
	if err := writeState(b.StatePath, state); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Refresh(context.Background(), Request{Caller: "codex/workspace-a"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("state mode = %o, want 600", gotMode)
	}
	if got.AccessFingerprint != TokenFingerprint(state.AccessToken) || got.RefreshFingerprint != TokenFingerprint(state.RefreshToken) {
		t.Fatalf("event = %#v", got)
	}
	if got.AccessFingerprint == state.AccessToken || got.RefreshFingerprint == state.RefreshToken {
		t.Fatalf("diagnostics exposed raw token: %#v", got)
	}
}

func TestRefreshDueUsesFiveMinuteDefault(t *testing.T) {
	b, now := testBroker(t, refreshFunc(func(context.Context, string) (Tokens, error) { return Tokens{}, nil }))
	state := initialState(now)
	state.ExpiresAtMS = now.Add(DefaultRefreshLead).UnixMilli()
	if err := writeState(b.StatePath, state); err != nil {
		t.Fatal(err)
	}
	if !b.RefreshDue() {
		t.Fatal("credential at refresh boundary is not due")
	}
	state.ExpiresAtMS++
	if err := writeState(b.StatePath, state); err != nil {
		t.Fatal(err)
	}
	if b.RefreshDue() {
		t.Fatal("credential beyond refresh boundary is due")
	}
}
