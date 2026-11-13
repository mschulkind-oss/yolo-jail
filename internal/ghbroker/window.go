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
func (b *Broker) exactGrantGate(ctx context.Context, invocation brokerstore.Request) brokerstore.Revalidate {
	return func(parent brokerstore.Request) (brokerstore.Policy, error) {
		if err := ctx.Err(); err != nil {
			return brokerstore.Policy{}, err
		}
		if !brokerstore.SameWorkspace(parent.Workspace, invocation.Workspace) {
			return brokerstore.Policy{}, errors.New("the grant belongs to another workspace")
		}
		// Grants are held by workspace, not by the jail/start that filed their parent. Recheck
		// that parent under the current invocation's live holder, then check the actual argv.
		held := parent
		held.Jail, held.Start, held.LaunchID, held.Workspace = invocation.Jail, invocation.Start, invocation.LaunchID, invocation.Workspace
		if _, err := b.authority.Revalidate(held); err != nil {
			return brokerstore.Policy{}, fmt.Errorf("the grant's own request no longer passes this launch's scope and policy: %w; refile the command", err)
		}
		return b.authority.Revalidate(invocation)
	}
}

func (b *Broker) window(ctx context.Context, req Request, jail string, stdout, stderr func([]byte), ev *brokeraudit.Event) (int, string) {
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
	// Both act and evaluate enter the same pre-effect decision function. The effectful
	// transaction below still revalidates the live launch and exact request before dispatch.
	assessment := b.evaluateWrite(ctx, req, jail, d, p)
	if assessment.Verdict == "refused" && strings.Contains(assessment.Rule, "unavailable") {
		return ExitUnavailable, assessment.Rule + "; restart the host broker"
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
	// The decision facts above determine whether this write reaches the covered-write effects.
	// A non-covering fact read is not repeated after the decision; merged-item cleanup is the
	// existing act-only consequence of that fact.
	t := targetOf(d.Argv, d.Repos)
	if assessment.Verdict == "runs" {
		if assessment.facts.merged {
			_, _ = b.store.EndWorkingResource(b.workspace, t.storeRepo(), t.resource(), brokerstore.EndMerged,
				brokerstore.WorkingPR, brokerstore.WorkingIssue, brokerstore.WorkingMerge, brokerstore.WorkingApprove)
		}
		if code, msg, ok := b.coveredWrite(ctx, req, jail, d, t, stdout, stderr, ev, &assessment.facts); ok {
			return code, msg
		}
	} else if assessment.Verdict == "asks" {
		if assessment.message != "" {
			stderr([]byte(assessment.message))
		}
		if assessment.endMerged {
			_, _ = b.store.EndWorkingResource(b.workspace, t.storeRepo(), t.resource(), brokerstore.EndMerged,
				brokerstore.WorkingPR, brokerstore.WorkingIssue, brokerstore.WorkingMerge, brokerstore.WorkingApprove)
		}
	}
	sum := sha256.Sum256(req.Stdin)
	q := brokerstore.Request{Jail: jail, Start: b.startID, Workspace: b.workspace, LaunchID: b.authority.scope.LaunchID, Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: d.Set, Policy: p, StdinDigest: hex.EncodeToString(sum[:])}
	q.Digest = brokerstore.CommandDigest(q)
	q.About = aboutOf(d.Argv, t)
	ev.CommandDigest, ev.StdinDigest = q.Digest, q.StdinDigest
	gate := b.exactGrantGate(ctx, q)
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
			return ExitTempFail, limit.Error() + "; nothing ran. `yolo wait` lists them; ask the human to answer them with `yolo approve` on the host before retrying"
		}
		if id, ok := brokerstore.IsLapsedAllow(err); ok {
			return ExitNoPerm, lapsedMessage(id)
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
		return ExitTempFail, pendingMessage(t, d.Argv, q.ID, b.notifierNote(jail, q.ID))
	}
	if status.State != "allowed" {
		return ExitNoPerm, fmt.Sprintf("%s is %s; nothing ran; ask the host user before retrying", q.ID, status.State)
	}
	if ok, code, msg := run(func(dispatch func() error) (brokerstore.Grant, bool, error) {
		return b.store.ConsumeExactDispatchContext(ctx, b.workspace, jail, d.Set, q.Digest, gate, dispatch)
	}); ok {
		return code, naming(q.ID, msg)
	}
	// Answered with `--add` or `--for`: no command grant, but an entry on the resource now.
	if code, msg, ok := b.coveredWrite(ctx, req, jail, d, t, stdout, stderr, ev, nil); ok {
		return code, naming(q.ID, msg)
	}
	return ExitNoPerm, allowedUncoveredMessage(q.ID)
}

// naming is msg, a refusal after request id was filed, led by id when msg does not already
// name it, so the agent can relay which request it concerns; "" stays "".
func naming(id, msg string) string {
	if msg == "" || strings.Contains(msg, id) {
		return msg
	}
	return id + ": " + msg
}

// allowedUncoveredMessage is the refusal when the host allowed request id but this broker
// finds no grant or working-set entry of that answer covering the command: the answer was
// revoked or ran out first, or it was recorded by a yolo newer than this running broker, which
// cannot read it. Nothing ran, and a new run files a new request.
func allowedUncoveredMessage(id string) string {
	return "the host allowed " + id + ", but this broker finds no grant or working-set entry of that answer " +
		"covering this command (revoked or expired since, or recorded by a newer yolo than this running broker); " +
		"nothing ran. Next: run the same command again, which files a new request, and ask the human to run " +
		"`yolo approve <its id>` on the host; if this repeats right after an answer, relaunch the jail so its " +
		"host broker matches the host's yolo"
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

// waitStatus polls request id until it is answered, the wait limit passes, or ctx ends. The
// limit bounds the wait for an answer, never a read: each status read runs under ctx alone,
// so an answer already recorded is returned even when the read itself outlasts the limit
// (a slow store on a loaded host). A limit that passes while the request is still pending
// returns context.DeadlineExceeded.
func (b *Broker) waitStatus(ctx context.Context, jail, id string) (brokerstore.Request, error) {
	limit := 30 * time.Second
	if b.waitLimit > 0 && b.waitLimit < limit {
		limit = b.waitLimit
	}
	expired := time.NewTimer(limit)
	defer expired.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
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
		case <-expired.C:
			return q, context.DeadlineExceeded
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
