package ghbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

type evaluation struct {
	Schema        string       `json:"schema"`
	Source        string       `json:"source"`
	Call          accessCall   `json:"call"`
	CommandDigest string       `json:"command_digest"`
	InputsDigest  string       `json:"inputs_digest,omitempty"`
	Facts         []accessFact `json:"facts"`
	CoveredBy     string       `json:"covered_by"`
	Next          []accessNext `json:"next"`
	RequestID     string       `json:"request_id,omitempty"`
	ReliesOnUntil *time.Time   `json:"relies_on_until"`
	Verdict       string       `json:"verdict"`
	Rule          string       `json:"rule"`
	Canonical     []string     `json:"canonical"`
	facts         writeDecisionFacts
	message       string
	endMerged     bool
}

type writeDecisionFacts struct {
	coveredBy     string
	failed        string
	merged        bool
	mergedItem    bool
	limit         string
	lapsed        string
	pinned        []string
	userKind      string
	userGrant     bool
	actor         string
	runsTried     bool
	workingFound  bool
	workingEntry  brokerstore.WorkingEntry
	reliesOnUntil time.Time
	issueRead     bool
	issueState    issueState
	message       string
	unavailable   bool
}

// evaluateWrite gathers all pre-effect host facts before the pure decision. It performs no
// request filing, grant spend, working-set update or proposed write.
func (b *Broker) evaluateWrite(ctx context.Context, req Request, jail string, d Decision, policy brokerstore.Policy) evaluation {
	facts := writeDecisionFacts{}
	t := targetOf(d.Argv, d.Repos)
	if b.delivery == nil || !b.delivery.Ready() || b.authority == nil || b.store == nil ||
		b.store.Audit == nil || (b.runner == nil && b.execute == nil) {
		facts.failed = "required request notice, host authority, audit or executor is unavailable"
		facts.unavailable = true
	}
	var requests []brokerstore.Request
	var grants []brokerstore.Grant
	var working []brokerstore.WorkingEntry
	var now time.Time
	var exactGrant brokerstore.Grant
	var exactEligible bool
	var exactEligibilityErr error
	var coveringGrant brokerstore.Grant
	var hasCoveringGrant bool
	var coveringGrantErr error
	var q brokerstore.Request
	if b.store != nil {
		sum := sha256.Sum256(req.Stdin)
		launchID := ""
		if b.authority != nil {
			launchID = b.authority.scope.LaunchID
		}
		q = brokerstore.Request{Jail: jail, Start: b.startID, Workspace: b.workspace, LaunchID: launchID,
			Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: d.Set, Policy: policy, StdinDigest: hex.EncodeToString(sum[:])}
		q.Digest = brokerstore.CommandDigest(q)
		view, err := b.store.InspectExactGrantContext(ctx, q, b.exactGrantGate(ctx, q))
		if err != nil {
			facts.failed = "could not read broker standing: " + err.Error()
			facts.unavailable = true
		} else {
			requests, grants, working, now = view.Requests, view.Grants, view.Working, view.Now
			exactGrant, exactEligible, exactEligibilityErr = view.Grant, view.Eligible, view.EligibilityErr
			coveringGrant, hasCoveringGrant, coveringGrantErr = view.CoveringGrant, view.HasCoveringGrant, view.CoveringErr
			kind, grant := b.userGrantCovers(t, d.Argv, "")
			facts.userKind, facts.userGrant = kind, grant
			if grant && kind == GrantApproveRun {
				var message bytes.Buffer
				actor, covered := b.approveRunCovered(ctx, t, d.Argv, func(p []byte) { message.Write(p) })
				facts.message = message.String()
				grant = covered && actor != ""
				facts.actor, facts.userGrant = actor, grant
				if grant {
					_, grant = b.userGrantCovers(t, d.Argv, actor)
					facts.userGrant = grant
				}
			}
			if grant && kind == GrantRuns {
				facts.runsTried = true
				var message bytes.Buffer
				argv, named, covered := b.runsCovered(ctx, t, d.Argv, func(p []byte) { message.Write(p) })
				facts.message = message.String()
				if covered {
					// actor carries what the transaction re-checks the grant against: for a
					// runs grant, the workflow a re-run's run was read to be.
					facts.pinned, facts.actor = argv, named
				}
				grant = covered
				facts.userGrant = grant
			}
			if grant {
				facts.coveredBy = kind
			}
			kinds, resource := t.coveringKinds(), t.resource()
			if facts.coveredBy == "" && len(kinds) > 0 && resource != "" {
				for _, e := range working {
					if !e.LiveAt(now) || e.Workspace != b.workspace || e.Repo != t.storeRepo() || e.Resource != resource || !contains(kinds, e.Kind) {
						continue
					}
					facts.workingFound, facts.workingEntry = true, e
					if t.Kind == targetPR || t.Kind == targetIssue || t.Kind == targetIssueOrPR {
						state, readErr := b.readIssue(ctx, t)
						facts.issueRead, facts.issueState = true, state
						if readErr != nil {
							facts.failed = "could not read issue state: " + readErr.Error()
							facts.message = "github-broker: could not read whether " + t.describe() + " is still open (" + readErr.Error() + "), so its working-set entry does not cover this write, which asks\n"
							break
						}
						if state.Merged {
							facts.merged, facts.mergedItem = true, true
							facts.message = "github-broker: " + t.describe() + " is merged, so it has left this workspace's working set; this write asks\n"
							break
						}
					}
					if e.Timed() {
						facts.coveredBy = e.ID
						facts.reliesOnUntil = e.Expires
					} else {
						facts.coveredBy = "working-set " + e.ID
					}
					break
				}
			}
			if facts.coveredBy == "" && facts.failed == "" {
				if exactEligible {
					facts.coveredBy = "Allow once " + exactGrant.ID
					facts.reliesOnUntil = exactGrant.Expires
				} else if hasCoveringGrant {
					facts.coveredBy = "grant " + coveringGrant.ID
					facts.reliesOnUntil = coveringGrant.Expires
				} else if exactEligibilityErr != nil || coveringGrantErr != nil {
					facts.failed, facts.unavailable = "could not validate a live approval grant", true
				}
			}
			if facts.coveredBy == "" && facts.failed == "" {
				for _, r := range requests {
					if r.State != "allowed" || r.Lapsed || r.Spent || r.Set != d.Set || r.Digest != q.Digest ||
						!brokerstore.SameWorkspace(r.Workspace, b.workspace) {
						continue
					}
					grantCount, expiredUnspent := 0, true
					for _, g := range grants {
						if g.Request != r.ID {
							continue
						}
						grantCount++
						if g.Digest == "" || g.Uses != g.Issued || !g.Ended.IsZero() && g.EndedBy() != brokerstore.EndExpired ||
							g.Ended.IsZero() && now.Before(g.Expires) {
							expiredUnspent = false
						}
					}
					if grantCount > 0 && expiredUnspent {
						facts.lapsed = "request " + r.ID + " was allowed, but its exact grant expired before the command ran"
						break
					}
				}
			}
			if facts.coveredBy == "" && facts.failed == "" && facts.lapsed == "" {
				var pending []string
				var cooldown string
				joined := false
				for _, r := range requests {
					pendingNow := r.State == "pending" && now.Before(r.Expires())
					effectiveState, decided := r.State, r.Decided
					if r.State == "pending" && !pendingNow {
						effectiveState, decided = "expired", r.Expires()
					}
					if r.Jail == jail && r.Start == b.startID && r.Set == d.Set && r.Digest == q.Digest && pendingNow {
						joined = true
					}
					if r.Jail == jail && pendingNow {
						pending = append(pending, r.ID)
					}
					if r.Jail == jail && r.Digest == q.Digest && (effectiveState == "denied" || effectiveState == "expired") && now.Before(decided.Add(10*time.Minute)) {
						cooldown = "denial cooldown after " + r.ID
					}
				}
				if !joined {
					facts.limit = cooldown
					if len(pending) >= 3 {
						facts.limit = "pending request limit reached"
					}
				}
			}
		}
	}
	v := decideWrite(d, facts)
	v.facts, v.message = facts, facts.message
	return v
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// decideWrite is the pure write verdict over classification, standing and pre-read facts.
func decideWrite(d Decision, facts writeDecisionFacts) evaluation {
	out := evaluation{Verdict: "asks", Rule: "no lasting grant covers this write", Canonical: d.Argv}
	if facts.pinned != nil {
		out.Canonical = facts.pinned
	}
	switch d.Outcome {
	case OutcomeRefused, OutcomeOutOfScope:
		out.Verdict, out.Rule = "refused", d.Reason
		return out
	}
	if facts.unavailable {
		out.Verdict, out.Rule = "refused", facts.failed
		return out
	}
	if facts.coveredBy != "" {
		out.Verdict, out.Rule = "runs", facts.coveredBy
		return out
	}
	if facts.lapsed != "" {
		out.Verdict, out.Rule = "refused", facts.lapsed
		return out
	}
	if facts.failed != "" {
		out.Rule = facts.failed + "; the write asks"
		return out
	}
	if facts.merged {
		out.Rule = "the item is merged"
		out.endMerged = facts.mergedItem
		return out
	}
	if facts.limit != "" {
		out.Verdict, out.Rule = "refused", facts.limit
	}
	return out
}

func writeEvaluationJSON(v evaluation) []byte {
	data, _ := json.Marshal(v)
	return append(data, '\n')
}
