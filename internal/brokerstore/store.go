// Package brokerstore is the host-only request/grant API of boundary-broker.md §7.
// Broker calls use it only behind required-delivery readiness; production writes
// still refuse without filing a request.
package brokerstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
)

const sessionLimit = 12 * time.Hour

// PendingLife is how long a filed request waits for an answer before it expires.
const PendingLife = time.Hour

// The kinds of refusal a front end reports with its own stable exit code
// (boundary-broker.md §6.6). Match them with errors.Is; the error's text is the message.
var (
	ErrAlreadyDecided  = errors.New("already decided")
	ErrUnknownID       = errors.New("unknown id")
	ErrRefusedByPolicy = errors.New("refused by policy") // the decision asks beyond the set's ceiling
	// ErrNotConfirmed is a pending request the host can no longer vouch for: its jail's
	// launch is gone without having ended it (a broker killed outright), its scope file is
	// missing, or its set or classification changed. No decision can allow it.
	ErrNotConfirmed = errors.New("not confirmed by the host")
)

// kindError carries its full message and one of the kinds above.
type kindError struct {
	msg  string
	kind error
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }
func refusal(kind error, format string, args ...any) error {
	return &kindError{msg: fmt.Sprintf(format, args...), kind: kind}
}

// The reasons a grant ends, recorded in Grant.EndReason.
const (
	EndExpired = "expired" // its window ran out
	EndUsed    = "used"    // its last use was spent
	EndRevoked = "revoked" // a human revoked it
	// EndStopped is the reason an older yolo recorded when the broker lifetime holding a
	// grant ended. A grant is now held by its workspace and no broker stopping ends it
	// (boundary-broker.md BB-D99), so only a record written before that carries it.
	EndStopped = "stopped"
)

// Policy is the resolved windowed set policy, supplied by the HOST broker after
// classification and scope checks, never decoded from a jail's request frame.
// Session distinguishes a session ceiling from an ordinary duration ceiling.
type Policy struct {
	Window    time.Duration
	MaxWindow time.Duration
	Session   bool
	Uses      int
}
type Request struct {
	ID                             string
	LaunchID                       string // host launch scope identity
	Jail, Start, Workspace         string // host-asserted jail identity and random broker lifetime
	Argv                           []string
	Repo, StdinDigest, Digest, Set string
	Policy                         Policy
	Filed, Decided                 time.Time
	State                          string
	// HookFailure is one line saying the user's on_request failed for this request, such
	// as "hook exited 127: sh: notify-send: not found"; HookOutput is the last of what it
	// printed. Both are terminal-safe, and empty unless it failed (NoteHookFailure).
	HookFailure string `json:",omitempty"`
	HookOutput  string `json:",omitempty"`
	// NeedsApproval is, for a git push, the refs among Argv's that nothing else covered: what
	// a front end marks as the reason the push asks. It is shown, never part of Digest.
	NeedsApproval []string `json:",omitempty"`
	// About is what the request does, for a person, as its filing broker read it; shown,
	// never part of Digest. Empty in a request an older yolo filed.
	About About `json:",omitzero"`
	// Spent is an Allow once whose command already spent it; Lapsed one whose window closed
	// unspent and whose retry was refused once naming it (LapsedAllowError). Neither is part
	// of Digest.
	Spent  bool `json:",omitempty"`
	Lapsed bool `json:",omitempty"`
	// SpentAt is when the command that spent its Allow once ran; zero unless Spent.
	SpentAt time.Time `json:",omitzero"`
	// Answer is how the host answered it (AnswerOnce, AnswerFor, AnswerAdd, AnswerRemember,
	// AnswerDeny), and Via who ran the `yolo approve` that did (ViaOnRequest, ViaHost); Until
	// is when an AnswerFor stops covering its commands. All three are the decision's, empty
	// while pending and in a request an older yolo decided (docs/design/jail-history.md
	// JH-D11). None is part of Digest.
	Answer string    `json:",omitempty"`
	Via    string    `json:",omitempty"`
	Until  time.Time `json:",omitzero"`
	// HookRan is the user's on_request hook having started for it (NoteHookRan); a hook that
	// then failed says so in HookFailure.
	HookRan bool `json:",omitempty"`
	// Reason is why a request left pending unanswered: ReasonBrokerStopped for one End closed.
	Reason string `json:",omitempty"`
}

// How a request was answered (Request.Answer): the jail-readable record's own spellings
// (approvalwait), so the two cannot drift.
const (
	AnswerOnce     = approvalwait.AnswerOnce     // Allow once: one run of the exact command
	AnswerFor      = approvalwait.AnswerFor      // allowed for a time (`--for`), the set or the request's resources
	AnswerAdd      = approvalwait.AnswerAdd      // its resources added to the working set (`--add`)
	AnswerRemember = approvalwait.AnswerRemember // Allow once, its refs remembered in the project's config
	AnswerDeny     = approvalwait.AnswerDeny
)

// Who answered it (Request.Via): `yolo approve` run by the request's own on_request hook (the
// hook's environment names the request, brokerhook.Env), or by the human at a host terminal.
const (
	ViaOnRequest = approvalwait.ViaOnRequest
	ViaHost      = approvalwait.ViaHost
)

