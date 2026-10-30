package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ReadableBrokerDeclaration is the stable declaration input to this unactivated normalizer.
// Canonical capability names are explicit projections of the legacy declaration; this package
// does not infer them from a broker source, pack or tool name.
type ReadableBrokerDeclaration struct {
	Pack                string
	Label               string
	Brokered            loopholedecl.Brokered
	ReadCapability      string
	KindCapabilities    map[string]string
	PendingCapabilities []string
	Settings            []loopholedecl.Setting
}

// ReadablePackDeclaration carries typed settings for a selected or unselected pack.
type ReadablePackDeclaration struct {
	Pack     string
	Settings []loopholedecl.Setting
}

// ReadableAllowanceInput is decoded canonical input plus the selected declarations and
// per-key provenance supplied by the future config source adapter. It is not a reader switch.
type ReadableAllowanceInput struct {
	User             *jsonx.OrderedMap
	Workspace        *jsonx.OrderedMap
	SelectedPacks    []string
	Declarations     []ReadableBrokerDeclaration
	PackSettings     []ReadablePackDeclaration
	UserOrigins      map[string][]string
	WorkspaceOrigins map[string][]string
}

type ReadableRequestStatus string

const (
	ReadableRequestGranted ReadableRequestStatus = "granted"
	ReadableRequestSkipped ReadableRequestStatus = "skipped"
	ReadableRequestPending ReadableRequestStatus = "pending-runtime"
)

// ReadableRequestResult preserves the authored key and its exact source-file provenance.
type ReadableRequestResult struct {
	Path       string
	Files      []string
	Capability string
	Value      any
	Status     ReadableRequestStatus
	Reason     string
}

type ReadableTableTombstone struct {
	Path  string
	Files []string
}

// ReadableAllowancePlan is the semantic result before actual remotes are read.
type ReadableAllowancePlan struct {
	TableTombstones    []ReadableTableTombstone
	Sources            []ReadableBrokerPolicy
	Requests           []ReadableRequestResult
	PackSettings       map[string]map[string]any
	PackSettingOrigins map[string]map[string][]string
	Diagnostics        []string
}

// ReadableBrokerPolicy retains ceiling lists separately; neither list itself is a grant.
type ReadableBrokerPolicy struct {
	Pack                string
	Label               string
	Source              string
	RemoteHost          string
	ReadCapability      string
	Ambiguous           bool
	KindCapabilities    map[string]string
	PendingCapabilities []string
	Always              []string
	OnRequest           []string
	Repos               []ReadableRepoRequest
	// RepoPath survives an empty repository list so removals retain their authored remediation key.
	RepoPath         string
	AlwaysOrigins    []string
	OnRequestOrigins []string
}

// ReadableRepoRequest is a requested concrete OWNER/REPO in the declared source's namespace.
type ReadableRepoRequest struct {
	Repository string
	Path       string
	Files      []string
}

// ReadableGrantDecision pairs a concrete legacy-compatible grant with its authored cause.
type ReadableGrantDecision struct {
	Grant   brokerscope.Grant
	Origins []string
	Files   []string
}

// ReadableGateSource is typed canonical scope and grant input for a future gate adapter.
// Read keeps actual remotes as read; Targets carries canonical identities separately, including
// named-host additions that cannot yet be serialized into an any-host exact-URL scope file.
type ReadableGateSource struct {
	Source     string
	Label      string
	RemoteHost string
	Read       brokerscope.Read
	EntryKey   string
	Entry      []EntryRepo
	Targets    []ReadableRepository
	Grants     []brokerscope.Grant
	Decisions  []ReadableGrantDecision
}

// ReadableObservedSource is diagnostic/equality evidence only; it never creates a gate source,
// grant, prompt, or approval record by itself.
type ReadableObservedSource struct {
	Source     string
	Label      string
	RemoteHost string
	EntryKey   string
	Read       brokerscope.Read
	Targets    []ReadableRepository
}

// ReadableRepository is a scope identity, not permission, with declared source/host provenance.
type ReadableRepository struct {
	Source     string
	RemoteHost string
	Repository string
	Origin     string
	Files      []string
}

// ReadableReadCapability preserves declared read capability state apart from concrete repository
// grants. It is metadata for a later classifier, not authority to perform a runtime action.
type ReadableReadCapability struct {
	Source         string
	Label          string
	Capability     string
	Always         bool
	OnRequest      bool
	Requested      bool
	RequestStatus  ReadableRequestStatus
	AlwaysFiles    []string
	OnRequestFiles []string
	RequestFiles   []string
}

// ReadableGateInput is the canonical concrete input a later gate adapter must consume.
type ReadableGateInput struct {
	ReadCapabilities       []ReadableReadCapability
	Sources                []ReadableGateSource
	ObservedSources        []ReadableObservedSource
	AdditionalRepositories []ReadableRepository
	Requests               []ReadableRequestResult
	PackSettings           map[string]map[string]any
	PackSettingOrigins     map[string]map[string][]string
	TableTombstones        []ReadableTableTombstone
	// Differences require an explicit no-write migration/gate decision; never ignore them and claim equivalence.
	Differences []string
}

