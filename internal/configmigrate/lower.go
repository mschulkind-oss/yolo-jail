// Package configmigrate plans and applies `yolo config migrate`: the one-time conversion of the
// old JSONC config files into the TOML files that replace them
// (docs/design/config-migration.md §4).
//
// A migration proves it lost nothing or writes nothing. Plan converts every file of the old
// graph, reads the files it would write through the same readers a launch uses, and compares
// the result with what those readers make of the old files as canonical JSON
// (config.SnapshotJSON). Any difference is printed key by key and nothing is written. Apply
// writes a plan that proved equal, keeps each original as <name>.pre-migrate, restores every
// file on failure, and respells the records only yolo reads.
package configmigrate

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Scope is which authored config a file belongs to.
type Scope uint8

const (
	// ScopeUser is the user config and everything it includes.
	ScopeUser Scope = iota + 1
	// ScopeProject is a workspace's project and local files and everything they include.
	ScopeProject
)

func (s Scope) String() string {
	if s == ScopeUser {
		return "user"
	}
	return "project"
}

// NotConverted is one old key this yolo has no lossless TOML spelling for. The file conversion
// leaves it out, and the old side of the equality check keeps it, so a plan holding one is
// unequal and writes nothing.
type NotConverted struct {
	// File is the old file that wrote it, empty for a key found only in the merged read.
	File string
	// Path is the old key, dotted, with list elements as [i].
	Path string
	// Reason says why, and what to do instead.
	Reason string
}

// convertLaterKey is the old user file's "stop offering" key (§3 step 1). It has nothing to say
// in a TOML file, so the conversion drops it and the comparison ignores it.
const convertLaterKey = "convert_later"

// loweringContext is what the lowering of one scope needs beyond the values.
type loweringContext struct {
	scope Scope
	// remotes are the workspace's remote repositories as host/owner/repo, lowercased, each once.
	remotes []string
	// keepUnconverted keeps every old key the lowering cannot convert at its old spelling (the
	// equality check's old side); otherwise it is left out and reported (a file's conversion).
	keepUnconverted bool
	// merged marks the lowering of the merged user-and-project read, which holds the user-only
	// vocabulary too and converts it the way a user file does.
	merged bool
	// user is the merged old user config the user-scope conversions rest on (RC-D32).
	user *userView
	// inlineEnv converts inline variable entries that follow every file in env_sources to
	// env.set and env.unset. The plan turns it off when the proof fails with it on (Plan).
	inlineEnv bool
}

// lowered is one lowering's result: the new-vocabulary values, the old path each new path came
// from (for comment placement), and what it could not convert.
type lowered struct {
	values  *jsonx.OrderedMap
	origins map[string][]string
	skipped []NotConverted
}

// renameReplacement is the first replacement the rename table gives an old path: the one table
// the conversion, the TOML refusal and the next conversion share (config-migration.md §2 item 2).
func renameReplacement(oldPath string) ([]string, bool) {
	for _, d := range config.RenameDescriptors() {
		if d.OldPath == oldPath && len(d.Replacements) > 0 {
			return strings.Split(d.Replacements[0], "."), true
		}
	}
	return nil, false
}

// renameDescriptorFor is the rename table's row for a concrete old path, matching `*` segments.
func renameDescriptorFor(path []string) (config.RenameDescriptor, bool) {
	var best config.RenameDescriptor
	found := false
	for _, d := range config.RenameDescriptors() {
		if patternMatches(strings.Split(d.OldPath, "."), path) {
			if !found || len(d.OldPath) > len(best.OldPath) {
				best, found = d, true
			}
		}
	}
	return best, found
}

func patternMatches(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i := range pattern {
		if pattern[i] != "*" && pattern[i] != path[i] {
			return false
		}
	}
	return true
}