// ReasonBrokerStopped is a request End closed: the broker lifetime that filed it stopped.
const ReasonBrokerStopped = approvalwait.ReasonBrokerStopped

// Expires is when the request stops waiting for an answer, whatever its state now.
func (q Request) Expires() time.Time { return q.Filed.Add(PendingLife) }

// Grant is one decision's authority. Its holder is Workspace: every jail of that workspace,
// through broker restarts, jail restarts and relaunches, until it expires, is used up or is
// revoked (boundary-broker.md BB-D99). Jail and Start are the jail and broker lifetime whose
// request it answered, kept for the record; neither limits who may use it.
type Grant struct {
	ID, Request, Jail, Start, Set, Digest string // empty Digest means the whole requested set
	// Workspace is the host workspace path of the request it answered: the grant's holder. A
	// record written before the field existed takes its request's (adoptWorkspaces).
	Workspace string `json:",omitempty"`
	// UsedBy is the jail that spent its latest use, "" until one is spent.
	UsedBy         string `json:",omitempty"`
	Expires, Ended time.Time
	Uses           int
	// EndReason is one of the End* constants once Ended is set. A record written before
	// the field existed has none; EndedBy infers one.
	EndReason string `json:",omitempty"`
	// Issued is the uses the decision granted, so a watch that first sees a grant after it
	// was used can still tell. A record written before the field existed has zero.
	Issued int `json:",omitempty"`
}

// EndedBy is why the grant ended, "" while it is live.
func (g Grant) EndedBy() string {
	switch {
	case g.Ended.IsZero():
		return ""
	case g.EndReason != "":
		return g.EndReason
	case g.Ended.Equal(g.Expires):
		return EndExpired
	case g.Uses == 0:
		return EndUsed
	}
	return EndStopped
}

// Decision cannot change the holder, set or command. Zero For means Allow once.
type Decision struct {
	Deny    bool
	For     time.Duration
	Session bool
	Uses    int
	// Add (DecideWorking only) puts the request's resources into the working set for good.
	Add bool
	// Remember (Decide only) records the Allow once as AnswerRemember: the caller has written
	// the request's refs into the project's config. It grants nothing more than Allow once.
	Remember bool
	// Via is who answered (ViaOnRequest, ViaHost), recorded on the request; it grants nothing.
	Via string
}
type Store struct {
	Dir string
	// Source names the broker source this store serves; empty means the directory's name.
	Source string
	// ApprovalStatus is CLI discovery metadata (selected, removed, or unknown). It never
	// grants authority; Revalidate remains the only gate for an approval.
	ApprovalStatus string
	// ApprovalDiagnostic explains an incomplete source-discovery result to the CLI listing.
	ApprovalDiagnostic string
	Now                func() time.Time
	// Revalidate is mandatory for approving decisions; it reads current host authority
	// under the transaction lock. Nil fails closed. Deny/revoke need no authority.
	Revalidate Revalidate
	// Audit, when supplied by a host adapter, runs under the shared transaction.
	Audit func(context.Context, string, Request, Grant) error
	// Entries, when set, is the source's answer to "what working-set entries would this
	// request's refs become", for DecideWorking: it fills each entry's Workspace, Repo, Kind,
	// Resource and By, and returns an error naming the next step when add cannot apply to the
	// request. Nil for a source whose requests take Allow once and Deny alone.
	Entries func(q Request, add bool) ([]WorkingEntry, error)
	// AfterTransaction, when set, runs after each transaction on this Store value releases
	// the store's lock, with that transaction's error. It is a test seam: a test lands
	// another process's write (a host decision) exactly between two transactions of a caller,
	// which is how an interleaving across transactions is reproduced without timing. Nil in
	// production.
	AfterTransaction func(error)
	// PublishRecords copies every request record written to its workspace's jail-readable
	// approval record (record.go). The host's brokers and `yolo approve` set it; a test store
	// writing fixture workspaces does not.
	PublishRecords bool
	// PublishFailed, when set, hears each record that could not be published.
	PublishFailed func(Request, error)
}

// Revalidate must re-read approved scope, current host policy, and live jail/start
// identity for this request. At consumption it must also classify and scope-check
// the ACTUAL invocation captured by the caller (a set grant can cover a different
// command/repository from the filed request). Never scope-check only the parent.
// It must return an error for unknown/stale authority,
// must not acquire the transaction lock, and must never trust jail-provided policy.
type Revalidate func(Request) (Policy, error)