// ResolveReadableAllowance validates canonical values against explicit selected declarations.
// Malformed values and undeclared names are errors; a valid outside-ceiling request is retained
// as a nonfatal skipped result.
func ResolveReadableAllowance(input ReadableAllowanceInput) (ReadableAllowancePlan, error) {
	settings, brokers, err := readableDeclarations(input)
	if err != nil {
		return ReadableAllowancePlan{}, err
	}
	selected := readableSet(input.SelectedPacks)
	plan := ReadableAllowancePlan{PackSettings: map[string]map[string]any{}, PackSettingOrigins: map[string]map[string][]string{}}
	seenSources := map[string]bool{}
	for _, declaration := range input.Declarations {
		broker := brokers[declaration.Brokered.Source]
		if selected[broker.Pack] && !seenSources[broker.Source] {
			plan.Sources = append(plan.Sources, broker)
			seenSources[broker.Source] = true
		}
	}
	allow, err := readableTable(input.User, "allow")
	if err != nil {
		return plan, err
	}
	for _, name := range allow.Keys() {
		value := readableValue(allow, name)
		if name == allowMountsName {
			// Core's own ceiling (RC-D24, mountsrequest.go), not a broker's or a pack's: judged
			// by its own validator, so a broker's resolution never reports it as undeclared.
			if problems := validateAllowMountsTable(value); len(problems) > 0 {
				return plan, fmt.Errorf("%s", strings.Join(problems, "; "))
			}
			continue
		}
		if isReadableTableOff(value) {
			if broker, exists := brokers[name]; exists && broker.Ambiguous {
				return plan, fmt.Errorf("allow.%s has ambiguous unselected broker declarations", name)
			}
			if _, broker := brokers[name]; !broker {
				if _, pack := settings[name]; !pack {
					return plan, fmt.Errorf("allow.%s names no declared broker source or pack; declared names: %s", name, strings.Join(readableDeclaredNames(settings, brokers), ", "))
				}
			}
			plan.TableTombstones = append(plan.TableTombstones, ReadableTableTombstone{Path: "allow." + name, Files: cloneOrigins(input.UserOrigins["allow."+name])})
			continue
		}
		table, ok := asMap(value)
		if !ok {
			return plan, fmt.Errorf("allow.%s: expected a table", name)
		}
		policy, isBroker := brokers[name]
		if isBroker && policy.Ambiguous {
			return plan, fmt.Errorf("allow.%s has ambiguous unselected broker declarations; remove the collision or declare a selected source", name)
		}
		if isBroker {
			if index := readableBrokerIndex(plan.Sources, name); index >= 0 {
				policy = plan.Sources[index]
			}
			isSelected := selected[policy.Pack]
			for _, key := range table.Keys() {
				value := readableValue(table, key)
				switch key {
				case "always", "on_request":
					if name == policy.Pack {
						if _, found, _ := readableSettingByKey(settings[policy.Pack], key); found {
							return plan, fmt.Errorf("allow.%s.%s ambiguously names both an allowance field and a pack setting", name, key)
						}
					}
					values, e := readableStringList(value, "allow."+name+"."+key)
					if e != nil {
						return plan, e
					}
					validated, e := validateReadableCapabilities(name, key, values, policy)
					if e != nil {
						return plan, e
					}
					if key == "always" {
						policy.Always, policy.AlwaysOrigins = validated, cloneOrigins(input.UserOrigins["allow."+name+".always"])
					} else {
						policy.OnRequest, policy.OnRequestOrigins = validated, cloneOrigins(input.UserOrigins["allow."+name+".on_request"])
					}
				default:
					var setting loopholedecl.Setting
					var found, ambiguous bool
					if name == policy.Pack {
						setting, found, ambiguous = readableSettingByKey(settings[policy.Pack], key)
					}
					if ambiguous {
						return plan, fmt.Errorf("allow.%s.%s has ambiguous setting declarations for pack %q", name, key, policy.Pack)
					}
					if !found {
						return plan, fmt.Errorf("allow.%s.%s is not declared; declared broker capabilities are %s and pack settings are %s", name, key,
							strings.Join(readableCapabilities(policy), ", "), strings.Join(readableSettingNames(settings[policy.Pack]), ", "))
					}
					normalized, settingErr := readableSettingValue(value, setting)
					if settingErr != nil {
						return plan, fmt.Errorf("allow.%s.%s: %w", name, key, settingErr)
					}
					if isSelected {
						putReadableSetting(&plan, policy.Pack, key, normalized, input.UserOrigins["allow."+name+"."+key])
					}
				}
			}
			if isSelected {
				if i := readableBrokerIndex(plan.Sources, name); i >= 0 {
					plan.Sources[i] = policy
				}
			} else {
				plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("allow.%s names an unselected broker pack; add %q to packs or remove this table", name, policy.Pack))
			}
			continue
		}
		declaredSettings, found := settings[name]
		if !found {
			return plan, fmt.Errorf("allow.%s names no declared broker source or pack; declared names: %s", name, strings.Join(readableDeclaredNames(settings, brokers), ", "))
		}
		for _, key := range table.Keys() {
			setting, found, ambiguous := readableSettingByKey(declaredSettings, key)
			if ambiguous {
				return plan, fmt.Errorf("allow.%s.%s has ambiguous setting declarations for pack %q", name, key, name)
			}
			if !found {
				return plan, fmt.Errorf("allow.%s.%s is not declared; declared pack settings are %s", name, key, strings.Join(readableSettingNames(declaredSettings), ", "))
			}
			value := readableValue(table, key)
			normalized, settingErr := readableSettingValue(value, setting)
			if settingErr != nil {
				return plan, fmt.Errorf("allow.%s.%s: %w", name, key, settingErr)
			}
			if selected[name] {
				putReadableSetting(&plan, name, key, normalized, input.UserOrigins["allow."+name+"."+key])
			}
		}
		if !selected[name] {
			plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("allow.%s names an unselected pack; add %q to packs or remove this table", name, name))
		}
	}

	settingPacks := make([]string, 0, len(settings))
	for pack := range settings {
		settingPacks = append(settingPacks, pack)
	}
	sort.Strings(settingPacks)
	for _, pack := range settingPacks {
		declaredSettings := settings[pack]
		if selected[pack] && len(declaredSettings) > 0 {
			if _, authored := allow.Get(pack); !authored {
				plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("selected pack %q has no allow.%s settings; declaration defaults are not promoted into user authority", pack, pack))
			}
		}
	}

	requests, err := readableTable(input.Workspace, "requests")
	if err != nil {
		return plan, err
	}
	for _, name := range requests.Keys() {
		value := readableValue(requests, name)
		if isReadableTableOff(value) {
			if broker, exists := brokers[name]; exists && broker.Ambiguous {
				return plan, fmt.Errorf("requests.%s has ambiguous unselected broker declarations", name)
			}
			if _, broker := brokers[name]; !broker {
				if _, pack := settings[name]; !pack {
					return plan, fmt.Errorf("requests.%s names no declared broker source or pack; declared names: %s", name, strings.Join(readableDeclaredNames(settings, brokers), ", "))
				}
			}
			plan.TableTombstones = append(plan.TableTombstones, ReadableTableTombstone{Path: "requests." + name, Files: cloneOrigins(input.WorkspaceOrigins["requests."+name])})
			continue
		}
		table, ok := asMap(value)
		if !ok {
			return plan, fmt.Errorf("requests.%s: expected a table", name)
		}
		policy, isBroker := brokers[name]
		if isBroker && policy.Ambiguous {
			return plan, fmt.Errorf("requests.%s has ambiguous unselected broker declarations; remove the collision or declare a selected source", name)
		}
		if isBroker {
			if index := readableBrokerIndex(plan.Sources, name); index >= 0 {
				policy = plan.Sources[index]
			}
		}
		if !isBroker {
			declaredSettings, exists := settings[name]
			if !exists {
				return plan, fmt.Errorf("requests.%s names no declared broker source or pack; declared names: %s", name, strings.Join(readableDeclaredNames(settings, brokers), ", "))
			}
			for _, key := range table.Keys() {
				setting, found, ambiguous := readableSettingByKey(declaredSettings, key)
				if ambiguous {
					return plan, fmt.Errorf("requests.%s.%s has ambiguous setting declarations for pack %q", name, key, name)
				}
				if !found {
					return plan, fmt.Errorf("requests.%s.%s is not declared; declared pack settings are %s", name, key, strings.Join(readableSettingNames(declaredSettings), ", "))
				}
				if setting.Scope == loopholedecl.SettingScopeUser {
					return plan, fmt.Errorf("requests.%s.%s is user-scope only; move it to allow.%s.%s", name, key, name, key)
				}
				value := readableValue(table, key)
				normalized, settingErr := readableSettingValue(value, setting)
				if settingErr != nil {
					return plan, fmt.Errorf("requests.%s.%s: %w", name, key, settingErr)
				}
				if selected[name] {
					putReadableSetting(&plan, name, key, normalized, input.WorkspaceOrigins["requests."+name+"."+key])
				}
			}
			if !selected[name] {
				plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("requests.%s names an unselected pack; add %q to packs or remove this request", name, name))
			}
			continue
		}
		if !selected[policy.Pack] {
			plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("requests.%s names an unselected broker pack; add %q to packs or remove this request", name, policy.Pack))
		}
		for _, key := range table.Keys() {
			path := "requests." + name + "." + key
			value := readableValue(table, key)
			if key == "repos" {
				policy.RepoPath = path
				if policy.RemoteHost == brokerscope.AnyRemoteHost {
					return plan, fmt.Errorf("%s: declared remote_host %q cannot accept OWNER/REPO additions; request repositories through a selected named-host source", path, brokerscope.AnyRemoteHost)
				}
				repos, e := readableStringList(value, path)
				if e != nil {
					return plan, e
				}
				for i, repo := range repos {
					if !brokerscope.ValidRepo(repo) {
						return plan, fmt.Errorf("%s[%d]: %q is not a concrete OWNER/REPO; wildcards are not requests", path, i, repo)
					}
					if !selected[policy.Pack] {
						continue
					}
					policy.Repos = appendReadableRepo(policy.Repos, ReadableRepoRequest{Repository: repo, Path: path, Files: cloneOrigins(input.WorkspaceOrigins[path])})
					plan.Requests = append(plan.Requests, ReadableRequestResult{Path: path, Files: cloneOrigins(input.WorkspaceOrigins[path]), Capability: "repos", Value: repo,
						Status: ReadableRequestGranted, Reason: "repository identity is scope input only; each selected consumer's own read allowance still applies"})
				}
				continue
			}
			shape, known := readableRequestShape(policy, key)
			setting, settingFound, settingAmbiguous := readableSettingByKey(settings[policy.Pack], key)
			if name == policy.Pack && known && settingFound {
				return plan, fmt.Errorf("%s ambiguously names both a broker capability and a pack setting", path)
			}
			if name == policy.Pack && !known {
				if settingAmbiguous {
					return plan, fmt.Errorf("%s has ambiguous setting declarations for pack %q", path, policy.Pack)
				}
				if settingFound {
					if setting.Scope == loopholedecl.SettingScopeUser {
						return plan, fmt.Errorf("%s is user-scope only; move it to allow.%s.%s", path, name, key)
					}
					normalized, settingErr := readableSettingValue(value, setting)
					if settingErr != nil {
						return plan, fmt.Errorf("%s: %w", path, settingErr)
					}
					if selected[policy.Pack] {
						putReadableSetting(&plan, policy.Pack, key, normalized, input.WorkspaceOrigins[path])
					}
					continue
				}
			}
			if !known {
				return plan, fmt.Errorf("%s is not a canonical request; declared capabilities are %s", path, strings.Join(readableCapabilities(policy), ", "))
			}
			if err := readableRequestType(value, shape); err != nil {
				return plan, fmt.Errorf("%s: %w", path, err)
			}
			if key != policy.ReadCapability && !readableHasCapability(policy, key) {
				return plan, fmt.Errorf("%s has no declared broker kind; declared capabilities are %s", path, strings.Join(readableCapabilities(policy), ", "))
			}
			if shape == SchemaTypeList {
				for index, name := range stringList(value) {
					if !brokerscope.ValidGrantRefPattern(name) {
						return plan, fmt.Errorf("%s[%d]: %q is not a ref name a grant can take: write a short name git accepts, such as \"main\" or \"v1.0\", optionally with one `*` matching any run of characters, such as \"patch/*\" or \"v*\"", path, index, name)
					}
				}
			}
			if !selected[policy.Pack] {
				continue
			}
			if !slices.Contains(policy.Always, key) && !slices.Contains(policy.OnRequest, key) {
				plan.Requests = append(plan.Requests, ReadableRequestResult{Path: path, Files: cloneOrigins(input.WorkspaceOrigins[path]), Capability: key, Value: value,
					Status: ReadableRequestSkipped, Reason: "not allowed by allow." + name + ".always or allow." + name + ".on_request"})
				continue
			}
			if shape == SchemaTypeList && len(stringList(value)) == 0 || shape == SchemaTypeBoolean && value == false {
				plan.Requests = append(plan.Requests, ReadableRequestResult{Path: path, Files: cloneOrigins(input.WorkspaceOrigins[path]), Capability: key, Value: value,
					Status: ReadableRequestSkipped, Reason: "an empty list or false request asks for no grants"})
				continue
			}
			status, reason := ReadableRequestGranted, ""
			if slices.Contains(policy.PendingCapabilities, key) {
				status, reason = ReadableRequestPending, "declared semantic action; H runtime delivery remains pending and use is not activated"
			}
			plan.Requests = append(plan.Requests, ReadableRequestResult{Path: path, Files: cloneOrigins(input.WorkspaceOrigins[path]), Capability: key, Value: value, Status: status, Reason: reason})
		}
		if selected[policy.Pack] {
			if i := readableBrokerIndex(plan.Sources, name); i >= 0 {
				plan.Sources[i] = policy
			}
		}
	}
	return plan, nil
}

