package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// ReadableNamedRepo is one `[repos.<name>]` table (GW-D76): the repository its `repo` line names,
// a pattern when Pattern, or, with no line, the clone's remote of that name. Repository is
// `<host>/<owner>/<repo>` (or `<host>/<owner>/*`), and "" means the remote.
type ReadableNamedRepo struct {
	Name       string
	Repository string
	Pattern    bool
	Files      []string
}

// namedRepoHost is the host a named repository is on, "" with no repo line.
func namedRepoHost(repository string) string {
	host, _, _ := strings.Cut(repository, "/")
	return host
}

// parseNamedRepo validates a `repo` line: `<host>/<owner>/<repo>`, or `<host>/<owner>/*` with the
// whole-segment wildcard in owner or repository (settled item 6). The host is never a wildcard.
func parseNamedRepo(path, value string) (repository string, pattern bool, err error) {
	host, rest, ok := strings.Cut(value, "/")
	if !ok || host == "" || strings.Contains(host, "*") {
		return "", false, fmt.Errorf("%s = %q: write the host too, such as \"github.com/acme/infra\"", path, value)
	}
	switch {
	case brokerscope.ValidRepo(rest):
		return value, false, nil
	case brokerscope.ValidPattern(rest) && strings.Contains(rest, "*"):
		return value, true, nil
	}
	return "", false, fmt.Errorf("%s = %q: write OWNER/REPO after the host, such as \"github.com/acme/infra\"; a `*` may stand for a whole owner or repository segment", path, value)
}

// validateNamedName refuses the names a `[repos.<name>]` table cannot take.
func validateNamedName(name string) error {
	if name == "all" {
		return fmt.Errorf("repos.all is not a name: `all` is the every-broker word of the ceiling; choose another name for this repository")
	}
	return nil
}

// readableNamedRepos reads the workspace's and the user's `[repos.<name>]` tables into plan: the
// user's table only for its repo line (Task 3 gives it a ceiling), the workspace's with its
// requests under each selected broker, each checked with the unqualified request's own rules.
func readableNamedRepos(plan *ReadableAllowancePlan, input ReadableAllowanceInput, brokers map[string]ReadableBrokerPolicy, selected map[string]bool) error {
	user, err := readableTable(input.User, "repos")
	if err != nil {
		return err
	}
	for _, name := range user.Keys() {
		table, ok := asMap(readableValue(user, name))
		if !ok {
			return fmt.Errorf("repos.%s: expected a table", name)
		}
		if err := validateNamedName(name); err != nil {
			return err
		}
		line, _ := readableValue(table, "repo").(string)
		if line == "" {
			return fmt.Errorf("repos.%s in your user config needs a repo line: write repo = \"github.com/OWNER/REPO\", or github.com/OWNER/* for a pattern", name)
		}
		if _, _, err := parseNamedRepo("repos."+name+".repo", line); err != nil {
			return err
		}
		if err := readableScopedCeiling(name, line, table, input, brokers); err != nil {
			return err
		}
	}
	workspace, err := readableTable(input.Workspace, "repos")
	if err != nil {
		return err
	}
	for _, name := range workspace.Keys() {
		path := "repos." + name
		table, ok := asMap(readableValue(workspace, name))
		if !ok {
			return fmt.Errorf("%s: expected a table", path)
		}
		if err := validateNamedName(name); err != nil {
			return err
		}
		named := ReadableNamedRepo{Name: name, Files: cloneOrigins(input.WorkspaceOrigins[path])}
		if line, found := readableValue(table, "repo").(string); found {
			if named.Repository, named.Pattern, err = parseNamedRepo(path+".repo", line); err != nil {
				return err
			}
		}
		requests, _ := asMap(readableValue(table, "requests"))
		if named.Repository == "" && (requests == nil || len(requests.Keys()) == 0) {
			return fmt.Errorf("%s names no repository: write repo = \"github.com/OWNER/REPO\", or a request under it, such as [%s.requests.github] runs = [\"ci.yml\"] (the clone's remote named %s is used then)", path, path, name)
		}
		plan.NamedRepos = append(plan.NamedRepos, named)
		if requests == nil {
			continue
		}
		if err := readableNamedRequests(plan, input, name, named, requests, brokers, selected); err != nil {
			return err
		}
	}
	return nil
}

