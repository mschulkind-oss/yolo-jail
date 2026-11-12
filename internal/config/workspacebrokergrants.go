package config

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// WorkspaceBrokerGrants is what one workspace config's own words grant one broker source, as the
// scope projection makes them (docs/design/readable-config.md RC-D60): the concrete grants, and
// the legacy grants the projection could not place on a concrete repository, each with why.
type WorkspaceBrokerGrants struct {
	Source string
	// Grants are the concrete grants, each once, sorted by their text.
	Grants []brokerscope.Grant
	// Unplaced are the projection's reasons for each grant it left off every repository, sorted.
	Unplaced []string
}

// WorkspaceBrokerGrantsOf is, per declared broker source, the concrete grants a workspace config
// asks for, whichever spelling asks: the legacy `brokered.<source>.grants`, placed on its concrete
// repository as the readable launch places it (legacyWorkspaceGrant), and the readable
// `requests.<source>.*`, each capability expanded through the declaring pack's
// kind_capabilities over the scope's targets (readableRequestGrants). The targets are the
// workspace's remotes, read from its git config, plus `requests.<source>.repos` and
// `brokered.<source>.repos`. A source declaring `unqualified_requests = "origin"` lowers its
// unqualified request lines to origin alone, as the gate does (GW-D79).
//
// It is what the project asks for, before any ceiling: the user's `allow.<source>` or
// `grant_kinds` gates both spellings alike, and is the user config's own fact. A read grant is
// left out the same way: it is the user's (BB-D107). A source the config asks nothing of is left
// out.
func WorkspaceBrokerGrantsOf(cfg *jsonx.OrderedMap, workspace string, declarations []ReadableBrokerDeclaration) []WorkspaceBrokerGrants {
	return workspaceBrokerGrants(cfg, workspace, declarations, true)
}

// LegacyWorkspaceBrokerGrantsOf is WorkspaceBrokerGrantsOf before `unqualified_requests` narrowed
// anything: every unqualified request line reaches every remote on the host and every listed
// repository. It is the migration's BEFORE side only, the baseline a conversion must preserve
// the writes of. It grants nothing and is never the gate's projection.
func LegacyWorkspaceBrokerGrantsOf(cfg *jsonx.OrderedMap, workspace string, declarations []ReadableBrokerDeclaration) []WorkspaceBrokerGrants {
	return workspaceBrokerGrants(cfg, workspace, declarations, false)
}