// ProduceReadableGateInput resolves actual remotes and selected named-host additions before
// materializing any per-consumer grant. It never fabricates an exact URL from OWNER/REPO.
func ProduceReadableGateInput(workspace string, plan ReadableAllowancePlan) (ReadableGateInput, error) {
	input := ReadableGateInput{Requests: cloneReadableRequests(plan.Requests), PackSettings: cloneReadableSettings(plan.PackSettings),
		PackSettingOrigins: cloneReadableSettingOrigins(plan.PackSettingOrigins), TableTombstones: append([]ReadableTableTombstone(nil), plan.TableTombstones...)}
	for i := range input.TableTombstones {
		input.TableTombstones[i].Files = cloneOrigins(input.TableTombstones[i].Files)
	}
	for _, policy := range plan.Sources {
		if policy.RemoteHost == brokerscope.AnyRemoteHost {
			continue
		}
		for _, requested := range policy.Repos {
			input.AdditionalRepositories = appendReadableRepository(input.AdditionalRepositories, ReadableRepository{
				Source: policy.Source, RemoteHost: policy.RemoteHost, Repository: requested.Repository, Origin: requested.Path, Files: cloneOrigins(requested.Files),
			})
		}
	}
	for _, policy := range plan.Sources {
		var remotes brokerscope.Read
		if policy.RemoteHost == brokerscope.AnyRemoteHost {
			remotes = brokerscope.ReadAllRemotes(workspace)
		} else {
			remotes = brokerscope.ReadRemotes(workspace, policy.RemoteHost)
		}
		targets := readableTargets(policy, remotes)
		if policy.RemoteHost == brokerscope.AnyRemoteHost {
			// Canonical named-host identities are available to this consumer as scope inputs;
			// the transport adapter must still resolve exact URLs at the same human gate.
			for _, additional := range input.AdditionalRepositories {
				targets = appendReadableRepository(targets, additional)
			}
		}
		readCapability := ReadableReadCapability{Source: policy.Source, Label: policy.Label, Capability: policy.ReadCapability,
			Always: slices.Contains(policy.Always, policy.ReadCapability), OnRequest: slices.Contains(policy.OnRequest, policy.ReadCapability),
			AlwaysFiles: cloneOrigins(policy.AlwaysOrigins), OnRequestFiles: cloneOrigins(policy.OnRequestOrigins)}
		for _, request := range input.Requests {
			if request.Path == "requests."+policy.Source+"."+policy.ReadCapability {
				readCapability.RequestStatus = request.Status
				readCapability.Requested = request.Status == ReadableRequestGranted && request.Value == true
				readCapability.RequestFiles = cloneOrigins(request.Files)
				break
			}
		}
		input.ReadCapabilities = append(input.ReadCapabilities, readCapability)
		observed := ReadableObservedSource{Source: policy.Source, Label: policy.Label, RemoteHost: policy.RemoteHost,
			EntryKey: policy.RepoPath, Read: cloneBrokerScopeRead(remotes), Targets: cloneReadableRepositories(targets)}
		input.ObservedSources = append(input.ObservedSources, observed)
		readAlways := slices.Contains(policy.Always, policy.ReadCapability)
		readRequested := slices.Contains(policy.OnRequest, policy.ReadCapability) && readableReadRequestedFromResults(input.Requests, policy.Source, policy.ReadCapability)
		readAllowed := readAlways || readRequested
		if !readAllowed || len(targets) == 0 {
			for i := range input.Requests {
				request := &input.Requests[i]
				if request.Status == ReadableRequestGranted && request.Capability != "repos" && request.Capability != policy.ReadCapability && strings.HasPrefix(request.Path, "requests."+policy.Source+".") {
					request.Status = ReadableRequestSkipped
					if !readAllowed {
						request.Reason = "the broker requires its own always read capability before a write request can take effect; add " + policy.ReadCapability + " to allow." + policy.Source + ".always"
					} else {
						request.Reason = "the workspace has no concrete repository target for this request"
					}
				}
			}
			continue
		}
		var decisions []ReadableGrantDecision
		var readOrigins, readFiles []string
		if readAlways {
			readOrigins = append(readOrigins, "allow."+policy.Source+".always."+policy.ReadCapability)
			readFiles = append(readFiles, policy.AlwaysOrigins...)
		}
		if readRequested {
			readOrigin := "requests." + policy.Source + "." + policy.ReadCapability
			readOrigins = append(readOrigins, readOrigin)
			readFiles = append(readFiles, policy.OnRequestOrigins...)
			for _, request := range input.Requests {
				if request.Path == readOrigin {
					readFiles = append(readFiles, request.Files...)
					break
				}
			}
		}
		for _, target := range targets {
			files := append(cloneOrigins(readFiles), target.Files...)
			for _, origin := range readOrigins {
				decisions = appendReadableGrant(decisions, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: target.RemoteHost + "/" + target.Repository}, origin, files)
			}
		}
		for _, capability := range policy.Always {
			if capability == policy.ReadCapability || slices.Contains(policy.PendingCapabilities, capability) {
				continue
			}
			kind := legacyKind(policy, capability)
			if kind == "" || brokerscope.GrantTakesRef(kind) {
				continue
			}
			for _, target := range targets {
				decisions = appendReadableGrant(decisions, brokerscope.Grant{Kind: kind, Repo: target.RemoteHost + "/" + target.Repository}, "allow."+policy.Source+".always."+capability, append(cloneOrigins(policy.AlwaysOrigins), target.Files...))
			}
		}
		for _, request := range input.Requests {
			if request.Status != ReadableRequestGranted || !strings.HasPrefix(request.Path, "requests."+policy.Source+".") || request.Capability == policy.ReadCapability || slices.Contains(policy.PendingCapabilities, request.Capability) {
				continue
			}
			ceilingOrigin := "allow." + policy.Source + ".on_request"
			ceilingFiles := policy.OnRequestOrigins
			if slices.Contains(policy.Always, request.Capability) {
				ceilingOrigin, ceilingFiles = "allow."+policy.Source+".always", policy.AlwaysOrigins
			}
			kind := legacyKind(policy, request.Capability)
			if kind == "" {
				continue
			}
			if brokerscope.GrantTakesRef(kind) {
				for _, target := range targets {
					for _, name := range stringList(request.Value) {
						grant := brokerscope.RefGrant(kind, target.RemoteHost+"/"+target.Repository, name)
						files := append(cloneOrigins(ceilingFiles), request.Files...)
						files = append(files, target.Files...)
						decisions = appendReadableGrant(decisions, grant, ceilingOrigin, files)
						decisions = appendReadableGrant(decisions, grant, request.Path, files)
					}
				}
				continue
			}
			requested, ok := request.Value.(bool)
			if !ok || !requested {
				continue
			}
			for _, target := range targets {
				grant := brokerscope.Grant{Kind: kind, Repo: target.RemoteHost + "/" + target.Repository}
				files := append(cloneOrigins(ceilingFiles), request.Files...)
				files = append(files, target.Files...)
				decisions = appendReadableGrant(decisions, grant, ceilingOrigin, files)
				decisions = appendReadableGrant(decisions, grant, request.Path, files)
			}
		}

		var valid []ReadableGrantDecision
		for i := range decisions {
			if !brokerscope.ValidGrant(decisions[i].Grant) {
				input.Differences = appendReadableDifference(input.Differences, fmt.Sprintf("%s target %q cannot be serialized as grant %q; preserve the typed target and write nothing until the scope is representable", policy.Source, decisions[i].Grant.Repo, decisions[i].Grant.String()))
				continue
			}
			valid = append(valid, decisions[i])
		}
		decisions = valid
		if len(decisions) == 0 {
			continue
		}
		entries := make([]EntryRepo, 0, len(policy.Repos))
		for _, repo := range policy.Repos {
			entries = appendReadableEntry(entries, EntryRepo{Repo: repo.Repository, Files: cloneOrigins(repo.Files)})
		}
		grants := make([]brokerscope.Grant, len(decisions))
		for i := range decisions {
			grants[i] = decisions[i].Grant
		}
		input.Sources = append(input.Sources, ReadableGateSource{Source: policy.Source, Label: policy.Label, RemoteHost: policy.RemoteHost,
			EntryKey: policy.RepoPath, Read: cloneBrokerScopeRead(remotes), Entry: entries, Targets: targets, Grants: grants, Decisions: decisions})
	}
	return input, nil
}

