package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const callBrokerScopeRecordSuffix = ".call-broker-declaration"

// ApprovedCallBrokerHTTP is nonsecret binding metadata captured with the selected gate.
// It is not a credential or a grant.
type ApprovedCallBrokerHTTP struct {
	Pack, Source, DeclarationHash string
	HTTP                          loopholedecl.CallBrokerHTTP
	ReadOverPost                  []string
}

// SelectedCallBrokerScope is one call-broker declaration from a selected staged pack tree.
type SelectedCallBrokerScope struct {
	Source, Label, Pack string
	Declaration         *loopholedecl.CallBroker
}

// WithCallBrokerDeclarations adds selected declaration disclosures and the corresponding declared
// roots to the same persisted config-change gate as broker scopes. The caller copies HTTP
// binding metadata only after the gate succeeds; this method grants no runtime authority.
func WithCallBrokerDeclarations(check *ScopeCheck, workspace string, selected []SelectedCallBrokerScope, read *WorkspaceRead) *ScopeCheck {
	if len(selected) == 0 {
		return check
	}
	if check == nil {
		check = &ScopeCheck{}
	}
	for _, selected := range selected {
		b := selected.Declaration
		if b == nil {
			continue
		}
		s := ScopeSource{Source: selected.Source, Label: selected.Label, Pack: selected.Pack, RemoteHost: b.Scope.RemoteHost,
			Form: brokerscope.FormCallRoot, Disclosure: CallBrokerDisclosure(b), ScopeKind: "call-broker root",
			EntryKey: brokeredRootsPath(selected.Source)}
		if b.HTTP != nil {
			s.Disclosure = append(s.Disclosure, "http selected pack="+selected.Pack, "http declaration_sha256="+loopholedecl.CallBrokerDeclarationHash(b))
			http := *b.HTTP
			http.RequestHeaders = slices.Clone(b.HTTP.RequestHeaders)
			s.HTTPBinding = &ApprovedCallBrokerHTTP{Pack: selected.Pack, Source: b.Source,
				DeclarationHash: loopholedecl.CallBrokerDeclarationHash(b), HTTP: http, ReadOverPost: slices.Clone(b.ReadOverPost)}
		}
		if b.Scope.Roots == "declared" {
			if read != nil {
				s.Entry = read.CallRoots(selected.Source)
			}
		} else {
			s.Read = brokerscope.ReadRemotes(workspace, b.Scope.RemoteHost)
			for i, remote := range s.Read.Remotes {
				parts := strings.SplitN(remote.Repo, "/", 2)
				if len(parts) != 2 {
					continue
				}
				root := strings.ReplaceAll(strings.ReplaceAll(b.Scope.RootTemplate, "{owner}", parts[0]), "{repo}", parts[1])
				if loopholedecl.ValidCallBrokerRoot(root) {
					s.Read.Remotes[i].Repo = root
				}
			}
		}
		check.Sources = append(check.Sources, s)
	}
	return check
}

