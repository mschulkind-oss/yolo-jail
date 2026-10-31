package ghbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// RequiredDelivery is the host-side visibility path for a pending request. The background
// broker log points to the existing host yolo approve command; a configured on_request hook
// may complement it, but manual host approval is always available.
type RequiredDelivery interface {
	Ready() bool
	Request(context.Context, brokerstore.Request) error
}

type logRequestDelivery struct {
	log io.Writer
}

func (d *logRequestDelivery) Ready() bool { return d != nil && d.log != nil }

func (d *logRequestDelivery) Request(ctx context.Context, q brokerstore.Request) error {
	if !d.Ready() {
		return errors.New("host broker log is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(d.log, "github-broker: request %s is pending; run `yolo approve %s` on the host\n", q.ID, q.ID)
	return err
}

// StoreAudit is the github store's required audit hook (brokercore.StoreAudit).
func StoreAudit(log *brokeraudit.Log) func(context.Context, string, brokerstore.Request, brokerstore.Grant) error {
	return brokercore.StoreAudit(log, Source)
}
func (b *Broker) window(ctx context.Context, req Request, jail string, d Decision, stdout, stderr func([]byte), ev *brokeraudit.Event) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if b.delivery == nil || !b.delivery.Ready() {
		return ExitNoPerm, "required request notice and answer delivery are unavailable; ask the user to run the command on the host"
	}
	if b.authority == nil || b.store == nil || b.store.Audit == nil || (b.runner == nil && b.execute == nil) {
		return ExitUnavailable, "host authority, audit or executor unavailable; restart the host broker"
	}
	d, p, err := b.authority.Classify(req.Argv, req.Repo)
	if err != nil {
		return ExitNoPerm, err.Error()
	}
	if d.Outcome == OutcomeStanding {
		code := ExitTempFail
		err := brokerlock.WithTransactionContext(ctx, b.store.Dir, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if jail != paths.JailShortHash(b.authority.scope.Container) || b.startID != b.authority.start {
				return errors.New("standing holder mismatch")
			}
			current, _, err := b.authority.Classify(req.Argv, req.Repo)
			if err != nil {
				return err
			}
			if current.Outcome != OutcomeStanding || current.Set != d.Set {
				return errors.New("standing policy changed; retry")
			}
			if err := b.audit.RequiredContext(ctx, brokeraudit.Event{Event: "standing-authorized", Service: Source, Jail: jail, Workspace: b.workspace, Argv: current.Argv, Repo: strings.Join(current.Repos, ","), Set: current.Set, Outcome: "authorized-before-dispatch"}); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			code = b.dispatch(ctx, req, current, stdout, stderr, ev)
			return nil
		})
		if err != nil {
			return ExitUnavailable, "standing authorization failed: " + err.Error() + "; repair the host policy/audit and retry"
		}
		return code, ""
	}
	// A user grant, a working-set entry or a timed grant covering the write runs it before any
	// Allow once is spent or a request is filed (workingset.go).
	t := targetOf(d.Argv, d.Repos)
	if code, msg, ok := b.coveredWrite(ctx, req, jail, d, t, stdout, stderr, ev); ok {
		return code, msg
	}
	sum := sha256.Sum256(req.Stdin)
	q := brokerstore.Request{Jail: jail, Start: b.startID, Workspace: b.workspace, LaunchID: b.authority.scope.LaunchID, Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: d.Set, Policy: p, StdinDigest: hex.EncodeToString(sum[:])}
	q.Digest = brokerstore.CommandDigest(q)
	ev.CommandDigest, ev.StdinDigest = q.Digest, q.StdinDigest
	// A grant is held by its workspace (boundary-broker.md BB-D99), so its parent request may
	// come from a jail or broker lifetime that is gone. The gate checks that the parent is this
	// workspace's, then revalidates BOTH the parent's command and the ACTUAL invocation against
	// this live launch's current scope and policy: the parent under this launch's holder, so a
	// set grant whose own repository left the scope covers nothing more (narrowing invalidates
	// a grant), and the invocation itself.
	gate := func(parent brokerstore.Request) (brokerstore.Policy, error) {
		if err := ctx.Err(); err != nil {
			return brokerstore.Policy{}, err
		}
		if !brokerstore.SameWorkspace(parent.Workspace, q.Workspace) {
			return brokerstore.Policy{}, errors.New("the grant belongs to another workspace")
		}
		held := parent
		held.Jail, held.Start, held.LaunchID, held.Workspace = q.Jail, q.Start, q.LaunchID, q.Workspace
		if _, err := b.authority.Revalidate(held); err != nil {
			return brokerstore.Policy{}, fmt.Errorf("the grant's own request no longer passes this launch's scope and policy: %w; refile the command", err)
		}
		return b.authority.Revalidate(q) // actual argv/repo, not only a set grant's parent
	}
	// run spends a grant through consume and, when one covers the command, dispatches it under
	// the store's lock and reports its exit.
	run := func(consume func(dispatch func() error) (brokerstore.Grant, bool, error)) (bool, int, string) {
		if ctx.Err() != nil {
			return true, ExitTempFail, "caller disconnected or broker stopped; nothing ran; run the same command again: a grant approved for it is kept across a broker or jail restart and covers the retry"
		}
		code := ExitTempFail
		var captured bytes.Buffer
		used, ok, err := consume(func() error {
			code = b.dispatch(ctx, req, d, captureInto(&captured, stdout), stderr, ev)
			return nil
		})
		if ok {
			ev.Request, ev.Grant = used.Request, used.ID
		}
		if err != nil {
			return true, ExitUnavailable, "authorization or required audit failed: " + err.Error() + "; retry after repairing the host authority"
		}
		if !ok {
			return false, 0, ""
		}
		if code == ghExitAuth {
			return true, ExitUnavailable, noHostLogin
		}
		// What an Allow once created joins the working set by that grant; a merge it ran ends
		// the PR's entry.
		b.afterWrite(t, d.Argv, code, captured.Bytes(), used.ID, stderr)
		return true, code, ""
	}
	// Spending and filing are one transaction: an answer landing between them would allow a
	// request no longer joined, leaving its grant unspent and this command waiting on a new one.
	var filed brokerstore.Request
	var filing *brokerstore.FilingError
	if ok, code, msg := run(func(dispatch func() error) (brokerstore.Grant, bool, error) {
		used, ok, f, err := b.store.SpendExactOrFileContext(ctx, q, gate, dispatch)
		filed = f
		if errors.As(err, &filing) {
			return used, false, nil
		}
		return used, ok, err
	}); ok {
		return code, msg
	}
	q = filed
	if filing != nil {
		err := filing.Err
		var limit *brokerstore.PendingLimitError
		if errors.As(err, &limit) {
			return ExitTempFail, limit.Error() + "; inspect those requests with `yolo gh status <id>` before retrying; nothing ran"
		}
		var denial *brokerstore.DenialCooldownError
		if errors.As(err, &denial) {
			return ExitNoPerm, denial.Error() + "; nothing ran and no new notification was filed; ask the host user before retrying"
		}
		return ExitUnavailable, "request filing failed: " + err.Error() + "; repair the host policy/audit and retry"
	}
	ev.Request = q.ID
	if err = b.delivery.Request(ctx, q); err != nil {
		return ExitTempFail, fmt.Sprintf("%s remains pending; delivery failed; run `yolo approve %s` on the host", q.ID, q.ID)
	}
	status, err := b.waitStatus(ctx, jail, q.ID)
	if err != nil {
		return ExitTempFail, fmt.Sprintf("%s remains pending; nothing ran; re-run the command once it is approved, if still wanted: a grant for it lasts across a broker or jail restart (`yolo gh status %s` answers only while this broker runs).%s%s", q.ID, q.ID, answerHint(t, q.ID)+dispatchHint(d.Argv, t), b.notifierNote(jail, q.ID))
	}
	if status.State != "allowed" {
		return ExitNoPerm, fmt.Sprintf("%s is %s; nothing ran; ask the host user before retrying", q.ID, status.State)
	}
	if ok, code, msg := run(func(dispatch func() error) (brokerstore.Grant, bool, error) {
		return b.store.ConsumeExactDispatchContext(ctx, b.workspace, jail, d.Set, q.Digest, gate, dispatch)
	}); ok {
		return code, msg
	}
	// Answered with `--add` or `--for`: no command grant, but an entry on the resource now.
	if code, msg, ok := b.coveredWrite(ctx, req, jail, d, t, stdout, stderr, ev); ok {
		return code, msg
	}
	return ExitNoPerm, "no live grant covers this command; refile and approve it on the host"
}