// WithReadableGateInput passes the producer's exact typed value to a gate-input spy/adapter.
func WithReadableGateInput(workspace string, plan ReadableAllowancePlan, consume func(ReadableGateInput) error) error {
	if consume == nil {
		return fmt.Errorf("readable gate input consumer is required")
	}
	input, err := ProduceReadableGateInput(workspace, plan)
	if err != nil {
		return err
	}
	return consume(input)
}

// ReadableLegacyGrantProjection carries the exact concrete canonical grants and every mismatch;
// a legacy scope the canonical list cannot express is a migration difference, never a wildcard.
type ReadableLegacyGrantProjection struct {
	Raw []brokerscope.Grant
	// Targets retains identities even when the legacy grant grammar cannot serialize them.
	Targets       []ReadableRepository
	Concrete      []brokerscope.Grant
	ScopeFreeRead bool
	Differences   []string
}

func CompareReadableLegacyGrants(source string, legacy, canonical []brokerscope.Grant) ReadableLegacyGrantProjection {
	projection := ReadableLegacyGrantProjection{Raw: append([]brokerscope.Grant(nil), legacy...), Concrete: append([]brokerscope.Grant(nil), canonical...)}
	old, next := map[brokerscope.Grant]bool{}, map[brokerscope.Grant]bool{}
	for _, grant := range legacy {
		old[grant] = true
	}
	for _, grant := range canonical {
		next[grant] = true
	}
	for _, grant := range legacy {
		if !next[grant] {
			projection.Differences = append(projection.Differences, fmt.Sprintf("legacy %s grant %q has no identical concrete canonical grant; report the migration difference rather than widen scope", source, grant.String()))
		}
	}
	for _, grant := range canonical {
		if !old[grant] {
			projection.Differences = append(projection.Differences, fmt.Sprintf("canonical %s grant %q changes legacy concrete scope; report the migration difference rather than write", source, grant.String()))
		}
	}
	return projection
}

// ExpandReadableLegacyGrants bounds a legacy AnyRepo grant to the supplied concrete canonical
// scope before comparison. Raw grants remain beside the expansion for provenance and migration;
// an empty scope expands to no grant and never to a global permission.
func ExpandReadableLegacyGrants(source string, raw []brokerscope.Grant, targets []ReadableRepository) ReadableLegacyGrantProjection {
	projection := ReadableLegacyGrantProjection{Raw: append([]brokerscope.Grant(nil), raw...), Targets: cloneReadableRepositories(targets)}
	concrete := map[string]bool{}
	for _, target := range targets {
		identity := target.RemoteHost + "/" + target.Repository
		concrete[strings.ToLower(identity)] = true
		if !brokerscope.ValidGrantRepo(identity) {
			projection.Differences = appendReadableDifference(projection.Differences, fmt.Sprintf("legacy %s target %q cannot be serialized as a concrete grant; preserve its typed target/raw state and write nothing until the scope is representable", source, identity))
		}
	}
	for _, grant := range raw {
		if grant.Kind == brokerscope.KindRead && grant.Repo == brokerscope.AnyRepo {
			projection.ScopeFreeRead = true
			if len(targets) == 0 {
				projection.Differences = append(projection.Differences, fmt.Sprintf("legacy %s read * permits allowlisted scope-free reads without any repository; concrete scope is empty and the new read-capability metadata has no classifier/approval delivery yet, so L must preserve raw state and write nothing", source))
			}
		}
		if grant.Repo == brokerscope.AnyRepo {
			if brokerscope.GrantTakesRef(grant.Kind) {
				projection.Differences = append(projection.Differences, fmt.Sprintf("legacy %s %s grant cannot use wildcard scope", source, grant.Kind))
				continue
			}
			for _, target := range targets {
				identity := target.RemoteHost + "/" + target.Repository
				grant.Repo = identity
				if !brokerscope.ValidGrant(grant) {
					projection.Differences = appendReadableDifference(projection.Differences, fmt.Sprintf("legacy %s target %q cannot serialize grant %q; preserve typed target/raw state and write nothing", source, identity, grant.String()))
					continue
				}
				projection.Concrete = appendReadableConcreteGrant(projection.Concrete, grant)
			}
			continue
		}
		if !brokerscope.ValidGrant(grant) || !concrete[strings.ToLower(grant.Repo)] {
			projection.Differences = append(projection.Differences, fmt.Sprintf("legacy %s grant %q is outside or not representable within the concrete scope; preserve as migration difference", source, grant.String()))
			continue
		}
		projection.Concrete = appendReadableConcreteGrant(projection.Concrete, grant)
	}
	return projection
}

func appendReadableDifference(differences []string, difference string) []string {
	if !slices.Contains(differences, difference) {
		return append(differences, difference)
	}
	return differences
}

func appendReadableConcreteGrant(grants []brokerscope.Grant, grant brokerscope.Grant) []brokerscope.Grant {
	if !slices.Contains(grants, grant) {
		return append(grants, grant)
	}
	return grants
}

// ReadableLegacyPackState preserves actual legacy selection and enable-precedence inputs.
type ReadableLegacyPackState struct {
	Selected         bool
	UserEnabled      *bool
	DefaultEnabled   *bool
	WorkspaceEnabled *bool
}