func currentPolicy(q Request, gate Revalidate) (Policy, error) {
	if gate == nil {
		return Policy{}, errors.New("current authority gate unavailable; retry after the host broker policy adapter is installed")
	}
	p, err := gate(q)
	if err != nil {
		return Policy{}, err
	}
	if err = validatePolicy(p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// SourceName is Source, or the store directory's name.
func (s *Store) SourceName() string {
	if s.Source != "" {
		return s.Source
	}
	return filepath.Base(s.Dir)
}

func (s *Store) audit(ctx context.Context, kind string, q Request, g Grant) error {
	if s.Audit != nil {
		return s.Audit(ctx, kind, q, g)
	}
	return nil
}

// CommandDigest binds canonical argv, repository and stdin digest, never a policy.
func CommandDigest(q Request) string { return digest(q) }

// Status reveals a request only to its exact authenticated holder and broker start.
func (s *Store) Status(jail, start, id string) (Request, error) {
	return s.StatusContext(context.Background(), jail, start, id)
}
func (s *Store) StatusContext(ctx context.Context, jail, start, id string) (Request, error) {
	var out Request
	err := s.transactionContext(ctx, func(r *records, _ time.Time) error {
		for _, q := range r.requests {
			if q.ID == id && q.Jail == jail && q.Start == start {
				out = q
				return nil
			}
		}
		return errors.New("request not found for this broker lifetime")
	})
	return out, err
}

type records struct {
	requests []Request
	grants   []Grant
	working  []WorkingEntry
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("store directory is not a real directory: %s", path)
	}
	return os.Chmod(path, 0700)
}

// transaction is the only mutator. A grant is usable only after its parent request
// is durably allowed; a crash between the two renames can leave no usable authority.
func (s *Store) transaction(fn func(*records, time.Time) error) error {
	return s.transactionContext(context.Background(), fn)
}
func (s *Store) transactionContext(ctx context.Context, fn func(*records, time.Time) error) (err error) {
	if s.AfterTransaction != nil {
		defer func() { s.AfterTransaction(err) }()
	}
	if err := privateDir(s.Dir); err != nil {
		return err
	}
	return brokerlock.WithTransactionContext(ctx, s.Dir, func() error {
		var err error
		for _, name := range []string{"requests", "grants", "working"} {
			if err := privateDir(filepath.Join(s.Dir, name)); err != nil {
				return err
			}
		}
		var r records
		if err = readRecords(filepath.Join(s.Dir, "requests"), &r.requests); err != nil {
			return err
		}
		if err = readRecords(filepath.Join(s.Dir, "grants"), &r.grants); err != nil {
			return err
		}
		if err = readRecords(filepath.Join(s.Dir, "working"), &r.working); err != nil {
			return err
		}
		adoptWorkspaces(&r)
		now := s.now()
		for i := range r.requests {
			q := &r.requests[i]
			if q.State == "pending" && !now.Before(q.Expires()) {
				q.State = "expired"
				q.Decided = q.Expires()
				if err = s.write("requests", q.ID, q); err != nil {
					return err
				}
			}
		}
		for i := range r.grants {
			g := &r.grants[i]
			if g.Ended.IsZero() && !now.Before(g.Expires) {
				g.Ended = g.Expires
				g.EndReason = EndExpired
				if err = s.write("grants", g.ID, g); err != nil {
					return err
				}
			}
		}
		for i := len(r.grants) - 1; i >= 0; i-- {
			g := r.grants[i]
			if !g.Ended.IsZero() && !now.Before(g.Ended.Add(7*24*time.Hour)) {
				if err = os.Remove(filepath.Join(s.Dir, "grants", g.ID+".json")); err != nil {
					return err
				}
				r.grants = append(r.grants[:i], r.grants[i+1:]...)
			}
		}
		for i := len(r.working) - 1; i >= 0; i-- {
			w := r.working[i]
			if (!w.Live() && !now.Before(w.Ended.Add(7*24*time.Hour))) || (w.Timed() && w.Live() && !now.Before(w.Expires.Add(7*24*time.Hour))) {
				if err = os.Remove(filepath.Join(s.Dir, "working", w.ID+".json")); err != nil {
					return err
				}
				r.working = append(r.working[:i], r.working[i+1:]...)
			}
		}
		for i := len(r.requests) - 1; i >= 0; i-- {
			q := r.requests[i]
			if !q.Decided.IsZero() && !now.Before(q.Decided.Add(7*24*time.Hour)) {
				live := false
				for _, g := range r.grants {
					if g.Request == q.ID {
						live = true
					}
				}
				if !live {
					if err = os.Remove(filepath.Join(s.Dir, "requests", q.ID+".json")); err != nil {
						return err
					}
					r.requests = append(r.requests[:i], r.requests[i+1:]...)
				}
			}
		}
		return fn(&r, now)
	})
}

// adoptWorkspaces gives each grant written before grants carried a workspace the workspace of
// the request it answered, which the store keeps while it keeps the grant (BB-D99's
// migration). It changes only the records read; the grant's next write stores it. A grant
// whose request is gone keeps no workspace, and so no workspace holds it.
func adoptWorkspaces(r *records) {
	filedIn := map[string]string{}
	for _, q := range r.requests {
		filedIn[q.ID] = q.Workspace
	}
	for i := range r.grants {
		if g := &r.grants[i]; g.Workspace == "" {
			g.Workspace = filedIn[g.Request]
		}
	}
}

// ResolveWorkspace is a grant holder's identity for a host workspace path: absolute, with its
// symlinks resolved, the rule runtime.ResolveWorkspace applies to the jail id, so `~/link` and
// the directory it names are one holder. A path that cannot be resolved (gone, or unreadable)
// keeps its lexical absolute form. "" stays "", which names no workspace.
func ResolveWorkspace(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		resolved = filepath.Clean(path)
	}
	if evaled, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = evaled
	}
	return resolved
}

