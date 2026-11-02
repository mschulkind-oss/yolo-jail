package gitbroker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// approval.go is a push asking for approval (docs/design/git-broker.md §5, GB-D16 to GB-D23):
// a push no grant or working-set entry covers whole, whose every ref could be approved, spends
// an Allow once the host already gave for exactly that push, or files one request for it,
// writes its notice to the broker's background log, and waits for the answer a bounded time
// (boundary-broker.md §3.2). Allowed in that time, the grant is spent and the push goes on;
// still pending or denied, every ref the push needed approval for is refused naming the
// request, and the agent pushes again once the host allowed it.

// pushWait is how long a push waits for its request's answer before it is refused as pending.
const pushWait = 30 * time.Second

// reasonNeedsApprovalPrefix starts the refusal of a ref whose push filed a request: then the
// request's id, and, once the host answered, how.
const reasonNeedsApprovalPrefix = "needs approval: "

// refusal is why a push that asked for approval is refused: reason goes on each ref's
// `! [remote rejected]` line and into the audit, and next is the message's last sentences, the
// step that works from where the user stands (docs/reference/happy-path-principle.md).
type refusal struct{ reason, next string }

// hostPush is the fallback every refusal can name: the push run on the host.
const hostPush = "push it from the host (`git push` in this workspace there)"

// pendingRefusal is a push whose request is still waiting for the host's answer: the menu of
// every path that answers it (menu.go, approvalwait.Menu), with the request's id.
func pendingRefusal(m approvalwait.Menu) refusal {
	return refusal{reasonNeedsApprovalPrefix + m.ID, " It needs approval. Allow once covers exactly this push, " +
		"once, within 15 minutes; a different push asks again.\n\n" + m.Render()}
}

// lapsedRefusal is a retry of a push the host allowed once as request id, whose window closed
// before the push ran. The store refuses it once, and the next run of the same push files a
// new request: the agent asks the human deliberately rather than a retry asking unnoticed.
func lapsedRefusal(id string) refusal {
	return refusal{reasonNeedsApprovalPrefix + id + ", allowed but its window closed",
		" The host allowed request " + id + ", but its 15-minute window closed before this push ran, so it " +
			"covers nothing now and no new request was filed. Tell the human, and only if they still want it, " +
			"run the same push again: that files a new request for them to answer; or " + hostPush + "."}
}

// answeredRefusal is a push whose request id the host answered, or closed, with state, and
// which no grant covers now. None of them sends the user back to `yolo approve <id>`, which
// refuses a request no longer pending.
func answeredRefusal(id, state string) refusal {
	again := " Run the same push again to file a new request, and ask the user to answer it with the " +
		"`yolo approve <id>` its refusal names."
	switch state {
	case "allowed":
		return refusal{reasonNeedsApprovalPrefix + id + ", allowed but its grant no longer covers this push",
			" The host allowed request " + id + ", but that Allow once no longer covers this push: another run of the " +
				"same push already spent it, the user revoked it, or this workspace's scope changed." + again}
	case "denied":
		return refusal{reasonNeedsApprovalPrefix + id + ", denied on the host",
			" The host denied request " + id + ": ask the user before pushing it again. The same push files no new " +
				"request for 10 minutes; or " + hostPush + "."}
	case "refused":
		return refusal{reasonNeedsApprovalPrefix + id + ", closed when its broker stopped",
			" Request " + id + " was closed unanswered because the broker that filed it stopped (its jail stopped " +
				"or restarted)." + again}
	case "expired":
		return refusal{reasonNeedsApprovalPrefix + id + ", expired unanswered",
			" Request " + id + " expired with no answer, and the same push files no new request for 10 minutes. " +
				"Run it again after that, or ask the user to " + hostPush + "."}
	}
	return refusal{reasonNeedsApprovalPrefix + id + ", " + state + " on the host",
		" Request " + id + " is " + state + " on the host and covers nothing. Run the same push again to file a new " +
			"request; `yolo approve` on the host lists the requests."}
}