// CallBrokerDisclosure is the exact selected call-broker policy shown at the config-change gate.
// The same HTTP fields are frozen by the passing gate; no credential value is displayed.
func CallBrokerDisclosure(b *loopholedecl.CallBroker) []string {
	if b == nil {
		return nil
	}
	out := []string{"call broker: " + b.Source + " front=" + b.Front + " program=" + b.Program + " executor=" + b.Executor + " scope roots=" + b.Scope.Roots}
	if b.Scope.RemoteHost != "" {
		out = append(out, "remote_host="+b.Scope.RemoteHost+" root_template="+b.Scope.RootTemplate)
	}
	if len(b.Classifier) > 0 {
		out = append(out, "classifier="+strings.Join(b.Classifier, " "))
	}
	programNames := make([]string, 0, len(b.ProgramIdentities))
	for name := range b.ProgramIdentities {
		programNames = append(programNames, name)
	}
	sort.Strings(programNames)
	for _, name := range programNames {
		identity := b.ProgramIdentities[name]
		out = append(out, "host program="+identity.Pack+"/"+identity.Bin+" sha256="+identity.SHA256)
	}
	for _, path := range b.CredentialPaths {
		out = append(out, "credential_path="+path)
	}
	for _, env := range b.EnvFromHost {
		out = append(out, "env_from_host="+env)
	}
	for _, r := range b.Resources {
		out = append(out, "resource "+r.Type+" parent="+r.Parent)
	}
	for _, c := range b.Cells {
		parts := []string{}
		for _, table := range c.Tables {
			if value, ok := c.Default[table]; ok {
				parts = append(parts, table+"="+fmt.Sprint(value))
			} else {
				parts = append(parts, table+"=ask")
			}
		}
		out = append(out, "cell "+c.Name+" tables="+strings.Join(c.Tables, ",")+" default="+strings.Join(parts, ",")+" max_for="+c.MaxFor)
	}
	if b.HTTP != nil {
		out = append(out, "http origin="+b.HTTP.Origin+" credential_source="+b.HTTP.Credential.Source+
			" credential_key="+b.HTTP.Credential.Key+" credential_header="+b.HTTP.Credential.Header+
			" credential_prefix="+b.HTTP.Credential.Prefix)
		for _, h := range b.HTTP.RequestHeaders {
			out = append(out, "http request_header="+h)
		}
	}
	for _, p := range b.ReadOverPost {
		out = append(out, "read_over_post="+p+" (method-floor exception)")
	}
	for _, j := range b.Joins {
		if j.Cell != "" {
			out = append(out, "joins working set: cell="+j.Cell)
		} else {
			out = append(out, "joins working set: follows="+j.FollowSource+" fact="+j.FollowFact)
		}
	}
	for _, lookup := range b.Lookups {
		out = append(out, "lookup="+lookup.Name+" cli="+strings.Join(lookup.CLI, " ")+" http="+lookup.HTTPMethod+" "+lookup.HTTPPath)
	}
	return out
}

func callBrokerScopeRecordKey(source string) string { return source + callBrokerScopeRecordSuffix }

func callBrokerInfoBySource(resolver LoopholeResolver) (map[string]LoopholeInfo, []string) {
	bySource := map[string]LoopholeInfo{}
	if resolver == nil {
		return bySource, nil
	}
	known, ok := resolver.Known()
	if !ok {
		return bySource, nil
	}
	var problems []string
	legacySources := map[string]string{}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	allSources := map[string]string{}
	for _, name := range names {
		info := known[name]
		if info.Brokered != nil {
			legacySources[info.Brokered.Source] = name
			allSources[info.Brokered.Source] = name
		}
		if info.CallBroker != nil {
			allSources[info.CallBroker.Source] = name
		}
	}
	for _, name := range names {
		info := known[name]
		if info.CallBroker == nil {
			continue
		}
		source := info.CallBroker.Source
		if previous := legacySources[source]; previous != "" {
			problems = append(problems, fmt.Sprintf("selected call broker %s declares source %q already owned by legacy broker %s; use a unique source name", name, source, previous))
		}
		if previous, exists := bySource[source]; exists && previous.Name != name {
			problems = append(problems, fmt.Sprintf("selected call brokers %s and %s declare duplicate source %q; give each source a unique name", previous.Name, name, source))
			continue
		}
		bySource[source] = info
		if info.CallBroker.ProgramIdentityError != "" && !info.CallBroker.ProgramIdentityPending {
			problems = append(problems, fmt.Sprintf("selected call broker %s program identity: %s", name, info.CallBroker.ProgramIdentityError))
		}
		for _, join := range info.CallBroker.Joins {
			if join.FollowSource != "" && allSources[join.FollowSource] == "" {
				problems = append(problems, fmt.Sprintf("call broker %s follows source %q, but no selected pack declares it; select that source's pack or remove the follows rule", name, join.FollowSource))
			}
		}
	}
	return bySource, problems
}