// ReadableLegacySetting preserves the authored scope separately from the declaration's allowed
// scope, so a workspace-permitted setting in user config stays at user scope when projected.
type ReadableLegacySetting struct {
	Key           string
	Value         any
	DeclaredScope string
	OriginScope   string
}

type ReadableLegacyPackMapping struct {
	LegacyName string
	PackHandle string
}

type ReadableLegacyPackProjection struct {
	PackSelected     bool
	MachineOff       *bool
	UserEnabled      *bool
	DefaultEnabled   *bool
	WorkspaceEnabled *bool
	EffectiveEnabled *bool
	AllowSettings    map[string]any
	RequestSettings  map[string]any
	AllowOrigins     map[string][]string
	RequestOrigins   map[string][]string
	Differences      []string
}

func readableEffectiveLegacyEnabled(state ReadableLegacyPackState) bool {
	if state.WorkspaceEnabled != nil {
		return *state.WorkspaceEnabled
	}
	if state.UserEnabled != nil {
		return *state.UserEnabled
	}
	if state.DefaultEnabled != nil {
		return *state.DefaultEnabled
	}
	return false
}

// whose declaration type and authored scope remain representable. It performs no writes.
func ProjectReadableLegacyPack(mapping ReadableLegacyPackMapping, values []ReadableLegacySetting, declarations []loopholedecl.Setting,
	state ReadableLegacyPackState, origins map[string][]string) ReadableLegacyPackProjection {
	projection := ReadableLegacyPackProjection{PackSelected: state.Selected, UserEnabled: cloneBool(state.UserEnabled), DefaultEnabled: cloneBool(state.DefaultEnabled),
		WorkspaceEnabled: cloneBool(state.WorkspaceEnabled), AllowSettings: map[string]any{}, RequestSettings: map[string]any{},
		AllowOrigins: map[string][]string{}, RequestOrigins: map[string][]string{}}
	effective := readableEffectiveLegacyEnabled(state)
	projection.EffectiveEnabled = &effective
	if state.UserEnabled != nil && !*state.UserEnabled || state.WorkspaceEnabled != nil && !*state.WorkspaceEnabled {
		off := !effective
		projection.MachineOff = &off
	}
	if mapping.LegacyName == "" || mapping.PackHandle == "" {
		projection.Differences = append(projection.Differences, "legacy pack identity has no explicit unambiguous readable mapping; preserve its settings as a migration difference")
		return projection
	}
	if !state.Selected || !effective {
		if len(values) != 0 {
			projection.Differences = append(projection.Differences, "legacy selection/default/machine-off state suppresses settings projection; do not activate a dormant service")
		}
		return projection
	}
	for _, value := range values {
		setting, found, ambiguous := readableSettingByKey(declarations, value.Key)
		if ambiguous {
			projection.Differences = append(projection.Differences, fmt.Sprintf("legacy setting %q has ambiguous declared owners under %q", value.Key, mapping.PackHandle))
			continue
		}
		var normalized any
		var settingErr error
		if found {
			normalized, settingErr = readableSettingValue(value.Value, setting)
		}
		if !found || value.DeclaredScope != setting.Scope || settingErr != nil {
			projection.Differences = append(projection.Differences, fmt.Sprintf("legacy setting %q has no exact declared type/scope projection under %q", value.Key, mapping.PackHandle))
			continue
		}
		switch value.OriginScope {
		case "user":
			if setting.Scope != loopholedecl.SettingScopeUser && setting.Scope != loopholedecl.SettingScopeWorkspace {
				projection.Differences = append(projection.Differences, fmt.Sprintf("legacy user setting %q has no user-scope projection", value.Key))
				continue
			}
			projection.AllowSettings[value.Key] = normalized
			projection.AllowOrigins[value.Key] = cloneOrigins(origins[value.Key])
		case "workspace":
			if setting.Scope == loopholedecl.SettingScopeUser {
				projection.Differences = append(projection.Differences, fmt.Sprintf("legacy workspace setting %q is user-scope only", value.Key))
				continue
			}
			projection.RequestSettings[value.Key] = normalized
			projection.RequestOrigins[value.Key] = cloneOrigins(origins[value.Key])
		default:
			projection.Differences = append(projection.Differences, fmt.Sprintf("legacy setting %q has unknown authored scope %q", value.Key, value.OriginScope))
		}
	}
	return projection
}

// ReadableConcreteTarget associates each granted repository identity with its origin and source.
type ReadableConcreteTarget struct{ Repository ReadableRepository }

// BrokeredGrantKindsUsingReadablePolicy reads the current user scope and returns the grant kinds
// authorized by its selected broker declaration. A canonical `allow.<source>` table is authority
// even when empty; it suppresses the legacy grant_kinds fallback. The shared readable resolver
// validates the declaration and capability mapping before this adapter projects capabilities back
// to the broker's grant kinds. Callers use the returned canonical flag to distinguish a valid
// empty authority from legacy mode, and treat every error as no write authority.
func BrokeredGrantKindsUsingReadablePolicy(source string) (kinds []string, canonical bool, err error) {
	user, sources, err := UserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		return nil, true, err
	}
	allowValue, hasAllow := user.Get("allow")
	if hasAllow {
		allow, ok := asMap(allowValue)
		if !ok {
			return nil, true, fmt.Errorf("allow: expected a table")
		}
		if _, canonical = allow.Get(source); canonical {
			selected, declarations, settings, err := readableBrokerSelection(user, sources)
			if err != nil {
				return nil, true, err
			}
			plan, err := ResolveReadableAllowance(ReadableAllowanceInput{
				User: user, SelectedPacks: selected, Declarations: declarations, PackSettings: settings,
			})
			if err != nil {
				return nil, true, err
			}
			var policy *ReadableBrokerPolicy
			for i := range plan.Sources {
				if plan.Sources[i].Source == source {
					policy = &plan.Sources[i]
					break
				}
			}
			if policy == nil {
				// A valid `off` tombstone carries canonical authority but no allowance.
				return nil, true, nil
			}
			kinds, err := readablePolicyGrantKinds(*policy, declarations)
			return kinds, true, err
		}
	}
	return grantKindsOf(user, source), false, nil
}

// readablePolicyGrantKinds projects a resolved broker policy back onto the grant kinds of its
// selected declaration: each kind whose capability the policy allows, always or on request, and
// that is not pending.
func readablePolicyGrantKinds(policy ReadableBrokerPolicy, declarations []ReadableBrokerDeclaration) ([]string, error) {
	for _, declaration := range declarations {
		if declaration.Pack != policy.Pack || declaration.Brokered.Source != policy.Source {
			continue
		}
		var kinds []string
		for _, kind := range declaration.Brokered.Grants {
			capability := policy.KindCapabilities[kind]
			if capability != "" && !slices.Contains(policy.PendingCapabilities, capability) &&
				(slices.Contains(policy.Always, capability) || slices.Contains(policy.OnRequest, capability)) {
				kinds = append(kinds, kind)
			}
		}
		return kinds, nil
	}
	return nil, fmt.Errorf("selected broker declaration for %q is missing", policy.Source)
}

// UserBrokerAuthority is what one user config grants a broker source for every workspace:
// whether every workspace reads its own approved scope, and the grant kinds a project may
// request, sorted. It is the brokers' answer, whichever spelling the config uses.
type UserBrokerAuthority struct {
	Source     string
	Read       bool
	GrantKinds []string
	// Problem is the readable resolver's refusal, "" when it resolved.
	Problem string
}

