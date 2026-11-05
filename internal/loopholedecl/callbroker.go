package loopholedecl

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// CallBroker is the strictly decoded declaration of a call-broker loophole. It is metadata only:
// this slice validates and discloses it but does not start a broker or grant runtime authority.
type CallBroker struct {
	Source          string
	Front           string
	Program         string
	Classifier      []string
	Executor        string
	CredentialPaths []string
	EnvFromHost     []string
	Scope           CallBrokerScope
	Resources       []CallBrokerResource
	Cells           []CallBrokerCell
	Lookups         []CallBrokerLookup
	ReadOverPost    []string
	HTTP            *CallBrokerHTTP
	Joins           []CallBrokerJoin
	// ProgramIdentities are resolved by the host from this call broker's selected staged
	// pack tree; parsing a manifest never reads or executes a program.
	ProgramIdentities    map[string]CallBrokerProgramIdentity `json:"-"`
	ProgramIdentityError string                               `json:"-"`
	// ProgramIdentityPending marks only the pre-stage case where no selected staged root
	// exists yet. It is metadata for config validation, never launch authority.
	ProgramIdentityPending bool `json:"-"`
}

type CallBrokerHTTP struct {
	Origin         string
	Credential     CallBrokerHTTPCredential
	RequestHeaders []string
}

type CallBrokerHTTPCredential struct {
	Source, Key, Header, Prefix string
}

type CallBrokerProgramIdentity struct {
	Pack, Bin, Path, RelPath, SHA256 string
}

type CallBrokerScope struct {
	Roots        string
	RemoteHost   string
	RootTemplate string
}

type CallBrokerResource struct{ Type, Parent string }
type CallBrokerCell struct {
	Name    string
	Tables  []string
	Default map[string]bool
	MaxFor  string
}
type CallBrokerLookup struct {
	Name                                           string
	CLI                                            []string
	HTTPMethod, HTTPPath                           string
	HTTPResource, HTTPIDPointer, HTTPParentPointer string
}
type CallBrokerJoin struct {
	Cell                     string
	FollowSource, FollowFact string
}

var callRootRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)

