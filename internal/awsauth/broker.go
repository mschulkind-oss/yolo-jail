package awsauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Decision says why no more than one mint was required to answer a fetch.
type Decision string

const (
	// DecisionCached: the cache held a credential with more than the re-mint lead
	// remaining. No `aws` invocation happened.
	DecisionCached Decision = "cached"
	// DecisionMinted: this call minted.
	DecisionMinted Decision = "minted"
	// DecisionWaited: this call blocked on the host-wide lock and found another
	// process had already minted. Two concurrent fetches, one mint.
	DecisionWaited Decision = "waited"
)

// Result is a served credential and why.
type Result struct {
	Credential Credential
	Decision   Decision
}

// Event is the credential-safe diagnostic emitted after a fetch. Credential
// fields are FINGERPRINTS, never bodies.
type Event struct {
	Caller      string
	Profile     string
	Narrowing   string
	Decision    Decision
	Fingerprint string
	ExpiresAtMS int64
	ErrorKind   FailureKind
	ErrorCode   string
}

// Broker owns the cache and serializes every mint through LockPath. Several
// Broker values and several processes coordinate when they name the same paths.
type Broker struct {
	StatePath string
	LockPath  string
	Config    Config
	Minter    Minter
	// RemintLead overrides the package default. A cached credential with less than
	// this remaining is re-minted.
	RemintLead time.Duration
	Now        func() time.Time
	Observe    func(Event)
}

func (b Broker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b Broker) lead() time.Duration {
	if b.RemintLead > 0 {
		return b.RemintLead
	}
	return RemintLead
}

// TickInterval is how often the proactive minter should ask RemintDue: HALF the
// re-mint lead, so the window cannot be skipped over by one tick.
func (b Broker) TickInterval() time.Duration { return b.lead() / 2 }

// warm reports whether a cached entry can be served as-is.
//
// THREE CONDITIONS, and each is a way the cache can be stale in a way a bare
// expiry check would miss: the credential must be complete, it must have been minted
// under the SAME narrowing this broker is configured for, and it must have more than
// the re-mint lead remaining. The middle one is the one that matters — a cache entry
// left by a daemon configured with a wider narrowing must never be served to a
// narrower configuration, and the reverse must not silently serve something narrower
// than asked for either.
func (b Broker) warm(cred Credential) bool {
	if !cred.Complete() {
		return false
	}
	if cred.NarrowingDigest != b.Config.Narrowing.Digest() {
		return false
	}
	return cred.ExpiresAt().Sub(b.now()) > b.lead()
}

// RemintDue is the PROACTIVE MINTER'S DECISION FUNCTION: true when the ticker
// should mint now.
//
// Pre-minting on the ticker is the design's R1 — the mint is the slow step, the
// adapter's serve budget is under 200 ms, and a request must never wait on a mint it
// could have avoided. A missing or unservable cache entry is due, because cold is the
// state the first request would otherwise pay for.
func (b Broker) RemintDue() bool {
	if b.Config.Profile == "" {
		return false
	}
	state, _ := loadState(b.StatePath)
	cred, ok := state.Credentials[b.Config.Profile]
	if !ok {
		return true
	}
	return !b.warm(cred)
}