// lower converts one old-vocabulary map into the new vocabulary. It is used twice: on each old
// file's own values to make its TOML, and on what the readers made of the whole old graph to make
// the equality check's old side. Keys the readers already lower (packages, gpu, network.ports …)
// arrive at their new spelling on the second use and pass through.
func lower(old *jsonx.OrderedMap, ctx loweringContext, file string) lowered {
	out := lowered{values: jsonx.NewOrderedMap(), origins: map[string][]string{}}
	skip := func(path []string, value any, reason string) {
		out.skipped = append(out.skipped, NotConverted{File: file, Path: dotted(path), Reason: reason})
		if ctx.keepUnconverted {
			put(out.values, path, value)
		}
	}
	place := func(newPath []string, value any, oldPath []string) bool {
		if _, present := get(out.values, newPath); present {
			return false
		}
		put(out.values, newPath, value)
		out.origins[strings.Join(newPath, "\x00")] = oldPath
		return true
	}
	var profiles any
	hasProfiles := false
	for _, key := range old.Keys() {
		value, _ := old.Get(key)
		path := []string{key}
		switch key {
		case convertLaterKey:
			continue
		case "loopholes":
			if ctx.userScoped() {
				lowerLoopholes(value, ctx, place, skip)
				continue
			}
		case "profile":
			if ctx.userScoped() {
				lowerProfileSelection(value, ctx, place, skip)
				continue
			}
		case "profiles":
			if ctx.userScoped() {
				profiles, hasProfiles = value, true
				continue
			}
		case "packages", "mise_tools", "gpu", "resources":
			target, ok := renameReplacement(key)
			if !ok || !place(target, value, path) {
				skip(path, value, "its new place, "+dotted(target)+", is already written in this file; keep one of the two")
			}
			continue
		case "network":
			lowerNetwork(value, ctx, place, skip)
			continue
		case "env_sources":
			lowerEnvSources(value, ctx.inlineEnv, place, skip)
			continue
		case "brokered":
			lowerBrokered(value, ctx, place, skip)
			continue
		}
		if d, ok := renameDescriptorFor(path); ok && !(d.Transform == config.RenameKey && len(d.Replacements) == 1 && d.Replacements[0] == d.OldPath) {
			skip(path, value, unconvertedReason(d))
			continue
		}
		if key == "providers" {
			var renamed map[string][]string
			value, renamed = convertModelRows(value, path)
			for newPath, oldPath := range renamed {
				out.origins[newPath] = oldPath
			}
		}
		kept, dropped := filterRenamedLeaves(value, path)
		// Place what converts first: a dropped leaf kept at its old spelling is written beside
		// it, and writing it first would leave the table's other keys out of the check.
		if kept != nil {
			place(path, kept, path)
		}
		for _, nc := range dropped {
			skip(nc.path, nc.value, nc.reason)
		}
	}
	if hasProfiles {
		lowerProfiles(profiles, ctx, &out, skip)
	}
	// The schema is the last word: a key with no readable spelling is not written.
	if !ctx.keepUnconverted {
		for _, nc := range unknownToReadableSchema(out.values, ctx.scope) {
			out.skipped = append(out.skipped, NotConverted{File: file, Path: nc, Reason: "it has no spelling in the TOML config; remove it from the old file, or keep the old file for now"})
			deletePath(out.values, strings.Split(nc, "."))
		}
	}
	for _, nc := range nullPaths(out.values, nil) {
		if !ctx.keepUnconverted {
			out.skipped = append(out.skipped, NotConverted{File: file, Path: nc, Reason: "a null has no TOML spelling; remove the key, or keep the old file for now"})
			deletePath(out.values, strings.Split(nc, "."))
		}
	}
	return out
}

func unconvertedReason(d config.RenameDescriptor) string {
	if d.TOMLRefusal != "" {
		return d.TOMLRefusal
	}
	if len(d.Replacements) > 0 {
		return "this yolo has no lossless conversion to " + strings.Join(d.Replacements, ", ") + " yet; rewrite it by hand in the new spelling, or keep the old file for now"
	}
	return "this yolo has no lossless conversion for it yet; keep the old file for now"
}