func ValidCallBrokerRoot(s string) bool {
	if !callRootRE.MatchString(s) || strings.ContainsAny(s, "\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
var callNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

var callBrokerKeys = []string{"source", "front", "program", "classifier", "executor", "credential_paths", "env_from_host", "scope", "http", "resource", "cell", "lookup", "read_over_post", "joins_working_set"}
var callScopeKeys = []string{"roots", "remote_host", "root_template"}
var callResourceKeys = []string{"type", "parent"}
var callCellKeys = []string{"name", "tables", "default", "max_for"}
var callLookupKeys = []string{"name", "cli", "http_method", "http_path", "http_resource", "http_id_pointer", "http_parent_pointer"}
var callJoinKeys = []string{"cell", "follows"}
var callFollowKeys = []string{"source", "fact"}
var callCellTables = []string{"working_set", "all"}

func parseCallBroker(manifestPath string, raw any) (*CallBroker, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'call_broker' must be a mapping", manifestPath)
	}
	if err := rejectUnknownCallKeys(manifestPath, "call_broker", m, callBrokerKeys); err != nil {
		return nil, err
	}
	b := &CallBroker{}
	stringField := func(key string, required bool) (string, error) {
		v, present := m.Get(key)
		if !present && !required {
			return "", nil
		}
		s, ok := v.(string)
		if !ok || (required && s == "") {
			return "", Errorf("%s: 'call_broker.%s' must be a non-empty string", manifestPath, key)
		}
		if s != "" {
			if err := refuseControlChars(manifestPath, "'call_broker."+key+"'", s); err != nil {
				return "", err
			}
		}
		return s, nil
	}
	var err error
	if b.Source, err = stringField("source", true); err != nil {
		return nil, err
	}
	if !sourceRE.MatchString(b.Source) {
		return nil, Errorf("%s: 'call_broker.source' must be a lowercase source name", manifestPath)
	}
	if b.Front, err = stringField("front", true); err != nil {
		return nil, err
	}
	if b.Front != "cli" && b.Front != "http" {
		return nil, Errorf("%s: 'call_broker.front' must be 'cli' or 'http'", manifestPath)
	}
	if b.Program, err = stringField("program", b.Front == "cli"); err != nil {
		return nil, err
	}
	if b.Executor, err = stringField("executor", false); err != nil {
		return nil, err
	}
	if b.Executor == "" {
		b.Executor = "core"
	}
	if b.Executor != "core" && !programTokenRE.MatchString(b.Executor) {
		return nil, Errorf("%s: 'call_broker.executor' must be 'core' or a {program:<bin>} identity", manifestPath)
	}
	classifier, ok := stringSliceField(m, "classifier")
	if !ok || len(classifier) == 0 {
		return nil, Errorf("%s: 'call_broker.classifier' must be a non-empty argv list", manifestPath)
	}
	for i, word := range classifier {
		if err := refuseControlChars(manifestPath, "'call_broker.classifier'", word); err != nil {
			return nil, err
		}
		if i == 0 && word != "yolo" && !programTokenRE.MatchString(word) {
			return nil, Errorf("%s: 'call_broker.classifier[0]' must be yolo or a {program:<bin>} identity", manifestPath)
		}
	}
	b.Classifier = classifier
	for _, field := range []struct {
		name string
		dst  *[]string
	}{{"credential_paths", &b.CredentialPaths}, {"env_from_host", &b.EnvFromHost}, {"read_over_post", &b.ReadOverPost}} {
		if values, present := m.Get(field.name); present && values != nil {
			list, ok := values.([]any)
			if !ok || !AllStrings(list) {
				return nil, Errorf("%s: 'call_broker.%s' must be a list of strings", manifestPath, field.name)
			}
			*field.dst = StringSlice(list)
		}
	}
	for _, name := range b.EnvFromHost {
		if !envNameRE.MatchString(name) {
			return nil, Errorf("%s: 'call_broker.env_from_host' entry %q is not an environment variable name", manifestPath, name)
		}
	}
	if b.Front == "http" {
		if b.HTTP, err = parseCallBrokerHTTP(manifestPath, getOrNil(m, "http")); err != nil {
			return nil, err
		}
		if b.HTTP.Credential.Source == "host_env" && !inList(b.HTTP.Credential.Key, b.EnvFromHost) {
			return nil, Errorf("%s: call_broker.http.credential.key must be declared in env_from_host; add the exact name and approve the selected binding", manifestPath)
		}
		if b.HTTP.Credential.Source == "fenced_file" && !inList(b.HTTP.Credential.Key, b.CredentialPaths) {
			return nil, Errorf("%s: call_broker.http.credential.key must be an approved credential_paths entry; declare the exact absolute path", manifestPath)
		}
	} else if _, has := m.Get("http"); has {
		return nil, Errorf("%s: call_broker.http requires an HTTP front; remove the block or select front http", manifestPath)
	}
	if b.Scope, err = parseCallBrokerScope(manifestPath, getOrNil(m, "scope")); err != nil {
		return nil, err
	}
	if b.Resources, err = parseCallBrokerResources(manifestPath, getOrNil(m, "resource")); err != nil {
		return nil, err
	}
	if b.Cells, err = parseCallBrokerCells(manifestPath, getOrNil(m, "cell")); err != nil {
		return nil, err
	}
	cellNames := map[string]bool{}
	for _, cell := range b.Cells {
		cellNames[cell.Name] = true
	}
	if b.Lookups, err = parseCallBrokerLookups(manifestPath, getOrNil(m, "lookup"), b.Resources); err != nil {
		return nil, err
	}
	if b.Joins, err = parseCallBrokerJoins(manifestPath, getOrNil(m, "joins_working_set")); err != nil {
		return nil, err
	}
	for i, join := range b.Joins {
		if join.Cell != "" && !cellNames[join.Cell] {
			return nil, Errorf("%s: 'call_broker.joins_working_set[%d].cell' names undeclared cell %q", manifestPath, i, join.Cell)
		}
	}
	if len(b.ReadOverPost) > 0 && b.Front != "http" {
		return nil, Errorf("%s: 'call_broker.read_over_post' applies only to an HTTP front", manifestPath)
	}
	seenPostPaths := map[string]bool{}
	for i, route := range b.ReadOverPost {
		if !strings.HasPrefix(route, "/") || strings.ContainsAny(route, "?#\\\\") || strings.Contains(route, "..") || hasHTTPControls(route) || seenPostPaths[route] {
			return nil, Errorf("%s: 'call_broker.read_over_post[%d]' must be a literal absolute path", manifestPath, i)
		}
		seenPostPaths[route] = true
	}
	return b, nil
}

// CallBrokerDeclarationHash binds the complete selected non-runtime declaration.
func CallBrokerDeclarationHash(b *CallBroker) string {
	data, _ := json.Marshal(b)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

var httpDNSLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var httpHeaderToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func parseCallBrokerHTTP(path string, raw any) (*CallBrokerHTTP, error) {
	m, err := callObject(path, "http", raw, []string{"origin", "credential", "request_headers"})
	if err != nil {
		return nil, err
	}
	str := func(m *jsonx.OrderedMap, key string) (string, bool) {
		v, found := m.Get(key)
		s, ok := v.(string)
		return s, found && ok && s != ""
	}
	origin, ok := str(m, "origin")
	if !ok {
		return nil, Errorf("%s: call_broker.http.origin must be an HTTPS DNS origin", path)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || strings.ContainsAny(origin, "?#") || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Host == "" || u.Hostname() == "" || strings.ContainsAny(u.Host, "*%\\") {
		return nil, Errorf("%s: call_broker.http.origin must be an HTTPS DNS origin without path, userinfo, query or fragment", path)
	}
	name := u.Hostname()
	if len(name) > 253 || !strings.Contains(name, ".") || strings.HasSuffix(name, ".") || strings.HasPrefix(u.Host, "[") || net.ParseIP(name) != nil {
		return nil, Errorf("%s: call_broker.http.origin must name a DNS host", path)
	}
	for _, label := range strings.Split(name, ".") {
		if !httpDNSLabel.MatchString(label) {
			return nil, Errorf("%s: call_broker.http.origin must name a DNS host", path)
		}
	}
	if strings.Contains(u.Host, ":") {
		port := u.Port()
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || !strings.HasSuffix(u.Host, ":"+port) {
			return nil, Errorf("%s: call_broker.http.origin has an invalid port", path)
		}
	}
	cm, err := callObject(path, "http.credential", getOrNil(m, "credential"), []string{"source", "key", "header", "prefix"})
	if err != nil {
		return nil, err
	}
	credential := CallBrokerHTTPCredential{}
	if credential.Source, ok = str(cm, "source"); !ok || (credential.Source != "host_env" && credential.Source != "fenced_file") {
		return nil, Errorf("%s: call_broker.http.credential.source must be host_env or fenced_file", path)
	}
	if credential.Key, ok = str(cm, "key"); !ok {
		return nil, Errorf("%s: call_broker.http.credential.key must be a non-empty string", path)
	}
	if credential.Source == "host_env" && !envNameRE.MatchString(credential.Key) || credential.Source == "fenced_file" && (!filepath.IsAbs(credential.Key) || filepath.Clean(credential.Key) != credential.Key || hasHTTPControls(credential.Key)) {
		return nil, Errorf("%s: call_broker.http.credential.key must name an exact host variable or absolute credential path", path)
	}
	if credential.Header, ok = str(cm, "header"); !ok || !validCallHTTPHeader(credential.Header) {
		return nil, Errorf("%s: call_broker.http.credential.header is invalid", path)
	}
	if v, found := cm.Get("prefix"); found {
		credential.Prefix, ok = v.(string)
		if !ok || len(credential.Prefix) > 128 || hasHTTPControls(credential.Prefix) {
			return nil, Errorf("%s: call_broker.http.credential.prefix must be at most 128 bytes without controls", path)
		}
	}
	value, present := m.Get("request_headers")
	if !present {
		return nil, Errorf("%s: call_broker.http.request_headers must be a list of header names", path)
	}
	list, ok := value.([]any)
	if !ok || !AllStrings(list) {
		return nil, Errorf("%s: call_broker.http.request_headers must be a list of header names", path)
	}
	headers := StringSlice(list)
	seen := map[string]bool{}
	for _, header := range headers {
		folded := strings.ToLower(header)
		if !validCallHTTPHeader(header) || folded == "authorization" || folded == strings.ToLower(credential.Header) || seen[folded] {
			return nil, Errorf("%s: call_broker.http.request_headers contains invalid, duplicate or credential header %q", path, header)
		}
		seen[folded] = true
	}
	return &CallBrokerHTTP{Origin: origin, Credential: credential, RequestHeaders: headers}, nil
}

func hasHTTPControls(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validCallHTTPHeader(s string) bool {
	if !httpHeaderToken.MatchString(s) {
		return false
	}
	folded := strings.ToLower(s)
	if strings.HasPrefix(folded, "proxy-") || strings.HasPrefix(folded, "yolo-") {
		return false
	}
	switch folded {
	case "host", "connection", "cookie", "set-cookie", "keep-alive", "te", "trailer", "transfer-encoding", "upgrade":
		return false
	}
	return true
}

var programTokenRE = regexp.MustCompile(`^\{program:[a-z][a-z0-9-]*\}$`)

func rejectUnknownCallKeys(path, object string, m *jsonx.OrderedMap, known []string) error {
	for _, k := range m.Keys() {
		if !inList(k, known) {
			return Errorf("%s: unknown key %q in '%s' (known: %s)", path, object+"."+k, object, strings.Join(sortedCopy(known), ", "))
		}
	}
	return nil
}
func stringSliceField(m *jsonx.OrderedMap, key string) ([]string, bool) {
	v, ok := m.Get(key)
	if !ok || v == nil {
		return nil, ok
	}
	list, ok := v.([]any)
	if !ok || !AllStrings(list) {
		return nil, false
	}
	return StringSlice(list), true
}
func callObject(path, name string, raw any, keys []string) (*jsonx.OrderedMap, error) {
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'call_broker.%s' must be a mapping", path, name)
	}
	if err := rejectUnknownCallKeys(path, "call_broker."+name, m, keys); err != nil {
		return nil, err
	}
	return m, nil
}
func parseCallBrokerScope(path string, raw any) (CallBrokerScope, error) {
	m, err := callObject(path, "scope", raw, callScopeKeys)
	if err != nil {
		return CallBrokerScope{}, err
	}
	roots, ok := getOrNil(m, "roots").(string)
	if !ok || (roots != "remotes" && roots != "declared") {
		return CallBrokerScope{}, Errorf("%s: 'call_broker.scope.roots' must be 'remotes' or 'declared'", path)
	}
	s := CallBrokerScope{Roots: roots}
	s.RemoteHost, _ = getOrNil(m, "remote_host").(string)
	s.RootTemplate, _ = getOrNil(m, "root_template").(string)
	if roots == "remotes" {
		if !remoteHostRE.MatchString(s.RemoteHost) {
			return s, Errorf("%s: 'call_broker.scope.remote_host' must be a lowercase hostname", path)
		}
		if !strings.Contains(s.RootTemplate, "{owner}") || !strings.Contains(s.RootTemplate, "{repo}") {
			return s, Errorf("%s: 'call_broker.scope.root_template' must contain {owner} and {repo}", path)
		}
		template := strings.ReplaceAll(strings.ReplaceAll(s.RootTemplate, "{owner}", "owner"), "{repo}", "repo")
		if strings.ContainsAny(template, "{}") || !ValidCallBrokerRoot(template) {
			return s, Errorf("%s: 'call_broker.scope.root_template' must be a safe root template using only {owner} and {repo}", path)
		}
	} else if s.RemoteHost != "" || s.RootTemplate != "" {
		return s, Errorf("%s: declared roots do not take remote_host or root_template", path)
	}
	return s, nil
}
func parseCallBrokerResources(path string, raw any) ([]CallBrokerResource, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, Errorf("%s: 'call_broker.resource' must be a non-empty list", path)
	}
	var out []CallBrokerResource
	seen := map[string]bool{}
	for i, v := range list {
		m, e := callObject(path, fmt.Sprintf("resource[%d]", i), v, callResourceKeys)
		if e != nil {
			return nil, e
		}
		typ, _ := getOrNil(m, "type").(string)
		parent := ""
		if rawParent, present := m.Get("parent"); present {
			var ok bool
			parent, ok = rawParent.(string)
			if !ok {
				return nil, Errorf("%s: 'call_broker.resource[%d].parent' must be a string", path, i)
			}
		}
		if !validCallName(typ) || seen[typ] {
			return nil, Errorf("%s: 'call_broker.resource[%d].type' must be a unique lowercase resource name", path, i)
		}
		seen[typ] = true
		out = append(out, CallBrokerResource{typ, parent})
	}
	for i, r := range out {
		if r.Parent != "" && !seen[r.Parent] {
			return nil, Errorf("%s: 'call_broker.resource[%d].parent' names undeclared resource %q", path, i, r.Parent)
		}
	}
	return out, nil
}
func parseCallBrokerCells(path string, raw any) ([]CallBrokerCell, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, Errorf("%s: 'call_broker.cell' must be a non-empty list", path)
	}
	var out []CallBrokerCell
	seen := map[string]bool{}
	for i, v := range list {
		m, e := callObject(path, fmt.Sprintf("cell[%d]", i), v, callCellKeys)
		if e != nil {
			return nil, e
		}
		name, _ := getOrNil(m, "name").(string)
		if !validCallName(name) || name == "read" || seen[name] {
			return nil, Errorf("%s: 'call_broker.cell[%d].name' must be unique and must not be 'read'", path, i)
		}
		seen[name] = true
		tables, ok := stringSliceField(m, "tables")
		if !ok || len(tables) == 0 {
			return nil, Errorf("%s: 'call_broker.cell[%d].tables' must list working_set and/or all", path, i)
		}
		tableSet := map[string]bool{}
		for _, t := range tables {
			if (t != "working_set" && t != "all") || tableSet[t] {
				return nil, Errorf("%s: 'call_broker.cell[%d].tables' has unknown or duplicate table %q", path, i, t)
			}
			tableSet[t] = true
		}
		defaults := map[string]bool{}
		if dv := getOrNil(m, "default"); dv != nil {
			dm, ok := dv.(*jsonx.OrderedMap)
			if !ok {
				return nil, Errorf("%s: 'call_broker.cell[%d].default' must be a mapping", path, i)
			}
			if e := rejectUnknownCallKeys(path, fmt.Sprintf("call_broker.cell[%d].default", i), dm, callCellTables); e != nil {
				return nil, e
			}
			for _, k := range dm.Keys() {
				v, _ := dm.Get(k)
				b, ok := v.(bool)
				if !ok || !tableSet[k] {
					return nil, Errorf("%s: 'call_broker.cell[%d].default.%s' must be a boolean for a declared table", path, i, k)
				}
				defaults[k] = b
			}
		}
		if defaults["all"] && !defaults["working_set"] {
			return nil, Errorf("%s: 'call_broker.cell[%d].default' violates working_set >= all", path, i)
		}
		maxFor := ""
		if rawMaxFor, present := m.Get("max_for"); present {
			var ok bool
			maxFor, ok = rawMaxFor.(string)
			if !ok {
				return nil, Errorf("%s: 'call_broker.cell[%d].max_for' must be a duration string such as 8h", path, i)
			}
		}
		if maxFor != "" && !validDuration(maxFor) {
			return nil, Errorf("%s: 'call_broker.cell[%d].max_for' must be a duration such as 8h", path, i)
		}
		out = append(out, CallBrokerCell{Name: name, Tables: tables, Default: defaults, MaxFor: maxFor})
	}
	return out, nil
}
func parseCallBrokerLookups(path string, raw any, resources []CallBrokerResource) ([]CallBrokerLookup, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'call_broker.lookup' must be a list", path)
	}
	var out []CallBrokerLookup
	seen := map[string]bool{}
	seenResource := map[string]bool{}
	for i, v := range list {
		m, e := callObject(path, fmt.Sprintf("lookup[%d]", i), v, callLookupKeys)
		if e != nil {
			return nil, e
		}
		name, _ := getOrNil(m, "name").(string)
		if !validCallName(name) || seen[name] {
			return nil, Errorf("%s: 'call_broker.lookup[%d].name' must be unique", path, i)
		}
		seen[name] = true
		cli, _ := stringSliceField(m, "cli")
		method, _ := getOrNil(m, "http_method").(string)
		route, _ := getOrNil(m, "http_path").(string)
		if len(cli) == 0 && (method == "" || route == "") {
			return nil, Errorf("%s: 'call_broker.lookup[%d]' needs cli argv or an HTTP method and path template", path, i)
		}
		resourceRaw, hasResource := m.Get("http_resource")
		idRaw, hasID := m.Get("http_id_pointer")
		parentRaw, hasParent := m.Get("http_parent_pointer")
		resource, resourceOK := resourceRaw.(string)
		idPointer, idOK := idRaw.(string)
		parentPointer, parentOK := parentRaw.(string)
		if hasResource || hasID || hasParent {
			parent, found := "", false
			for _, declared := range resources {
				if declared.Type == resource {
					parent, found = declared.Parent, true
				}
			}
			if !hasResource || !resourceOK || !hasID || !idOK || !found || seenResource[resource] || method != "GET" || !strings.HasPrefix(route, "/") || strings.Count(route, "{id}") != 1 || strings.ContainsAny(route, "?#\\") || strings.Contains(route, "..") || !validCallJSONPointer(idPointer) || parent != "" && (!hasParent || !parentOK || !validCallJSONPointer(parentPointer)) || parent == "" && hasParent {
				return nil, Errorf("%s: 'call_broker.lookup[%d]' needs a unique GET native-ID template with typed resource and valid ID/parent JSON Pointers", path, i)
			}
			seenResource[resource] = true
		}
		out = append(out, CallBrokerLookup{Name: name, CLI: cli, HTTPMethod: method, HTTPPath: route, HTTPResource: resource, HTTPIDPointer: idPointer, HTTPParentPointer: parentPointer})
	}
	return out, nil
}
func validCallJSONPointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i >= len(pointer) || pointer[i] != '0' && pointer[i] != '1' {
				return false
			}
		}
	}
	return true
}

