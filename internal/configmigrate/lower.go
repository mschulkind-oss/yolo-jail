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
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	// drop records an old key the lowering converts to nothing (lowered.inert); lower sets it.
	drop func(path []string, value any, why string)
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
	// twinOf writes a legacy `wire_id` twin as `twin_of` its sibling's key (MF-D3). The plan
	// turns it off when the provider table differs with it on (Plan).
	twinOf bool
	// twinsOnly, when not nil, limits twinOf to these dotted row paths (convertModelRows).
	twinsOnly map[string]bool
	// repos is the workspace's named-repository state a project's brokered repos and grants convert
	// against (reposbinding.go); nil outside a workspace's conversion.
	repos *legacyRepos
}

// lowered is one lowering's result: the new-vocabulary values, the old path each new path came
// from (for comment placement), what it could not convert, and the rows written as `twin_of`.
type lowered struct {
	values  *jsonx.OrderedMap
	origins map[string][]string
	skipped []NotConverted
	// inert are the old keys converted to nothing because they say nothing a launch reads.
	inert []DroppedInert
	twins []Twin
	// variants are the (provider, variant) pairs lowerProfiles wrote, in order (RC-D62).
	variants [][2]string
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
	// The lowering places the old file's own tables and then edits them (a null respelled or
	// removed), so it works on a copy: the old file's values are read again after it.
	old = jsonx.DeepCopyMap(old)
	out := lowered{values: jsonx.NewOrderedMap(), origins: map[string][]string{}}
	skip := func(path []string, value any, reason string) {
		out.skipped = append(out.skipped, NotConverted{File: file, Path: dotted(path), Reason: reason})
		if ctx.keepUnconverted {
			put(out.values, path, value)
		}
	}
	ctx.drop = func(path []string, value any, why string) {
		out.inert = append(out.inert, DroppedInert{Path: dotted(path), Value: describe(value), Why: why})
	}
	place := func(newPath []string, value any, oldPath []string) bool {
		if prior, present := get(out.values, newPath); present {
			merged, ok := unionNamedRequest(newPath, prior, value)
			if !ok {
				return false
			}
			put(out.values, newPath, merged)
			return true
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
			if value = lowerProjectLoopholes(value, ctx, place, skip); value == nil {
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
		case "packs":
			if ctx.userScoped() {
				lowerPacks(value, ctx, place, skip, func(path []string, reason string) {
					out.skipped = append(out.skipped, NotConverted{File: file, Path: dotted(path), Reason: reason})
				})
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
			lowerEnvSources(value, place, skip)
			continue
		case "env":
			// What the readers made of a legacy env_sources null (a null on both lists) is
			// the reset TOML spells as an empty env.files.
			value = envResetSpelling(value)
		case "brokered":
			lowerBrokered(value, ctx, place, skip)
			continue
		case "runtime", "confinement", "macos_user_mode":
			lowerRemovedSetupKey(key, value, ctx, file, place, skip)
			continue
		case "requests":
			// A project's own requests table merges leaf by leaf with what its loophole blocks
			// placed under requests.<pack> (a request-scope setting, LC-D17), so neither hides the
			// other; a leaf written twice is reported, never dropped in silence.
			if tbl, ok := value.(*jsonx.OrderedMap); ok && !ctx.userScoped() {
				lowerProjectRequests(tbl, place, skip)
				continue
			}
		case "repos":
			if tbl, ok := value.(*jsonx.OrderedMap); ok {
				placeNamedRepos(tbl, out.values, place, skip)
				continue
			}
		}
		if d, ok := renameDescriptorFor(path); ok && !(d.Transform == config.RenameKey && len(d.Replacements) == 1 && d.Replacements[0] == d.OldPath) {
			skip(path, value, unconvertedReason(d))
			continue
		}
		var renamed map[string][]string
		switch key {
		case "providers":
			var twins []Twin
			value, renamed, twins = convertModelRows(value, path, ctx.twinOf, ctx.twinsOnly)
			out.twins = append(out.twins, twins...)
		case "model_facts":
			value, renamed = lowerModelFacts(value)
		}
		for newPath, oldPath := range renamed {
			out.origins[newPath] = oldPath
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
	// A ceiling only a switch file gives (§6's switch row) has no user file writing the
	// loophole; the top-level file carries it.
	if gv := ctx.view().grants; ctx.scope == ScopeUser && !ctx.merged && gv != nil && (file == "" || file == gv.topFile) {
		for _, name := range sortedDeclNames(gv) {
			d := gv.decls[name]
			if c := gv.ceiling[d.Pack]; c != nil && c.fromSwitchOnly {
				placeLoopholeCeiling(gv, d, place, []string{"loopholes", name})
			}
		}
	}
	// A TOML user config read as the old side: the ceiling lines its legacy workspace switches
	// admitted, which the conversion writes into it (tomlCeilingNeeded, LG-D16).
	if gv := ctx.view().grants; ctx.scope == ScopeUser && file == "" && gv != nil && len(gv.tomlCeiling) > 0 {
		addCeilingKinds(out.values, gv.tomlCeiling)
	}
	if hasProfiles {
		lowerProfiles(profiles, ctx, &out, skip)
	}
	// The schema is the last word: a key with no readable spelling is not written.
	if !ctx.keepUnconverted {
		for _, nc := range unknownToReadableSchema(out.values, ctx.scope) {
			out.skipped = append(out.skipped, NotConverted{File: file, Path: nc, Reason: "it has no spelling in the TOML config; " + noSpellingYet})
			deletePath(out.values, strings.Split(nc, "."))
		}
	}
	// A null converts to its TOML removal spelling where there is one, the lowering the
	// inherited delivery uses too (config.LowerLegacyNulls, RC-D50).
	config.LowerLegacyNulls(out.values)
	for _, nc := range nullPaths(out.values, nil) {
		if !ctx.keepUnconverted {
			out.skipped = append(out.skipped, NotConverted{File: file, Path: nc, Reason: "a null has no TOML spelling; " + noSpellingYet})
			deletePath(out.values, strings.Split(nc, "."))
		}
	}
	return out
}

// lowerRemovedSetupKey converts a key named setups replace (named-setups.md §10; SUP-D15). In a
// user file a value naming exactly one setup becomes `setup` (`runtime` "podman" and "container"),
// and `confinement = "jail"`, the default an unset `setup` selects, is dropped as inert. Every
// other value names no one setup (the Seatbelt values, whose account lived in another key; `host`,
// which is `yolo host` now), and a project file's never converts, since a project cannot choose
// where its agent runs: each is reported with its refusal, which names the verbs that finish it.
func lowerRemovedSetupKey(key string, value any, ctx loweringContext, file string, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	path := []string{key}
	// The merged read converts the user config's value alone: a project's value it holds instead
	// is the project's, which never converts.
	fromUser := ctx.scope == ScopeUser
	if ctx.merged {
		uv, ok := ctx.view().removedSetupKeys[key]
		fromUser = ok && jsonx.MustDumpsCompact(uv) == jsonx.MustDumpsCompact(value)
	}
	if !fromUser {
		// A workspace's local file is edited with --local, never --project.
		flag := "--project"
		switch filepath.Base(file) {
		case paths.WorkspaceLocalConfigTOMLName, paths.WorkspaceLocalConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONName:
			flag = "--local"
		}
		skip(path, value, config.RemovedSetupKeyMessage(key, value, flag))
		return
	}
	if name := config.SetupForRemovedKey(key, value); name != "" {
		if v := ctx.view(); v != nil && v.setupFile != "" {
			// Another user file already writes `setup`, which decides (Graph.UserSetupFile).
			skip(path, value, setupWrittenReason(key, v.setupFile))
			return
		}
		if !place([]string{"setup"}, name, path) {
			skip(path, value, "`setup` is already written in this file, and it decides; remove the old line:\n    yolo config unset "+key+" --user")
		}
		return
	}
	if config.RemovedConfinementIsDefault(key, value) {
		ctx.drop(path, value, "it named the default, which an unset `setup` selects")
		return
	}
	skip(path, value, config.RemovedSetupKeyMessage(key, value, "--user"))
}

func unconvertedReason(d config.RenameDescriptor) string {
	if d.TOMLRefusal != "" {
		return d.TOMLRefusal
	}
	if len(d.Replacements) > 0 {
		return "this yolo has no lossless conversion to " + strings.Join(d.Replacements, ", ") + " yet; rewrite it by hand in the new spelling, or leave it: this yolo keeps reading the old file"
	}
	return "this yolo has no lossless conversion for it yet; leave it: this yolo keeps reading the old file"
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
					converted, ok = config.LowerLegacyPublishPorts(list)
				} else {
					converted, ok = forwardPorts(list)
				}
			}
			if !isList || !ok {
				skip(oldPath, v, "an entry the old reader refuses has no "+dotted(target)+" spelling; fix it in the old file (`yolo check` names it), then run `yolo config migrate` again")
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

// lowerEnvSources converts an env_sources list verbatim to env.sources through
// config.LowerEnvSources, the one lowering every read of a legacy file uses too (RC-D50): the same
// list in the same order, an inline table as { set, unset }. Never split into env.files, env.set
// or env.unset: env.set and env.unset apply after every layer's list, so moving a variable there
// would move it past a later file (a user [u.env, {A}] under a project [w.env] applies u.env, A,
// w.env), and the walk keeps a layer's sources ahead of its files.
func lowerEnvSources(value any, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	oldPath := []string{"env_sources"}
	target, ok := renameReplacement("env_sources")
	if !ok {
		skip(oldPath, value, "env_sources has no new spelling in this yolo")
		return
	}
	if value == nil {
		// A legacy null replaces the lists the layers beneath wrote; TOML's spelling of that
		// reset is an empty env.sources (config.lowerLegacyEnvSources reads the null the same way).
		if !place(target, []any{}, oldPath) {
			skip(oldPath, value, dotted(target)+" is already written in this file; keep one of the two")
		}
		return
	}
	list, ok := value.([]any)
	if !ok {
		skip(oldPath, value, "env_sources must be a list")
		return
	}
	lowered, err := config.LowerEnvSources(list)
	if err != nil {
		skip(oldPath, value, err.Error())
		return
	}
	if len(lowered) == 0 {
		return
	}
	if !place(target, lowered, oldPath) {
		skip(oldPath, value, dotted(target)+" is already written in this file; keep one of the two")
	}
}

// lowerBrokered converts a project's brokered repository list and its branch and tag grants to
// requests. A request names refs on every remote of the workspace, so a grant converts only when
// the repository it names is the workspace's one remote repository; any other grant would widen
// or narrow what may be pushed (readable-config-plan.md §7) and is left at its old spelling. A
// user file's brokered table becomes allow.<source> instead (lowerUserBrokered).
func lowerBrokered(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	sources, ok := value.(*jsonx.OrderedMap)
	if !ok {
		skip([]string{"brokered"}, value, "brokered must be an object")
		return
	}
	if ctx.scope != ScopeProject {
		lowerUserBrokered(sources, ctx, place, skip)
		return
	}
	// A named table's repo line is written once in the file, whichever source or list declares it.
	declared := map[string]string{}
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
				lowerBrokeredRepos(source, v, ctx, oldPath, declared, place, skip)
			case "grants":
				lowerGrants(source, v, ctx, declared, place, skip)
			default:
				d, _ := renameDescriptorFor(oldPath)
				skip(oldPath, v, unconvertedReason(d))
			}
		}
	}
}

// lowerBrokeredRepos converts a project's brokered.<source>.repos list to the named tables a TOML
// file's requests.<source>.repos list becomes (reposbinding.go), each with a copy of the lines the
// merged workspace writes unqualified. An entry that stays keeps its old spelling with its reason.
func lowerBrokeredRepos(source string, value any, ctx loweringContext, oldPath []string, declared map[string]string, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	entries, ok := value.([]any)
	if !ok {
		skip(oldPath, value, "repos must be a list")
		return
	}
	if ctx.repos == nil {
		skip(oldPath, value, "the workspace's repositories are not known here; "+noSpellingYet)
		return
	}
	tables, kept, notConverted := ctx.repos.convert(source, entries, "", legacyJSONCList)
	var reasons []string
	for _, n := range notConverted {
		reasons = append(reasons, n.Reason)
	}
	for _, t := range tables {
		if t.repo != "" {
			if reason := declareRepoTable(t.name, t.repo, declared, oldPath, place); reason != "" {
				reasons = append(reasons, reason)
			}
		}
		for _, l := range t.lines {
			at := []string{"repos", t.name, "requests", source, l.kind}
			if !place(at, l.value, oldPath) {
				reasons = append(reasons, dotted(at)+" is already written in this file; keep one of the two")
			}
		}
	}
	if len(kept) > 0 || len(reasons) > 0 {
		// A kept list is written back as a list even when empty: a null is no spelling of it.
		if kept == nil {
			kept = []any{}
		}
		skip(oldPath, kept, strings.Join(reasons, "; "))
	}
}

// declareRepoTable writes the repo line of the named table name, unless this file declared it
// already for repo; it says why when another value holds the line.
func declareRepoTable(name, repo string, declared map[string]string, oldPath []string, place func([]string, any, []string) bool) string {
	if declared[name] == repo {
		return ""
	}
	at := []string{"repos", name, "repo"}
	if !place(at, repo, oldPath) {
		return dotted(at) + " is already written in this file; keep one of the two"
	}
	declared[name] = repo
	return ""
}

func lowerGrants(source string, value any, ctx loweringContext, declared map[string]string, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	list, ok := value.([]any)
	base := []string{"brokered", source, "grants"}
	if !ok {
		skip(base, value, "grants must be a list")
		return
	}
	declaration, declReason := projectBrokerDeclaration(source)
	var leftover []any
	var reasons []string
	values := map[string]any{}
	var order []string
	wanted := map[string]string{}
	var wantedOrder []string
	for i, entry := range list {
		at := dotted(base) + "[" + strconv.Itoa(i) + "]"
		reason := declReason
		if reason == "" {
			reason = grantRequest(entry, declaration, ctx, grantSink{
				add: func(key, ref string) {
					prior, seen := values[key]
					if !seen {
						order = append(order, key)
					}
					if ref == "" {
						// A yes/no kind: one grant or several say the same `= true`.
						values[key] = true
						return
					}
					refs, _ := prior.([]any)
					values[key] = append(refs, ref)
				},
				declare: func(name, repo string) {
					if _, seen := wanted[name]; !seen {
						wanted[name] = repo
						wantedOrder = append(wantedOrder, name)
					}
				},
			})
		}
		if reason != "" {
			leftover = append(leftover, entry)
			reasons = append(reasons, at+": "+reason)
		}
	}
	for _, name := range wantedOrder {
		if reason := declareRepoTable(name, wanted[name], declared, base, place); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	for _, key := range order {
		if !place(strings.Split(key, "."), values[key], base) {
			reasons = append(reasons, dotted(base)+": "+key+" is already written in this file; keep one of the two")
		}
	}
	if len(leftover) > 0 {
		skip(base, leftover, strings.Join(reasons, "; "))
	}
}

// projectBrokerDeclaration is the shipped declaration of the broker named source, whose
// kind_capabilities a project's grants convert through (RC-D60), or why there is none. The user
// side converts through the same declarations (lowerUserBrokered).
func projectBrokerDeclaration(source string) (config.ReadableBrokerDeclaration, string) {
	declarations, err := shippedBrokers()
	if err != nil {
		return config.ReadableBrokerDeclaration{}, "the shipped broker declarations could not be read (" + err.Error() + "); " + noSpellingYet
	}
	var found []config.ReadableBrokerDeclaration
	for _, d := range declarations {
		if d.Brokered.Source == source {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 1:
		return found[0], ""
	case 0:
		return config.ReadableBrokerDeclaration{}, "no shipped pack declares a broker named " + source + ", so its [requests." + source + "] capabilities are not known here; " + noSpellingYet
	default:
		return config.ReadableBrokerDeclaration{}, "more than one shipped pack declares the " + source + " broker; " + noSpellingYet
	}
}

// noSpellingYet is the step for a setting this yolo cannot convert yet (RC-D61): nothing is the
// user's to change, and leaving it costs nothing, since this yolo reads the old file.
const noSpellingYet = "this yolo has no TOML spelling for it yet, so leave it: this yolo keeps reading the old file"

// grantSink receives what a grant converts to: add one requests line (key is its dotted path, ref
// its name, "" for a yes/no kind), and declare the named table a grant on another repository needs.
type grantSink struct {
	add     func(key, ref string)
	declare func(name, repo string)
}

// grantRequest converts one grant into sink, or says why it cannot. The kind converts through
// the declaring pack's kind_capabilities (RC-D60): a kind that names something (a branch, a
// tag, a workflow, an actor) adds its name to the list requests.<source>.<capability>, and a
// yes/no kind (create-branch, create-pr, create-issue) adds "", which is `= true`.
func grantRequest(entry any, declaration config.ReadableBrokerDeclaration, ctx loweringContext, sink grantSink) string {
	m, ok := entry.(*jsonx.OrderedMap)
	if !ok {
		return "a grant must be an object"
	}
	kind, _ := m.Get("kind")
	repo, hasRepo := m.Get("repo")
	kindText, _ := kind.(string)
	repoText, _ := repo.(string)
	pending := map[string]bool{}
	for _, c := range declaration.PendingCapabilities {
		pending[c] = true
	}
	capability := declaration.KindCapabilities[brokerscope.CanonicalGrantKind(kindText)]
	switch {
	case capability == "":
		var maps []string
		for _, k := range declaration.Brokered.Grants {
			if c := declaration.KindCapabilities[k]; c != "" && !pending[c] {
				maps = append(maps, k+" → "+c)
			}
		}
		return describe(kindText) + " is not a grant kind the " + declaration.Pack + " pack maps to a capability (it maps " + strings.Join(maps, ", ") + "); " + noSpellingYet
	case pending[capability]:
		return describe(kindText) + " maps to " + capability + ", which the " + declaration.Pack + " pack has not built yet; " + noSpellingYet
	}
	field := brokerscope.GrantNameKey(brokerscope.CanonicalGrantKind(kindText))
	want, keys := 2, "kind and repo"
	refText := ""
	if field != "" {
		want, keys = 3, "kind, repo and "+field
		ref, _ := m.Get(field)
		refText, _ = ref.(string)
		if refText == "" {
			return "a " + strconv.Quote(kindText) + " grant names a " + field + ", and this one names none; name it in the old file, then run `yolo config migrate` again"
		}
	}
	if m.Len() != want || !hasRepo {
		return "a " + strconv.Quote(kindText) + " grant with keys other than " + keys + " has no request spelling; " + noSpellingYet
	}
	if declaration.Brokered.RemoteHost == brokerscope.AnyRemoteHost {
		if !soleRemote(ctx.remotes, repoText) {
			return grantScopeReason(ctx.remotes, repoText)
		}
		sink.add(dotted([]string{"requests", declaration.Brokered.Source, capability}), refText)
		return ""
	}
	return namedHostGrant(repoText, declaration.Brokered.Source, capability, refText, ctx, sink)
}

// namedHostGrant converts a grant on a named host (GW-D79, GW-D80): a grant on the origin
// repository is the unqualified request, which names every remote; a grant on another repository
// is a line of that repository's named table, declared for it when it is not declared yet.
func namedHostGrant(repo, source, capability, ref string, ctx loweringContext, sink grantSink) string {
	if ctx.repos == nil {
		return "the workspace's repositories are not known here; " + noSpellingYet
	}
	if repo == brokerscope.AnyRepo {
		return grantScopeReason(ctx.remotes, repo)
	}
	if strings.Contains(repo, "*") || strings.Count(repo, "/") != 2 {
		return notPlainRepoReason(repo, source, legacyJSONCList)
	}
	if origin := ctx.repos.originRepo(); origin != "" && strings.EqualFold(origin, repo) {
		sink.add(dotted([]string{"requests", source, capability}), ref)
		return ""
	}
	name, ok := ctx.repos.claimRepoName(repo)
	if !ok {
		return fmt.Sprintf("the name %s is taken by another repository in this workspace; run `yolo request repos <alias> %s`, then `yolo config migrate` again", name, repo)
	}
	sink.declare(name, repo)
	sink.add(dotted([]string{"repos", name, "requests", source, capability}), ref)
	return ""
}

func soleRemote(remotes []string, repo string) bool {
	return len(remotes) == 1 && repo != brokerscope.AnyRepo && strings.EqualFold(remotes[0], repo)
}

// grantScopeReason is why a grant on repo has no request spelling in a workspace with these
// remotes, ending in its exact next step (RC-D61): an unqualified request names every remote of
// the workspace (GW-D66).
func grantScopeReason(remotes []string, repo string) string {
	keepAlone := "a request names every remote of this workspace. To keep this grant on " + repo + " alone, leave the old file (this yolo keeps reading it)"
	switch {
	case repo == brokerscope.AnyRepo:
		return "a grant on every repository (\"*\") would apply to remotes added later; a request names the remotes this workspace has now. To mean every remote, leave the old file (this yolo keeps reading it)"
	case len(remotes) == 0:
		return "this workspace has no git remote, so a request could not be shown to name " + repo + "; add the remote (git remote add), then run `yolo config migrate` again"
	case len(remotes) > 1:
		return "this workspace has more than one remote (" + strings.Join(remotes, ", ") + "), and this grant names only " + repo + "; " + keepAlone
	default:
		return "the workspace's remote is " + remotes[0] + ", not " + repo + "; " + keepAlone + "; if every remote may take it, set \"repo\" to " + remotes[0] + " and convert again"
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

// placeNamedRepos places a file's own `repos` tables beside what its brokered repos lists already
// wrote (lowerBrokeredRepos). A table nothing wrote takes its whole value; one partly written
// merges leaf by leaf, an identical leaf is written once, and only a leaf another value holds is
// refused, so the two never refuse each other as a whole (GW-D80).
func placeNamedRepos(tbl, into *jsonx.OrderedMap, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	var placeLeaf func(path []string, value any)
	placeLeaf = func(path []string, value any) {
		prior, present := get(into, path)
		if sub, isTable := value.(*jsonx.OrderedMap); present && isTable {
			if _, priorTable := prior.(*jsonx.OrderedMap); priorTable {
				for _, k := range sub.Keys() {
					v, _ := sub.Get(k)
					placeLeaf(append(append([]string(nil), path...), k), v)
				}
				return
			}
		}
		if present && reflect.DeepEqual(prior, value) {
			return
		}
		if !place(path, value, path) {
			skip(path, value, dotted(path)+" is already written in this file; keep one of the two")
		}
	}
	for _, name := range tbl.Keys() {
		v, _ := tbl.Get(name)
		placeLeaf([]string{"repos", name}, v)
	}
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
// fileTwins is the set of row paths each file's own conversion writes as `twin_of`, the only twins
// the merged read may assume (convertModelRows).
func fileTwins(set map[string]bool, doc *jsonx.OrderedMap, ctx loweringContext) map[string]bool {
	if doc != nil {
		ctx.keepUnconverted = true
		for _, tw := range lower(jsonx.DeepCopyMap(doc), ctx, "").twins {
			set[tw.Path] = true
		}
	}
	return set
}

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

// envResetSpelling is env with a null env.sources and env.files, the readers' form of a legacy
// env_sources null, respelled as the empty env.files that says the same in TOML. Anything else
// is returned as it is.
func envResetSpelling(value any) any {
	env, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return value
	}
	sources, hasSources := env.Get("sources")
	files, hasFiles := env.Get("files")
	if !hasSources || !hasFiles || sources != nil || files != nil {
		return value
	}
	out := jsonx.NewOrderedMap()
	for _, k := range env.Keys() {
		switch k {
		case "sources":
		case "files":
			out.Set("files", []any{})
		default:
			v, _ := env.Get(k)
			out.Set(k, v)
		}
	}
	return out
}

// addCeilingKinds appends each pack's kinds to values' `allow.<pack>.on_request`, creating the
// tables and the list where absent.
func addCeilingKinds(values *jsonx.OrderedMap, ceiling map[string][]string) {
	allow, ok := mapAt(values, "allow")
	if !ok {
		allow = jsonx.NewOrderedMap()
		values.Set("allow", allow)
	}
	for pack, kinds := range ceiling {
		table, ok := mapAt(allow, pack)
		if !ok {
			table = jsonx.NewOrderedMap()
			allow.Set(pack, table)
		}
		list, _ := tableGet(table, "on_request")
		items, _ := list.([]any)
		items = append([]any(nil), items...)
		for _, kind := range kinds {
			items = append(items, kind)
		}
		table.Set("on_request", items)
	}
}

// lowerProjectRequests places a project's `requests` table one pack's kind at a time.
func lowerProjectRequests(tbl *jsonx.OrderedMap, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	for _, pack := range tbl.Keys() {
		v, _ := tbl.Get(pack)
		at := []string{"requests", pack}
		inner, ok := v.(*jsonx.OrderedMap)
		if !ok || inner.Len() == 0 {
			if !place(at, v, at) {
				skip(at, v, "requests."+pack+" is already written in this file; keep one of the two")
			}
			continue
		}
		for _, kind := range inner.Keys() {
			kv, _ := inner.Get(kind)
			kat := []string{"requests", pack, kind}
			if !place(kat, kv, kat) {
				skip(kat, kv, "requests."+pack+"."+kind+" is already written in this file; keep one of the two")
			}
		}
	}
}