// SameWorkspace reports whether two recorded workspace paths name one workspace, compared by
// ResolveWorkspace, so a record minted under a symlinked spelling, or before grants resolved
// one, still matches. An empty path names none.
func SameWorkspace(a, b string) bool {
	return a != "" && b != "" && (filepath.Clean(a) == filepath.Clean(b) || ResolveWorkspace(a) == ResolveWorkspace(b))
}

func validID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) || len(id) != 34 {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}
func readRecords[T any](dir string, out *[]T) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !validID(id, recordPrefix(filepath.Base(dir))) {
			return fmt.Errorf("invalid store record name %q", e.Name())
		}
		f, err := os.OpenFile(filepath.Join(dir, e.Name()), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("store record is not a regular file: %s", e.Name())
		}
		b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		f.Close()
		if err != nil {
			return err
		}
		if len(b) > 1<<20 {
			return errors.New("store record exceeds 1 MiB")
		}
		var v T
		if err = json.Unmarshal(b, &v); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return nil
}

// recordPrefix is the id prefix of a record kind's directory: r- requests, g- grants and
// w- working-set entries.
func recordPrefix(kind string) string {
	switch kind {
	case "grants":
		return "g-"
	case "working":
		return "w-"
	}
	return "r-"
}

func (s *Store) write(kind, id string, v any) error {
	if !validID(id, recordPrefix(kind)) {
		return errors.New("invalid record identity")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("store record exceeds 1 MiB")
	}
	dir := filepath.Join(s.Dir, kind)
	f, err := os.CreateTemp(dir, ".write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, id+".json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return err
	}
	if q, ok := requestOf(v); ok && kind == "requests" {
		s.publish(q)
	}
	return nil
}

// NewStartID draws the host broker lifetime identity (BB-D27).
func NewStartID() (string, error) { return newID("") }