type droppedLeaf struct {
	path   []string
	value  any
	reason string
}

// filterRenamedLeaves copies value, leaving out every nested key the rename table names (for
// example providers.*.base_url). Such a key needs a conversion this slice does not perform.
func filterRenamedLeaves(value any, path []string) (any, []droppedLeaf) {
	m, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return value, nil
	}
	out := jsonx.NewOrderedMap()
	var dropped []droppedLeaf
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		child := append(append([]string(nil), path...), k)
		// A readable model row is already in the new spelling (convertModelRows), where
		// `max_tokens` and `reasoning` keep the legacy row's key names.
		if len(child) == 4 && child[0] == "providers" && child[2] == "models" {
			if _, readable := isReadableRow(v); readable {
				out.Set(k, v)
				continue
			}
		}
		if d, ok := renameDescriptorFor(child); ok && !(d.Transform == config.RenameKey && len(d.Replacements) == 1 && d.Replacements[0] == d.OldPath) {
			dropped = append(dropped, droppedLeaf{path: child, value: v, reason: unconvertedReason(d)})
			continue
		}
		kept, more := filterRenamedLeaves(v, child)
		dropped = append(dropped, more...)
		out.Set(k, kept)
	}
	return out, dropped
}

func lowerNetwork(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	network, ok := value.(*jsonx.OrderedMap)
	if !ok {
		skip([]string{"network"}, value, "network must be an object")
		return
	}
	rest := jsonx.NewOrderedMap()
	for _, k := range network.Keys() {
		v, _ := network.Get(k)
		oldPath := []string{"network", k}
		switch k {
		case "ports", "forward_host_ports":
			target, ok := renameReplacement("network." + k)
			if !ok {
				skip(oldPath, v, "the rename table has no row for it")
				continue
			}
			list, isList := v.([]any)
			var converted []any
			if isList {
				if k == "ports" {
					converted, ok = publishPorts(list)
				} else {
					converted, ok = forwardPorts(list)
				}
			}
			if !isList || !ok {
				skip(oldPath, v, "an entry bound to an address or a protocol has no "+dotted(target)+" spelling; keep the old file for now")
				continue
			}
			if !place(target, converted, oldPath) {
				skip(oldPath, v, dotted(target)+" is already written in this file; keep one of the two")
			}
		default:
			rest.Set(k, v)
		}
	}
	if rest.Len() > 0 {
		place([]string{"network"}, rest, []string{"network"})
	}
}

func publishPorts(entries []any) ([]any, bool) {
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		text, ok := entry.(string)
		if !ok || strings.Contains(text, "/") {
			return nil, false
		}
		parts := strings.Split(text, ":")
		if len(parts) != 2 {
			return nil, false
		}
		host, hostOK := portNumber(parts[0])
		jail, jailOK := portNumber(parts[1])
		if !hostOK || !jailOK {
			return nil, false
		}
		port := jsonx.NewOrderedMap()
		port.Set("host", int64(host))
		port.Set("jail", int64(jail))
		out = append(out, port)
	}
	return out, true
}

func forwardPorts(entries []any) ([]any, bool) {
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		var local, host int
		if text, ok := entry.(string); ok {
			parsed, err := config.ParsePortForwards([]any{text}, nil)
			if err != nil || len(parsed) != 1 {
				return nil, false
			}
			local, host = parsed[0].LocalPort, parsed[0].HostPort
		} else if n, ok := jsonx.AsInt(entry); ok {
			local, host = int(n), int(n)
		} else {
			return nil, false
		}
		if local < 1 || local > 65535 || host < 1 || host > 65535 {
			return nil, false
		}
		port := jsonx.NewOrderedMap()
		port.Set("jail", int64(local))
		port.Set("host", int64(host))
		out = append(out, port)
	}
	return out, true
}

func portNumber(text string) (int, bool) {
	n, err := strconv.Atoi(text)
	return n, err == nil && n >= 1 && n <= 65535
}