// readableScopedCeiling checks one user `[repos.<name>]` table's `allow` (GW-D64): each key is a
// broker's own table, `[repos.<name>.allow.<broker>]`, whose repo line is on that broker's host and
// whose lists resolve as the broker's `[allow.<broker>]` does. A list written directly under
// `allow` names no broker and is refused, as the broad list is refused under `[allow]`.
func readableScopedCeiling(name, line string, table *jsonx.OrderedMap, input ReadableAllowanceInput, brokers map[string]ReadableBrokerPolicy) error {
	value := readableValue(table, "allow")
	if value == nil {
		return nil
	}
	allow, ok := asMap(value)
	if !ok {
		return fmt.Errorf("repos.%s.allow: expected a table", name)
	}
	for _, key := range allow.Keys() {
		path := "repos." + name + ".allow." + key
		if key == "always" || key == "on_request" {
			return fmt.Errorf("%s: a capability list belongs under a broker's table, as [allow.github] holds the broad list for every workspace; write [repos.%s.allow.<broker>] (such as [repos.%s.allow.github]) for this repository", path, name, name)
		}
		broker, known := brokers[key]
		if !known {
			return fmt.Errorf("%s names no declared broker source; declared names: %s", path, strings.Join(readableBrokerNames(brokers), ", "))
		}
		if broker.CallBroker != nil || broker.RemoteHost == brokerscope.AnyRemoteHost {
			return fmt.Errorf("%s: %s takes no repository ceiling; a repository's ceiling names a host-served broker", path, key)
		}
		if namedRepoHost(line) != broker.RemoteHost {
			return fmt.Errorf("%s: %s serves %s, and repos.%s names %s; a ceiling under a name admits only the host its broker serves", path, key, broker.RemoteHost, name, namedRepoHost(line))
		}
		own, ok := asMap(readableValue(allow, key))
		if !ok {
			return fmt.Errorf("%s: expected a table", path)
		}
		for _, capability := range own.Keys() {
			if capability != "always" && capability != "on_request" {
				return fmt.Errorf("%s.%s: a repository's ceiling holds only always and on_request", path, capability)
			}
		}
		if _, err := ResolveReadableAllowance(ReadableAllowanceInput{User: scopedAllowOnly(key, own),
			SelectedPacks: input.SelectedPacks, Declarations: input.Declarations, PackSettings: input.PackSettings}); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// scopedAllowOnly is a user config holding only `allow.<source>` as table, the one entry a
// repository's ceiling resolves through: the broker's own declaration validates its lists.
func scopedAllowOnly(source string, table *jsonx.OrderedMap) *jsonx.OrderedMap {
	only := jsonx.NewOrderedMap()
	only.Set(source, table)
	user := jsonx.NewOrderedMap()
	user.Set("allow", only)
	return user
}

// readableNamedRequests checks and records one table's requests, each under the broker it names.
// A capability no broker names is a Git line under a name, which is not built yet (settled 9).
func readableNamedRequests(plan *ReadableAllowancePlan, input ReadableAllowanceInput, name string, named ReadableNamedRepo, requests *jsonx.OrderedMap, brokers map[string]ReadableBrokerPolicy, selected map[string]bool) error {
	for _, source := range requests.Keys() {
		path := "repos." + name + ".requests." + source
		value := readableValue(requests, source)
		broker, known := brokers[source]
		if !known {
			if owner, capability, ok := anyHostCapabilityOf(brokers, source); ok {
				return fmt.Errorf("%s is not built yet: a Git line under a name waits for a later slice; write it as yolo request %s %s %s", path, owner, capability, strings.Join(stringList(value), " "))
			}
			return fmt.Errorf("%s names no declared broker source; declared names: %s", path, strings.Join(readableBrokerNames(brokers), ", "))
		}
		table, ok := asMap(value)
		if !ok {
			return fmt.Errorf("%s: expected a table", path)
		}
		if broker.CallBroker != nil {
			return fmt.Errorf("%s: a call-broker source takes no request under a name", path)
		}
		if broker.RemoteHost == brokerscope.AnyRemoteHost {
			return fmt.Errorf("%s is not built yet: a Git line under a name waits for a later slice; write it as yolo request %s", path, broker.Source)
		}
		if name == "origin" && broker.UnqualifiedRequests == loopholedecl.UnqualifiedRequestsOrigin {
			return fmt.Errorf("%s: origin's %s lines have one home, [requests.%s]", path, source, source)
		}
		if named.Repository != "" && namedRepoHost(named.Repository) != broker.RemoteHost {
			return fmt.Errorf("%s: %s serves %s, and repos.%s names %s; a request under a name reaches only the host its broker serves", path, source, broker.RemoteHost, name, namedRepoHost(named.Repository))
		}
		if !selected[broker.Pack] {
			plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("%s names an unselected broker pack; add %q to packs or remove this request", path, broker.Pack))
			continue
		}
		index := readableBrokerIndex(plan.Sources, source)
		if index < 0 {
			continue
		}
		policy := plan.Sources[index]
		for _, capability := range table.Keys() {
			reqPath := path + "." + capability
			capValue := readableValue(table, capability)
			if capability == broker.ReadCapability {
				return fmt.Errorf("%s: a repository named here is read already; to read it, add %s to [allow.%s] always", reqPath, capability, source)
			}
			shape, known := readableRequestShape(policy, capability)
			if !known {
				return fmt.Errorf("%s is not a canonical request; declared capabilities are %s", reqPath, strings.Join(readableCapabilities(policy), ", "))
			}
			if !readableHasCapability(policy, capability) {
				return fmt.Errorf("%s has no declared broker kind; declared capabilities are %s", reqPath, strings.Join(readableCapabilities(policy), ", "))
			}
			if err := readableRequestType(capValue, shape); err != nil {
				return fmt.Errorf("%s: %w", reqPath, err)
			}
			if shape == SchemaTypeList {
				if err := validateReadableRequestNames(reqPath, legacyKind(policy, capability), capValue); err != nil {
					return err
				}
			}
			result := ReadableRequestResult{Path: reqPath, Files: cloneOrigins(input.WorkspaceOrigins[reqPath]), Capability: capability,
				Value: capValue, Source: source, Table: name}
			switch {
			case !slices.Contains(policy.Always, capability) && !slices.Contains(policy.OnRequest, capability):
				result.Status, result.Reason = ReadableRequestSkipped, outsideCeilingReason(source)
			case shape == SchemaTypeList && len(stringList(capValue)) == 0 || shape == SchemaTypeBoolean && capValue == false:
				result.Status, result.Reason = ReadableRequestSkipped, "an empty list or false request asks for no grants"
			case slices.Contains(policy.PendingCapabilities, capability):
				result.Status, result.Reason = ReadableRequestPending, "declared semantic action; H runtime delivery remains pending and use is not activated"
			default:
				result.Status = ReadableRequestGranted
			}
			plan.Requests = append(plan.Requests, result)
		}
	}
	return nil
}

// anyHostCapabilityOf finds an any-host source that names a capability the workspace wrote under a
// name (`repos.<name>.requests.push`), so its refusal can name the Git command that asks for it.
func anyHostCapabilityOf(brokers map[string]ReadableBrokerPolicy, capability string) (owner, kind string, ok bool) {
	for _, broker := range brokers {
		if broker.RemoteHost == brokerscope.AnyRemoteHost && readableHasMappedCapability(broker.KindCapabilities, capability) {
			return broker.Source, capability, true
		}
	}
	return "", "", false
}

func readableBrokerNames(brokers map[string]ReadableBrokerPolicy) []string {
	names := make([]string, 0, len(brokers))
	for name := range brokers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// namedReadTargets are the named repositories a source's host serves, read by its requests' read
// scope (GW-D76): a repo line on this source's host, pattern or literal. A table with no repo line
// is a remote already in read scope.
func namedReadTargets(named []ReadableNamedRepo, policy ReadableBrokerPolicy) []ReadableRepository {
	var out []ReadableRepository
	if policy.RemoteHost == brokerscope.AnyRemoteHost {
		return nil
	}
	for _, repo := range named {
		if repo.Repository == "" || namedRepoHost(repo.Repository) != policy.RemoteHost {
			continue
		}
		_, owned, _ := strings.Cut(repo.Repository, "/")
		out = appendReadableRepository(out, ReadableRepository{Source: policy.Source, RemoteHost: policy.RemoteHost,
			Repository: owned, Origin: "repos." + repo.Name, Files: cloneOrigins(repo.Files), Pattern: repo.Pattern})
	}
	return out
}

// namedRequestTarget is the repository a named table's request under source lands on: its repo
// line, or with none the clone's remote of that name on the source's host (GW-D76).
func namedRequestTarget(workspace string, policy ReadableBrokerPolicy, named ReadableNamedRepo) (ReadableRepository, error) {
	if named.Repository != "" {
		host, owned, _ := strings.Cut(named.Repository, "/")
		return ReadableRepository{Source: policy.Source, RemoteHost: host, Repository: owned, Origin: "repos." + named.Name,
			Files: cloneOrigins(named.Files), Pattern: named.Pattern}, nil
	}
	remotes := brokerscope.ReadRemotes(workspace, policy.RemoteHost)
	var names []string
	for _, remote := range remotes.Remotes {
		if remote.Name == named.Name {
			return ReadableRepository{Source: policy.Source, RemoteHost: policy.RemoteHost, Repository: remote.Repo,
				Origin: "remote " + remote.Name, Files: cloneOrigins(named.Files)}, nil
		}
		names = append(names, remote.Name)
	}
	return ReadableRepository{}, fmt.Errorf("repos.%s has no repo line and no remote named %s on %s; the remotes on %s are %s: write repo = \"%s/OWNER/REPO\" under it, or yolo request repos %s %s/OWNER/REPO",
		named.Name, named.Name, policy.RemoteHost, policy.RemoteHost, strings.Join(names, ", "), policy.RemoteHost, named.Name, policy.RemoteHost)
}

// readableRequestOfSource is a request a source's gate lowering owns: a legacy request under
// `requests.<source>.` or one a named table makes for that source.
func readableRequestOfSource(request ReadableRequestResult, source string) bool {
	return strings.HasPrefix(request.Path, "requests."+source+".") || request.Table != "" && request.Source == source
}

// namedRequestTargets is the repository each granted request of a named table under source lands
// on, by table name. Only granted requests are resolved: a skipped one names no repository.
func namedRequestTargets(workspace string, policy ReadableBrokerPolicy, named []ReadableNamedRepo, requests []ReadableRequestResult) (map[string]ReadableRepository, error) {
	out := map[string]ReadableRepository{}
	for _, request := range requests {
		if request.Table == "" || request.Source != policy.Source || request.Status != ReadableRequestGranted {
			continue
		}
		if _, done := out[request.Table]; done {
			continue
		}
		i := slices.IndexFunc(named, func(n ReadableNamedRepo) bool { return n.Name == request.Table })
		if i < 0 {
			return nil, fmt.Errorf("%s names no repos.%s table", request.Path, request.Table)
		}
		target, err := namedRequestTarget(workspace, policy, named[i])
		if err != nil {
			return nil, err
		}
		out[request.Table] = target
	}
	return out, nil
}

// readableNamedOrigins records the file each `[repos.<name>]` table and its keys are written in,
// the origins a gate line ends with (`repos.<name>`, `repos.<name>.repo`, and each request).
func readableNamedOrigins(origins map[string][]string, config *jsonx.OrderedMap, sources *Sources) {
	if config == nil {
		return
	}
	repos, ok := asMap(readableValue(config, "repos"))
	if !ok {
		return
	}
	for _, name := range repos.Keys() {
		table, ok := asMap(readableValue(repos, name))
		if !ok {
			continue
		}
		origins["repos."+name] = scopeSourceFiles(sources, "repos", name)
		for _, key := range table.Keys() {
			if key != "requests" {
				origins["repos."+name+"."+key] = scopeSourceFiles(sources, "repos", name, key)
				continue
			}
			requests, ok := asMap(readableValue(table, key))
			if !ok {
				continue
			}
			for _, source := range requests.Keys() {
				values, ok := asMap(readableValue(requests, source))
				if !ok {
					origins["repos."+name+".requests."+source] = scopeSourceFiles(sources, "repos", name, "requests", source)
					continue
				}
				for _, capability := range values.Keys() {
					path := []string{"repos", name, "requests", source, capability}
					origins[strings.Join(path, ".")] = scopeSourceFiles(sources, path...)
				}
			}
		}
	}
}

// namedRepoOf is a workspace table's repo line, unvalidated: the resolver has said what a bad one
// is, and a projection never reads a table the resolver has not passed.
func namedRepoOf(name string, table *jsonx.OrderedMap) ReadableNamedRepo {
	line, _ := readableValue(table, "repo").(string)
	return ReadableNamedRepo{Name: name, Repository: line, Pattern: strings.Contains(line, "*")}
}