func newID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
func digest(q Request) string {
	b, _ := json.Marshal([]any{q.Argv, q.Repo, q.StdinDigest})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func validatePolicy(p Policy) error {
	if p.Window <= 0 || p.MaxWindow <= 0 || p.Window > p.MaxWindow || p.Uses <= 0 || (p.Session && p.MaxWindow != sessionLimit) {
		return errors.New("invalid resolved windowed set policy")
	}
	return nil
}

// File joins an identical pending command only in this jail, lifetime and set.
func (s *Store) File(input Request) (Request, error) {
	return s.FileContext(context.Background(), input)
}
func (s *Store) FileContext(ctx context.Context, input Request) (Request, error) {
	var out Request
	if err := checkFiling(input); err != nil {
		return out, err
	}
	err := s.transactionContext(ctx, func(r *records, now time.Time) error {
		var err error
		out, err = s.fileIn(ctx, r, now, input)
		return err
	})
	return out, err
}

// checkFiling refuses a request that lacks what filing it needs.
func checkFiling(input Request) error {
	if input.Jail == "" || input.Start == "" || input.Workspace == "" || input.Set == "" || input.Repo == "" || len(input.Argv) == 0 || input.StdinDigest == "" {
		return errors.New("request needs host holder, workspace, set, command, repository and stdin digest")
	}
	return validatePolicy(input.Policy)
}

// fileIn joins input to an identical pending request of the same jail, lifetime and set, or
// files it new, inside the caller's transaction.
func (s *Store) fileIn(ctx context.Context, r *records, now time.Time, input Request) (Request, error) {
	if s.Revalidate != nil {
		if _, err := currentPolicy(input, s.Revalidate); err != nil {
			return Request{}, err
		}
	}
	input.Digest = digest(input)
	input.About = input.About.safe()
	for _, q := range r.requests {
		if q.Jail == input.Jail && q.Start == input.Start && q.Set == input.Set && q.Digest == input.Digest && q.State == "pending" {
			return q, nil
		}
	}
	if q, lapsed, err := s.lapsedIn(r, input); err != nil {
		return Request{}, err
	} else if lapsed {
		return Request{}, &LapsedAllowError{Request: q.ID}
	}
	if err := filingLimit(r, input, now); err != nil {
		return Request{}, err
	}
	id, err := newID("r-")
	if err != nil {
		return Request{}, err
	}
	input.ID = id
	input.Filed = now
	input.Decided = time.Time{}
	input.State = "pending"
	// The decision's facts are the store's to record, never the filer's to supply.
	input.Answer, input.Via, input.Until, input.Reason = "", "", time.Time{}, ""
	input.SpentAt, input.HookRan = time.Time{}, false
	if err := s.audit(ctx, "request", input, Grant{}); err != nil {
		return Request{}, err
	}
	if err := s.write("requests", id, input); err != nil {
		return Request{}, err
	}
	return input, nil
}

// FilingError is a failure of SpendExactOrFileContext while it joined or filed a request,
// after it found no grant to spend. Unwrap gives the cause: a *PendingLimitError, a
// *DenialCooldownError, or another failure.
type FilingError struct{ Err error }

func (e *FilingError) Error() string { return e.Err.Error() }
func (e *FilingError) Unwrap() error { return e.Err }

// SpendExactOrFileContext is ConsumeExactDispatchContext followed, when no grant covers the
// invocation, by FileContext, in ONE transaction. It spends a live exact grant for input (its
// workspace, jail, set and canonical digest) through revalidate, then runs dispatch under the
// same lock, as ConsumeExactDispatchContext does, and returns the grant with spent true.
// Otherwise it joins input to an identical pending request of the same jail, lifetime and set,
// or files it new, as FileContext does, and returns that request.
//
// Run as two transactions, an answer landing between them was lost: the request it allowed was
// no longer pending, so the filing made a new request, and nobody spent the grant the answer
// issued. In one transaction no decision can land between finding no grant and joining the
// request. A failure while joining or filing is a *FilingError; any other error is the spend's.
func (s *Store) SpendExactOrFileContext(ctx context.Context, input Request, revalidate Revalidate, dispatch func() error) (Grant, bool, Request, error) {
	var (
		g     Grant
		spent bool
		filed Request
	)
	if input.Workspace == "" || input.Jail == "" {
		return g, false, filed, errors.New("spending a grant needs the host workspace and the jail spending it")
	}
	input.Digest = digest(input)
	err := s.transactionContext(ctx, func(r *records, now time.Time) error {
		var err error
		g, spent, err = s.spendIn(ctx, r, now, input.Workspace, input.Jail, input.Set, input.Digest, revalidate, true)
		if err != nil || spent {
			return afterSpend(ctx, spent, err, dispatch)
		}
		if err = checkFiling(input); err == nil {
			filed, err = s.fileIn(ctx, r, now, input)
		}
		if err != nil {
			return &FilingError{err}
		}
		return nil
	})
	return g, spent, filed, err
}

// InspectContext reads the store's current records under its transaction lock without expiring,
// pruning, creating or rewriting any record. It is for broker decisions that must predict
// without spending a grant or changing the store.
func (s *Store) InspectContext(ctx context.Context) ([]Request, []Grant, []WorkingEntry, time.Time, error) {
	out, now, err := s.inspectContext(ctx, nil)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	return out.requests, out.grants, out.working, now, nil
}

// ExactInspection is one non-mutating store view and its exact-grant eligibility result.
type ExactInspection struct {
	Requests       []Request
	Grants         []Grant
	Working        []WorkingEntry
	Now            time.Time
	Grant          Grant
	Eligible       bool
	EligibilityErr error
}

// InspectExactGrantContext returns one existing-store snapshot and determines exact-grant
// eligibility against that same snapshot and clock while its existing lock is held. Revalidate
// must be read-only and must not acquire this store lock.
func (s *Store) InspectExactGrantContext(ctx context.Context, input Request, revalidate Revalidate) (ExactInspection, error) {
	if input.Workspace == "" || input.Jail == "" {
		return ExactInspection{}, errors.New("checking a grant needs the host workspace and jail identity")
	}
	input.Digest = digest(input)
	var result ExactInspection
	out, now, err := s.inspectContext(ctx, func(r *records, at time.Time) error {
		result.Now = at
		result.Requests, result.Grants, result.Working = r.requests, r.grants, r.working
		result.Grant, _, _, _, result.Eligible, result.EligibilityErr = eligibleGrantIn(
			r, at, input.Workspace, input.Set, input.Digest, revalidate, true)
		return nil
	})
	if err != nil {
		return ExactInspection{}, err
	}
	// The callback owns a decoded snapshot, not records held by the store. Use the final
	// adopted view returned by the inspection helper for all subsequent local decisions.
	result.Requests, result.Grants, result.Working, result.Now = out.requests, out.grants, out.working, now
	return result, nil
}

func (s *Store) inspectContext(ctx context.Context, inspect func(*records, time.Time) error) (records, time.Time, error) {
	var out records
	var now time.Time
	err := brokerlock.WithInspectionContext(ctx, s.Dir, func() error {
		for _, name := range []string{"requests", "grants", "working"} {
			dir := filepath.Join(s.Dir, name)
			fi, err := os.Lstat(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("store directory is not a real directory: %s", dir)
			}
			switch name {
			case "requests":
				if err := readRecords(dir, &out.requests); err != nil {
					return err
				}
			case "grants":
				if err := readRecords(dir, &out.grants); err != nil {
					return err
				}
			case "working":
				if err := readRecords(dir, &out.working); err != nil {
					return err
				}
			}
		}
		adoptWorkspaces(&out)
		now = s.now()
		if inspect != nil {
			return inspect(&out, now)
		}
		return nil
	})
	if err != nil {
		return records{}, time.Time{}, err
	}
	return out, now, nil
}

func (s *Store) List() ([]Request, []Grant, error) {
	var out records
	err := s.transaction(func(r *records, _ time.Time) error { out = *r; return nil })
	return out.requests, out.grants, err
}

// Decide compares the state seen by the frontend; only pending can transition.
func (s *Store) Decide(id, expected string, d Decision) (Grant, error) {
	var out Grant
	err := s.transaction(func(r *records, now time.Time) error {
		for i := range r.requests {
			q := &r.requests[i]
			if q.ID != id {
				continue
			}
			if q.State != expected || q.State != "pending" {
				return refusal(ErrAlreadyDecided, "request %s is already %s", id, q.State)
			}
			if err := validatePolicy(q.Policy); err != nil {
				return refusal(ErrNotConfirmed, "%v", err)
			}
			kind := "decision"
			if d.Deny {
				if d.For != 0 || d.Session || d.Uses != 0 {
					return errors.New("deny cannot also grant")
				}
				if err := s.audit(context.Background(), kind, *q, Grant{}); err != nil {
					return err
				}
				q.State = "denied"
				q.Decided = now
				q.Answer, q.Via = AnswerDeny, d.Via
				return s.write("requests", q.ID, q)
			}
			current, err := currentPolicy(*q, s.Revalidate)
			if err != nil {
				return refusal(ErrNotConfirmed, "%v", err)
			}
			// Frontends cannot widen either the filed request or today's ceiling.
			q.Policy.Window = min(q.Policy.Window, current.Window)
			q.Policy.MaxWindow = min(q.Policy.MaxWindow, current.MaxWindow)
			q.Policy.Uses = min(q.Policy.Uses, current.Uses)
			q.Policy.Session = q.Policy.Session && current.Session
			duration := min(15*time.Minute, q.Policy.MaxWindow)
			uses := 1
			command := q.Digest
			if d.Session && d.For != 0 {
				return errors.New("choose a duration or session, not both")
			}
			if d.For < 0 {
				return errors.New("grant duration must be positive")
			}
			if d.For != 0 || d.Session {
				duration = d.For
				if d.Session {
					if !q.Policy.Session {
						return refusal(ErrRefusedByPolicy, "session exceeds this set's policy ceiling")
					}
					duration = sessionLimit
				}
				if duration <= 0 || duration > q.Policy.MaxWindow {
					return refusal(ErrRefusedByPolicy, "duration exceeds this set's policy ceiling")
				}
				uses = q.Policy.Uses
				command = ""
			}
			if d.Uses != 0 {
				if d.Uses < 1 || d.Uses > uses {
					return refusal(ErrRefusedByPolicy, "uses exceed this grant's policy ceiling")
				}
				uses = d.Uses
			}
			gid, err := newID("g-")
			if err != nil {
				return err
			}
			out = Grant{ID: gid, Request: q.ID, Jail: q.Jail, Start: q.Start, Workspace: ResolveWorkspace(q.Workspace), Set: q.Set, Digest: command, Expires: now.Add(duration), Uses: uses, Issued: uses}
			if err = s.audit(context.Background(), kind, *q, out); err != nil {
				return err
			}
			if err = s.write("grants", gid, out); err != nil {
				return err
			}
			q.State = "allowed"
			q.Decided = now
			q.Via = d.Via
			switch {
			case command == "":
				q.Answer, q.Until = AnswerFor, out.Expires
			case d.Remember:
				q.Answer = AnswerRemember
			default:
				q.Answer = AnswerOnce
			}
			return s.write("requests", q.ID, q)
		}
		return refusal(ErrUnknownID, "request %q not found", id)
	})
	return out, err
}

// TimedGrantDefault is how long an Allow push to <ref> for a time lasts when the host gives no
// duration (boundary-broker.md §6.4).
const TimedGrantDefault = 15 * time.Minute

// DecideWorking answers a pending request with a per-resource grant instead of a command
// grant: d.Add puts the resources the request asks for into the workspace's working set, and
// otherwise d.For (TimedGrantDefault when zero) lets the workspace write to them for that long
// (boundary-broker.md BB-D76, git-broker.md GB-D26). The resources are the store's Entries
// hook's, so the source says what a request's resources are and refuses a request whose
// resources cannot be added (a tag). The request becomes allowed and no command grant is
// recorded; every jail of the workspace is covered through the entries. It returns the entries
// now holding those resources.
func (s *Store) DecideWorking(id string, d Decision) ([]WorkingEntry, error) {
	if s.Entries == nil {
		return nil, refusal(ErrRefusedByPolicy, "this source's requests take Allow once and Deny only")
	}
	var out []WorkingEntry
	err := s.transaction(func(r *records, now time.Time) error {
		for i := range r.requests {
			q := &r.requests[i]
			if q.ID != id {
				continue
			}
			if q.State != "pending" {
				return refusal(ErrAlreadyDecided, "request %s is already %s", id, q.State)
			}
			if err := validatePolicy(q.Policy); err != nil {
				return refusal(ErrNotConfirmed, "%v", err)
			}
			if d.Deny || d.Session || d.Uses != 0 || d.For < 0 || (d.Add && d.For != 0) {
				return errors.New("choose Add, or a positive duration, for a per-resource grant")
			}
			current, err := currentPolicy(*q, s.Revalidate)
			if err != nil {
				return refusal(ErrNotConfirmed, "%v", err)
			}
			duration := d.For
			if duration == 0 {
				duration = TimedGrantDefault
			}
			if !d.Add && duration > min(q.Policy.MaxWindow, current.MaxWindow) {
				return refusal(ErrRefusedByPolicy, "duration exceeds this set's policy ceiling of %s", min(q.Policy.MaxWindow, current.MaxWindow))
			}
			entries, err := s.Entries(*q, d.Add)
			if err != nil {
				return refusal(ErrRefusedByPolicy, "%v", err)
			}
			if len(entries) == 0 {
				return refusal(ErrRefusedByPolicy, "request %s names no resource a per-resource grant can cover", id)
			}
			var expires time.Time
			if !d.Add {
				expires = now.Add(duration)
			}
			for _, e := range entries {
				e.Expires = expires
				held, _, err := s.upsertWorkingIn(r, now, e)
				if err != nil {
					return err
				}
				out = append(out, held)
			}
			if err := s.audit(context.Background(), "decision", *q, Grant{ID: out[0].ID, Request: q.ID, Jail: q.Jail, Start: q.Start, Workspace: ResolveWorkspace(q.Workspace), Set: q.Set, Expires: expires}); err != nil {
				return err
			}
			q.State = "allowed"
			q.Decided = now
			q.Via = d.Via
			if d.Add {
				q.Answer = AnswerAdd
			} else {
				q.Answer, q.Until = AnswerFor, expires
			}
			return s.write("requests", q.ID, q)
		}
		return refusal(ErrUnknownID, "request %q not found", id)
	})
	return out, err
}

// Consume revalidates current authority inside the same transaction immediately
// before matching/spending. A missing gate fails closed for an otherwise live grant.
// Spending is persisted before execution: a crash cannot refund a use.
//
// A grant matches by workspace, never by jail or broker lifetime (boundary-broker.md BB-D99):
// any jail of the workspace whose request it answered may spend it, under any broker. jail is
// the jail spending it, recorded on the grant (Grant.UsedBy) and so in the grant-used audit.
func (s *Store) Consume(workspace, jail, set, commandDigest string, revalidate Revalidate) (Grant, bool, error) {
	return s.ConsumeContext(context.Background(), workspace, jail, set, commandDigest, revalidate)
}
func (s *Store) ConsumeContext(ctx context.Context, workspace, jail, set, commandDigest string, revalidate Revalidate) (Grant, bool, error) {
	return s.ConsumeDispatchContext(ctx, workspace, jail, set, commandDigest, revalidate, nil)
}

// ConsumeDispatchContext durably spends and audits before invoking dispatch under
// the same source lock. Dispatch must honor ctx and never nest a store/scope lock.
// Scope removal/revocation that wins this lock prevents dispatch; one that follows
// waits for bounded dispatch. A crash or dispatch failure never refunds the use.
// The gate receives the grant's parent request, whose jail and broker lifetime may be gone,
// so it must check the invocation being authorized against current host authority rather
// than the parent's holder.
func (s *Store) ConsumeDispatchContext(ctx context.Context, workspace, jail, set, commandDigest string, revalidate Revalidate, dispatch func() error) (Grant, bool, error) {
	return s.consumeDispatchContext(ctx, workspace, jail, set, commandDigest, revalidate, dispatch, false)
}

// ConsumeExactDispatchContext spends only a nonempty digest-bound grant whose parent request
// and granted digest match this invocation. Sources with legacy set-wide grants can opt into
// this path without changing ConsumeDispatchContext semantics for other sources.
func (s *Store) ConsumeExactDispatchContext(ctx context.Context, workspace, jail, set, commandDigest string, revalidate Revalidate, dispatch func() error) (Grant, bool, error) {
	if commandDigest == "" {
		return Grant{}, false, errors.New("spending an exact grant needs the canonical command digest")
	}
	return s.consumeDispatchContext(ctx, workspace, jail, set, commandDigest, revalidate, dispatch, true)
}

func (s *Store) consumeDispatchContext(ctx context.Context, workspace, jail, set, commandDigest string, revalidate Revalidate, dispatch func() error, exactOnly bool) (Grant, bool, error) {
	var out Grant
	found := false
	if workspace == "" || jail == "" {
		return out, false, errors.New("spending a grant needs the host workspace and the jail spending it")
	}
	err := s.transactionContext(ctx, func(r *records, now time.Time) error {
		var err error
		out, found, err = s.spendIn(ctx, r, now, workspace, jail, set, commandDigest, revalidate, exactOnly)
		return afterSpend(ctx, found, err, dispatch)
	})
	return out, found, err
}

// afterSpend runs dispatch, still under the store's lock, once a use was durably spent and
// audited; err is the spend's.
func afterSpend(ctx context.Context, found bool, err error, dispatch func() error) error {
	if err != nil || !found {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if dispatch != nil {
		return dispatch()
	}
	return nil
}

// spendIn spends and audits one use of the first live grant covering the invocation, inside
// the caller's transaction, and reports whether it found one.
func (s *Store) spendIn(ctx context.Context, r *records, now time.Time, workspace, jail, set, commandDigest string, revalidate Revalidate, exactOnly bool) (Grant, bool, error) {
	_, parent, grantAt, parentAt, found, err := eligibleGrantIn(r, now, workspace, set, commandDigest, revalidate, exactOnly)
	if err != nil || !found {
		return Grant{}, false, err
	}
	stored := &r.grants[grantAt]
	stored.Uses--
	stored.UsedBy = jail
	if stored.Uses == 0 {
		stored.Ended = now
		stored.EndReason = EndUsed
	}
	if err := s.write("grants", stored.ID, *stored); err != nil {
		return Grant{}, false, err
	}
	if stored.Uses == 0 && stored.Digest != "" && !parent.Spent {
		q := &r.requests[parentAt]
		q.Spent, q.SpentAt = true, now
		if err := s.write("requests", q.ID, *q); err != nil {
			return Grant{}, false, err
		}
	}
	if err := s.audit(ctx, "grant-used", parent, *stored); err != nil {
		return Grant{}, false, err
	}
	return *stored, true, nil
}

// eligibleGrantIn is the shared, non-mutating eligibility check used by prediction and spendIn.
func eligibleGrantIn(r *records, now time.Time, workspace, set, commandDigest string,
	revalidate Revalidate, exactOnly bool) (Grant, Request, int, int, bool, error) {
	for grantAt := range r.grants {
		g := &r.grants[grantAt]
		if !SameWorkspace(g.Workspace, workspace) || g.Set != set || !g.Ended.IsZero() || g.Uses <= 0 ||
			!now.Before(g.Expires) || (g.Digest != "" && g.Digest != commandDigest) || (exactOnly && g.Digest == "") {
			continue
		}
		for parentAt, q := range r.requests {
			if q.ID != g.Request || q.State != "allowed" || !SameWorkspace(q.Workspace, workspace) || q.Set != set ||
				(exactOnly && (q.Digest != commandDigest || q.Digest != g.Digest)) {
				continue
			}
			current, err := currentPolicy(q, revalidate)
			if err != nil {
				return Grant{}, Request{}, 0, 0, false, err
			}
			if current.MaxWindow < q.Policy.MaxWindow || current.Window < q.Policy.Window || current.Uses < q.Policy.Uses ||
				(q.Policy.Session && !current.Session) {
				return Grant{}, Request{}, 0, 0, false, errors.New("current policy narrowed; refile the command under the host's current policy")
			}
			return *g, q, grantAt, parentAt, true, nil
		}
	}
	return Grant{}, Request{}, 0, 0, false, nil
}

// ExactGrantEligibleContext reports whether spendIn would consume an exact grant, without effects.
func (s *Store) ExactGrantEligibleContext(ctx context.Context, input Request, revalidate Revalidate) (Grant, bool, error) {
	if input.Workspace == "" || input.Jail == "" {
		return Grant{}, false, errors.New("checking a grant needs the host workspace and jail identity")
	}
	input.Digest = digest(input)
	requests, grants, _, now, err := s.InspectContext(ctx)
	if err != nil {
		return Grant{}, false, err
	}
	r := records{requests: requests, grants: grants}
	g, _, _, _, found, err := eligibleGrantIn(&r, now, input.Workspace, input.Set, input.Digest, revalidate, true)
	return g, found, err
}

// Revoke ends one grant or working-set entry by its id, or with "all" every live grant
// (RevokeIn says what `all` leaves).
func (s *Store) Revoke(id string) error {
	_, err := s.RevokeIn(id, "")
	return err
}

// NoteHookRan records that the user's on_request hook started for request id. Like
// NoteHookFailure it decides nothing and is not audited.
func (s *Store) NoteHookRan(id string) error {
	return s.transaction(func(r *records, _ time.Time) error {
		for i := range r.requests {
			q := &r.requests[i]
			if q.ID != id {
				continue
			}
			if q.HookRan {
				return nil
			}
			q.HookRan = true
			return s.write("requests", q.ID, q)
		}
		return refusal(ErrUnknownID, "request %q not found", id)
	})
}

// NoteHookFailure records that the user's on_request failed for request id, with a one-line
// failure and the tail of its output, so every listing of the request shows it. It decides
// nothing and is not audited: the hook's output never moves a request's state.
func (s *Store) NoteHookFailure(id, failure, output string) error {
	return s.transaction(func(r *records, _ time.Time) error {
		for i := range r.requests {
			q := &r.requests[i]
			if q.ID != id {
				continue
			}
			q.HookFailure, q.HookOutput = failure, output
			return s.write("requests", q.ID, q)
		}
		return refusal(ErrUnknownID, "request %q not found", id)
	})
}

// End closes this broker lifetime for jail: each request it filed that is still pending is
// refused, since the call waiting on it is gone. It ends no grant: a grant is held by its
// workspace and outlives every broker lifetime (boundary-broker.md BB-D99). Report and retry
// an incomplete teardown.
func (s *Store) End(jail, start string) error {
	return s.transaction(func(r *records, now time.Time) error {
		for i := range r.requests {
			q := &r.requests[i]
			if q.Jail == jail && q.Start == start && q.State == "pending" {
				q.State = "refused"
				q.Decided = now
				q.Reason = ReasonBrokerStopped
				if err := s.write("requests", q.ID, q); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