// Fetch returns a servable credential, minting at most once.
//
// # The fast path is deliberately lock-free
//
// A warm serve reads the state file and returns. It does not take the lock, because
// writeState replaces the file by rename — a reader sees one complete generation or
// the other — and because the adapter's whole budget is under 200 ms against an SDK
// that gives up at 1000 ms. Taking a host-wide lock on the read path would make every
// warm serve wait behind whatever mint happened to be in flight.
//
// # The slow path reloads UNDER the lock, and that is what makes one mint
//
// Two fetches that both see a cold cache both take the lock; the second reloads
// inside it, finds what the first wrote, and returns DecisionWaited. Without the
// reload it would mint again — the same bug internal/openaiauth's reload-under-lock
// exists to prevent, for the same reason.
//
// NO RETRY. The SDK already retries three times, so a retry here would multiply a
// remote failure by three inside a 1000 ms ceiling.
func (b Broker) Fetch(ctx context.Context, caller string) (Result, error) {
	if b.StatePath == "" || b.LockPath == "" {
		return Result{}, fmt.Errorf("the aws-auth broker needs a state path and a lock path")
	}
	if b.Config.Profile == "" || b.Config.Narrowing.Kind == "" {
		return Result{}, &MintError{Kind: FailureUnavailable, Code: "NotConfigured",
			Message: "the aws-auth service has no resolved profile and narrowing"}
	}
	if state, err := loadState(b.StatePath); err == nil {
		if cred, ok := state.Credentials[b.Config.Profile]; ok && b.warm(cred) {
			result := Result{Credential: cred, Decision: DecisionCached}
			b.emit(caller, result, nil)
			return result, nil
		}
	}

	var result Result
	err := b.withLock(func() error {
		state, _ := loadState(b.StatePath)
		if cred, ok := state.Credentials[b.Config.Profile]; ok && b.warm(cred) {
			result = Result{Credential: cred, Decision: DecisionWaited}
			return nil
		}
		minter := b.Minter
		minter.Config = b.Config
		if minter.Now == nil {
			minter.Now = b.Now
		}
		cred, err := minter.Mint(ctx)
		if err != nil {
			return err
		}
		state.Credentials[b.Config.Profile] = cred
		if err := writeState(b.StatePath, state); err != nil {
			return err
		}
		result = Result{Credential: cred, Decision: DecisionMinted}
		return nil
	})
	b.emit(caller, result, err)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Current is a lock-free snapshot of the cached credential for the configured
// profile, WITHOUT minting. The status view and the self-check read it.
func (b Broker) Current() (Credential, bool) {
	state, err := loadState(b.StatePath)
	if err != nil {
		return Credential{}, false
	}
	cred, ok := state.Credentials[b.Config.Profile]
	return cred, ok
}

// Status is the FINGERPRINT-ONLY view. There is no field here a credential body
// could travel in, which is the property rather than an omission: this is what a
// `yolo check` line and an audit record are built from, and neither has any use for
// the secret.
func (b Broker) Status() map[string]any {
	view := map[string]any{
		"profile":   b.Config.Profile,
		"narrowing": string(b.Config.Narrowing.Kind),
		"cached":    false,
	}
	cred, ok := b.Current()
	if !ok {
		return view
	}
	view["cached"] = true
	view["fingerprint"] = cred.Fingerprint()
	view["expires_at"] = cred.ExpiresAtMS
	view["minted_at"] = cred.MintedAtMS
	view["remaining_seconds"] = int64(cred.ExpiresAt().Sub(b.now()) / time.Second)
	view["remint_due"] = !b.warm(cred)
	return view
}

func (b Broker) emit(caller string, result Result, err error) {
	if b.Observe == nil {
		return
	}
	event := Event{
		Caller: caller, Profile: b.Config.Profile,
		Narrowing: string(b.Config.Narrowing.Kind), Decision: result.Decision,
		Fingerprint: result.Credential.Fingerprint(), ExpiresAtMS: result.Credential.ExpiresAtMS,
	}
	var mintErr *MintError
	if errors.As(err, &mintErr) {
		event.ErrorKind, event.ErrorCode = mintErr.Kind, mintErr.Code
	} else if err != nil {
		event.ErrorKind, event.ErrorCode = FailureUnavailable, "broker_error"
	}
	b.Observe(event)
}

// withLock serializes mints machine-wide with a flock on a file BESIDE the state
// file. Beside, rather than anywhere else, so that every process which can write the
// cache contends for the same inode whatever it calls its state directory.
func (b Broker) withLock(fn func() error) error {
	dir := filepath.Dir(b.LockPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create aws-auth lock directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure aws-auth lock directory: %w", err)
	}
	lock, err := os.OpenFile(b.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open aws-auth mint lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock aws-auth credential state: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}
