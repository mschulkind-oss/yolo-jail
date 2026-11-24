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

// ReadableScopedCeiling is one user `[repos.<name>.allow.<source>]` table (GW-D64) after it resolved:
// the host and owner/repo (or owner/*) its repo line admits, and the always and on_request lists it
// holds. It admits a request or an always grant only on an identity it admits (scopedCeilingAdmits).
type ReadableScopedCeiling struct {
	Name       string
	Source     string
	Host       string
	Repository string
	Always     []string
	OnRequest  []string
	Never      []string
	NeverFiles []string
	// AlwaysFiles and OnRequestFiles are the user config files each list is written in.
	AlwaysFiles    []string
	OnRequestFiles []string
}

// scopedCeilingAdmits reports whether c's repo line admits target: the same host, and each segment
// of c's repository is `*` or the target's own segment. A workspace pattern target is admitted only
// by a ceiling whose segments cover its `*` segments too, so a literal owner or repository never
// admits a wildcard (GW-D80).
func scopedCeilingAdmits(c ReadableScopedCeiling, target ReadableRepository) bool {
	if c.Host != target.RemoteHost {
		return false
	}
	cOwner, cRepo, ok := strings.Cut(c.Repository, "/")
	tOwner, tRepo, ok2 := strings.Cut(target.Repository, "/")
	return ok && ok2 && segmentCovers(cOwner, tOwner) && segmentCovers(cRepo, tRepo)
}

func segmentCovers(ceiling, name string) bool {
	return ceiling == "*" || strings.EqualFold(ceiling, name)
}

// scopedCeilingAdmitting is the first of ceilings for source that admits target and holds capability,
// always before on_request, with the list it holds it in.
func scopedCeilingAdmitting(ceilings []ReadableScopedCeiling, source string, target ReadableRepository, capability string) (ReadableScopedCeiling, string, bool) {
	for _, c := range ceilings {
		if c.Source != source || !scopedCeilingAdmits(c, target) {
			continue
		}
		if slices.Contains(c.Always, capability) {
			return c, "always", true
		}
		if slices.Contains(c.OnRequest, capability) {
			return c, "on_request", true
		}
	}
	return ReadableScopedCeiling{}, "", false
}

// narrowerNeverRefuses reports whether the most specific user repository line naming capability
// for source and target holds it in `never` (design 3.2 and 3.3: a narrower line governs, and at
// equal specificity the stricter one wins). Lines are the `[repos.<name>.allow.<source>]` tables
// and the direct `[repos.<name>.allow]` native-read lists. A narrower `always` or `on_request`
// governs over a less specific `never`, so the answer is false there. An unqualified source line
// is not narrower than itself and never reaches here.
func narrowerNeverRefuses(plan *ReadableAllowancePlan, source string, target ReadableRepository, capability, readWord string) bool {
	_, refused := narrowerNeverLine(plan, source, target, capability, readWord)
	return refused
}

// narrowerNeverLine is narrowerNeverRefuses with the config path of the `never` list that refuses,
// so a refusal can name the one verb that removes it: `repos.<name>.allow.<source>.never` for a
// broker's own table and `repos.<name>.allow.never` for a native read list.
func narrowerNeverLine(plan *ReadableAllowancePlan, source string, target ReadableRepository, capability, readWord string) (path string, refused bool) {
	best := -1
	var bestPath string
	consider := func(c ReadableScopedCeiling, never, grants bool, neverPath string) {
		if (!never && !grants) || !scopedCeilingAdmits(c, target) {
			return
		}
		wild := strings.Count(c.Repository, "*")
		if best < 0 || wild < best {
			best, refused, bestPath = wild, false, ""
		}
		if wild == best && never {
			refused, bestPath = true, neverPath
		}
	}
	for _, c := range plan.NativeCeilings {
		if c.Source == source {
			consider(c, slices.Contains(c.Never, capability), slices.Contains(c.Always, capability) || slices.Contains(c.OnRequest, capability), "repos."+c.Name+".allow."+source+".never")
		}
	}
	if capability == readWord {
		for _, r := range plan.NativeReads {
			if r.Source != source || r.Name == "" || r.Layer != "ceiling" {
				continue
			}
			host := namedRepoHost(r.Repository)
			consider(ReadableScopedCeiling{Host: host, Repository: strings.TrimPrefix(r.Repository, host+"/")}, r.Never, r.Always || r.OnRequest, "repos."+r.Name+".allow.never")
		}
	}
	return bestPath, best >= 0 && refused
}

// scopedCeilingHolds reports whether any user `[repos.<name>.allow.<source>]` table lists capability,
// on any identity. The plan keeps such a request alive for the gate, which decides per identity; an
// unreadable table holds nothing here, and its own error is reported where it is resolved.
func scopedCeilingHolds(user *jsonx.OrderedMap, source, capability string) bool {
	tables, err := scopedCeilingTables(user, source)
	if err != nil {
		return false
	}
	for _, t := range tables {
		if slices.Contains(stringList(readableValue(t.table, "always")), capability) || slices.Contains(stringList(readableValue(t.table, "on_request")), capability) {
			return true
		}
	}
	return false
}

