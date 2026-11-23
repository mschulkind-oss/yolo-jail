package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// ScopedBrokerScope is authored policy metadata. It does not carry approved roots,
// live authority or executable context state.
type ScopedBrokerScope struct {
	Alias, Source, Resource, Layer, Context string
	Selected                                bool
	Matcher                                 brokerpolicy.ScopeMatcher
	Always, OnRequest, Never                []string
	Reads                                   map[string]string
	Tables                                  map[string]map[string]string
	Origins                                 map[string][]string
}

type BrokerExecutionContext struct {
	Name, Source string
	Fields       map[string]string
	Origins      map[string][]string
	Selected     bool
}

var brokerAliasRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func scopedDeclarationGrants(settings []ReadablePackDeclaration, pack, label string) []loopholedecl.Grant {
	var grants []loopholedecl.Grant
	for _, declaration := range settings {
		if declaration.Pack == pack && declaration.Loophole == label {
			grants = append(grants, declaration.Grants...)
		}
	}
	return grants
}

func declaredCallBrokerPolicyToken(b *loopholedecl.CallBroker, token string) bool {
	if b == nil {
		return false
	}
	for _, cell := range b.Cells {
		if cell.IsRead() {
			if token == "read."+cell.Name {
				return true
			}
			continue
		}
		for _, table := range cell.Tables {
			if token == table || token == table+"."+cell.Name {
				return true
			}
		}
	}
	return false
}

func scopedPolicyOrigins(origins map[string][]string, config *jsonx.OrderedMap, sources *Sources) {
	var visit func(*jsonx.OrderedMap, []string)
	visit = func(table *jsonx.OrderedMap, path []string) {
		origins[strings.Join(path, ".")] = scopeSourceFiles(sources, path...)
		for _, key := range table.Keys() {
			value := readableValue(table, key)
			childPath := append(slices.Clone(path), key)
			origins[strings.Join(childPath, ".")] = scopeSourceFiles(sources, childPath...)
			if child, ok := asMap(value); ok {
				visit(child, childPath)
			}
		}
	}
	if config == nil {
		return
	}
	for _, key := range []string{"scopes", "contexts", "requests", "allow", "repos"} {
		if table, ok := asMap(readableValue(config, key)); ok {
			visit(table, []string{key})
		}
	}
}

func readableCallBrokerHasReads(b *loopholedecl.CallBroker, grants []loopholedecl.Grant) bool {
	for _, cell := range b.Cells {
		if cell.IsRead() {
			return true
		}
	}
	for _, grant := range grants {
		if grant.Kind == "read" {
			return true
		}
	}
	return false
}

func validateScopedTokens(path string, tokens []string, policy ReadableBrokerPolicy) ([]string, error) {
	seen := map[string]bool{}
	for _, token := range tokens {
		valid := false
		if policy.CallBroker != nil {
			valid = token == "read" && policy.ReadAvailable
			for _, cell := range policy.CallBroker.Cells {
				if cell.IsRead() {
					valid = valid || token == "read."+cell.Name
					continue
				}
				for _, table := range cell.Tables {
					valid = valid || token == table || token == table+"."+cell.Name
				}
			}
		} else {
			if strings.HasPrefix(token, "read.") {
				return nil, fmt.Errorf("%s: %s is a native read; use %s instead of %s", path, policy.Source, policy.ReadCapability, token)
			}
			valid = readableHasCapability(policy, token) || token == "read"
		}
		if !valid {
			return nil, fmt.Errorf("%s: token %q is not declared by selected source %s; use yolo config unset %s --user", path, token, policy.Source, path)
		}
		if seen[token] {
			return nil, fmt.Errorf("%s: token %q is listed twice; remove the duplicate with yolo config set", path, token)
		}
		seen[token] = true
	}
	return slices.Clone(tokens), nil
}

func positionedPolicyLine(path string, origins map[string][]string) string {
	files := origins[path]
	if len(files) == 0 {
		files = origins[policyListOriginPath(path)]
	}
	if len(files) == 0 {
		return path
	}
	return path + " (" + strings.Join(files, ", ") + ")"
}

