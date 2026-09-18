package awsauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingRunner serves one canned Output and counts how many times it ran. A mint
// count is the real assertion behind every caching claim here.
func countingRunner(out Output, calls *atomic.Int32) Runner {
	return func(_ context.Context, _ []string) Output {
		calls.Add(1)
		return out
	}
}

func testBroker(t *testing.T, cfg Config, run Runner) (Broker, time.Time) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	dir := t.TempDir()
	b := Broker{
		StatePath: filepath.Join(dir, StateFileName),
		LockPath:  filepath.Join(dir, LockFileName),
		Config:    cfg,
		Minter:    Minter{Run: run, Binary: "aws"},
		Now:       func() time.Time { return now },
	}
	return b, now
}

func unnarrowed(profile string) Config {
	return Config{Profile: profile, Narrowing: Narrowing{Kind: NarrowNone}}
}

// expiringIn builds `--format process` output whose Expiration is d from now.
func expiringIn(now time.Time, d time.Duration, keyID string) Output {
	return okOut(`{"Version":1,"AccessKeyId":"` + keyID + `","SecretAccessKey":"zzsecretzz",` +
		`"SessionToken":"zztokenzz","Expiration":"` + now.Add(d).UTC().Format(time.RFC3339) + `"}`)
}

func TestAColdCacheMintsAndAWarmCacheDoesNot(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIA1"), &calls)

	first, err := b.Fetch(context.Background(), "jail-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.Decision != DecisionMinted {
		t.Errorf("first decision = %q, want %q", first.Decision, DecisionMinted)
	}

	// THE WARM SERVE. The 200 ms adapter budget is met by not minting at all; the
	// assertion that matters is the mint count, and the elapsed bound is the budget
	// restated loosely enough not to flake.
	start := time.Now()
	second, err := b.Fetch(context.Background(), "jail-b")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("a warm serve took %v; the adapter's whole budget is 200ms", elapsed)
	}
	if second.Decision != DecisionCached {
		t.Errorf("second decision = %q, want %q", second.Decision, DecisionCached)
	}
	if second.Credential.AccessKeyID != first.Credential.AccessKeyID {
		t.Error("a warm serve returned a different credential")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("minted %d times across two fetches, want 1", got)
	}
}

// TestConcurrentFetchesMintExactlyOnce pins the reload-under-lock. Deleting the
// reload inside withLock makes this fail with a mint per caller.
func TestConcurrentFetchesMintExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	b.Minter.Run = func(_ context.Context, _ []string) Output {
		if calls.Add(1) == 1 {
			once.Do(func() { close(started) })
		}
		<-release
		return expiringIn(now, time.Hour, "ASIA1")
	}

	const callers = 12
	var wg sync.WaitGroup
	results := make(chan Result, callers)
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := b.Fetch(context.Background(), "jail")
			results <- r
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
			t.Fatalf("concurrent fetch failed: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("minted %d times for %d concurrent fetches, want 1", got, callers)
	}
	minted := 0
	for r := range results {
		if r.Credential.AccessKeyID != "ASIA1" {
			t.Errorf("caller got %q", r.Credential.AccessKeyID)
		}
		if r.Decision == DecisionMinted {
			minted++
		}
	}
	if minted != 1 {
		t.Errorf("%d callers reported having minted, want 1", minted)
	}
}

// TestACredentialInsideTheRemintWindowIsRemintedRatherThanServed: the re-mint lead
// is 10 minutes, twice the SDK's own 5-minute refresh window, so a credential the SDK
// would consider expiring is already replaced.
func TestACredentialInsideTheRemintWindowIsRemintedRatherThanServed(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, 9*time.Minute, "ASIA1"), &calls)
	if _, err := b.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	// The cached credential has 9 minutes left, inside the 10-minute lead, so the
	// next fetch mints again rather than serving it.
	if _, err := b.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("minted %d times, want 2 — a credential inside the re-mint window was served", got)
	}
	if RemintLead != 10*time.Minute {
		t.Errorf("RemintLead = %v, want 10m (design §8, twice the SDK's 5-minute window)", RemintLead)
	}
}