// lowerEnvSources converts an env_sources list of dotenv paths to env.files. An inline object
// entry sets variables at its place in the list. env.set and env.unset apply after every file, so
// inline entries convert only when they follow every file in the list (and inlineEnv allows it):
// merged in order, a string value becomes env.set and a null one env.unset. An inline entry
// before a file is not converted.
func lowerEnvSources(value any, inlineEnv bool, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	oldPath := []string{"env_sources"}
	list, ok := value.([]any)
	if !ok {
		skip(oldPath, value, "env_sources must be a list")
		return
	}
	const placed = "an inline variable entry takes effect at its place in the list, which env.set cannot say; move those variables into a dotenv file listed here, or keep the old file for now"
	var files []any
	set, unset := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
	for _, entry := range list {
		if path, isString := entry.(string); isString {
			if set.Len()+unset.Len() > 0 {
				skip(oldPath, value, placed)
				return
			}
			files = append(files, path)
			continue
		}
		vars, isMap := entry.(*jsonx.OrderedMap)
		if !isMap || !inlineEnv {
			skip(oldPath, value, placed)
			return
		}
		for _, name := range vars.Keys() {
			v, _ := vars.Get(name)
			switch v := v.(type) {
			case string:
				unset.Delete(name)
				set.Delete(name)
				set.Set(name, v)
			case nil:
				set.Delete(name)
				unset.Set(name, nil)
			default:
				skip(oldPath, value, "an inline variable's value must be a string or null")
				return
			}
		}
	}
	target, ok := renameReplacement("env_sources")
	if !ok {
		skip(oldPath, value, "env_sources has no new spelling in this yolo")
		return
	}
	env := target[:len(target)-1]
	names := make([]any, 0, unset.Len())
	for _, name := range unset.Keys() {
		names = append(names, name)
	}
	parts := []struct {
		key   string
		value any
		write bool
	}{
		{target[len(target)-1], files, len(files) > 0},
		{"set", set, set.Len() > 0},
		{"unset", names, len(names) > 0},
	}
	for _, part := range parts {
		if !part.write {
			continue
		}
		if !place(append(append([]string(nil), env...), part.key), part.value, oldPath) {
			skip(oldPath, value, "env."+part.key+" is already written in this file; keep one of the two")
			return
		}
	}
}

// lowerBrokered converts a project's brokered repository list and its branch and tag grants to
// requests. A request names refs on every remote of the workspace, so a grant converts only when
// the repository it names is the workspace's one remote repository; any other grant would widen
// or narrow what may be pushed (readable-config-plan.md §7) and is left at its old spelling.
func lowerBrokered(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	sources, ok := value.(*jsonx.OrderedMap)
	if !ok {
		skip([]string{"brokered"}, value, "brokered must be an object")
		return
	}
	if ctx.scope != ScopeProject {
		skip([]string{"brokered"}, value, "a user-scope grant becomes an allow.<source> capability named by the pack that declares the broker, which this conversion does not read; write the [allow.<source>] table by hand (`yolo check` names each capability), or keep the old file for now")
		return
	}
	for _, source := range sources.Keys() {
		sv, _ := sources.Get(source)
		table, ok := sv.(*jsonx.OrderedMap)
		if !ok {
			skip([]string{"brokered", source}, sv, "a broker's entry must be an object")
			continue
		}
		for _, key := range table.Keys() {
			v, _ := table.Get(key)
			oldPath := []string{"brokered", source, key}
			switch key {
			case "repos":
				if _, ok := renameReplacement("brokered.*.repos"); !ok {
					skip(oldPath, v, "the rename table has no row for it")
					continue
				}
				if !place([]string{"requests", source, "repos"}, v, oldPath) {
					skip(oldPath, v, "requests."+source+".repos is already written in this file")
				}
			case "grants":
				lowerGrants(source, v, ctx, place, skip)
			default:
				d, _ := renameDescriptorFor(oldPath)
				skip(oldPath, v, unconvertedReason(d))
			}
		}
	}
}

