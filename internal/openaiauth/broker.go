package openaiauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const DefaultRefreshLead = 5 * time.Minute

// ErrLoginRequired means OpenAI permanently rejected the canonical grant. The
// last credential generation remains available for status and stale-view
// repair, but no current client may redeem it again until a new login replaces
// the state.
var ErrLoginRequired = errors.New("OpenAI authentication requires login")

// Tokens is the provider-neutral result returned by an upstream token
// redemption. A missing RefreshToken means the provider retained the previous
// refresh token.
type Tokens struct {
	AccessToken  string
	IDToken      string
	RefreshToken string
	ExpiresAt    time.Time
	AccountID    string
}

// Refresher redeems exactly one canonical refresh token. Implementations own
// the provider-specific HTTP request and response schema.
type Refresher interface {
	Refresh(context.Context, string) (Tokens, error)
}

type ErrorKind string

const (
	ErrorTransient ErrorKind = "transient"
	ErrorPermanent ErrorKind = "permanent"
)

// RefreshError lets an upstream adapter say whether another attempt can help.
// Code is safe diagnostic metadata such as "invalid_grant"; it must not contain
// response bodies, tokens, authorization codes, or PKCE verifiers.
type RefreshError struct {
	Kind ErrorKind
	Code string
	Err  error
}

func (e *RefreshError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Code != "" {
		return e.Code
	}
	return string(e.Kind)
}

func (e *RefreshError) Unwrap() error { return e.Err }

// Request identifies the caller's credential view. Codex supplies the opaque
// generation from its view. Pi and the proactive refresher omit it. No agent
// receives or submits the canonical refresh token.
type Request struct {
	CallerGeneration uint64
	Caller           string
}

type Decision string

const (
	DecisionCached    Decision = "cached"
	DecisionStale     Decision = "stale-caller"
	DecisionRefreshed Decision = "refreshed"
)

// Result is the current canonical generation and why no more than one upstream
// redemption was required.
type Result struct {
	State    State
	Decision Decision
}

// Event is the credential-safe diagnostic emitted after a transaction. Token
// fields contain fingerprints, never token bodies.
type Event struct {
	Caller             string
	Decision           Decision
	Generation         uint64
	AccessFingerprint  string
	RefreshFingerprint string
	ExpiresAtMS        int64
	ErrorKind          ErrorKind
	ErrorCode          string
}

// Broker serializes every state-changing operation through LockPath. Multiple
// Broker values and processes coordinate when they use the same path.
type Broker struct {
	StatePath   string
	LockPath    string
	Refresher   Refresher
	RefreshLead time.Duration
	Now         func() time.Time
	Observe     func(Event)
}

func (b Broker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b Broker) lead() time.Duration {
	if b.RefreshLead > 0 {
		return b.RefreshLead
	}
	return DefaultRefreshLead
}

// RefreshDue reports whether canonical state needs proactive refresh. Invalid
// or absent state returns false: those conditions require login or repair, not
// an unattended redemption loop.
func (b Broker) RefreshDue() bool {
	state, err := loadState(b.StatePath)
	return err == nil && !state.LoginRequired && time.UnixMilli(state.ExpiresAtMS).Sub(b.now()) <= b.lead()
}

// Current returns the complete, validated canonical generation without
// refreshing it. Atomic replacement makes this safe for agent-view renderers
// that only need a snapshot.
func (b Broker) Current() (State, error) {
	if b.StatePath == "" {
		return State{}, errors.New("OpenAI auth broker state path is required")
	}
	return loadState(b.StatePath)
}

// Replace installs a newly completed login as the next canonical generation.
// It shares the refresh lock with Refresh and Logout, so a browser login cannot
// race an in-flight rotation.
func (b Broker) Replace(tokens Tokens) (State, error) {
	if b.StatePath == "" || b.LockPath == "" {
		return State{}, errors.New("OpenAI auth broker state and lock paths are required")
	}
	if tokens.AccessToken == "" || tokens.IDToken == "" || tokens.RefreshToken == "" || tokens.ExpiresAt.IsZero() {
		return State{}, errors.New("OpenAI login returned incomplete tokens")
	}
	var installed State
	err := b.withLock(func() error {
		generation := uint64(1)
		if current, err := loadState(b.StatePath); err == nil {
			generation = current.Generation + 1
		}
		installed = State{
			Version:       stateVersion,
			AccessToken:   tokens.AccessToken,
			IDToken:       tokens.IDToken,
			RefreshToken:  tokens.RefreshToken,
			ExpiresAtMS:   tokens.ExpiresAt.UnixMilli(),
			AccountID:     tokens.AccountID,
			Generation:    generation,
			LastRefreshMS: b.now().UnixMilli(),
		}
		return writeState(b.StatePath, installed)
	})
	return installed, err
}