// TestACacheEntryFromADifferentNarrowingIsAMiss is the guard the narrowing digest
// exists for. Removing the digest comparison in warm() makes this fail by serving a
// WIDER credential to a narrower configuration — the one outcome this feature cannot
// afford, since the narrowing is the only defence the endpoint has.
func TestACacheEntryFromADifferentNarrowingIsAMiss(t *testing.T) {
	var calls atomic.Int32
	wide, now := testBroker(t, unnarrowed("p"), nil)
	wide.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIAWIDE"), &calls)
	if _, err := wide.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}

	narrow := wide
	narrow.Config = Config{Profile: "p", Narrowing: Narrowing{
		Kind: NarrowRole, RoleARN: "arn:aws:iam::1:role/r"}}
	narrow.Minter.Run = countingRunner(okOut(assumeJSON), &calls)
	got, err := narrow.Fetch(context.Background(), "jail")
	if err != nil {
		t.Fatal(err)
	}
	if got.Credential.AccessKeyID == "ASIAWIDE" {
		t.Fatal("the un-narrowed cache entry was served to the narrowed configuration")
	}
	if got.Decision != DecisionMinted {
		t.Errorf("decision = %q, want %q", got.Decision, DecisionMinted)
	}
}

// TestRemintDueIsTheTickersDecision covers the proactive minter's whole decision:
// pre-mint on the ticker, never inside a request (design R1).
func TestRemintDueIsTheTickersDecision(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIA1"), &calls)

	if !b.RemintDue() {
		t.Error("a cold cache is not due; the first request would pay for the mint")
	}
	if _, err := b.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	if b.RemintDue() {
		t.Error("a credential with an hour left is due")
	}

	// Advance the clock to inside the window.
	later := b
	later.Now = func() time.Time { return now.Add(55 * time.Minute) }
	if !later.RemintDue() {
		t.Error("a credential with 5 minutes left is not due")
	}

	// A broker with no profile has nothing to mint and must not spin.
	blank := b
	blank.Config = Config{}
	if blank.RemintDue() {
		t.Error("an unconfigured broker reported a re-mint due")
	}

	// HALF the lead, so the window cannot be stepped over by one tick.
	if b.TickInterval() != RemintLead/2 {
		t.Errorf("TickInterval = %v, want half the re-mint lead", b.TickInterval())
	}
}

// TestStatusIsFingerprintOnly: this view feeds a `yolo check` line and an audit
// record, and neither has any use for the secret.
func TestStatusIsFingerprintOnly(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIASECRETKEY"), &calls)
	if _, err := b.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	status := b.Status()
	if status["cached"] != true {
		t.Fatalf("status = %v", status)
	}
	if status["fingerprint"] != Fingerprint("ASIASECRETKEY") {
		t.Errorf("fingerprint = %v", status["fingerprint"])
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ASIASECRETKEY", "zzsecretzz", "zztokenzz"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("status view carries %q: %s", secret, encoded)
		}
	}
	// An unminted broker still answers, saying only that nothing is cached.
	empty, _ := testBroker(t, unnarrowed("p"), nil)
	if empty.Status()["cached"] != false {
		t.Errorf("an empty cache reported cached: %v", empty.Status())
	}
}

// TestTheStateFileIs0600InA0700Dir: the mode is the host-side, single-user half of
// the story. What keeps this file out of a jail is the manifest's state_files, not
// this mode — but a world-readable credential cache on a shared host is its own bug.
func TestTheStateFileIs0600InA0700Dir(t *testing.T) {
	var calls atomic.Int32
	dir := filepath.Join(t.TempDir(), "nested", "aws-auth")
	b := Broker{
		StatePath: filepath.Join(dir, StateFileName),
		LockPath:  filepath.Join(dir, LockFileName),
		Config:    unnarrowed("p"),
		Now:       func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	b.Minter = Minter{Run: countingRunner(expiringIn(b.Now(), time.Hour, "ASIA1"), &calls)}
	if _, err := b.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode = %04o, want 0600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("state dir mode = %04o, want 0700", perm)
	}
	// The lock lives BESIDE the state file, so every process that can write the
	// cache contends for one inode.
	if _, err := os.Stat(b.LockPath); err != nil {
		t.Errorf("no lock file beside the state file: %v", err)
	}
	if filepath.Dir(b.LockPath) != filepath.Dir(b.StatePath) {
		t.Error("the lock is not beside the state file")
	}
}