func validateNativeReadAliases(path string, table *jsonx.OrderedMap, policy ReadableBrokerPolicy, origins map[string][]string, lists bool) error {
	if policy.CallBroker != nil || policy.ReadCapability == "read" || policy.ReadCapability == "" {
		return nil
	}
	var literal, native string
	if lists {
		for _, field := range []string{"always", "on_request", "never"} {
			tokens, _ := readableStringList(readableValue(table, field), path+"."+field)
			for i, token := range tokens {
				itemPath := fmt.Sprintf("%s.%s[%d]", path, field, i)
				if token == "read" {
					literal = itemPath
				}
				if token == policy.ReadCapability {
					native = itemPath
				}
			}
		}
	} else {
		if _, has := table.Get("read"); has {
			literal = path + ".read"
		}
		if _, has := table.Get(policy.ReadCapability); has {
			native = path + "." + policy.ReadCapability
		}
	}
	if literal != "" {
		if err := nativeReadLegacyAliasRefusal(literal, policy, origins); err != nil {
			return err
		}
	}
	if literal != "" && native != "" {
		return &scopedBrokerConflict{paths: []string{literal, native}, message: fmt.Sprintf("name the same native read; use only %s with yolo config unset", policy.ReadCapability), origins: origins}
	}
	return nil
}

// ScopedBrokerCeilingValue implements only the declared never extension. With no
// covering never it calls the shipped resolver, including all-to-working_set coverage.
func ScopedBrokerCeilingValue(allow *jsonx.OrderedMap, table, cell string) string {
	own := table + "." + cell
	if table == "read" && cell == "" {
		own = "read"
	}
	value := callBrokerLegacyCeiling(scopedAllowOnly("policy", allow), "policy", table, cell)
	if value == "off" {
		value = "absent"
	}
	return string(brokerpolicy.CeilingValue(own, table, stringList(readableValue(allow, "always")), stringList(readableValue(allow, "on_request")), stringList(readableValue(allow, "never")), brokerpolicy.Value(value)))
}

func validateScopedCeilingLists(path string, allow *jsonx.OrderedMap, policy ReadableBrokerPolicy, origins map[string][]string) error {
	if err := validateNativeReadAliases(path, allow, policy, origins, true); err != nil {
		return err
	}
	for _, field := range []string{"always", "on_request", "never"} {
		raw, has := allow.Get(field)
		if !has {
			continue
		}
		values, err := readableStringList(raw, path+"."+field)
		if err != nil {
			return err
		}
		if field == "never" {
			if err := refuseLegacyBrokerPolicyLine(path+"."+field, origins); err != nil {
				return err
			}
			if _, err := validateScopedTokens(path+"."+field, values, policy); err != nil {
				return err
			}
		}
		// Old pack grant words keep their existing path. New read and never words
		// are checked solely against this operation declaration, never other modules.
		for _, token := range values {
			if field == "never" || token == "read" || strings.HasPrefix(token, "read.") {
				if strings.HasPrefix(token, "read.") {
					if err := refuseLegacyBrokerPolicyLine(path+"."+field, origins); err != nil {
						return err
					}
				}
				if _, err := validateScopedTokens(path+"."+field, []string{token}, policy); err != nil {
					return err
				}
			}
		}
	}
	if policy.CallBroker != nil {
		for _, cell := range policy.CallBroker.Cells {
			if cell.IsRead() || !slices.Contains(cell.Tables, "all") || !slices.Contains(cell.Tables, "working_set") {
				continue
			}
			all := ScopedBrokerCeilingValue(allow, "all", cell.Name)
			working := ScopedBrokerCeilingValue(allow, "working_set", cell.Name)
			if policyOrder(working) < policyOrder(all) {
				return fmt.Errorf("%s: working_set >= all is required for %s; use yolo config set %s", path, cell.Name, path)
			}
		}
	}
	never := stringList(readableValue(allow, "never"))
	for _, token := range never {
		for _, field := range []string{"always", "on_request"} {
			if slices.Contains(stringList(readableValue(allow, field)), token) {
				return &scopedBrokerConflict{paths: []string{path + ".never", path + "." + field}, message: fmt.Sprintf("both hold %q; remove one with yolo config set", token), origins: origins}
			}
		}
	}
	return nil
}

func requestWord(value any) (string, error) {
	switch value {
	case true:
		return "true", nil
	case false:
		return "false", nil
	case "ask":
		return "ask", nil
	}
	return "", fmt.Errorf("expected true, false, or \"ask\"; use yolo config set")
}