func validateCallBrokerConfig(workspace string, ws *jsonx.OrderedMap, resolver LoopholeResolver, errs, warns *[]string, edit *editValidationContext) {
	bySource, duplicateProblems := callBrokerInfoBySource(resolver)
	for _, problem := range duplicateProblems {
		add(errs, problem)
	}
	bv, _ := ws.Get(brokeredKey)
	sources, _ := asMap(bv)
	if sources != nil {
		for _, source := range sources.Keys() {
			sv, _ := sources.Get(source)
			src, ok := asMap(sv)
			if !ok {
				continue
			}
			roots, has := src.Get(brokeredRootsKey)
			if !has || roots == nil {
				continue
			}
			info, selected := bySource[source]
			if !selected {
				add(errs, "config."+brokeredKey+"."+source+"."+brokeredRootsKey+": no selected pack declares this call-broker source; select its pack or remove this entry")
				continue
			}
			if info.CallBroker.Scope.Roots != "declared" {
				add(errs, "config."+brokeredKey+"."+source+"."+brokeredRootsKey+": this call broker reads roots from git remotes; remove the entry and add the repository as a remote")
			}
		}
	}
	user := UserScopeConfigOrEmpty()
	if edit != nil && edit.rawUser != nil {
		user = edit.rawUser
	}
	if user == nil {
		user = jsonx.NewOrderedMap()
	}
	for source, info := range bySource {
		validateCallBrokerCells(source, info.CallBroker, user, ws, errs, warns)
	}
	_ = workspace
}

// validateCallBrokerCells applies the declared defaults and the same user ceiling + workspace
// request pair used by the built-in broker. Missing ceiling is false. Requests outside the ceiling
// are named as skipped; they never become authority through pack metadata alone.
func validateCallBrokerCells(source string, b *loopholedecl.CallBroker, user, ws *jsonx.OrderedMap, errs, warns *[]string) {
	if b == nil {
		return
	}
	known := map[string]loopholedecl.CallBrokerCell{}
	for _, c := range b.Cells {
		known[c.Name] = c
	}
	var requestSource *jsonx.OrderedMap
	rv, _ := ws.Get("requests")
	rm, _ := asMap(rv)
	if rm != nil {
		v, _ := rm.Get(source)
		requestSource, _ = asMap(v)
	}
	if requestSource != nil {
		for _, table := range requestSource.Keys() {
			if table != "working_set" && table != "all" {
				add(errs, "config.requests."+source+"."+table+": not a declared call-broker table; use working_set or all")
				continue
			}
			v, _ := requestSource.Get(table)
			tm, ok := asMap(v)
			if !ok {
				add(errs, "config.requests."+source+"."+table+": expected a table of declared call-broker cells")
				continue
			}
			for _, name := range tm.Keys() {
				cell, exists := known[name]
				if !exists {
					add(errs, "config.requests."+source+"."+table+"."+name+": unknown call-broker cell; use a declared cell or remove this key")
					continue
				}
				if !inList(cell.Tables, table) {
					add(errs, "config.requests."+source+"."+table+"."+name+": table/cell pair is not declared by the call-broker cell; use a declared table or remove this key")
				}
			}
		}
	}
	allow, _ := user.Get("allow")
	am, _ := asMap(allow)
	var sourceAllow *jsonx.OrderedMap
	if am != nil {
		sourceValue, _ := am.Get(source)
		sourceAllow, _ = asMap(sourceValue)
	}
	allowed := func(table, cell string) bool {
		return callBrokerCeiling(sourceAllow, table, cell)
	}
	if requestSource == nil {
		requestSource = jsonx.NewOrderedMap()
	}
	for _, cell := range b.Cells {
		values := map[string]string{}
		for _, table := range []string{"working_set", "all"} {
			value, valid := callBrokerRequested(cell, requestSource, table)
			values[table] = value
			if !valid {
				add(errs, "config.requests."+source+"."+table+"."+cell.Name+": expected true, false, or \"ask\"")
			}
		}
		order := func(value string) int {
			switch value {
			case "false":
				return 0
			case "true":
				return 2
			default: // an absent line is an ask for the invariant
				return 1
			}
		}
		if inList(cell.Tables, "working_set") && inList(cell.Tables, "all") && order(values["all"]) > order(values["working_set"]) {
			add(errs, "config.requests."+source+": working_set."+cell.Name+" is less permissive than all."+cell.Name+"; keep working_set at least as permissive as all, or narrow all")
		}
		for _, table := range []string{"working_set", "all"} {
			if values[table] == "true" && !allowed(table, cell.Name) && !(table == "working_set" && allowed("all", cell.Name)) {
				add(warns, "config.requests."+source+"."+table+"."+cell.Name+": call-broker default/request is outside the user ceiling and will not run; allow it on the host with `yolo allow "+source+" on_request "+table+"."+cell.Name+"`, or remove the request")
			}
		}
	}
}
