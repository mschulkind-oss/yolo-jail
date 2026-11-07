package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// The one-sign-in tier (docs/design/mcp-auth-sharing.md §9.3, MA-D10, MA-D17): a user-level
// remote server with `sign_in = "once"` has one sign-in on the machine. An agent signs in the
// way it always does; the host broker adopts that sign-in from the agent's machine-wide store,
// keeps the single-use refresh token itself, and writes each refreshed access token back, so
// every jail reading that store uses one sign-in and none of them ever redeems the refresh
// token.

// OnceServer is one user-level server configured with sign_in = "once".
type OnceServer struct {
	Name     string `json:"name"`
	Identity string `json:"identity"`
	URL      string `json:"url"`
}

// OnceSource is an agent's machine-wide store the broker adopts from and writes back to.
type OnceSource struct {
	Pack   string
	Store  SourceStore
	Fields packdecl.MCPAuthFields
	// Match reports the server URL and issuer a native entry names (the pack's mcp_auth match
	// function), and false for an entry it does not recognize.
	Match func(key string, entry json.RawMessage) (url, issuer string, ok bool)
}

// NewOnceStore opens the one-sign-in tier's canonical store: records.json beside its lock.
func NewOnceStore(dir string) (*CanonicalStore, error) {
	store, err := NewCanonicalStore(dir)
	if err != nil {
		return nil, err
	}
	store.Filename, store.LockName = "records.json", "records.lock"
	return store, nil
}

// OnceBroker runs the one-sign-in passes for one broker process. Every jail's broker runs
// them; the per-record refresh lock and a re-read inside it make one adoption or refresh per
// window across all of them (ID-6).
type OnceBroker struct {
	Canonical *CanonicalStore
	Client    TokenClient
	Now       func() time.Time
	Audit     func(Event)
	// Warn receives a token-free line for the broker's log.
	Warn func(string)

	// refused holds the SHA-256 of each source refresh token a definitive failure refused, so
	// it is not sent again; in memory only, never written.
	refused map[string]bool
	retry   map[string]retryState
}

type retryState struct {
	attempts int
	next     time.Time
}

// retryDelays is §8.4's transient backoff: 30 s, 1, 2, then every 5 minutes.
var retryDelays = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

// refreshMargin is §8.4's rule: refresh when a quarter of the lifetime or 10 minutes remain,
// whichever is later.
func refreshMargin(lifetimeMS int64) time.Duration {
	return max(time.Duration(lifetimeMS)*time.Millisecond/4, 10*time.Minute)
}

// defaultLifetime stands in for a token response with no expires_in.
const defaultLifetime = time.Hour

func (b *OnceBroker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *OnceBroker) audit(action, server string, generation uint64) {
	if b.Audit != nil {
		b.Audit(Event{Action: action, Server: server, Generation: generation})
	}
}

func (b *OnceBroker) warn(format string, args ...any) {
	if b.Warn != nil {
		b.Warn(fmt.Sprintf(format, args...))
	}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (b *OnceBroker) retryReady(server OnceServer) bool {
	state, ok := b.retry[keyFor(server.Name, server.Identity)]
	return !ok || !b.now().Before(state.next)
}

func (b *OnceBroker) backOff(server OnceServer) {
	if b.retry == nil {
		b.retry = map[string]retryState{}
	}
	key := keyFor(server.Name, server.Identity)
	state := b.retry[key]
	state.next = b.now().Add(retryDelays[min(state.attempts, len(retryDelays)-1)])
	state.attempts++
	b.retry[key] = state
}

func (b *OnceBroker) succeeded(server OnceServer) {
	delete(b.retry, keyFor(server.Name, server.Identity))
}

func (b *OnceBroker) refuse(token string) {
	if b.refused == nil {
		b.refused = map[string]bool{}
	}
	b.refused[tokenHash(token)] = true
}

func (b *OnceBroker) findRecord(server OnceServer) (*Record, error) {
	file, err := b.Canonical.Read()
	if err != nil {
		return nil, err
	}
	for i := range file.Records {
		if file.Records[i].Server == server.Name && file.Records[i].Identity == server.Identity {
			record := file.Records[i]
			return &record, nil
		}
	}
	return nil, nil
}

// Pass reconciles every one-sign-in server against one source: adopt a new sign-in, refresh
// what is due, write back what the source lacks, and act on a sign-out.
func (b *OnceBroker) Pass(ctx context.Context, src OnceSource, servers []OnceServer) error {
	var problems []error
	for _, server := range servers {
		if err := b.passServer(ctx, src, server); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", server.Name, err))
		}
	}
	return errors.Join(problems...)
}

