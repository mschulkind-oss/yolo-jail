package gitbroker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

type gitAccessStatus struct {
	Schema       string                `json:"schema"`
	Source       string                `json:"source"`
	State        string                `json:"state"`
	Read         string                `json:"read"`
	Next         string                `json:"next"`
	Repositories []string              `json:"repositories"`
	ScopeGrants  []gitAccessScopeGrant `json:"scope_grants"`
	Requests     []gitAccessRequest    `json:"requests"`
	Grants       []gitAccessGrant      `json:"grants"`
	WorkingSet   []gitAccessWorking    `json:"working_set"`
	Limits       gitAccessLimits       `json:"limits"`
}

type gitAccessScopeGrant struct {
	Kind string `json:"kind"`
	Repo string `json:"repo"`
	Name string `json:"name,omitempty"`
}
type gitAccessRequest struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Answer  string    `json:"answer,omitempty"`
	Filed   time.Time `json:"filed"`
	Expires time.Time `json:"expires"`
	Used    bool      `json:"used,omitempty"`
}
type gitAccessGrant struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Set      string    `json:"set"`
	Repo     string    `json:"repo"`
	Resource string    `json:"resource"`
	Expires  time.Time `json:"expires"`
	Uses     int       `json:"uses"`
}
type gitAccessWorking struct {
	ID       string    `json:"id"`
	Repo     string    `json:"repo"`
	Kind     string    `json:"kind"`
	Resource string    `json:"resource"`
	By       string    `json:"by"`
	Expires  time.Time `json:"expires,omitempty"`
}
type gitAccessLimits struct {
	Pending       int       `json:"pending"`
	PendingCap    int       `json:"pending_cap"`
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
}

// accessStatus reads only the live host-approved scope and the asserted workspace's
// records. No request argv, hook, credential, or settings value crosses this boundary.
func (b *Broker) accessStatus(ctx context.Context, jail string) (gitAccessStatus, error) {
	out := gitAccessStatus{Schema: "yolo.access/1", Source: Source, State: "running", Repositories: []string{}, ScopeGrants: []gitAccessScopeGrant{}, Requests: []gitAccessRequest{}, Grants: []gitAccessGrant{}, WorkingSet: []gitAccessWorking{}}
	out.Limits.PendingCap = 3
	if b.store == nil || (jail != paths.HostServicesDirName(holderJail(b.bound)) && !(strings.HasPrefix(jail, paths.HostServicesSessionPrefix(b.bound.Container)) && paths.IsHostServicesSessionDirName(jail))) {
		return out, errors.New("this endpoint does not serve the asserted jail or its store")
	}
	f, err := brokercore.Live(Source, b.scopeFile, b.bound, b.start)
	if err != nil {
		return out, err
	}
	if f.Form != brokerscope.FormRemoteURL {
		return out, errors.New("this launch has no remote-url scope")
	}
	invalidRemote := false
	if brokerscope.HoldsRead(f.Grants) {
		out.Read = "granted"
		for _, remote := range f.Repos {
			repo, err := gitproto.Identity(remote)
			if err != nil {
				out.Read = "partial"
				invalidRemote = true
				continue
			}
			if !brokerscope.ReadCovers(f.Grants, repo, func(a, b string) bool { return a == b }) {
				out.Read = "partial"
			}
		}
	} else {
		out.Read = "not-granted"
	}
	for _, remote := range f.Repos {
		repo, err := gitproto.Identity(remote)
		if err != nil {
			invalidRemote = true
			// The configured remote may include user-info. Never echo an invalid URL.
			repo = "invalid remote (run `yolo check` on the host)"
		}
		out.Repositories = append(out.Repositories, termsafe.Visible(repo))
	}
	if invalidRemote {
		out.Next = "run `yolo check` on the host, then correct the repository URL with `git remote set-url <remote> <url>` and relaunch"
	} else if out.Read != "granted" {
		out.Next = "ask the human to run `yolo allow git fetch` on the host in this workspace, then relaunch"
	}
	for _, g := range f.Grants {
		name := g.Branch
		if name == "" {
			name = g.Tag
		}
		out.ScopeGrants = append(out.ScopeGrants, gitAccessScopeGrant{termsafe.Visible(g.Kind), termsafe.Visible(g.Repo), termsafe.Visible(name)})
	}
	requests, grants, working, now, err := b.store.InspectContext(ctx)
	if err != nil {
		return out, err
	}
	parents := make(map[string]brokerstore.Request)
	for _, q := range requests {
		if !brokerstore.SameWorkspace(q.Workspace, b.bound.Workspace) {
			continue
		}
		parents[q.ID] = q
		state, decided := q.State, q.Decided
		if state == "pending" && !now.Before(q.Expires()) {
			state, decided = "expired", q.Expires()
		}
		if state == "pending" && q.Jail == holderJail(b.bound) {
			out.Limits.Pending++
		}
		if (state == "denied" || state == "expired") && q.Jail == holderJail(b.bound) && now.Before(decided.Add(10*time.Minute)) && decided.Add(10*time.Minute).After(out.Limits.CooldownUntil) {
			out.Limits.CooldownUntil = decided.Add(10 * time.Minute)
		}
		if q.Filed.Before(now.Add(-24*time.Hour)) && q.State != "pending" {
			continue
		}
		out.Requests = append(out.Requests, gitAccessRequest{termsafe.Visible(q.ID), termsafe.Visible(state), termsafe.Visible(q.Answer), q.Filed, q.Expires(), q.Spent})
	}
	for _, g := range grants {
		parent, ok := parents[g.Request]
		if !ok || !brokerstore.SameWorkspace(g.Workspace, b.bound.Workspace) || !g.Ended.IsZero() || !now.Before(g.Expires) {
			continue
		}
		kind := "allow-once"
		if g.Digest == "" {
			kind = "timed"
		}
		out.Grants = append(out.Grants, gitAccessGrant{termsafe.Visible(g.ID), kind, termsafe.Visible(g.Set), termsafe.Visible(parent.Repo), termsafe.Visible(parent.About.Target), g.Expires, g.Uses})
	}
	for _, w := range working {
		if !brokerstore.SameWorkspace(w.Workspace, b.bound.Workspace) || !w.LiveAt(now) {
			continue
		}
		out.WorkingSet = append(out.WorkingSet, gitAccessWorking{termsafe.Visible(w.ID), termsafe.Visible(w.Repo), termsafe.Visible(w.Kind), termsafe.Visible(w.Resource), termsafe.Visible(w.By), w.Expires})
	}
	return out, nil
}

func marshalGitStatus(status gitAccessStatus) []byte {
	body, _ := json.Marshal(status)
	return body
}
