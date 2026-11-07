package config

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
// `brokered.<source>.repos`.
//
// It is what the project asks for, before any ceiling: the user's `allow.<source>` or
// `grant_kinds` gates both spellings alike, and is the user config's own fact. A read grant is
// left out the same way: it is the user's (BB-D107). A source the config asks nothing of is left
// out.
func WorkspaceBrokerGrantsOf(cfg *jsonx.OrderedMap, workspace string, declarations []ReadableBrokerDeclaration) []WorkspaceBrokerGrants {
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
				for _, placed := range readableRequestGrants(kind, readableValue(table, capability), targets) {
					add(placed.Grant)
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