func validateScopedReadRequest(path string, value any, policy ReadableBrokerPolicy) error {
	if !policy.ReadAvailable {
		return fmt.Errorf("%s: source declares no read cell or read grant kind; remove this line with yolo config unset", path)
	}
	if cells, ok := asMap(value); ok {
		if policy.CallBroker == nil {
			return fmt.Errorf("%s: native read takes no table; use %s", path, policy.ReadCapability)
		}
		for _, name := range cells.Keys() {
			cell, found := callBrokerCell(policy.CallBroker, name)
			if !found || !cell.IsRead() {
				return fmt.Errorf("%s.%s: not a declared read cell; use yolo config unset", path, name)
			}
			if _, err := requestWord(readableValue(cells, name)); err != nil {
				return fmt.Errorf("%s.%s: %w", path, name, err)
			}
		}
		return nil
	}
	_, err := requestWord(value)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func readScopedBrokerPolicy(plan *ReadableAllowancePlan, input ReadableAllowanceInput, brokers map[string]ReadableBrokerPolicy, selected map[string]bool) (err error) {
	defer func() {
		if err != nil && termsafe.HasUnsafe(err.Error()) {
			err = fmt.Errorf("%s", termsafe.Visible(err.Error()))
		}
	}()
	if err := refuseLegacyBrokerPolicyLine("contexts", input.UserOrigins); err != nil {
		return err
	}
	if err := refuseLegacyBrokerPolicyLine("scopes", input.UserOrigins); err != nil {
		return err
	}
	if err := refuseLegacyBrokerPolicyLine("scopes", input.WorkspaceOrigins); err != nil {
		return err
	}
	contexts, err := readableTable(input.User, "contexts")
	if err != nil {
		return err
	}
	for _, name := range contexts.Keys() {
		path := "contexts." + name
		if name == "" {
			return fmt.Errorf("contexts: context name must be nonempty; rename it with yolo config set")
		}
		table, ok := asMap(readableValue(contexts, name))
		if !ok {
			return fmt.Errorf("%s: expected a table", path)
		}
		source, ok := readableValue(table, "source").(string)
		if !ok || !loopholedecl.ValidBrokerSourceName(source) {
			return fmt.Errorf("%s.source: expected a source name; use yolo config set", path)
		}
		policy, known := brokers[source]
		c := BrokerExecutionContext{Name: name, Source: source, Fields: map[string]string{}, Origins: map[string][]string{}, Selected: known && selected[policy.Pack]}
		if c.Selected && (policy.CallBroker == nil || policy.CallBroker.Context == nil) {
			return fmt.Errorf("%s: selected source %s declares no context block; remove the context with yolo config unset", path, source)
		}
		for _, field := range table.Keys() {
			if field == "source" {
				c.Origins[field] = cloneOrigins(input.UserOrigins[path+".source"])
				continue
			}
			if field == "" {
				return fmt.Errorf("%s: field names must be nonempty; rename the field with yolo config set", path)
			}
			value, ok := readableValue(table, field).(string)
			if !ok {
				return fmt.Errorf("%s.%s: expected a string", path, field)
			}
			if err := brokerpolicy.ValidateText(value); err != nil {
				return fmt.Errorf("%s.%s: %w", path, field, err)
			}
			if c.Selected {
				if _, has := policy.CallBroker.Context.Fields[field]; !has {
					return fmt.Errorf("%s.%s: undeclared context field; use yolo config unset", path, field)
				}
			}
			c.Fields[field] = value
			c.Origins[field] = cloneOrigins(input.UserOrigins[path+"."+field])
		}
		if c.Selected {
			for field := range policy.CallBroker.Context.Fields {
				if _, has := c.Fields[field]; !has {
					return fmt.Errorf("%s.%s: missing declared context field; use yolo config set", path, field)
				}
			}
		}
		plan.Contexts = append(plan.Contexts, c)
	}
	for _, layer := range []struct {
		name    string
		values  *jsonx.OrderedMap
		origins map[string][]string
	}{{"ceiling", input.User, input.UserOrigins}, {"ask", input.Workspace, input.WorkspaceOrigins}} {
		scopes, err := readableTable(layer.values, "scopes")
		if err != nil {
			return err
		}
		identities := map[string]string{}
		for _, alias := range scopes.Keys() {
			path := "scopes." + alias
			if !brokerAliasRE.MatchString(alias) {
				return fmt.Errorf("%s: alias must be a TOML bare key; rename it with yolo config set", path)
			}
			table, ok := asMap(readableValue(scopes, alias))
			if !ok {
				return fmt.Errorf("%s: expected a table", path)
			}
			source, ok := readableValue(table, "source").(string)
			if !ok || !loopholedecl.ValidBrokerSourceName(source) {
				return fmt.Errorf("%s.source: expected a broker source; use yolo config set", path)
			}
			resource, ok := readableValue(table, "resource").(string)
			if !ok {
				return fmt.Errorf("%s.resource: expected a string; use yolo config set", path)
			}
			identity := source + "\x00" + resource
			if old := identities[identity]; old != "" {
				return &scopedBrokerConflict{paths: []string{old, path}, message: "name the same source/resource; remove one alias with yolo config unset", origins: layer.origins}
			}
			identities[identity] = path
			policy, known := brokers[source]
			scope := ScopedBrokerScope{Alias: alias, Source: source, Resource: resource, Layer: layer.name, Selected: known && selected[policy.Pack], Reads: map[string]string{}, Tables: map[string]map[string]string{}, Origins: map[string][]string{}}
			for _, field := range table.Keys() {
				scope.Origins[field] = cloneOrigins(layer.origins[path+"."+field])
			}
			if known && policy.CallBroker == nil {
				return fmt.Errorf("%s: native source %s uses [repos.%s], not scopes; use yolo config set repos.%s.repo", path, source, alias, alias)
			}
			if scope.Selected {
				scope.Matcher, err = brokerpolicy.NewScopeMatcher(policy.CallBroker.Scope.Nesting, policy.CallBroker.Scope.Separator, policy.CallBroker.Scope.Anchors)
				if err != nil {
					return err
				}
				if err := scope.Matcher.ValidateScope(resource); err != nil {
					return fmt.Errorf("%s.resource: %w", path, err)
				}
			} else {
				plan.Diagnostics = append(plan.Diagnostics, path+" is inert: source is not selected; select its pack to validate its scope")
			}
			if context, has := table.Get("context"); has {
				if layer.name != "ceiling" {
					return fmt.Errorf("%s.context is user-scope only; move it to user config", path)
				}
				scope.Context, ok = context.(string)
				if !ok || scope.Context == "" {
					return fmt.Errorf("%s.context: expected a nonempty context name", path)
				}
				if scope.Selected {
					matched := false
					for _, c := range plan.Contexts {
						if c.Name == scope.Context && c.Source == source && c.Selected {
							matched = true
						}
					}
					if !matched {
						return fmt.Errorf("%s.context: missing matching declared context; use yolo config set contexts.%s.source %s --user", path, scope.Context, source)
					}
					metadata := policy.CallBroker.Context
					if metadata == nil {
						return fmt.Errorf("%s.context: source has no context declaration; remove route with yolo config unset", path)
					}
					if policy.CallBroker.Scope.Nesting == "segment" {
						routable := false
						for _, anchor := range metadata.RouteAnchors {
							if strings.HasPrefix(resource, anchor) {
								routable = true
							}
						}
						if !routable {
							return fmt.Errorf("%s.context: scope is outside declared route_anchors; remove route with yolo config unset", path)
						}
					}
				}
			}
			if raw, has := table.Get("allow"); has {
				if layer.name != "ceiling" {
					return fmt.Errorf("%s.allow is user-scope only; move to user config", path)
				}
				allow, ok := asMap(raw)
				if !ok {
					return fmt.Errorf("%s.allow: expected a table", path)
				}
				for _, field := range allow.Keys() {
					if field != "always" && field != "on_request" && field != "never" {
						return fmt.Errorf("%s.allow.%s: unknown list; use yolo config unset", path, field)
					}
					values, err := readableStringList(readableValue(allow, field), path+".allow."+field)
					if err != nil {
						return err
					}
					if scope.Selected {
						if _, err := validateScopedTokens(path+".allow."+field, values, policy); err != nil {
							return err
						}
					}
					switch field {
					case "always":
						scope.Always = values
					case "on_request":
						scope.OnRequest = values
					case "never":
						scope.Never = values
					}
					scope.Origins["allow."+field] = cloneOrigins(layer.origins[path+".allow."+field])
				}
				if scope.Selected {
					if err := validateScopedCeilingLists(path+".allow", allow, policy, layer.origins); err != nil {
						return err
					}
				}
			}
			if raw, has := table.Get("requests"); has {
				if layer.name != "ask" {
					return fmt.Errorf("%s.requests is workspace-only; move to project config", path)
				}
				requests, ok := asMap(raw)
				if !ok {
					return fmt.Errorf("%s.requests: expected a table", path)
				}
				for _, field := range requests.Keys() {
					value := readableValue(requests, field)
					if field == "read" {
						if scope.Selected {
							if err := validateScopedReadRequest(path+".requests.read", value, policy); err != nil {
								return err
							}
						}
						if cells, ok := asMap(value); ok {
							for _, cell := range cells.Keys() {
								word, err := requestWord(readableValue(cells, cell))
								if err != nil {
									return err
								}
								scope.Reads[cell] = word
								scope.Origins["requests.read."+cell] = cloneOrigins(layer.origins[path+".requests.read."+cell])
							}
						} else {
							word, err := requestWord(value)
							if err != nil {
								return err
							}
							scope.Reads[""] = word
							scope.Origins["requests.read"] = cloneOrigins(layer.origins[path+".requests.read"])
						}
						continue
					}
					if field != "all" && field != "working_set" {
						return fmt.Errorf("%s.requests.%s: unknown table; use yolo config unset", path, field)
					}
					cells, ok := asMap(value)
					if !ok {
						return fmt.Errorf("%s.requests.%s: expected a table", path, field)
					}
					scope.Tables[field] = map[string]string{}
					for _, name := range cells.Keys() {
						if scope.Selected {
							cell, found := callBrokerCell(policy.CallBroker, name)
							if !found || cell.IsRead() || !slices.Contains(cell.Tables, field) {
								return fmt.Errorf("%s.requests.%s.%s: not a declared write table/cell; use yolo config unset", path, field, name)
							}
						}
						word, err := requestWord(readableValue(cells, name))
						if err != nil {
							return fmt.Errorf("%s.requests.%s.%s: %w", path, field, name, err)
						}
						scope.Tables[field][name] = word
						scope.Origins["requests."+field+"."+name] = cloneOrigins(layer.origins[path+".requests."+field+"."+name])
					}
				}
			}
			plan.Scopes = append(plan.Scopes, scope)
		}
	}
	if err := validateScopedTableOrder(plan, input, brokers); err != nil {
		return err
	}
	reportScopedRequests(plan, input, brokers)
	return nil
}

func policyOrder(word string) int {
	switch word {
	case "never":
		return -1
	case "false":
		return 0
	case "absent", "":
		return 0
	case "ask", "on_request":
		return 1
	case "true", "always":
		return 2
	}
	return 0
}

func validateScopedTableOrder(plan *ReadableAllowancePlan, input ReadableAllowanceInput, brokers map[string]ReadableBrokerPolicy) error {
	for _, scope := range plan.Scopes {
		if !scope.Selected {
			continue
		}
		policy := brokers[scope.Source]
		for _, cell := range policy.CallBroker.Cells {
			if cell.IsRead() || !slices.Contains(cell.Tables, "all") || !slices.Contains(cell.Tables, "working_set") {
				continue
			}
			values := map[string]string{}
			for _, table := range []string{"all", "working_set"} {
				value := "absent"
				applying := orderedScopeMetadata(plan, scope.Source, scope.Layer, scope.Resource)
				for _, candidate := range applying {
					if scope.Layer == "ask" {
						if word, has := candidate.Tables[table][cell.Name]; has {
							value = word
							break
						}
					} else {
						allow := scopeAllowValue(candidate)
						word := ScopedBrokerCeilingValue(allow, table, cell.Name)
						if word != "absent" {
							value = word
							break
						}
					}
				}
				if value == "absent" {
					if scope.Layer == "ask" {
						requests, _ := readableTable(input.Workspace, "requests")
						own, _ := asMap(readableValue(requests, scope.Source))
						value, _ = callBrokerRequested(cell, own, table)
					} else {
						allows, _ := readableTable(input.User, "allow")
						own, _ := asMap(readableValue(allows, scope.Source))
						if own != nil {
							value = ScopedBrokerCeilingValue(own, table, cell.Name)
						}
					}
				}
				values[table] = value
			}
			order := policyOrder
			if scope.Layer == "ask" {
				order = func(word string) int {
					if word == "absent" || word == "" {
						return 1
					}
					return policyOrder(word)
				}
			}
			if order(values["working_set"]) < order(values["all"]) {
				return fmt.Errorf("scopes.%s: %s violates working_set >= all for %s, including inherited values; use yolo config set scopes.%s.%s.working_set", scope.Alias, scope.Layer, cell.Name, scope.Alias, map[bool]string{true: "requests", false: "allow"}[scope.Layer == "ask"])
			}
		}
	}
	return nil
}

func orderedScopeMetadata(plan *ReadableAllowancePlan, source, layer, resource string) []ScopedBrokerScope {
	var ids []string
	byResource := map[string]ScopedBrokerScope{}
	var matcher brokerpolicy.ScopeMatcher
	for _, scope := range plan.Scopes {
		if scope.Selected && scope.Source == source && scope.Layer == layer {
			ids = append(ids, scope.Resource)
			byResource[scope.Resource] = scope
			matcher = scope.Matcher
		}
	}
	var out []ScopedBrokerScope
	for _, match := range matcher.ApplyingScopes(ids, []string{resource}) {
		out = append(out, byResource[match.Scope])
	}
	return out
}

func scopedRequestCeiling(plan *ReadableAllowancePlan, input ReadableAllowanceInput, source, resource, table, cell string) string {
	candidates := orderedScopeMetadata(plan, source, "ceiling", resource)
	for _, scope := range candidates {
		word := ScopedBrokerCeilingValue(scopeAllowValue(scope), table, cell)
		if word != "absent" {
			return word
		}
	}
	allows, _ := readableTable(input.User, "allow")
	own, _ := asMap(readableValue(allows, source))
	if own == nil {
		return "absent"
	}
	return ScopedBrokerCeilingValue(own, table, cell)
}

func reportScopedRequests(plan *ReadableAllowancePlan, input ReadableAllowanceInput, brokers map[string]ReadableBrokerPolicy) {
	for _, scope := range plan.Scopes {
		if !scope.Selected || scope.Layer != "ask" {
			continue
		}
		appendRequest := func(path, capability, table, cell, word string) {
			if word != "true" {
				return
			}
			ceiling := scopedRequestCeiling(plan, input, scope.Source, scope.Resource, table, cell)
			status, reason := ReadableRequestPending, "scoped operation policy metadata; runtime consumer is not active"
			if ceiling == "absent" || ceiling == "never" {
				status, reason = ReadableRequestSkipped, outsideCeilingReason(scope.Source)
				plan.Diagnostics = append(plan.Diagnostics, path+" = true is outside its governing ceiling and counts as absent; use yolo config set or yolo allow on the host")
			}
			plan.ScopedRequests = append(plan.ScopedRequests, ReadableRequestResult{Path: path, Files: cloneOrigins(input.WorkspaceOrigins[path]), Capability: capability, Value: true, Status: status, Reason: reason, Source: scope.Source})
		}
		for _, table := range []string{"working_set", "all"} {
			for _, cell := range brokers[scope.Source].CallBroker.Cells {
				if word, has := scope.Tables[table][cell.Name]; has {
					appendRequest("scopes."+scope.Alias+".requests."+table+"."+cell.Name, table+"."+cell.Name, table, cell.Name, word)
				}
			}
		}
		readKeys := []string{""}
		for _, cell := range brokers[scope.Source].CallBroker.Cells {
			if cell.IsRead() {
				readKeys = append(readKeys, cell.Name)
			}
		}
		for _, cell := range readKeys {
			word, has := scope.Reads[cell]
			if !has {
				continue
			}
			if cell != "" {
				appendRequest("scopes."+scope.Alias+".requests.read."+cell, "read."+cell, "read", cell, word)
				continue
			}
			// The broad alias is permitted only if every read it names is permitted.
			policy := brokers[scope.Source]
			var reads []string
			for _, cell := range policy.CallBroker.Cells {
				if cell.IsRead() {
					reads = append(reads, cell.Name)
				}
			}
			if len(reads) == 0 {
				reads = []string{""}
			}
			for _, cell := range reads {
				capability := "read"
				if cell != "" {
					capability += "." + cell
				}
				appendRequest("scopes."+scope.Alias+".requests.read", capability, "read", cell, word)
			}
		}
	}
}

func scopeAllowValue(scope ScopedBrokerScope) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, field := range []struct {
		name   string
		values []string
	}{{"always", scope.Always}, {"on_request", scope.OnRequest}, {"never", scope.Never}} {
		var values []any
		for _, value := range field.values {
			values = append(values, value)
		}
		out.Set(field.name, values)
	}
	return out
}