// UserBrokerAuthorityOf reads user, a user config as the readers made it, the way the brokers
// do: a source with an `allow.<source>` table answers through the readable resolver
// (ResolveReadableAllowance, as BrokeredGrantKindsUsingReadablePolicy does), with selected the
// packs the config selects; any other answers through the legacy `brokered.<source>` readers. It
// reads nothing from disk, so `yolo config migrate` can prove a conversion against it. One entry
// per source the declarations name, in their order.
func UserBrokerAuthorityOf(user *jsonx.OrderedMap, selected []string, declarations []ReadableBrokerDeclaration) []UserBrokerAuthority {
	if user == nil {
		user = jsonx.NewOrderedMap()
	}
	allow, _ := asMap(readableValue(user, "allow"))
	var out []UserBrokerAuthority
	seen := map[string]bool{}
	for _, declaration := range declarations {
		source := declaration.Brokered.Source
		if seen[source] {
			continue
		}
		seen[source] = true
		a := UserBrokerAuthority{Source: source}
		var table any
		hasTable := false
		if allow != nil {
			table, hasTable = allow.Get(source)
		}
		if !hasTable {
			a.Read = containsGrant(userGrantsOf(user, source), brokerscope.Grant{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo})
			a.GrantKinds = grantKindsOf(user, source)
		} else {
			// Only this source's table: another `allow` table answers to its own declaration,
			// which is not this source's question.
			onlyAllow := jsonx.NewOrderedMap()
			onlyAllow.Set(source, table)
			only := jsonx.NewOrderedMap()
			only.Set("allow", onlyAllow)
			plan, err := ResolveReadableAllowance(ReadableAllowanceInput{User: only, SelectedPacks: selected, Declarations: declarations})
			if err != nil {
				a.Problem = err.Error()
			} else if i := readableBrokerIndex(plan.Sources, source); i >= 0 {
				policy := plan.Sources[i]
				a.Read = slices.Contains(policy.Always, policy.ReadCapability)
				if a.GrantKinds, err = readablePolicyGrantKinds(policy, declarations); err != nil {
					a.Problem = err.Error()
				}
			}
		}
		sort.Strings(a.GrantKinds)
		out = append(out, a)
	}
	return out
}

// readableDeclarations validates the selected broker and pack declarations and resolves their
// ownership before any canonical authority can reach a broker.
func readableDeclarations(input ReadableAllowanceInput) (map[string][]loopholedecl.Setting, map[string]ReadableBrokerPolicy, error) {
	settings := map[string][]loopholedecl.Setting{}
	brokers := map[string]ReadableBrokerPolicy{}
	selected := readableSet(input.SelectedPacks)
	for _, declaration := range input.PackSettings {
		if declaration.Pack == "" {
			return nil, nil, fmt.Errorf("empty pack declaration")
		}
		settings[declaration.Pack] = append(settings[declaration.Pack], declaration.Settings...)
	}
	for _, declaration := range input.Declarations {
		b := declaration.Brokered
		if declaration.Pack == "" || declaration.Label == "" || b.Source == "" || declaration.ReadCapability == "" {
			return nil, nil, fmt.Errorf("pack %q broker declaration needs a pack handle, label, source and read capability", declaration.Pack)
		}
		if declaration.ReadCapability == "repos" || declaration.ReadCapability == "always" || declaration.ReadCapability == "on_request" {
			return nil, nil, fmt.Errorf("pack %q read capability %q collides with allowance/request table fields", declaration.Pack, declaration.ReadCapability)
		}
		mapping := map[string]string{}
		seenKinds, seenCapabilities := map[string]bool{}, map[string]string{}
		for _, kind := range b.Grants {
			if seenKinds[kind] {
				return nil, nil, fmt.Errorf("pack %q source %q repeats declared grant kind %q", declaration.Pack, b.Source, kind)
			}
			seenKinds[kind] = true
			if kind == brokerscope.KindRead {
				return nil, nil, fmt.Errorf("pack %q source %q must declare read capability separately, not map the read grant kind", declaration.Pack, b.Source)
			}
			capability, found := declaration.KindCapabilities[kind]
			if !found || capability == "" {
				return nil, nil, fmt.Errorf("pack %q source %q grant kind %q has no canonical capability mapping", declaration.Pack, b.Source, kind)
			}
			if capability == declaration.ReadCapability {
				return nil, nil, fmt.Errorf("pack %q maps read capability %q onto write grant kind %q", declaration.Pack, capability, kind)
			}
			if capability == "repos" || capability == "always" || capability == "on_request" {
				return nil, nil, fmt.Errorf("pack %q maps capability %q, which collides with allowance/request table fields", declaration.Pack, capability)
			}
			if other, exists := seenCapabilities[capability]; exists {
				return nil, nil, fmt.Errorf("pack %q maps capability %q ambiguously to grant kinds %q and %q", declaration.Pack, capability, other, kind)
			}
			seenCapabilities[capability] = kind
			mapping[kind] = capability
		}
		for kind := range declaration.KindCapabilities {
			if _, found := mapping[kind]; !found {
				return nil, nil, fmt.Errorf("pack %q maps undeclared grant kind %q; declared: %s", declaration.Pack, kind, strings.Join(b.Grants, ", "))
			}
		}
		pending := []string{}
		for _, capability := range declaration.PendingCapabilities {
			if capability == declaration.ReadCapability || !readableHasMappedCapability(mapping, capability) || slices.Contains(pending, capability) {
				return nil, nil, fmt.Errorf("pack %q pending capability %q must uniquely name a declared non-read broker grant capability", declaration.Pack, capability)
			}
			pending = append(pending, capability)
		}
		settings[declaration.Pack] = append(settings[declaration.Pack], declaration.Settings...)
		policy := ReadableBrokerPolicy{Pack: declaration.Pack, Label: declaration.Label, Source: b.Source, RemoteHost: b.RemoteHost,
			ReadCapability: declaration.ReadCapability, KindCapabilities: mapping, PendingCapabilities: pending}
		if previous, found := brokers[b.Source]; found {
			previousSelected, currentSelected := selected[previous.Pack], selected[declaration.Pack]
			switch {
			case previousSelected && currentSelected:
				return nil, nil, fmt.Errorf("broker source %q is declared by two selected packs %q and %q", b.Source, previous.Pack, declaration.Pack)
			case currentSelected:
				brokers[b.Source] = policy
			case previousSelected:
				// An irrelevant unselected declaration cannot shadow the selected source.
			default:
				previous.Ambiguous = true
				brokers[b.Source] = previous
			}
		} else {
			brokers[b.Source] = policy
		}
	}
	return settings, brokers, nil
}

func readableTable(config *jsonx.OrderedMap, key string) (*jsonx.OrderedMap, error) {
	if config == nil {
		return jsonx.NewOrderedMap(), nil
	}
	value, found := config.Get(key)
	if !found {
		return jsonx.NewOrderedMap(), nil
	}
	if value == nil {
		return nil, fmt.Errorf("%s: expected a table", key)
	}
	table, ok := asMap(value)
	if !ok {
		return nil, fmt.Errorf("%s: expected a table", key)
	}
	return table, nil
}
func isReadableTableOff(value any) bool {
	text, ok := value.(string)
	return ok && text == "off"
}

func readableValue(table *jsonx.OrderedMap, key string) any { value, _ := table.Get(key); return value }

func readableStringList(value any, path string) ([]string, error) {
	if list, ok := value.([]string); ok {
		return append([]string(nil), list...), nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a list of strings", path)
	}
	out := make([]string, len(list))
	for i, item := range list {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("%s[%d]: expected a non-empty string", path, i)
		}
		out[i] = text
	}
	return out, nil
}

func validateReadableCapabilities(source, field string, values []string, policy ReadableBrokerPolicy) ([]string, error) {
	out, seen := make([]string, 0, len(values)), map[string]bool{}
	for i, capability := range values {
		if !readableHasCapability(policy, capability) {
			return nil, fmt.Errorf("allow.%s.%s[%d]: %q is not declared; declared capabilities: %s", source, field, i, capability, strings.Join(readableCapabilities(policy), ", "))
		}
		if seen[capability] {
			return nil, fmt.Errorf("allow.%s.%s[%d]: capability %q is listed twice", source, field, i, capability)
		}
		seen[capability] = true
		out = append(out, capability)
	}
	return out, nil
}
func readableHasMappedCapability(mapping map[string]string, capability string) bool {
	for _, mapped := range mapping {
		if mapped == capability {
			return true
		}
	}
	return false
}

func readableRequestShape(policy ReadableBrokerPolicy, capability string) (SchemaType, bool) {
	if capability == policy.ReadCapability {
		return SchemaTypeBoolean, true
	}
	kind := legacyKind(policy, capability)
	if kind == "" {
		return 0, false
	}
	if brokerscope.GrantTakesRef(kind) {
		return SchemaTypeList, true
	}
	return SchemaTypeBoolean, true
}