// TestAMintFailureReachesTheCallerAsAMintError: the daemon turns this straight into
// the 4xx body, so the type has to survive the broker.
func TestAMintFailureReachesTheCallerAsAMintError(t *testing.T) {
	var events []Event
	b, _ := testBroker(t, unnarrowed("bedrock"), nil)
	b.Minter.Run = func(_ context.Context, _ []string) Output {
		return failOut("Error loading SSO Token: Token for https://x/start does not exist")
	}
	b.Observe = func(e Event) { events = append(events, e) }
	_, err := b.Fetch(context.Background(), "jail-1")
	var mintErr *MintError
	if !errors.As(err, &mintErr) {
		t.Fatalf("err = %v (%T), want a *MintError", err, err)
	}
	if mintErr.Kind != FailureLoginRequired {
		t.Errorf("kind = %q", mintErr.Kind)
	}
	if len(events) != 1 {
		t.Fatalf("%d events, want 1", len(events))
	}
	if events[0].ErrorKind != FailureLoginRequired || events[0].Caller != "jail-1" {
		t.Errorf("event = %+v", events[0])
	}
	if events[0].Fingerprint != "(none)" {
		t.Errorf("a failed mint emitted a credential fingerprint: %+v", events[0])
	}
	// Nothing was cached, so the next fetch tries again rather than serving a hole.
	if !b.RemintDue() {
		t.Error("a failed mint left the cache looking warm")
	}
}

// TestACorruptCacheIsAMissRatherThanAnOutage: the only thing in the file is a
// credential that can be re-minted in one shell-out.
func TestACorruptCacheIsAMissRatherThanAnOutage(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIA1"), &calls)
	if err := os.MkdirAll(filepath.Dir(b.StatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.StatePath, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := b.Fetch(context.Background(), "jail")
	if err != nil {
		t.Fatalf("a corrupt cache became an outage: %v", err)
	}
	if got.Decision != DecisionMinted {
		t.Errorf("decision = %q, want %q", got.Decision, DecisionMinted)
	}
}

// TestTheCacheIsKeyedByProfile is OQ-SSO2's shape: one host process, several AWS
// identities, no second daemon.
func TestTheCacheIsKeyedByProfile(t *testing.T) {
	var calls atomic.Int32
	a, now := testBroker(t, unnarrowed("alpha"), nil)
	a.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIAALPHA"), &calls)
	if _, err := a.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	beta := a
	beta.Config = unnarrowed("beta")
	beta.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIABETA"), &calls)
	if _, err := beta.Fetch(context.Background(), "jail"); err != nil {
		t.Fatal(err)
	}
	// Both entries survive in the one file: minting for beta did not evict alpha.
	state, err := loadState(a.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Credentials) != 2 {
		t.Fatalf("cache holds %d entries, want 2: %v", len(state.Credentials), state.Credentials)
	}
	if state.Credentials["alpha"].AccessKeyID != "ASIAALPHA" ||
		state.Credentials["beta"].AccessKeyID != "ASIABETA" {
		t.Errorf("entries crossed: %v", state.Credentials)
	}
	// And alpha still serves warm without a third mint.
	got, err := a.Fetch(context.Background(), "jail")
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != DecisionCached || calls.Load() != 2 {
		t.Errorf("decision = %q after %d mints, want cached after 2", got.Decision, calls.Load())
	}
}

func TestFetchNeedsItsPathsAndItsConfig(t *testing.T) {
	if _, err := (Broker{Config: unnarrowed("p")}).Fetch(context.Background(), "j"); err == nil {
		t.Error("a broker with no paths served")
	}
	b, _ := testBroker(t, Config{}, nil)
	if _, err := b.Fetch(context.Background(), "j"); err == nil {
		t.Error("an unconfigured broker served")
	}
}