func parseCallBrokerJoins(path string, raw any) ([]CallBrokerJoin, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'call_broker.joins_working_set' must be a list", path)
	}
	var out []CallBrokerJoin
	for i, v := range list {
		m, e := callObject(path, fmt.Sprintf("joins_working_set[%d]", i), v, callJoinKeys)
		if e != nil {
			return nil, e
		}
		var j CallBrokerJoin
		j.Cell, _ = getOrNil(m, "cell").(string)
		if rawFollow := getOrNil(m, "follows"); rawFollow != nil {
			fm, e := callObject(path, fmt.Sprintf("joins_working_set[%d].follows", i), rawFollow, callFollowKeys)
			if e != nil {
				return nil, e
			}
			j.FollowSource, _ = getOrNil(fm, "source").(string)
			j.FollowFact, _ = getOrNil(fm, "fact").(string)
		}
		if (j.Cell == "") == (j.FollowSource == "") {
			return nil, Errorf("%s: 'call_broker.joins_working_set[%d]' must name exactly one cell or follows source", path, i)
		}
		if j.Cell != "" && !validCallName(j.Cell) {
			return nil, Errorf("%s: invalid joins_working_set cell %q", path, j.Cell)
		}
		if j.FollowSource != "" && (!sourceRE.MatchString(j.FollowSource) || j.FollowFact == "") {
			return nil, Errorf("%s: follows needs a valid source and fact", path)
		}
		out = append(out, j)
	}
	return out, nil
}
func validCallName(s string) bool { return callNameRE.MatchString(s) }
func validDuration(s string) bool { return regexp.MustCompile(`^[1-9][0-9]*(s|m|h)$`).MatchString(s) }