// notifierNote is a sentence for the agent when the user's on_request failed for request
// id, "" otherwise. It says only that it failed, never its output.
func (b *Broker) notifierNote(jail, id string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if q, err := b.store.StatusContext(ctx, jail, b.startID, id); err == nil && q.HookFailure != "" {
		return NotifierFailed
	}
	return ""
}

// NotifierFailed is what the agent is told when the user's on_request failed for its
// request.
const NotifierFailed = "; the user's notifier failed, so they may not have seen this request: tell them to run `yolo approve` on the host"

func (b *Broker) waitStatus(ctx context.Context, jail, id string) (brokerstore.Request, error) {
	limit := 30 * time.Second
	if b.waitLimit > 0 && b.waitLimit < limit {
		limit = b.waitLimit
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return brokerstore.Request{}, err
		}
		q, err := b.store.StatusContext(ctx, jail, b.startID, id)
		if err != nil {
			return q, err
		}
		if q.State != "pending" {
			return q, nil
		}
		select {
		case <-ctx.Done():
			return q, ctx.Err()
		case <-tick.C:
		}
	}
}
func (b *Broker) requestStatus(ctx context.Context, req Request, jail string, stdout, stderr func([]byte)) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if b.store == nil || b.authority == nil {
		stderr([]byte("github-broker: status unavailable; restart the broker on the host\n"))
		return ExitUnavailable
	}
	if _, err := b.authority.live(); err != nil {
		stderr([]byte("github-broker: " + err.Error() + "; relaunch the jail\n"))
		return ExitNoPerm
	}
	var q brokerstore.Request
	var err error
	if req.Operation == "wait" {
		q, err = b.waitStatus(ctx, jail, req.RequestID)
	} else {
		q, err = b.store.StatusContext(ctx, jail, b.startID, req.RequestID)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return ExitTempFail
		}
		stderr([]byte("github-broker: request not found for this jail; run `yolo approve` on the host\n"))
		return ExitNoPerm
	}
	stdout([]byte(q.ID + " " + q.State + "\n"))
	if q.State == "pending" && q.HookFailure != "" {
		stderr([]byte("github-broker: " + strings.TrimPrefix(NotifierFailed, "; ") + "\n"))
	}
	switch q.State {
	case "allowed":
		return 0
	case "pending":
		return ExitTempFail
	default:
		return ExitNoPerm
	}
}

func (b *Broker) dispatch(ctx context.Context, req Request, d Decision, stdout, stderr func([]byte), ev *brokeraudit.Event) int {
	if req.writeDeadline != nil {
		if deadline, ok := ctx.Deadline(); ok {
			req.writeDeadline(deadline)
		}
		stop := context.AfterFunc(ctx, func() { req.writeDeadline(time.Now()) })
		defer stop()
	}
	var stdin []byte
	if d.ReadsStdin {
		stdin = req.Stdin
		if stdin == nil {
			stdin = []byte{}
		}
	}
	var res RunResult
	if b.execute != nil {
		res = b.execute(ctx, d.Argv, stdin, stdout, stderr)
	} else {
		res = b.runner.RunContext(ctx, d.Argv, stdin, stdout, stderr)
	}
	ev.Outcome = "ran"
	ev.BytesOut = res.BytesOut
	ev.Redactions = res.Redactions
	return res.Exit
}