// Logout removes the machine-wide grant while holding the same lock used by
// refresh and login replacement. Missing state is already logged out.
func (b Broker) Logout() error {
	if b.StatePath == "" || b.LockPath == "" {
		return errors.New("OpenAI auth broker state and lock paths are required")
	}
	return b.withLock(func() error {
		if err := os.Remove(b.StatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove OpenAI credential state: %w", err)
		}
		return nil
	})
}

// Refresh returns the current generation, refreshing once if it is due. It
// reloads state only after acquiring the machine-wide lock. A stale Codex
// caller receives the current generation without upstream I/O, even when that
// generation is due; its next native retry presents the current marker.
func (b Broker) Refresh(ctx context.Context, request Request) (Result, error) {
	if b.StatePath == "" || b.LockPath == "" {
		return Result{}, errors.New("OpenAI auth broker state and lock paths are required")
	}
	if b.Refresher == nil {
		return Result{}, errors.New("OpenAI auth broker refresher is required")
	}
	var result Result
	var event Event
	err := b.withLock(func() error {
		state, err := loadState(b.StatePath)
		if err != nil {
			return fmt.Errorf("load OpenAI credential state: %w", err)
		}
		event = eventFor(state, request.Caller)
		if request.CallerGeneration != 0 && request.CallerGeneration != state.Generation {
			result = Result{State: state, Decision: DecisionStale}
			event.Decision = DecisionStale
			return nil
		}
		if state.LoginRequired {
			event.ErrorKind = ErrorPermanent
			event.ErrorCode = state.LastErrorCode
			return ErrLoginRequired
		}
		if time.UnixMilli(state.ExpiresAtMS).Sub(b.now()) > b.lead() {
			result = Result{State: state, Decision: DecisionCached}
			event.Decision = DecisionCached
			return nil
		}

		// Once redemption may have started, caller cancellation must not strand
		// the canonical state after the upstream consumed its refresh token.
		tokens, err := b.Refresher.Refresh(context.WithoutCancel(ctx), state.RefreshToken)
		if err != nil {
			var refreshErr *RefreshError
			if errors.As(err, &refreshErr) {
				event.ErrorKind, event.ErrorCode = refreshErr.Kind, refreshErr.Code
				if refreshErr.Kind == ErrorPermanent {
					state.LoginRequired = true
					state.LastErrorCode = refreshErr.Code
					if writeErr := writeState(b.StatePath, state); writeErr != nil {
						return errors.Join(err, writeErr)
					}
				}
			}
			return err
		}
		if tokens.AccessToken == "" || tokens.ExpiresAt.IsZero() {
			return errors.New("OpenAI refresh returned incomplete tokens")
		}
		state.AccessToken = tokens.AccessToken
		if tokens.IDToken != "" {
			state.IDToken = tokens.IDToken
		}
		if tokens.RefreshToken != "" {
			state.RefreshToken = tokens.RefreshToken
		}
		state.ExpiresAtMS = tokens.ExpiresAt.UnixMilli()
		if tokens.AccountID != "" {
			state.AccountID = tokens.AccountID
		}
		state.Generation++
		state.LoginRequired = false
		state.LastRefreshMS = b.now().UnixMilli()
		state.LastErrorCode = ""
		if err := writeState(b.StatePath, state); err != nil {
			return err
		}
		result = Result{State: state, Decision: DecisionRefreshed}
		event = eventFor(state, request.Caller)
		event.Decision = DecisionRefreshed
		return nil
	})
	if b.Observe != nil {
		b.Observe(event)
	}
	return result, err
}

func eventFor(state State, caller string) Event {
	return Event{
		Caller:             caller,
		Generation:         state.Generation,
		AccessFingerprint:  TokenFingerprint(state.AccessToken),
		RefreshFingerprint: TokenFingerprint(state.RefreshToken),
		ExpiresAtMS:        state.ExpiresAtMS,
	}
}

func (b Broker) withLock(fn func() error) error {
	dir := filepath.Dir(b.LockPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create OpenAI auth lock directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure OpenAI auth lock directory: %w", err)
	}
	lock, err := os.OpenFile(b.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open OpenAI auth lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock OpenAI credential state: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}