func readableHasCapability(policy ReadableBrokerPolicy, name string) bool {
	if name == policy.ReadCapability {
		return true
	}
	for _, cap := range policy.KindCapabilities {
		if cap == name {
			return true
		}
	}
	return false
}
func readableCapabilities(policy ReadableBrokerPolicy) []string {
	seen := map[string]bool{policy.ReadCapability: true}
	out := []string{policy.ReadCapability}
	var rest []string
	for _, cap := range policy.KindCapabilities {
		if !seen[cap] {
			seen[cap] = true
			rest = append(rest, cap)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
func readableSettingNames(settings []loopholedecl.Setting) []string {
	out := make([]string, 0, len(settings))
	for _, setting := range settings {
		out = append(out, setting.Key)
	}
	return out
}
func readableDeclaredNames(settings map[string][]loopholedecl.Setting, brokers map[string]ReadableBrokerPolicy) []string {
	seen, out := map[string]bool{}, []string{}
	for name := range settings {
		seen[name] = true
		out = append(out, name)
	}
	for name := range brokers {
		if !seen[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
func readableRequestType(value any, kind SchemaType) error {
	if kind == SchemaTypeList {
		values, err := readableStringList(value, "request")
		if err != nil {
			return fmt.Errorf("expected a list of strings")
		}
		seen := map[string]bool{}
		for _, item := range values {
			if item == "" || seen[item] {
				return fmt.Errorf("entries must be non-empty and unique")
			}
			seen[item] = true
		}
		return nil
	}
	if kind == SchemaTypeBoolean {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected a boolean")
		}
	}
	return nil
}
func readableSettingByKey(settings []loopholedecl.Setting, key string) (loopholedecl.Setting, bool, bool) {
	var found loopholedecl.Setting
	count := 0
	for _, setting := range settings {
		if setting.Key == key {
			found = setting
			count++
		}
	}
	return found, count > 0, count > 1
}

func readableSettingValue(value any, setting loopholedecl.Setting) (any, error) {
	normalized, message := loopholedecl.CoerceSettingValue(setting.Type, value)
	if message != "" {
		return nil, fmt.Errorf("%s", message)
	}
	return normalized, nil
}

func putReadableSetting(plan *ReadableAllowancePlan, pack, key string, value any, origins []string) {
	if plan.PackSettings[pack] == nil {
		plan.PackSettings[pack] = map[string]any{}
		plan.PackSettingOrigins[pack] = map[string][]string{}
	}
	plan.PackSettings[pack][key] = value
	for _, origin := range origins {
		if !slices.Contains(plan.PackSettingOrigins[pack][key], origin) {
			plan.PackSettingOrigins[pack][key] = append(plan.PackSettingOrigins[pack][key], origin)
		}
	}
}
func readableSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
func cloneOrigins(values []string) []string { return append([]string(nil), values...) }
func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneBrokerScopeRead(read brokerscope.Read) brokerscope.Read {
	read.Remotes = append([]brokerscope.Remote(nil), read.Remotes...)
	read.Excluded = append([]string(nil), read.Excluded...)
	return read
}

func cloneReadableRepositories(repositories []ReadableRepository) []ReadableRepository {
	out := append([]ReadableRepository(nil), repositories...)
	for i := range out {
		out[i].Files = cloneOrigins(out[i].Files)
	}
	return out
}

func cloneReadableSettingOrigins(origins map[string]map[string][]string) map[string]map[string][]string {
	out := make(map[string]map[string][]string, len(origins))
	for pack, settings := range origins {
		copy := make(map[string][]string, len(settings))
		for key, files := range settings {
			copy[key] = cloneOrigins(files)
		}
		out[pack] = copy
	}
	return out
}

func cloneReadableSettings(settings map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(settings))
	for pack, values := range settings {
		copy := make(map[string]any, len(values))
		for key, value := range values {
			switch typed := value.(type) {
			case []string:
				copy[key] = append([]string(nil), typed...)
			default:
				copy[key] = value
			}
		}
		out[pack] = copy
	}
	return out
}

func cloneReadableRequests(requests []ReadableRequestResult) []ReadableRequestResult {
	out := append([]ReadableRequestResult(nil), requests...)
	for i := range out {
		out[i].Files = cloneOrigins(out[i].Files)
		switch value := out[i].Value.(type) {
		case []any:
			out[i].Value = append([]any(nil), value...)
		case []string:
			out[i].Value = append([]string(nil), value...)
		}
	}
	return out
}
func readableBrokerIndex(policies []ReadableBrokerPolicy, source string) int {
	for i := range policies {
		if policies[i].Source == source {
			return i
		}
	}
	return -1
}
func readableReadRequestedFromResults(requests []ReadableRequestResult, source, capability string) bool {
	path := "requests." + source + "." + capability
	for _, request := range requests {
		if request.Path == path && request.Status == ReadableRequestGranted {
			requested, ok := request.Value.(bool)
			return ok && requested
		}
	}
	return false
}
func readableRepo(requests []ReadableRepoRequest, request ReadableRepoRequest) []ReadableRepoRequest {
	for i := range requests {
		if strings.EqualFold(requests[i].Repository, request.Repository) {
			for _, file := range request.Files {
				if !slices.Contains(requests[i].Files, file) {
					requests[i].Files = append(requests[i].Files, file)
				}
			}
			return requests
		}
	}
	return append(requests, request)
}
func appendReadableRepository(repos []ReadableRepository, repo ReadableRepository) []ReadableRepository {
	for i := range repos {
		if repos[i].Source == repo.Source && repos[i].RemoteHost == repo.RemoteHost && strings.EqualFold(repos[i].Repository, repo.Repository) {
			if repo.Origin != "" && !slices.Contains(repos[i].Files, repo.Origin) {
				repos[i].Files = append(repos[i].Files, repo.Origin)
			}
			for _, f := range repo.Files {
				if !slices.Contains(repos[i].Files, f) {
					repos[i].Files = append(repos[i].Files, f)
				}
			}
			return repos
		}
	}
	repo.Files = cloneOrigins(repo.Files)
	return append(repos, repo)
}
func readableTargets(policy ReadableBrokerPolicy, remotes brokerscope.Read) []ReadableRepository {
	var out []ReadableRepository
	for _, remote := range remotes.Remotes {
		host, repo := policy.RemoteHost, remote.Repo
		if policy.RemoteHost == brokerscope.AnyRemoteHost {
			identity, _ := gitproto.Identity(remote.Repo)
			var ok bool
			host, repo, ok = strings.Cut(identity, "/")
			if !ok {
				continue
			}
		}
		if policy.RemoteHost != brokerscope.AnyRemoteHost && !brokerscope.ValidRepo(repo) {
			continue
		}
		out = appendReadableRepository(out, ReadableRepository{Source: policy.Source, RemoteHost: host, Repository: repo, Origin: "remote " + remote.Name})
	}
	if policy.RemoteHost != brokerscope.AnyRemoteHost {
		for _, request := range policy.Repos {
			out = appendReadableRepository(out, ReadableRepository{Source: policy.Source, RemoteHost: policy.RemoteHost, Repository: request.Repository, Origin: request.Path, Files: cloneOrigins(request.Files)})
		}
	}
	return out
}
func appendReadableGrant(decisions []ReadableGrantDecision, grant brokerscope.Grant, origin string, files []string) []ReadableGrantDecision {
	for i := range decisions {
		if decisions[i].Grant == grant {
			if origin != "" && !slices.Contains(decisions[i].Origins, origin) {
				decisions[i].Origins = append(decisions[i].Origins, origin)
			}
			for _, file := range files {
				if !slices.Contains(decisions[i].Files, file) {
					decisions[i].Files = append(decisions[i].Files, file)
				}
			}
			return decisions
		}
	}
	origins := []string{}
	if origin != "" {
		origins = append(origins, origin)
	}
	return append(decisions, ReadableGrantDecision{Grant: grant, Origins: origins, Files: cloneOrigins(files)})
}
func appendReadableEntry(entries []EntryRepo, entry EntryRepo) []EntryRepo {
	for i := range entries {
		if strings.EqualFold(entries[i].Repo, entry.Repo) {
			for _, file := range entry.Files {
				if !slices.Contains(entries[i].Files, file) {
					entries[i].Files = append(entries[i].Files, file)
				}
			}
			return entries
		}
	}
	entry.Files = cloneOrigins(entry.Files)
	return append(entries, entry)
}
func legacyKind(policy ReadableBrokerPolicy, capability string) string {
	for kind, mapped := range policy.KindCapabilities {
		if mapped == capability {
			return kind
		}
	}
	return ""
}
func stringList(value any) []string { values, _ := readableStringList(value, "request"); return values }
func appendReadableRepo(requests []ReadableRepoRequest, request ReadableRepoRequest) []ReadableRepoRequest {
	return readableRepo(requests, request)
}

// AllowNudgeKind names which way an `allow.<name>` table disagrees with the pack list.
type AllowNudgeKind string

const (
	// AllowNudgeUnselected: the table names a pack yolo knows that `packs` does not list. A table
	// selects nothing, so this is an error whose fix is `yolo pack add <pack>`.
	AllowNudgeUnselected AllowNudgeKind = "unselected"
	// AllowNudgeUnknown: the table names no pack and no broker source at all (a typo, usually).
	AllowNudgeUnknown AllowNudgeKind = "unknown"
	// AllowNudgeLoginNoProfile: a listed login pack whose table names no profile. A warning: the
	// pack installs and its service serves nothing.
	AllowNudgeLoginNoProfile AllowNudgeKind = "login-no-profile"
)

// AllowNudge is one finding of AllowPackNudges (docs/design/config-user.md §7).
type AllowNudge struct {
	Kind AllowNudgeKind
	// Table is the authored name under `allow.`; Pack is the pack that table belongs to (the one
	// `yolo pack add` takes), "" for an unknown table.
	Table, Pack string
	// Closest is the nearest known name to an unknown Table, "" when none is near.
	Closest string
	// Setting is, for a login finding, the setting key that names the profile.
	Setting string
	// Location is "<file>:<line>:<col>" of the authored key.
	Location string
}

// Problem is what is wrong, location first; NextStep is the one command or edit that fixes it.
func (n AllowNudge) Problem() string {
	switch n.Kind {
	case AllowNudgeUnselected:
		return fmt.Sprintf("%s: allow.%s names a pack that is not selected.", n.Location, n.Table)
	case AllowNudgeUnknown:
		return fmt.Sprintf("%s: allow.%s: no such pack.", n.Location, n.Table)
	default:
		return fmt.Sprintf("%s: packs lists %s, but [allow.%s] names no %s, so it serves nothing.",
			n.Location, n.Pack, n.Pack, n.Setting)
	}
}

// NextStep names the fix.
func (n AllowNudge) NextStep() string {
	switch n.Kind {
	case AllowNudgeUnselected:
		return fmt.Sprintf("Add it (yolo pack add %s) or delete the table.", n.Pack)
	case AllowNudgeUnknown:
		if n.Closest == "" {
			return "`yolo pack ls` lists the packs yolo ships; name one, or delete the table."
		}
		return "Did you mean " + n.Closest + "? Rename the table heading, or delete it."
	default:
		return fmt.Sprintf("Set one: yolo config set allow.%s.%s <value>", n.Pack, n.Setting)
	}
}

// Message is Problem and NextStep as one line, for a launch's refusal.
func (n AllowNudge) Message() string { return n.Problem() + " " + n.NextStep() }

// AllowPackFacts is what AllowPackNudges judges the tables against. Names maps every name an
// `allow.` table may carry (a pack's, a broker source's) to the pack `yolo pack add` takes.
// LoginProfile maps a login pack to the setting key that names its profile; both come from pack
// declarations, never from a list of tool names here.
type AllowPackFacts struct {
	Listed       []string
	Names        map[string]string
	LoginProfile map[string]string
}

// AllowPackNudges compares user-scope `allow.<name>` tables with the pack list. The location of
// each finding is where the authored key was written (userSrc may be nil).
func AllowPackNudges(user *jsonx.OrderedMap, userSrc *Sources, facts AllowPackFacts) []AllowNudge {
	listed := readableSet(facts.Listed)
	userPath := paths.UserConfigPath()
	locate := func(path string) string {
		if locs := userSrc.Locations("config." + path); len(locs) > 0 {
			return locs[0]
		}
		return userPath
	}
	var out []AllowNudge
	allow, _ := readableTable(user, "allow")
	if allow == nil {
		allow = jsonx.NewOrderedMap()
	}
	known := make([]string, 0, len(facts.Names))
	for name := range facts.Names {
		known = append(known, name)
	}
	sort.Strings(known)
	for _, name := range allow.Keys() {
		if name == allowMountsName || isReadableTableOff(readableValue(allow, name)) {
			continue
		}
		pack, isKnown := facts.Names[name]
		switch {
		case !isKnown:
			out = append(out, AllowNudge{Kind: AllowNudgeUnknown, Table: name, Closest: closestAllowName(name, known), Location: locate("allow." + name)})
		case !listed[pack]:
			out = append(out, AllowNudge{Kind: AllowNudgeUnselected, Table: name, Pack: pack, Location: locate("allow." + name)})
		}
	}
	logins := make([]string, 0, len(facts.LoginProfile))
	for pack := range facts.LoginProfile {
		logins = append(logins, pack)
	}
	sort.Strings(logins)
	for _, pack := range logins {
		if !listed[pack] {
			continue
		}
		setting := facts.LoginProfile[pack]
		if table, ok := asMap(readableValue(allow, pack)); ok {
			if profile, _ := table.Get(setting); profile != nil && profile != "" {
				continue
			}
		}
		out = append(out, AllowNudge{Kind: AllowNudgeLoginNoProfile, Pack: pack, Table: pack, Setting: setting, Location: locate("packs")})
	}
	return out
}

func closestAllowName(name string, known []string) string {
	limit := min(2, max(1, len(name)/4))
	best, bestDist := "", limit+1
	for _, candidate := range known {
		if d := allowEditDistance(name, candidate); d < bestDist {
			best, bestDist = candidate, d
		}
	}
	return best
}

func allowEditDistance(a, b string) int {
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// EmbeddedAllowPackFacts builds the facts from the shipped packs' declarations: every shipped
// pack name and broker source maps to its pack, and a pack whose gated env contribution names a
// `region_profile_setting` is a login pack keyed by that setting. A listed pack that is not
// shipped is still known by its own name.
func EmbeddedAllowPackFacts(listed []string) AllowPackFacts {
	facts := AllowPackFacts{Listed: listed, Names: map[string]string{}, LoginProfile: map[string]string{}}
	shipped := packload.Embedded()
	for _, pack := range shipped {
		if pack == nil || pack.Name == "" {
			continue
		}
		facts.Names[pack.Name] = pack.Name
		if pack.Decl == nil {
			continue
		}
		for _, gated := range pack.Decl.GatedEnvContributions() {
			if gated.RegionProfileSetting != "" {
				facts.LoginProfile[pack.Name] = gated.RegionProfileSetting
			}
		}
	}
	if declarations, _, err := ReadableDeclarationsForPacks(shipped); err == nil {
		for _, d := range declarations {
			if d.Brokered.Source != "" {
				if _, taken := facts.Names[d.Brokered.Source]; !taken {
					facts.Names[d.Brokered.Source] = d.Pack
				}
			}
		}
	}
	for _, name := range listed {
		if _, ok := facts.Names[name]; !ok {
			facts.Names[name] = name
		}
	}
	return facts
}

// ListedPackNames is the pack names the user scope lists, as LoadPacks reads them.
func ListedPackNames(warn Warn) ([]string, error) {
	entries, err := LoadPacks(warn)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names, nil
}

// AllowPackSwitchErrors is the launch's half of the pack-switch rule: one located error per
// `allow.<name>` table that names a pack `packs` does not list (or no pack at all). It reads the
// user scope itself; a read that fails reports nothing here, because the config gate has already
// said so.
func AllowPackSwitchErrors() []string {
	user, src, err := UserScopeConfigWithSources(true, func(string) {})
	if err != nil || user == nil {
		return nil
	}
	if _, has := user.Get("allow"); !has {
		return nil
	}
	listed, err := ListedPackNames(nil)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range AllowPackNudges(user, src, EmbeddedAllowPackFacts(listed)) {
		if n.Kind != AllowNudgeLoginNoProfile {
			out = append(out, n.Message())
		}
	}
	return out
}