// filingRefusal is a push whose request the store would not file, err saying why.
func filingRefusal(err error) refusal {
	var limit *brokerstore.PendingLimitError
	var denial *brokerstore.DenialCooldownError
	if id, ok := brokerstore.IsLapsedAllow(err); ok {
		return lapsedRefusal(id)
	}
	switch {
	case errors.As(err, &limit):
		return refusal{reasonNeedsApproval + "; this jail already has 3 requests waiting",
			" It needs approval, but this jail already has 3 requests waiting (" + strings.Join(limit.Requests, ", ") +
				"), so no new one was filed. Ask the user to run `yolo approve` on the host, which lists them, and to " +
				"answer each with `yolo approve <id>` or `yolo approve <id> --deny`; then run this push again."}
	case errors.As(err, &denial):
		return refusal{reasonNeedsApproval + "; the host denied this same push as " + denial.Request,
			" The host denied this same push as request " + denial.Request + ", or let it expire unanswered, so the " +
				"broker files no new request for it until " + denial.Until.Format(time.RFC3339) + ". Ask the user " +
				"before pushing it again, or " + hostPush + "."}
	}
	return storeFailure("the request could not be filed", err)
}

// storeRefusal is a push whose Allow once the store could not look up, err saying why.
func storeRefusal(err error) refusal {
	return storeFailure("the host could not check its grants", err)
}

// storeFailure is a push the request store failed: what failed, and err.
func storeFailure(what string, err error) refusal {
	return refusal{reasonNeedsApproval + "; " + what + " (" + err.Error() + ")",
		" It needs approval, but " + what + " (" + err.Error() + "). Run the push again; if it repeats, ask the " +
			"user to run `yolo approve` on the host, which reads the same store and reports what is wrong, or to " +
			hostPush + "."}
}

// RefusalNextSteps is the next step of every way a push asking for approval can be refused,
// for a sample request, so the CLI's tests can check that each command they name exists.
func RefusalNextSteps() []string {
	const id = "r-00000000000000000000000000000000"
	rs := []refusal{pendingRefusal((&pushMenu{project: []string{"yolo request git push main"}, ceiling: []string{AllowOnRequestCommand(grantBranch)}}).menu(id, []string{"refs/heads/main"})), lapsedRefusal(id), filingRefusal(&brokerstore.PendingLimitError{Requests: []string{id}}),
		filingRefusal(&brokerstore.DenialCooldownError{Request: id}), filingRefusal(errors.New("e")), storeRefusal(errors.New("e"))}
	for _, s := range []string{"allowed", "denied", "refused", "expired", "other"} {
		rs = append(rs, answeredRefusal(id, s))
	}
	var out []string
	for _, r := range rs {
		out = append(out, r.next)
	}
	return out
}

// pushAbout is what a push request does, for a person (brokerstore.About). The headline
// counts the refs, never names them: a ref name is the agent's text, so it goes in Refs
// alone. The remote is named only when every character of it is safe to show unescaped.
func pushAbout(repo string, needs []string) brokerstore.About {
	where := "a git remote"
	if repo != "" && brokerstore.SafeLabel(repo) {
		where = repo
	}
	refs := "1 ref"
	if len(needs) != 1 {
		refs = fmt.Sprintf("%d refs", len(needs))
	}
	return brokerstore.About{Action: brokerstore.ActionPush, Target: where, Headline: "push " + refs + " to " + where,
		Refs: slices.Clone(needs)}
}

// revalidate is this broker's push revalidation, bound to its launch and lifetime.
func (b *Broker) revalidate(q brokerstore.Request) (brokerstore.Policy, error) {
	return revalidatePush(b.scopeFile, b.bound, b.start, q)
}