// workspaceBrokerGrants is the projection both exported functions share; narrow selects the
// declared origin narrowing, so the two can differ only there.
func workspaceBrokerGrants(cfg *jsonx.OrderedMap, workspace string, declarations []ReadableBrokerDeclaration, narrow bool) []WorkspaceBrokerGrants {
	if cfg == nil {
		return nil
	}
	read := &WorkspaceRead{Config: cfg}
	requests, _ := asMap(readableValue(cfg, "requests"))
	seen := map[string]bool{}
	var out []WorkspaceBrokerGrants
	for _, d := range declarations {
		source := d.Brokered.Source
		if d.CallBroker != nil || source == "" || seen[source] {
			continue
		}
		seen[source] = true
		var table *jsonx.OrderedMap
		if requests != nil {
			table, _ = asMap(readableValue(requests, source))
		}
		policy := ReadableBrokerPolicy{Source: source, RemoteHost: d.Brokered.RemoteHost,
			KindCapabilities: d.KindCapabilities, PendingCapabilities: d.PendingCapabilities}
		if narrow {
			policy.UnqualifiedRequests = d.Brokered.UnqualifiedRequests
		}
		entries := read.Entry(source)
		if policy.RemoteHost != brokerscope.AnyRemoteHost {
			if table != nil {
				for _, repo := range stringList(readableValue(table, "repos")) {
					if brokerscope.ValidRepo(repo) {
						policy.Repos = appendReadableRepo(policy.Repos, ReadableRepoRequest{Repository: repo})
					}
				}
			}
			for _, entry := range entries {
				policy.Repos = appendReadableRepo(policy.Repos, ReadableRepoRequest{Repository: entry.Repo})
			}
		}
		var remotes brokerscope.Read
		switch {
		case workspace == "":
		case policy.RemoteHost == brokerscope.AnyRemoteHost:
			remotes = brokerscope.ReadAllRemotes(workspace)
		default:
			remotes = brokerscope.ReadRemotes(workspace, policy.RemoteHost)
		}
		targets := readableTargets(policy, remotes)

		// An origin-declaring source's unqualified lines reach origin alone; without an origin they
		// reach nothing, and the reason is said for each line that would have lowered a grant.
		requestTargets, originReason := targets, ""
		if policy.UnqualifiedRequests == loopholedecl.UnqualifiedRequestsOrigin {
			origin, err := readableOriginTarget(policy, remotes)
			if err != nil {
				requestTargets, originReason = nil, err.Error()
			} else {
				requestTargets = []ReadableRepository{origin}
			}
		}

		got := WorkspaceBrokerGrants{Source: source}
		add := func(g brokerscope.Grant) {
			if g.Kind == brokerscope.KindRead {
				return
			}
			if !brokerscope.ValidGrant(g) {
				got.Unplaced = appendReadableDifference(got.Unplaced, source+" grant "+g.String()+" cannot be serialized as a grant")
				return
			}
			got.Grants = appendReadableConcreteGrant(got.Grants, g)
		}
		if table != nil {
			for _, capability := range table.Keys() {
				if capability == "repos" || capability == d.ReadCapability || inList(d.PendingCapabilities, capability) {
					continue
				}
				kind := legacyKind(policy, capability)
				if kind == "" {
					continue
				}
				value := readableValue(table, capability)
				if originReason != "" && len(readableRequestGrants(kind, value, targets)) > 0 {
					got.Unplaced = appendReadableDifference(got.Unplaced, source+" "+capability+": "+originReason)
					continue
				}
				for _, placed := range readableRequestGrants(kind, value, requestTargets) {
					add(placed.Grant)
				}
			}
		}
		// A named table's requests for this source land on its own repository (GW-D76), never on
		// the unqualified lines' targets; the reason is said where it cannot be placed.
		if repos, ok := asMap(readableValue(cfg, "repos")); ok && policy.RemoteHost != brokerscope.AnyRemoteHost {
			for _, name := range repos.Keys() {
				namedTable, ok := asMap(readableValue(repos, name))
				if !ok {
					continue
				}
				// A repo-only table declares its repository and asks nothing under it.
				namedRequests, ok := asMap(readableValue(namedTable, "requests"))
				if !ok {
					continue
				}
				asked, ok := asMap(readableValue(namedRequests, source))
				if !ok {
					continue
				}
				target, err := namedRequestTarget(workspace, policy, namedRepoOf(name, namedTable))
				if err != nil {
					got.Unplaced = appendReadableDifference(got.Unplaced, source+" repos."+name+": "+err.Error())
					continue
				}
				for _, capability := range asked.Keys() {
					kind := legacyKind(policy, capability)
					if capability == d.ReadCapability || inList(d.PendingCapabilities, capability) || kind == "" {
						continue
					}
					for _, placed := range readableRequestGrants(kind, readableValue(asked, capability), []ReadableRepository{target}) {
						add(placed.Grant)
					}
				}
			}
		}
		legacy, _ := rawWorkspaceReadableGrants(read, source, d.Brokered.Grants)
		for _, grant := range legacy {
			if grant.Kind == brokerscope.KindRead {
				continue
			}
			placed, difference := legacyWorkspaceGrant(source, policy.RemoteHost, grant, entries, targets)
			if difference != "" {
				got.Unplaced = appendReadableDifference(got.Unplaced, grant.String()+": "+difference)
				continue
			}
			add(placed)
		}
		if len(got.Grants) == 0 && len(got.Unplaced) == 0 {
			continue
		}
		sort.Slice(got.Grants, func(i, j int) bool { return got.Grants[i].String() < got.Grants[j].String() })
		sort.Strings(got.Unplaced)
		out = append(out, got)
	}
	return out
}
