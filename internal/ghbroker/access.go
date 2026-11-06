package ghbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const AccessProtocolVersion = 1

type accessNext struct {
	Who   string `json:"who"`
	Where string `json:"where"`
	Run   string `json:"run"`
}
type accessCall struct {
	Source    string   `json:"source"`
	Canonical []string `json:"canonical"`
	Digest    string   `json:"digest"`
}
type accessFact struct {
	Read  string `json:"read"`
	Value string `json:"value"`
}

// AccessStatus is the github broker's own workspace-only readout. It deliberately omits
// request argv, hook output, host paths and configuration values.
type AccessStatus struct {
	Schema       string             `json:"schema"`
	Source       string             `json:"source"`
	State        string             `json:"state"`
	Next         string             `json:"next"`
	Repositories []string           `json:"repositories"`
	Read         string             `json:"read"`
	ScopeGrants  []accessScopeGrant `json:"scope_grants"`
	Grants       []accessGrant      `json:"grants"`
	WorkingSet   []accessWorking    `json:"working_set"`
	Requests     []accessRequest    `json:"requests"`
	Limits       accessLimits       `json:"limits"`
}
type accessScopeGrant struct {
	Kind string `json:"kind"`
	Repo string `json:"repo"`
	Name string `json:"name,omitempty"`
}
type accessGrant struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Set      string    `json:"set"`
	Repo     string    `json:"repo,omitempty"`
	Resource string    `json:"resource,omitempty"`
	Expires  time.Time `json:"expires"`
	Uses     int       `json:"uses"`
}
type accessWorking struct {
	ID       string    `json:"id"`
	Repo     string    `json:"repo"`
	Kind     string    `json:"kind"`
	Resource string    `json:"resource"`
	By       string    `json:"by"`
	Expires  time.Time `json:"expires,omitempty"`
}
type accessRequest struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Filed   time.Time `json:"filed"`
	Expires time.Time `json:"expires"`
	Answer  string    `json:"answer,omitempty"`
	Used    bool      `json:"used,omitempty"`
}
type accessLimits struct {
	Pending       int       `json:"pending"`
	PendingCap    int       `json:"pending_cap"`
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
}

func (b *Broker) accessStatus(ctx context.Context, jail string) (AccessStatus, error) {
	out := AccessStatus{Schema: "yolo.access/1", Source: Source, State: "running", Repositories: []string{}, ScopeGrants: []accessScopeGrant{}, Grants: []accessGrant{}, WorkingSet: []accessWorking{}, Requests: []accessRequest{}, Limits: accessLimits{PendingCap: 3}}
	if b.authority == nil || b.store == nil {
		return out, errors.New("host authority or store is unavailable")
	}
	if b.runner == nil && b.execute == nil {
		out.State = "inert"
		if b.unavailable != "" {
			out.Next = "run `yolo check` on the host to inspect why the GitHub broker cannot use gh, repair it, then relaunch"
		} else {
			out.Next = "install GitHub CLI and log in on the host with `gh auth login`, then relaunch"
		}
	}
	if holder := b.holder(jail); holder != paths.JailShortHash(b.authority.scope.Container) {
		return out, errors.New("this endpoint does not serve the asserted jail")
	}
	f, err := b.authority.live()
	if err != nil {
		return out, err
	}
	scope := scopeOfFile(f)
	out.Repositories = scope.Repos()
	for i := range out.Repositories {
		out.Repositories[i] = safe(out.Repositories[i])
	}
	readAll := brokerscope.HoldsRead(f.Grants)
	readPartial := false
	for _, repo := range scope.Repos() {
		if _, missing := scope.unread([]string{repo}); missing {
			readPartial = true
			break
		}
	}
	switch {
	case !readAll:
		out.Read = "not-granted"
	case readPartial:
		out.Read = "partial"
	default:
		out.Read = "granted"
	}
	for _, grant := range f.Grants {
		name := grant.Branch
		if name == "" {
			name = grant.Tag
		}
		if name == "" {
			name = grant.Workflow
		}
		if name == "" {
			name = grant.Actor
		}
		out.ScopeGrants = append(out.ScopeGrants, accessScopeGrant{Kind: safe(grant.Kind), Repo: safe(grant.Repo), Name: safe(name)})
	}
	requests, grants, working, now, err := b.store.InspectContext(ctx)
	if err != nil {
		return out, err
	}
	owned := map[string]bool{}
	parents := map[string]brokerstore.Request{}
	for _, q := range requests {
		if !brokerstore.SameWorkspace(q.Workspace, b.workspace) {
			continue
		}
		if q.State == "pending" && q.Jail == b.holder(jail) && now.Before(q.Expires()) {
			out.Limits.Pending++
		}
		cooldownState, decided, requestState := q.State, q.Decided, q.State
		if q.State == "pending" && !now.Before(q.Expires()) {
			cooldownState, decided, requestState = "expired", q.Expires(), "expired"
		}
		if q.Jail == b.holder(jail) && (cooldownState == "denied" || cooldownState == "expired") && now.Before(decided.Add(10*time.Minute)) && decided.Add(10*time.Minute).After(out.Limits.CooldownUntil) {
			out.Limits.CooldownUntil = decided.Add(10 * time.Minute)
		}
		owned[q.ID] = true
		parents[q.ID] = q
		if q.Filed.Before(now.Add(-24*time.Hour)) && q.State != "pending" {
			continue
		}
		out.Requests = append(out.Requests, accessRequest{ID: safe(q.ID), State: safe(requestState), Filed: q.Filed, Expires: q.Expires(), Answer: safe(q.Answer), Used: q.Spent})
	}
	for _, g := range grants {
		if !owned[g.Request] || !brokerstore.SameWorkspace(g.Workspace, b.workspace) || !g.Ended.IsZero() || !now.Before(g.Expires) {
			continue
		}
		kind := "allow-once"
		if g.Digest == "" {
			kind = "timed"
		}
		parent := parents[g.Request]
		out.Grants = append(out.Grants, accessGrant{ID: safe(g.ID), Kind: kind, Set: safe(g.Set), Repo: safe(parent.Repo), Resource: safe(parent.About.Target), Expires: g.Expires, Uses: g.Uses})
	}
	for _, w := range working {
		if !brokerstore.SameWorkspace(w.Workspace, b.workspace) || !w.LiveAt(now) {
			continue
		}
		out.WorkingSet = append(out.WorkingSet, accessWorking{ID: safe(w.ID), Repo: safe(w.Repo), Kind: safe(w.Kind), Resource: safe(w.Resource), By: safe(w.By), Expires: w.Expires})
	}
	return out, nil
}