// askForPush asks for approval of the push of cmds with the push options opts to repo, in
// which the refs needs are those
// nothing else covered. It returns the grant it spent and true when an Allow once covers the
// push: the use is spent and audited under the store's lock, which is released before the push
// is forwarded (GB-D19). Otherwise it returns the refusal each ref in needs is refused with.
//
// An answer that is not an Allow once (Allow push for a time, or Add to the working set,
// GB-D26) leaves no grant to spend: its entries are in the working-set store, so once the host
// allowed the request cover is asked again, and a push every ref of which it now covers goes on.
func (b *Broker) askForPush(ctx context.Context, repo string, cmds []gitproto.Command, opts, needs []string, cover coverer, ev *brokeraudit.Event) (brokerstore.Grant, refusal, bool) {
	q := b.pushRequest(repo, cmds, opts)
	q.NeedsApproval = needs
	q.About = pushAbout(repo, needs)
	ev.CommandDigest, ev.StdinDigest = q.Digest, q.StdinDigest
	// A grant is held by its workspace (BB-D99), so its request may come from a jail or broker
	// lifetime that is gone: the parent must be this workspace's, and both it, held by this
	// launch, and this push must pass the live scope now.
	gate := func(parent brokerstore.Request) (brokerstore.Policy, error) {
		if err := ctx.Err(); err != nil {
			return brokerstore.Policy{}, err
		}
		if !brokerstore.SameWorkspace(parent.Workspace, q.Workspace) {
			return brokerstore.Policy{}, errors.New("the grant belongs to another workspace")
		}
		held := parent
		held.Jail, held.Start, held.LaunchID, held.Workspace = q.Jail, q.Start, q.LaunchID, q.Workspace
		if _, err := b.revalidate(held); err != nil {
			return brokerstore.Policy{}, fmt.Errorf("the grant's own request no longer passes this launch's scope: %w", err)
		}
		return b.revalidate(q)
	}
	spend := func() (brokerstore.Grant, bool, *refusal) {
		g, ok, err := b.store.ConsumeExactDispatchContext(ctx, q.Workspace, q.Jail, setPush, q.Digest, gate, nil)
		if err != nil {
			r := storeRefusal(err)
			return g, false, &r
		}
		if ok {
			ev.Request, ev.Grant = g.Request, g.ID
		}
		return g, ok, nil
	}
	// Spending and filing are one transaction: an answer landing between them would allow a
	// request no longer joined, leaving its grant unspent and this push waiting on a new one.
	g, ok, filed, err := b.store.SpendExactOrFileContext(ctx, q, gate, nil)
	var filing *brokerstore.FilingError
	switch {
	case errors.As(err, &filing):
		return brokerstore.Grant{}, filingRefusal(filing.Err), false
	case err != nil:
		return g, storeRefusal(err), false
	case ok:
		ev.Request, ev.Grant = g.Request, g.ID
		return g, refusal{}, true
	}
	ev.Request = filed.ID
	// No notifier: the background log only (GB-D10, BB-D98). It names the request and nothing
	// else, since every jail on the machine shares the log.
	fmt.Fprintf(b.log, "%s: request %s is pending; run `yolo approve %s` on the host\n", Name, filed.ID, filed.ID)
	status, err := b.waitStatus(ctx, q.Jail, filed.ID)
	switch {
	case err != nil || status.State == "pending":
		m := pushMenuFrom(ctx)
		m.markListed()
		return brokerstore.Grant{}, pendingRefusal(m.menu(filed.ID, needs)), false
	case status.State != "allowed":
		return brokerstore.Grant{}, answeredRefusal(filed.ID, status.State), false
	}
	if g, ok, why := spend(); ok || why != nil {
		if why != nil {
			return g, *why, false
		}
		return g, refusal{}, true
	}
	if by, ok := b.coveredNow(cmds, needs, cover); ok {
		return brokerstore.Grant{ID: by}, refusal{}, true
	}
	return brokerstore.Grant{}, answeredRefusal(filed.ID, "allowed"), false
}

// coveredNow reports whether cover now covers every ref in needs, and the entry that covers the
// first: what a host's Allow push for a time or Add to the working set did to the push.
func (b *Broker) coveredNow(cmds []gitproto.Command, needs []string, cover coverer) (string, bool) {
	if cover == nil {
		return "", false
	}
	first := ""
	for _, c := range cmds {
		if !slices.Contains(needs, c.Ref) {
			continue
		}
		_, by, ok := cover(c)
		if !ok {
			return "", false
		}
		if first == "" {
			first = by
		}
	}
	return first, first != ""
}

// waitStatus polls request id until it is no longer pending, for at most the push wait.
func (b *Broker) waitStatus(ctx context.Context, jail, id string) (brokerstore.Request, error) {
	limit := pushWait
	if b.waitLimit > 0 && b.waitLimit < limit {
		limit = b.waitLimit
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		q, err := b.store.StatusContext(ctx, jail, b.start, id)
		if err != nil || q.State != "pending" {
			return q, err
		}
		select {
		case <-ctx.Done():
			return q, ctx.Err()
		case <-tick.C:
		}
	}
}