func lowerGrants(source string, value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	list, ok := value.([]any)
	base := []string{"brokered", source, "grants"}
	if !ok {
		skip(base, value, "grants must be a list")
		return
	}
	var leftover []any
	var reasons []string
	refs := map[string][]any{}
	var order []string
	for i, entry := range list {
		at := dotted(base) + "[" + strconv.Itoa(i) + "]"
		reason := grantRequest(source, entry, ctx, func(key, ref string) {
			if _, seen := refs[key]; !seen {
				order = append(order, key)
			}
			refs[key] = append(refs[key], ref)
		})
		if reason != "" {
			leftover = append(leftover, entry)
			reasons = append(reasons, at+": "+reason)
		}
	}
	for _, key := range order {
		if !place(strings.Split(key, "."), refs[key], base) {
			reasons = append(reasons, dotted(base)+": "+key+" is already written in this file")
		}
	}
	if len(leftover) > 0 {
		skip(base, leftover, strings.Join(reasons, "; "))
	}
}

// grantRequest converts one grant through add, or says why it cannot.
func grantRequest(source string, entry any, ctx loweringContext, add func(key, ref string)) string {
	m, ok := entry.(*jsonx.OrderedMap)
	if !ok {
		return "a grant must be an object"
	}
	kind, _ := m.Get("kind")
	repo, _ := m.Get("repo")
	kindText, _ := kind.(string)
	repoText, _ := repo.(string)
	field := ""
	switch kindText {
	case brokerscope.KindBranch:
		field = "branch"
	case brokerscope.KindTag:
		field = "tag"
	}
	target, hasRow := renameReplacement("brokered.*.grants.*." + field)
	ref, _ := m.Get(field)
	refText, _ := ref.(string)
	switch {
	case field == "" || !hasRow:
		d, _ := renameDescriptorFor([]string{"brokered", source, "grants", "*", "kind"})
		return "a " + strconv.Quote(kindText) + " grant has no request spelling in this conversion (it becomes " + strings.Join(d.Replacements, " or ") + "); keep the old file for now"
	case len(target) < 2 || source != target[1]:
		return "only the " + target[1] + " broker takes " + dotted(target) + "; keep the old file for now"
	case m.Len() != 3 || refText == "":
		return "a grant with keys beyond kind, repo and " + field + " has no request spelling; keep the old file for now"
	case !soleRemote(ctx.remotes, repoText):
		return grantScopeReason(ctx.remotes, repoText)
	}
	add(dotted(target), refText)
	return ""
}

func soleRemote(remotes []string, repo string) bool {
	return len(remotes) == 1 && repo != brokerscope.AnyRepo && strings.EqualFold(remotes[0], repo)
}

func grantScopeReason(remotes []string, repo string) string {
	switch {
	case repo == brokerscope.AnyRepo:
		return "a grant on every repository (\"*\") would apply to remotes added later, which a request cannot say; keep the old file for now"
	case len(remotes) == 0:
		return "this workspace has no git remote, so a request could not be shown to name " + repo + "; add the remote, or keep the old file for now"
	case len(remotes) > 1:
		return "a request names refs on every remote of this workspace (" + strings.Join(remotes, ", ") + "), and this grant names only " + repo + "; keep the old file for now"
	default:
		return "the workspace's remote is " + remotes[0] + ", not " + repo + "; keep the old file for now"
	}
}

// workspaceRemotes are the workspace's remote repositories as host/owner/repo, lowercased, each
// once, read as the gate reads them (brokerscope.ReadAllRemotes: the git config as text).
func workspaceRemotes(workspace string) []string {
	if workspace == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range brokerscope.ReadAllRemotes(workspace).Remotes {
		repo := remoteRepo(r.Repo)
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		out = append(out, repo)
	}
	sort.Strings(out)
	return out
}