// ScopedCeilingAllowsRequest reports whether a user `[repos.<name>.allow.<source>]` table admits
// repo and lists capability, in `always` or `on_request`. repo is a repo line, `<host>/<owner>/<repo>`
// or a whole-segment pattern. The table is matched by the repository its own repo line admits
// (scopedCeilingAdmits), never by its name (GW-D64). It is the report `yolo request` prints, not
// authority: the gate decides again from the same tables. An unreadable user config, a repo that
// is no repo line, and a table whose line is none each allow nothing.
func ScopedCeilingAllowsRequest(source, repo, capability string) bool {
	repository, pattern, err := parseNamedRepo("repo", repo)
	if err != nil {
		return false
	}
	user, _, err := UserScopeConfigWithSources(false, func(string) {})
	if err != nil {
		return false
	}
	tables, err := scopedCeilingTables(user, source)
	if err != nil {
		return false
	}
	host := namedRepoHost(repository)
	target := ReadableRepository{Source: source, RemoteHost: host, Repository: strings.TrimPrefix(repository, host+"/"), Pattern: pattern}
	for _, t := range tables {
		line, _, err := parseNamedRepo("repo", t.line)
		if err != nil {
			continue
		}
		ceiling := ReadableScopedCeiling{Name: t.name, Source: source, Host: namedRepoHost(line), Repository: strings.TrimPrefix(line, namedRepoHost(line)+"/")}
		if !scopedCeilingAdmits(ceiling, target) {
			continue
		}
		if slices.Contains(stringList(readableValue(t.table, "always")), capability) || slices.Contains(stringList(readableValue(t.table, "on_request")), capability) {
			return true
		}
	}
	return false
}