// candidate is a source entry for the server that holds a refresh token not yet spent or
// refused: a sign-in the broker has not taken.
func (b *OnceBroker) candidate(src OnceSource, server OnceServer, entries map[string]json.RawMessage, record *Record) (string, bool) {
	var keys []string
	for key, raw := range entries {
		url, _, ok := src.Match(key, raw)
		if !ok || url != server.URL {
			continue
		}
		rt, _ := stringAt(raw, src.Fields.RefreshToken)
		if rt == "" || b.refused[tokenHash(rt)] || (record != nil && record.AdoptedRefreshH == tokenHash(rt)) {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", false
	}
	// Latest sign-in wins (§9.3): the entry whose access token expires last.
	sort.Slice(keys, func(i, j int) bool {
		ei := expiryOf(src, entries[keys[i]])
		ej := expiryOf(src, entries[keys[j]])
		if ei != ej {
			return ei > ej
		}
		return keys[i] < keys[j]
	})
	return keys[0], true
}

func expiryOf(src OnceSource, raw json.RawMessage) int64 {
	if src.Fields.ExpiresAt.Path == "" {
		return 0
	}
	value, err := rawAt(raw, src.Fields.ExpiresAt.Path)
	if err != nil {
		return 0
	}
	ms, _ := decodeExpiry(value, src.Fields.ExpiresAt.Unit)
	return ms
}

func (b *OnceBroker) passServer(ctx context.Context, src OnceSource, server OnceServer) error {
	entries, parsed, _ := src.Store.Read()
	if !parsed {
		// An absent, torn or unreadable store proves nothing; the next pass looks again.
		return nil
	}
	record, err := b.findRecord(server)
	if err != nil {
		return err
	}
	if key, ok := b.candidate(src, server, entries, record); ok {
		if !b.retryReady(server) {
			return nil
		}
		return b.adopt(ctx, src, server, key)
	}
	if record == nil || record.State != "live" {
		return nil
	}
	raw, present := entries[record.SourceKey]
	if !present {
		return b.signOut(src, server)
	}
	if b.now().UnixMilli() >= record.ExpiresAtMS-refreshMargin(record.LifetimeMS).Milliseconds() {
		if !b.retryReady(server) {
			return nil
		}
		return b.refresh(ctx, src, server)
	}
	if b.ours(src, raw, record) && !b.current(src, raw, record) {
		return b.writeBack(src, record)
	}
	return nil
}

// originOf reports whether src is the store record was adopted from: the one store whose
// losing the entry signs the sign-in out everywhere (OQ-MA4, ID-18).
func originOf(record *Record, src OnceSource) bool {
	return record.SourceDir == "" || record.SourceDir == src.Store.Dir
}

// ours reports whether a source entry is the broker's to write: it holds no refresh token, or
// only the spent one adoption took.
func (b *OnceBroker) ours(src OnceSource, raw json.RawMessage, record *Record) bool {
	rt, _ := stringAt(raw, src.Fields.RefreshToken)
	return rt == "" || tokenHash(rt) == record.AdoptedRefreshH
}

func (b *OnceBroker) current(src OnceSource, raw json.RawMessage, record *Record) bool {
	at, _ := stringAt(raw, src.Fields.AccessToken)
	rt, _ := stringAt(raw, src.Fields.RefreshToken)
	return at == record.AccessToken && rt == "" && expiryOf(src, raw) == record.ExpiresAtMS
}

// withEntryTokens sets the access token and expiry in a native entry and removes its refresh
// token (MA-D17's write-back shape).
func withEntryTokens(src OnceSource, raw json.RawMessage, access string, expiresAtMS int64) (json.RawMessage, error) {
	entry, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	if err := setStringPath(entry, src.Fields.AccessToken, access); err != nil {
		return nil, err
	}
	if src.Fields.ExpiresAt.Path != "" {
		value := expiresAtMS
		if src.Fields.ExpiresAt.Unit == "s" {
			value /= 1000
		}
		encoded, _ := json.Marshal(value)
		if err := setRawPath(entry, src.Fields.ExpiresAt.Path, encoded); err != nil {
			return nil, err
		}
	}
	if src.Fields.RefreshToken != "" {
		if err := deletePath(entry, strings.Split(src.Fields.RefreshToken, ".")); err != nil {
			return nil, err
		}
	}
	return marshalRawObject(entry)
}

func setStringPath(object map[string]json.RawMessage, path, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return setRawPath(object, path, encoded)
}

func setRawPath(object map[string]json.RawMessage, path string, value json.RawMessage) error {
	if path == "" {
		return errors.New("empty JSON path")
	}
	parts := strings.Split(path, ".")
	if len(parts) == 1 {
		object[parts[0]] = value
		return nil
	}
	nested := map[string]json.RawMessage{}
	if raw, ok := object[parts[0]]; ok {
		var err error
		if nested, err = decodeObject(raw); err != nil {
			return err
		}
	}
	if err := setRawPath(nested, strings.Join(parts[1:], "."), value); err != nil {
		return err
	}
	encoded, err := marshalRawObject(nested)
	if err != nil {
		return err
	}
	object[parts[0]] = encoded
	return nil
}

func (b *OnceBroker) lockRecord(server OnceServer) (func() error, error) {
	return b.Canonical.LockRefresh(server.Name, server.Identity)
}

// adopt takes the sign-in at key: under the record's refresh lock, with the source re-read so
// a broker that got there first is seen, it confirms the issuer by discovery from the
// configured URL and redeems the refresh token once. Only a successful refresh commits the
// record, and only after the commit is the source entry stripped of its refresh token.
func (b *OnceBroker) adopt(ctx context.Context, src OnceSource, server OnceServer, key string) error {
	release, err := b.lockRecord(server)
	if err != nil {
		return err
	}
	defer release()
	entries, parsed, _ := src.Store.Read()
	if !parsed {
		return nil
	}
	raw, ok := entries[key]
	if !ok {
		return nil
	}
	record, err := b.findRecord(server)
	if err != nil {
		return err
	}
	if current, ok := b.candidate(src, server, map[string]json.RawMessage{key: raw}, record); !ok || current != key {
		return nil // another broker adopted it while this one waited for the lock
	}
	_, issuer, _ := src.Match(key, raw)
	rt, _ := stringAt(raw, src.Fields.RefreshToken)
	clientID := ""
	if src.Fields.ClientID != "" {
		clientID, _ = stringAt(raw, src.Fields.ClientID)
	}
	discovery, err := b.Client.Discover(ctx, server.URL)
	if err != nil {
		b.backOff(server)
		b.warn("%s: discovery failed, the sign-in stays with %s for now: %v", server.Name, src.Pack, err)
		return nil
	}
	if issuer == "" || !sameIssuer(issuer, discovery.Issuer) {
		b.refuse(rt)
		b.audit("adopt-refused", server.Name, 0)
		b.warn("%s: the sign-in names an issuer the server does not; %s keeps signing itself in", server.Name, src.Pack)
		return nil
	}
	tokens, err := b.Client.Refresh(ctx, discovery, clientID, rt, server.URL)
	if err != nil {
		var rerr *RefreshError
		if errors.As(err, &rerr) && rerr.Definitive {
			b.refuse(rt)
			b.audit("adopt-refused", server.Name, 0)
			b.warn("%s: %v; %s keeps signing itself in", server.Name, err, src.Pack)
			return nil
		}
		b.backOff(server)
		b.warn("%s: %v; adoption retries on backoff", server.Name, err)
		return nil
	}
	b.succeeded(server)
	now := b.now()
	lifetime := time.Duration(tokens.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = defaultLifetime
	}
	expires := now.Add(lifetime).UnixMilli()
	newRT := tokens.RefreshToken
	if newRT == "" {
		newRT = rt
	}
	entry, err := withEntryTokens(src, raw, tokens.AccessToken, expires)
	if err != nil {
		return err
	}
	var generation uint64
	err = b.Canonical.WithLock(func(file *CanonicalFile) error {
		next := Record{Server: server.Name, Identity: server.Identity, URL: server.URL, Generation: 1, State: "live",
			AdoptedFrom: src.Pack, Entry: entry, AccessToken: tokens.AccessToken, RefreshToken: newRT, ExpiresAtMS: expires,
			ClientID: clientID, TokenEndpoint: discovery.TokenEndpoint, RefreshedAt: now.UTC().Format(time.RFC3339),
			SourceKey: key, Issuer: discovery.Issuer, LifetimeMS: lifetime.Milliseconds(), AdoptedRefreshH: tokenHash(rt),
			SourceDir: src.Store.Dir}
		for i := range file.Records {
			if file.Records[i].Server == server.Name && file.Records[i].Identity == server.Identity {
				next.Generation = file.Records[i].Generation + 1
				generation = next.Generation
				file.Records[i] = next
				return nil
			}
		}
		generation = next.Generation
		file.Records = append(file.Records, next)
		return nil
	})
	if err != nil {
		// The refresh token is spent and the new one was not stored: the agent signs in again.
		return fmt.Errorf("the adopted sign-in could not be stored; sign in again with %s: %w", src.Pack, err)
	}
	b.audit("adopt", server.Name, generation)
	return b.writeBack(src, &Record{SourceKey: key, AccessToken: tokens.AccessToken, ExpiresAtMS: expires, AdoptedRefreshH: tokenHash(rt)})
}

// writeBack puts the record's access token and expiry into the source entry, under the
// agent's lock, when that entry is still the broker's to write.
func (b *OnceBroker) writeBack(src OnceSource, record *Record) error {
	err := src.Store.Update(func(entries map[string]json.RawMessage) (bool, error) {
		raw, ok := entries[record.SourceKey]
		if !ok || !b.ours(src, raw, record) || b.current(src, raw, record) {
			return false, nil
		}
		next, err := withEntryTokens(src, raw, record.AccessToken, record.ExpiresAtMS)
		if err != nil {
			return false, err
		}
		entries[record.SourceKey] = next
		return true, nil
	})
	if err != nil {
		b.warn("write-back to %s's store did not happen yet: %v", src.Pack, err)
	}
	return nil
}

// refresh redeems the broker's own refresh token when the record is due, re-reading the
// record under its lock so a refresh another broker just made is not repeated.
func (b *OnceBroker) refresh(ctx context.Context, src OnceSource, server OnceServer) error {
	release, err := b.lockRecord(server)
	if err != nil {
		return err
	}
	defer release()
	record, err := b.findRecord(server)
	if err != nil || record == nil || record.State != "live" {
		return err
	}
	if b.now().UnixMilli() < record.ExpiresAtMS-refreshMargin(record.LifetimeMS).Milliseconds() {
		return b.writeBack(src, record)
	}
	tokens, err := b.Client.Refresh(ctx, Discovery{Issuer: record.Issuer, TokenEndpoint: record.TokenEndpoint}, record.ClientID, record.RefreshToken, server.URL)
	if err != nil {
		var rerr *RefreshError
		if errors.As(err, &rerr) && rerr.Definitive {
			return b.markDead(src, server, record, err)
		}
		b.backOff(server)
		b.warn("%s: %v; retrying on backoff", server.Name, err)
		return nil
	}
	b.succeeded(server)
	now := b.now()
	lifetime := time.Duration(tokens.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = defaultLifetime
	}
	var updated Record
	err = b.Canonical.WithLock(func(file *CanonicalFile) error {
		for i := range file.Records {
			r := &file.Records[i]
			if r.Server != server.Name || r.Identity != server.Identity {
				continue
			}
			r.AccessToken, r.ExpiresAtMS, r.LifetimeMS = tokens.AccessToken, now.Add(lifetime).UnixMilli(), lifetime.Milliseconds()
			if tokens.RefreshToken != "" {
				r.RefreshToken = tokens.RefreshToken
			}
			r.RefreshedAt = now.UTC().Format(time.RFC3339)
			if entry, err := withEntryTokens(src, r.Entry, r.AccessToken, r.ExpiresAtMS); err == nil {
				r.Entry = entry
			}
			updated = *r
			return nil
		}
		return errors.New("the record disappeared during its refresh")
	})
	if err != nil {
		return fmt.Errorf("a refreshed sign-in could not be stored; sign in again with %s: %w", src.Pack, err)
	}
	b.audit("refresh", server.Name, updated.Generation)
	return b.writeBack(src, &updated)
}

// markDead records a definitive refresh failure (§8.4) and removes the source entry when it
// is still the broker's copy, so the agent asks to sign in again instead of using a token
// that cannot be renewed.
func (b *OnceBroker) markDead(src OnceSource, server OnceServer, record *Record, cause error) error {
	var generation uint64
	err := b.Canonical.WithLock(func(file *CanonicalFile) error {
		for i := range file.Records {
			r := &file.Records[i]
			if r.Server == server.Name && r.Identity == server.Identity {
				r.State, r.AccessToken, r.RefreshToken = "dead", "", ""
				generation = r.Generation
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	b.audit("dead", server.Name, generation)
	b.warn("%s: %v; sign in again with %s", server.Name, cause, src.Pack)
	if err := src.Store.Update(func(entries map[string]json.RawMessage) (bool, error) {
		raw, ok := entries[record.SourceKey]
		if !ok || !b.ours(src, raw, record) {
			return false, nil
		}
		at, _ := stringAt(raw, src.Fields.AccessToken)
		if at != record.AccessToken {
			return false, nil
		}
		delete(entries, record.SourceKey)
		return true, nil
	}); err != nil {
		b.warn("%s: the dead sign-in is still in %s's store: %v", server.Name, src.Pack, err)
	}
	return nil
}

// signOut is OQ-MA4: the entry left a cleanly parsed store, so the sign-in ends everywhere.
// It is confirmed under the record's lock against a fresh read first.
func (b *OnceBroker) signOut(src OnceSource, server OnceServer) error {
	release, err := b.lockRecord(server)
	if err != nil {
		return err
	}
	defer release()
	entries, parsed, _ := src.Store.Read()
	record, err := b.findRecord(server)
	if err != nil || !parsed || record == nil || record.State != "live" {
		return err
	}
	if _, present := entries[record.SourceKey]; present || !originOf(record, src) {
		// Present, or absent from another store of the agent's that never signed in to it,
		// which is not a sign-out (ID-18): that store's own sign-in, when it makes one, is
		// adopted like any other.
		return nil
	}
	var generation uint64
	err = b.Canonical.WithLock(func(file *CanonicalFile) error {
		kept := file.Records[:0]
		for _, r := range file.Records {
			if r.Server == server.Name && r.Identity == server.Identity {
				generation = r.Generation
				continue
			}
			kept = append(kept, r)
		}
		file.Records = kept
		return nil
	})
	if err != nil {
		return err
	}
	b.audit("sign-out", server.Name, generation)
	return nil
}