func remoteRepo(raw string) string {
	host := ""
	switch {
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		host = u.Hostname()
	case !strings.Contains(raw, "://"):
		hostPart, _, ok := strings.Cut(raw, ":")
		if !ok {
			return ""
		}
		if _, after, found := strings.Cut(hostPart, "@"); found {
			hostPart = after
		}
		host = hostPart
	}
	if host == "" {
		return ""
	}
	repo := brokerscope.RepoFromRemoteURL(raw, host)
	if repo == "" {
		return ""
	}
	return strings.ToLower(host + "/" + repo)
}

// unknownToReadableSchema lists every path in values the readable schema does not name.
func unknownToReadableSchema(values *jsonx.OrderedMap, scope Scope) []string {
	keys := config.ReadableSchemaKeys()
	var out []string
	var walk func(v any, path []string)
	walk = func(v any, path []string) {
		if len(path) > 0 {
			if !schemaNames(keys, path) {
				out = append(out, dotted(path))
				return
			}
			if !schemaHasChildren(keys, path) {
				return
			}
		}
		switch t := v.(type) {
		case *jsonx.OrderedMap:
			for _, k := range t.Keys() {
				child, _ := t.Get(k)
				walk(child, append(append([]string(nil), path...), k))
			}
		case []any:
			for _, item := range t {
				if _, isMap := item.(*jsonx.OrderedMap); isMap {
					walk(item, append(append([]string(nil), path...), "*"))
				}
			}
		}
	}
	walk(values, nil)
	_ = scope
	return dedupe(out)
}

func schemaNames(keys []config.SchemaKey, path []string) bool {
	for _, k := range keys {
		if patternMatches(strings.Split(k.Path, "."), path) {
			return true
		}
	}
	return false
}

func schemaHasChildren(keys []config.SchemaKey, path []string) bool {
	for _, k := range keys {
		parts := strings.Split(k.Path, ".")
		if len(parts) > len(path) && patternMatches(parts[:len(path)], path) {
			return true
		}
	}
	return false
}

func nullPaths(v any, path []string) []string {
	var out []string
	switch t := v.(type) {
	case nil:
		return []string{dotted(path)}
	case *jsonx.OrderedMap:
		for _, k := range t.Keys() {
			child, _ := t.Get(k)
			out = append(out, nullPaths(child, append(append([]string(nil), path...), k))...)
		}
	case []any:
		for _, item := range t {
			if found := nullPaths(item, nil); len(found) > 0 {
				return []string{dotted(path)}
			}
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func dotted(path []string) string { return strings.Join(path, ".") }

func get(m *jsonx.OrderedMap, path []string) (any, bool) {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(*jsonx.OrderedMap)
		if !ok {
			return nil, false
		}
		cur, ok = mm.Get(k)
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func put(m *jsonx.OrderedMap, path []string, value any) {
	cur := m
	for _, k := range path[:len(path)-1] {
		next, ok := cur.Get(k)
		child, isMap := next.(*jsonx.OrderedMap)
		if !ok || !isMap {
			child = jsonx.NewOrderedMap()
			cur.Set(k, child)
		}
		cur = child
	}
	cur.Set(path[len(path)-1], value)
}

func deletePath(m *jsonx.OrderedMap, path []string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		m.Delete(path[0])
		return
	}
	next, ok := m.Get(path[0])
	child, isMap := next.(*jsonx.OrderedMap)
	if !ok || !isMap {
		return
	}
	deletePath(child, path[1:])
}

// canonicalOld is the equality check's old side for one scope: what the readers made of the old
// files, lowered, with every key the lowering cannot convert kept at its old spelling.
func canonicalOld(read *jsonx.OrderedMap, ctx loweringContext) (*jsonx.OrderedMap, []NotConverted) {
	ctx.keepUnconverted = true
	l := lower(jsonx.DeepCopyMap(read), ctx, "")
	return l.values, l.skipped
}

func describe(v any) string {
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return s
}