// userNamedRepository is the repo line the user's `[repos.<name>]` table names, "" when the user
// config declares no such table or no line for it.
func userNamedRepository(user *jsonx.OrderedMap, name string) string {
	table, err := readableTable(user, "repos")
	if err != nil {
		return ""
	}
	entry, ok := asMap(readableValue(table, name))
	if !ok {
		return ""
	}
	line, _ := readableValue(entry, "repo").(string)
	return line
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
	case brokerscope.ValidPattern(rest) && strings.Contains(rest, "*"):
		pattern = true
	default:
		return "", false, fmt.Errorf("%s = %q: write OWNER/REPO after the host, such as \"github.com/acme/infra\"; a `*` may stand for a whole owner or repository segment", path, value)
	}
	// The grant grammar is the one a repo line becomes: a host it would not spell as written is refused.
	if !brokerscope.ValidGrant(brokerscope.Grant{Kind: brokerscope.KindRead, Repo: value, RepoPattern: pattern}) {
		return "", false, fmt.Errorf("%s = %q: write the host too, in lowercase, such as \"github.com/acme/infra\"", path, value)
	}
	return value, pattern, nil
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
		if err := readableScopedCeiling(plan, name, line, table, input, brokers); err != nil {
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
// broker's own table, `[repos.<name>.allow.<broker>]`, whose lists resolve as the broker's
// `[allow.<broker>]` does, and records each as a ReadableScopedCeiling. Its repo line on another host
// is not a refusal: it admits nothing on this broker's host. A list written directly under `allow`
// names no broker and is refused, as the broad list is refused under `[allow]`.
func readableScopedCeiling(plan *ReadableAllowancePlan, name, line string, table *jsonx.OrderedMap, input ReadableAllowanceInput, brokers map[string]ReadableBrokerPolicy) error {
	value := readableValue(table, "allow")
	if value == nil {
		return nil
	}
	allow, ok := asMap(value)
	if !ok {
		return fmt.Errorf("repos.%s.allow: expected a table", name)
	}
	if _, direct := allow.Get("always"); direct || readableValue(allow, "on_request") != nil || readableValue(allow, "never") != nil {
		policy, err := anyHostReadPolicy(brokers)
		if err != nil {
			return fmt.Errorf("repos.%s.allow: direct lists require a declared any-host read source; write other ceilings under their broker table, such as [repos.%s.allow.github] or the broad [allow.github]: %w", name, name, err)
		}
		for _, key := range allow.Keys() {
			if key != "always" && key != "on_request" && key != "never" {
				return fmt.Errorf("repos.%s.allow.%s: named native read cannot mix broker tables and lists; use yolo config unset", name, key)
			}
		}
		return nativeReadLists(plan, name, line, "repos."+name+".allow", allow, policy, input.UserOrigins)
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
		own, ok := asMap(readableValue(allow, key))
		if !ok {
			return fmt.Errorf("%s: expected a table", path)
		}
		for _, capability := range own.Keys() {
			if capability != "always" && capability != "on_request" && capability != "never" {
				return fmt.Errorf("%s.%s: a repository's ceiling holds only always and on_request", path, capability)
			}
		}
		if err := validateScopedCeilingLists(path, own, broker, input.UserOrigins); err != nil {
			return err
		}
		resolved, err := ResolveReadableAllowance(ReadableAllowanceInput{User: scopedAllowOnly(key, own),
			SelectedPacks: input.SelectedPacks, Declarations: input.Declarations, PackSettings: input.PackSettings})
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if i := readableBrokerIndex(resolved.Sources, key); i >= 0 {
			always, err := validateReadableCapabilities(key, "always", stringList(readableValue(own, "always")), broker)
			if err != nil {
				return err
			}
			onRequest, err := validateReadableCapabilities(key, "on_request", stringList(readableValue(own, "on_request")), broker)
			if err != nil {
				return err
			}
			plan.NativeCeilings = append(plan.NativeCeilings, ReadableScopedCeiling{Name: name, Source: key, Host: namedRepoHost(line), Repository: strings.TrimPrefix(line, namedRepoHost(line)+"/"), Always: always, OnRequest: onRequest, Never: slices.Clone(resolved.Sources[i].Never), AlwaysFiles: cloneOrigins(input.UserOrigins[path+".always"]), OnRequestFiles: cloneOrigins(input.UserOrigins[path+".on_request"]), NeverFiles: cloneOrigins(input.UserOrigins[path+".never"])})
			if err := stripScopedNativeRead(&resolved.Sources[i], plan, name, line, path, own, input.UserOrigins); err != nil {
				return err
			}
			hasLegacyWrite := len(resolved.Sources[i].Always) != 0 || len(resolved.Sources[i].OnRequest) != 0
			readOnlyNew := false
			for _, field := range []string{"always", "on_request"} {
				for _, token := range stringList(readableValue(own, field)) {
					if token == broker.ReadCapability || token == "read" {
						readOnlyNew = true
					}
				}
			}
			_, hasAlways := own.Get("always")
			_, hasOnRequest := own.Get("on_request")
			_, hasNever := own.Get("never")
			if !hasLegacyWrite && (readOnlyNew || hasNever && !hasAlways && !hasOnRequest) {
				continue
			}
			plan.ScopedCeilings = append(plan.ScopedCeilings, ReadableScopedCeiling{Name: name, Source: key, Host: namedRepoHost(line),
				Repository: strings.TrimPrefix(line, namedRepoHost(line)+"/"), Always: resolved.Sources[i].Always, OnRequest: resolved.Sources[i].OnRequest, Never: resolved.Sources[i].Never, NeverFiles: cloneOrigins(input.UserOrigins[path+".never"]),
				AlwaysFiles:    cloneOrigins(input.UserOrigins["repos."+name+".allow."+key+".always"]),
				OnRequestFiles: cloneOrigins(input.UserOrigins["repos."+name+".allow."+key+".on_request"])})
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
			policy, policyErr := anyHostReadPolicy(brokers)
			if policyErr == nil && (source == "read" || source == policy.ReadCapability) {
				if err := validateNativeReadAliases("repos."+name+".requests", requests, policy, input.WorkspaceOrigins, false); err != nil {
					return err
				}
				if err := nativeReadRequest(plan, name, named.Repository, path, value, policy, input.WorkspaceOrigins); err != nil {
					return err
				}
				continue
			}
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
		if err := validateNativeReadAliases(path, table, broker, input.WorkspaceOrigins, false); err != nil {
			return err
		}
		readTable := shallowCopyMap(table)
		for _, key := range table.Keys() {
			if key == "read" || key == broker.ReadCapability {
				if err := nativeReadRequest(plan, name, named.Repository, path+"."+key, readableValue(table, key), broker, input.WorkspaceOrigins); err != nil {
					return err
				}
				readTable.Delete(key)
			}
		}
		table = readTable
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
				Value: capValue, Source: source, Table: name, Repository: named.Repository, UserRepository: userNamedRepository(input.User, name)}
			switch {
			case !slices.Contains(policy.Always, capability) && !slices.Contains(policy.OnRequest, capability) && !scopedCeilingHolds(input.User, source, capability):
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
			if key == "allow" {
				readableNamedAllowOrigins(origins, name, table, sources)
				continue
			}
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

// readableNamedAllowOrigins records the file each list of a `[repos.<name>.allow.<source>]` table is
// written in, `repos.<name>.allow.<source>.<list>`, the origin a scoped ceiling's grant carries.
func readableNamedAllowOrigins(origins map[string][]string, name string, table *jsonx.OrderedMap, sources *Sources) {
	allow, ok := asMap(readableValue(table, "allow"))
	if !ok {
		return
	}
	for _, source := range allow.Keys() {
		lists, ok := asMap(readableValue(allow, source))
		if !ok {
			continue
		}
		for _, list := range lists.Keys() {
			path := []string{"repos", name, "allow", source, list}
			origins[strings.Join(path, ".")] = scopeSourceFiles(sources, path...)
		}
	}
}

// namedRepoOf is a workspace table's repo line, unvalidated: the resolver has said what a bad one
// is, and a projection never reads a table the resolver has not passed.
func namedRepoOf(name string, table *jsonx.OrderedMap) ReadableNamedRepo {
	line, _ := readableValue(table, "repo").(string)
	return ReadableNamedRepo{Name: name, Repository: line, Pattern: strings.Contains(line, "*")}
}