type askCoverage struct {
	By      string
	Expires time.Time
}

func (b *Broker) fileAsk(ctx context.Context, req Request, jail string, d Decision, policy brokerstore.Policy) (string, askCoverage, error) {
	if b.authority == nil || b.store == nil || b.store.Audit == nil || b.delivery == nil || !b.delivery.Ready() {
		return "", askCoverage{}, errors.New("request filing, host authority, audit or notice delivery is unavailable")
	}
	current, currentPolicy, err := b.authority.Classify(req.Argv, req.Repo)
	if err != nil {
		return "", askCoverage{}, err
	}
	if current.Outcome != OutcomeWindowed || current.Set != d.Set || currentPolicy != policy || strings.Join(current.Repos, ",") != strings.Join(d.Repos, ",") {
		return "", askCoverage{}, errors.New("current classification changed; run `yolo access` again")
	}
	target := targetOf(current.Argv, current.Repos)
	var issue issueState
	issueRead := false
	if len(target.coveringKinds()) != 0 && (target.Kind == targetPR || target.Kind == targetIssue || target.Kind == targetIssueOrPR) {
		issue, err = b.readIssue(ctx, target)
		issueRead = err == nil
	}
	// A command a runs grant could cover is checked by the same reads coveredWrite makes
	// (runsCovered), once, before the transaction; the transaction re-reads the grant.
	_, isRuns := runsOf(current.Argv, target)
	runsWorkflow, runsOK := "", false
	if kind, held := b.userGrantCovers(target, current.Argv, ""); isRuns && held && kind == GrantRuns {
		_, runsWorkflow, runsOK = b.runsCovered(ctx, target, current.Argv, func([]byte) {})
	}
	var actor string
	actorRead := false
	if approval, ok := approveRunOf(current.Argv, target); ok {
		run, readErr := b.readRun(ctx, target.Repo, approval)
		if readErr == nil && sameRepo(run.HeadRepo, target.Repo) {
			actor, actorRead = run.Actor, true
		}
	}
	sum := sha256.Sum256(req.Stdin)
	q := brokerstore.Request{Jail: jail, Start: b.startID, Workspace: b.workspace, LaunchID: b.authority.scope.LaunchID,
		Argv: current.Argv, Repo: strings.Join(current.Repos, ","), Set: current.Set, Policy: currentPolicy,
		StdinDigest: hex.EncodeToString(sum[:]), About: aboutOf(current.Argv, target)}
	q.Digest = brokerstore.CommandDigest(q)
	var standing askCoverage
	filed, coveredGrant, covered, err := b.store.FileIfStillAsksWithCoverageContext(ctx, q, b.exactGrantGate(ctx, q),
		func(working []brokerstore.WorkingEntry, now time.Time) (bool, error) {
			fresh, freshPolicy, err := b.authority.Classify(req.Argv, req.Repo)
			if err != nil || fresh.Outcome != OutcomeWindowed || fresh.Set != current.Set || freshPolicy != currentPolicy ||
				!slices.Equal(fresh.Argv, current.Argv) || !slices.Equal(fresh.Repos, current.Repos) {
				return false, errors.New("current classification changed; run `yolo access` again")
			}
			named := actor
			if isRuns {
				named = runsWorkflow
			}
			kind, userGrant := b.userGrantCovers(target, current.Argv, named)
			if userGrant && kind == GrantApproveRun && !actorRead {
				userGrant = false
			}
			if userGrant && kind == GrantRuns {
				userGrant = runsOK
			}
			if userGrant {
				standing = askCoverage{By: kind}
				return true, nil
			}
			kinds, resource := target.coveringKinds(), target.resource()
			if len(kinds) > 0 && resource != "" {
				for _, entry := range working {
					if !entry.LiveAt(now) || entry.Workspace != b.workspace || entry.Repo != target.storeRepo() ||
						entry.Resource != resource || !contains(kinds, entry.Kind) {
						continue
					}
					if target.Kind == targetPR || target.Kind == targetIssue || target.Kind == targetIssueOrPR {
						if !issueRead || issue.Merged {
							continue
						}
					}
					standing = askCoverage{By: "working-set " + entry.ID, Expires: entry.Expires}
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		return "", askCoverage{}, err
	}
	if covered {
		if coveredGrant.ID != "" {
			kind := "Allow once "
			if coveredGrant.Digest == "" {
				kind = "timed grant "
			}
			return "", askCoverage{By: kind + safe(coveredGrant.ID), Expires: coveredGrant.Expires}, nil
		}
		return "", standing, nil
	}
	if err := b.delivery.Request(ctx, filed); err != nil {
		fmt.Fprintf(b.log, "github-broker: filed request %s but host notice failed: %v\n", filed.ID, err)
	}
	return filed.ID, askCoverage{}, nil
}

func safe(s string) string { return termsafe.Visible(s) }

func newPrediction(req Request, d Decision, v evaluation) evaluation {
	stdinSum := sha256.Sum256(req.Stdin)
	q := brokerstore.Request{Argv: d.Argv, Repo: strings.Join(d.Repos, ","), StdinDigest: hex.EncodeToString(stdinSum[:]), Set: d.Set}
	if len(q.Argv) == 0 {
		q.Argv = v.Canonical
	}
	if q.Repo == "" {
		q.Repo = req.Repo
	}
	q.Digest = brokerstore.CommandDigest(q)
	displayArgv := append([]string{"gh"}, v.Canonical...)
	v.Schema = "yolo.access.prediction/1"
	v.Source = Source
	if !v.facts.reliesOnUntil.IsZero() {
		expires := v.facts.reliesOnUntil
		v.ReliesOnUntil = &expires
	}
	v.Call = accessCall{Source: Source, Canonical: safeArgs(displayArgv), Digest: q.Digest}
	v.Canonical = safeArgs(v.Canonical)
	v.Rule = safe(v.Rule)
	v.Facts = []accessFact{}
	if v.Next == nil {
		v.Next = []accessNext{}
	}
	if v.Verdict == "refused" && len(v.Next) == 0 {
		v.Next = refusalNext(v.Rule)
	}
	if v.Verdict == "asks" && len(v.Next) == 0 {
		v.Next = []accessNext{{Who: "agent", Where: "jail", Run: safe(shquote.Join(append([]string{"yolo", "access", "--ask", "--"}, displayArgv...)))}, {Who: "human", Where: "host", Run: "yolo approve <request-id>"}}
	}
	if v.Verdict == "runs" && v.CoveredBy == "" {
		v.CoveredBy = safe(v.Rule)
	}
	v.CommandDigest = q.Digest
	return v
}

func refusalNext(rule string) []accessNext {
	step := "yolo gh --help"
	switch {
	case strings.Contains(rule, "no read grant"):
		step = "yolo allow github read"
	case strings.Contains(rule, "auth token") || strings.Contains(rule, "host's GitHub login"):
		step = "yolo gh -- api <endpoint>"
	case strings.Contains(rule, "executor") || strings.Contains(rule, "host gh is unavailable"):
		step = "yolo check"
	}
	where := "jail"
	who := "agent"
	if strings.HasPrefix(step, "gh ") || strings.HasPrefix(step, "yolo allow ") || step == "yolo check" {
		where, who = "host", "human"
	}
	return []accessNext{{Who: who, Where: where, Run: step}}
}

func safeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = safe(a)
	}
	return out
}
func marshalAccess(v any) []byte { b, _ := json.Marshal(v); return append(b, '\n') }
