// Package server is the queue's HTTP side: the bootstrap-code and bearer-token authentication
// of browser-trust-boundary.md, the request checks and headers every response carries, and the
// read-only routes. It has no route that changes anything.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"sync"
	"time"
)

// The bootstrap code and session token parameters from the trust-boundary doc §4.2.
const (
	CodeBytes  = 16
	TokenBytes = 32
	CodeTTL    = 120 * time.Second
	WarnAfter  = 5 // failed exchanges for one live code before the terminal is told
	MaxTokens  = 8
)

// Auth holds, in memory only, the one live bootstrap code and the live session tokens.
//
// Every exchange is checked in constant time and a correct live code always succeeds: failed
// attempts never burn the code and are never throttled, because anything that can reach the
// port could otherwise lock the person at the terminal out. The code is 128-bit, so guessing
// cannot succeed; the warning is only so the person knows someone is trying.
type Auth struct {
	mu     sync.Mutex
	now    func() time.Time
	rand   io.Reader
	code   string
	exp    time.Time
	fails  int
	warned bool
	tokens []string
	spent  []spentCode // every code this process traded for a token; see ExchangeResult

	// OnWarn is called, outside the lock, once per code when WarnAfter failed exchanges have
	// been seen for it. It gets the failure count.
	OnWarn func(failures int)

	// OnExchange is called, outside the lock, after a code was traded for a token.
	OnExchange func()

	// OnRace is called, outside the lock, after an exchange for a code that was ALREADY
	// traded: whoever holds that link lost a race to something else that read it. Every
	// session token has been revoked by then.
	OnRace func()
}

// Result is the outcome of one exchange.
type Result int

const (
	// Failed is a wrong, expired or replaced code.
	Failed Result = iota
	// Exchanged is a live code, now spent, traded for a token.
	Exchanged
	// Reused is a code that was already traded: the person's tab lost a race to something
	// else that had the link. All tokens are revoked.
	Reused
)

// spentCode is a code already traded for a token. Each is 22 characters and a code is spent only by
// a person pressing Enter, so every one is kept for the life of the process: a replay of any of
// them alarms, however old.
type spentCode struct {
	code    string
	alarmed bool
}

// NewAuth makes an Auth with no live code. A nil clock is time.Now.
func NewAuth(now func() time.Time) *Auth {
	if now == nil {
		now = time.Now
	}
	return &Auth{now: now, rand: rand.Reader}
}

func (a *Auth) mint(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(a.rand, b); err != nil {
		panic("server: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewCode mints a fresh single-use code, replacing any earlier one, and resets the failure
// count and the warning.
func (a *Auth) NewCode() string { return a.NewCodeWith(nil) }

// NewCodeWith is NewCode, but calls prepare with the new code BEFORE the code can be
// exchanged: an exchange waits until prepare returns. A caller that writes the code somewhere
// it must later clean up (the launcher file) records it there first, so an exchange can never
// outrun the record. prepare must not call back into a.
func (a *Auth) NewCodeWith(prepare func(code string)) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	code := a.mint(CodeBytes)
	if prepare != nil {
		prepare(code)
	}
	a.code = code
	a.exp = a.now().Add(CodeTTL)
	a.fails, a.warned = 0, false
	return a.code
}

// Exchange trades a code for a session token; see ExchangeResult.
func (a *Auth) Exchange(code string) (string, bool) {
	tok, r := a.ExchangeResult(code)
	return tok, r == Exchanged
}

// ExchangeResult trades a code for a session token. A wrong or expired code fails and is
// counted; the WarnAfter-th failure for the live code raises OnWarn once. A code that was
// already traded is a race: the first replay of each such code revokes every token and raises OnRace
// once, and later replays of it do nothing but return Reused, so a stolen link is
// a detected event rather than a silent session.
func (a *Auth) ExchangeResult(code string) (string, Result) {
	a.mu.Lock()
	match := subtle.ConstantTimeCompare([]byte(code), []byte(a.code)) == 1
	live := a.code != "" && a.now().Before(a.exp)
	spentAt := -1
	for i, b := range a.spent {
		if subtle.ConstantTimeCompare([]byte(code), []byte(b.code)) == 1 {
			spentAt = i
		}
	}
	var tok string
	res := Failed
	warn := 0
	alarm := false
	switch {
	case match && live:
		a.spent = append(a.spent, spentCode{code: a.code})
		a.code, a.fails, a.warned = "", 0, false
		tok = a.mint(TokenBytes)
		a.tokens = append(a.tokens, tok)
		if len(a.tokens) > MaxTokens {
			a.tokens = a.tokens[len(a.tokens)-MaxTokens:]
		}
		res = Exchanged
	case spentAt >= 0:
		// The first replay of a spent code is the alarm. Later replays of it change nothing,
		// so a holder cannot use the alarm to churn sessions, terminal output or browsers.
		if !a.spent[spentAt].alarmed {
			a.spent[spentAt].alarmed = true
			a.tokens = nil
			alarm = true
		}
		res = Reused
	default:
		a.fails++
		if a.fails >= WarnAfter && !a.warned {
			a.warned, warn = true, a.fails
		}
	}
	onWarn, onExchange, onRace := a.OnWarn, a.OnExchange, a.OnRace
	a.mu.Unlock()
	if warn > 0 && onWarn != nil {
		onWarn(warn)
	}
	if res == Exchanged && onExchange != nil {
		onExchange()
	}
	if alarm && onRace != nil {
		onRace()
	}
	return tok, res
}

// Valid reports whether tok is a live session token, comparing against every token in constant
// time per comparison and without an early exit.
func (a *Auth) Valid(tok string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	found := 0
	for _, t := range a.tokens {
		found |= subtle.ConstantTimeCompare([]byte(tok), []byte(t))
	}
	return found == 1
}

// TokenCount is the number of live tokens.
func (a *Auth) TokenCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.tokens)
}
