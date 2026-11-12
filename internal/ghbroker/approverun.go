package ghbroker

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// approverun.go is the `approve-run` user grant, modeled on the `runs` grant
// (runs.go): a grant naming a GitHub login, or `*`, on a repository lets the broker approve
// a workflow run held at action_required unasked, but only when one host read of the run shows
// that actor triggered it and that its head is the repository itself. A run whose head is a fork
// is never covered, whatever the actor: approving it runs code from outside the repository with
// the workflow's permissions. The one covered command is `gh api -X POST
// repos/{o}/{r}/actions/runs/{id}/approve`, with a numeric id and no body; an approval of a
// deployment (pending_deployments) and every workflow command a runs grant does not cover fall
// through to a timed grant or Allow once, as any repository-level write does.

// GrantApproveRun is the user grant kind for approving a workflow run, as packs/github declares
// it; a covered approval is audited under the set of the same name, as a run is under its.
const GrantApproveRun = brokerscope.KindApproveRun

// runApproval is one run approval a canonical argv asks for.
type runApproval struct {
	RunID string // the run's numeric id, as the endpoint names it
}

// approveRunOf is the run approval the canonical argv asks for, ok false for anything else:
// `gh api` with method POST, stated (`-X POST`), of repos/{o}/{r}/actions/runs/{id}/approve on
// t's repository, written plainly, with a numeric id, no field and no --input. An endpoint
// carrying a query or a fragment is never one.
func approveRunOf(argv []string, t writeTarget) (runApproval, bool) {
	if t.Kind != targetRepo || t.Repo == "" {
		return runApproval{}, false
	}
	p, err := parseArgv(argv)
	if err != nil || p.cmd == nil || p.cmd.path != "api" {
		return runApproval{}, false
	}
	if len(p.positionals) != 1 || p.has("input") || p.has("field") || p.has("raw-field") ||
		strings.ContainsAny(p.positionals[0], "?#") {
		return runApproval{}, false
	}
	vals := p.values("method")
	if len(vals) == 0 || !strings.EqualFold(vals[len(vals)-1], "POST") {
		return runApproval{}, false
	}
	path, why := apiEndpointPath(p.positionals[0])
	if why != "" {
		return runApproval{}, false
	}
	m := apiRepoPathRE.FindStringSubmatch(path)
	if m == nil || !strings.EqualFold(m[1]+"/"+m[2], t.Repo) {
		return runApproval{}, false
	}
	rest := strings.Split(strings.Trim(m[3], "/"), "/")
	for _, seg := range rest {
		if d, err := url.PathUnescape(seg); err != nil || d != seg {
			return runApproval{}, false
		}
	}
	if len(rest) != 4 || rest[0] != "actions" || rest[1] != "runs" || rest[3] != "approve" || !numericID(rest[2]) {
		return runApproval{}, false
	}
	return runApproval{RunID: rest[2]}, true
}

// numericID reports whether s is a run id as GitHub writes one: decimal digits, no leading zero.
func numericID(s string) bool {
	if s == "" || len(s) > 20 || s[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// approveRunGrantHeld reports whether grants hold an approve-run grant on repo (or `*`) naming
// actor or `*`. An empty actor matches any approve-run grant on repo: that is the look before the
// run is read, which only says a grant could cover it.
func approveRunGrantHeld(grants []brokerscope.Grant, repo, actor string) bool {
	for _, g := range grants {
		if g.Kind == GrantApproveRun && grantRepoCovers(g, repo) &&
			(actor == "" || g.Actor == brokerscope.AnyActor || strings.EqualFold(g.Actor, actor)) {
			return true
		}
	}
	return false
}

// heldRun is what the broker's own read of a run says about it.
type heldRun struct {
	Actor    string // triggering_actor.login
	HeadRepo string // head_repository.full_name
}

// readRun is the broker's own host read of repos/{o}/{r}/actions/runs/{id}: who triggered the
// run and which repository its head is in. Any failed or unreadable read, or one that names
// another run, an empty actor or no head repository, is an error, and the approval asks.
func (b *Broker) readRun(ctx context.Context, repo string, a runApproval) (heldRun, error) {
	var obj struct {
		ID              uint64 `json:"id"`
		TriggeringActor *struct {
			Login string `json:"login"`
		} `json:"triggering_actor"`
		HeadRepository *struct {
			FullName string `json:"full_name"`
		} `json:"head_repository"`
	}
	if err := b.hostRead(ctx, "repos/"+repo+"/actions/runs/"+a.RunID, &obj); err != nil {
		return heldRun{}, err
	}
	switch {
	case strconv.FormatUint(obj.ID, 10) != a.RunID:
		return heldRun{}, fmt.Errorf("the answer names run %d, not %s", obj.ID, a.RunID)
	case obj.TriggeringActor == nil || obj.TriggeringActor.Login == "":
		return heldRun{}, fmt.Errorf("the answer names no triggering actor")
	case obj.TriggeringActor.Login == brokerscope.AnyActor || !brokerscope.ValidGrantActor(obj.TriggeringActor.Login):
		return heldRun{}, fmt.Errorf("the answer's triggering actor %q is not a GitHub login", obj.TriggeringActor.Login)
	case obj.HeadRepository == nil || obj.HeadRepository.FullName == "":
		return heldRun{}, fmt.Errorf("the answer names no head repository")
	}
	return heldRun{Actor: obj.TriggeringActor.Login, HeadRepo: obj.HeadRepository.FullName}, nil
}

// approveRunHint is the clause a pending request for a run approval adds: the lasting grant that
// would let it run unasked. "" for any other write.
func approveRunHint(argv []string, t writeTarget) string {
	a, ok := approveRunOf(argv, t)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" A lasting right to approve run %s, and every run the same actor triggers from %s itself, is an approve-run grant "+
		"naming that actor's login (`yolo request github approve_runs <actor>`, such as `yolo request github approve_runs 'github-actions[bot]'`, "+
		"then `yolo config apply` on the host); a run from a fork still asks.", a.RunID, t.Repo)
}

// approveRunCovered is coveredWrite's check of a run approval an approve-run grant may cover:
// one host read of the run, whose head must be the repository itself and whose triggering actor
// an approve-run grant must name (or `*`). It returns that actor and true, or says on stderr why
// the grant does not cover the approval, which then asks.
func (b *Broker) approveRunCovered(ctx context.Context, t writeTarget, argv []string, stderr func([]byte)) (string, bool) {
	a, _ := approveRunOf(argv, t)
	run, err := b.readRun(ctx, t.Repo, a)
	switch {
	case err != nil:
		stderr([]byte("github-broker: could not read run " + a.RunID + " on " + t.Repo + " (" + err.Error() +
			"), so this workspace's approve-run grant does not cover approving it, which asks\n"))
		return "", false
	case !strings.EqualFold(run.HeadRepo, t.Repo):
		stderr([]byte("github-broker: run " + a.RunID + "'s head is in " + strconv.Quote(run.HeadRepo) + ", not " + t.Repo +
			" itself; this workspace's approve-run grant never covers a fork's run, so approving it asks\n"))
		return "", false
	}
	if _, held := b.userGrantCovers(t, argv, run.Actor); !held {
		stderr([]byte("github-broker: run " + a.RunID + " was triggered by " + run.Actor + ", whom no approve-run grant " +
			"of this workspace names, so approving it asks (`yolo request github approve_runs '" + run.Actor +
			"'`, then `yolo config apply` on the host, grants it)\n"))
		return "", false
	}
	return run.Actor, true
}
