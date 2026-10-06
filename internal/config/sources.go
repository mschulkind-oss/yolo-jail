package config

// sources.go records where each value of a composed config was written, so a refusal that
// names a key can also name the file, line and column it came from
// (userguide/reference/configuration.md#the-config-files).
//
// THE PROBLEM IT SOLVES. A config is composed from many files: the user config and every file
// it includes (recursively), the inherited nested-launch file in a jail, a --user-layer, and
// the workspace's yolo-jail.jsonc and yolo-jail.local.jsonc with their own includes. The merge
// (MergeConfig) returns one map, and that map no longer says which file any key came from, so
// a refusal such as "config.use_profiles: RENAMED" left the user to search every one of them.
//
// HOW. The loaders build a provenance tree beside the map, and the ONE merge function
// (mergeConfig, below MergeConfig in load.go) folds two trees exactly as it folds two maps —
// one implementation, so the record cannot disagree with the merge about which value won.
// Each object member and list element in the tree carries its WRITERS: every file that wrote a
// value at that place, lowest precedence first, so the last is the file whose value the
// merge kept. A key overridden by a later file keeps the earlier writer in the list too,
// because a refusal about a key written twice has to be fixed in both places.
//
// The tree holds each file's bytes and the path to the value in that file, never a line
// number: a json5.Index of the file turns them into one only when a message needs it, built
// once per file on the first such message.
//
// ONLY A READER THAT REPORTS KEEPS ONE. The *WithSources loaders build the tree, and so do the
// validators that locate a refusal in one workspace file; LoadConfig and the other plain
// readers do not (loadJSONCFile's record), since a launch reads its config many times over and
// all but the reporting reads would discard it. A plain reader with a problem to report
// (LoadPacks, LoadProfiles, …) reads the user scope again for the record, on that path only.
//
// WHAT IT CANNOT SEE. A config the loader did not read from files has no tree: the in-jail
// copy of the host's assembled config (LoadConfig's short-circuit for the jail's own
// workspace), whose host files are not in the jail, and any map a caller built by hand. A nil
// *Sources annotates nothing.

import (
	"strconv"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Sources is the provenance of a composed config: for each object member and list element,
// the files that wrote it. Built by the *WithSources loaders and MergeConfigWithSources; a nil
// Sources is valid and knows nothing.
type Sources struct {
	root *srcNode
}

// srcFile is one config file as the loader read it.
type srcFile struct {
	path string
	data []byte

	// contained says the bytes came from inside the workspace, read beneath its root
	// (wsroot.go), and rel is then the file's workspace-relative name. Both are zero for every
	// file a workspace load that records did not read, and for one it read any other way.
	contained bool
	rel       string
	// seq is the file's place in its workspace load's read order, which is merge order: the
	// config file, its includes, the local file, its includes.
	seq int
	// via is the include that reached the file ("include_if_found[0] in yolo-jail.jsonc"), ""
	// for a top-level file.
	via string

	// index is where every value of data sits (json5.Index), parsed once, on the first
	// location asked of this file: a refusal naming several of its keys, or a key several
	// files write, then costs one parse per file rather than one per location, and a config
	// nothing is wrong with costs none.
	indexOnce sync.Once
	index     *json5.Index
}

// locate is the span of the value at steps in this file, ok false where the file does not
// locate it: nothing there, or a key along the path written twice (json5.Index.Locate).
func (f *srcFile) locate(steps []json5.Step) (json5.Span, bool) {
	f.indexOnce.Do(func() { f.index, _ = json5.NewIndex(f.data) })
	if f.index == nil {
		return json5.Span{}, false
	}
	span, ok, err := f.index.Locate(steps...)
	return span, ok && err == nil
}

// writtenTwice is json5.Index.Locate's error for steps in this file, the key along them that is
// written more than once, nil when there is none (or the file does not parse, which its loader
// already reported).
func (f *srcFile) writtenTwice(steps []json5.Step) error {
	f.indexOnce.Do(func() { f.index, _ = json5.NewIndex(f.data) })
	if f.index == nil {
		return nil
	}
	_, _, err := f.index.Locate(steps...)
	return err
}

// srcOrigin is one place a value was written: a file, and the path to the value inside it.
type srcOrigin struct {
	file  *srcFile
	steps []json5.Step
}

// srcNode mirrors one value of a composed config. Nodes are never modified once built: a merge
// builds new nodes and shares the subtrees it does not change, as MergeConfig shares values.
type srcNode struct {
	writers []srcOrigin         // lowest precedence first; the last one's value won
	keys    map[string]*srcNode // an object's members
	elems   []*srcNode          // a list's elements, in the composed list's order
}

func (n *srcNode) key(k string) *srcNode {
	if n == nil {
		return nil
	}
	return n.keys[k]
}

func (n *srcNode) elem(i int) *srcNode {
	if n == nil || i < 0 || i >= len(n.elems) {
		return nil
	}
	return n.elems[i]
}

func (n *srcNode) writerList() []srcOrigin {
	if n == nil {
		return nil
	}
	return n.writers
}

// without is n with key k gone, for a key the loader consumes (include_if_found).
func (n *srcNode) without(k string) *srcNode {
	if n == nil || n.keys[k] == nil {
		return n
	}
	out := &srcNode{writers: n.writers, keys: make(map[string]*srcNode, len(n.keys)), elems: n.elems}
	for name, c := range n.keys {
		if name != k {
			out.keys[name] = c
		}
	}
	return out
}

// fileTree is the provenance tree of one file's decoded document: every value written by f,
// at the path it sits at in f.
func fileTree(f *srcFile, v any) *srcNode {
	return fileTreeAt(f, v, nil)
}

func fileTreeAt(f *srcFile, v any, steps []json5.Step) *srcNode {
	n := &srcNode{writers: []srcOrigin{{file: f, steps: steps}}}
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		n.keys = make(map[string]*srcNode, t.Len())
		for _, k := range t.Keys() {
			cv, _ := t.Get(k)
			n.keys[k] = fileTreeAt(f, cv, appendStep(steps, json5.Key(k)))
		}
	case []any:
		n.elems = make([]*srcNode, len(t))
		for i, e := range t {
			n.elems[i] = fileTreeAt(f, e, appendStep(steps, json5.Elem(i)))
		}
	}
	return n
}

// appendStep appends without sharing steps' backing array, which sibling paths also extend.
func appendStep(steps []json5.Step, s json5.Step) []json5.Step {
	out := make([]json5.Step, len(steps), len(steps)+1)
	copy(out, steps)
	return append(out, s)
}

func concatWriters(a, b []srcOrigin) []srcOrigin {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]srcOrigin, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// replacedBy is the node for a value the merge REPLACED: the override's value and structure,
// with the replaced value's writers kept ahead of the override's, since the key is still
// written in those files.
func replacedBy(old, override *srcNode) *srcNode {
	if old == nil {
		return override
	}
	if override == nil {
		// A value the merge took from a map with no record: nothing is known about it, and
		// naming the replaced file would name a value that is not the one in effect.
		return nil
	}
	return &srcNode{
		writers: concatWriters(old.writers, override.writers),
		keys:    override.keys,
		elems:   override.elems,
	}
}

// alsoWrittenIn is the node for a list element a later file wrote again, equal to the one
// the merge kept (mergeLists' dedup): the kept value's structure, with every writer of either
// copy at each level, so a refusal about the entry or anything inside it names both files.
func alsoWrittenIn(kept, again *srcNode) *srcNode {
	if kept == nil {
		return nil // the kept value has no record, so the entry is not attributable
	}
	if again == nil {
		return kept
	}
	n := &srcNode{writers: concatWriters(kept.writers, again.writers)}
	if kept.keys != nil {
		n.keys = make(map[string]*srcNode, len(kept.keys))
		for k, c := range kept.keys {
			n.keys[k] = alsoWrittenIn(c, again.key(k))
		}
	}
	if kept.elems != nil {
		n.elems = make([]*srcNode, len(kept.elems))
		for i, c := range kept.elems {
			n.elems[i] = alsoWrittenIn(c, again.elem(i))
		}
	}
	return n
}

// sourcesOf wraps a tree, nil for none.
func sourcesOf(n *srcNode) *Sources {
	if n == nil {
		return nil
	}
	return &Sources{root: n}
}

func (s *Sources) node() *srcNode {
	if s == nil {
		return nil
	}
	return s.root
}

// MergeConfigWithSources is MergeConfig over two configs and their provenance: the merged
// config, which is exactly MergeConfig's, and where each of its values was written. Either
// Sources may be nil (a config with no record), and the values it contributed are then
// unattributed.
func MergeConfigWithSources(base *jsonx.OrderedMap, baseSrc *Sources, override *jsonx.OrderedMap,
	overrideSrc *Sources) (*jsonx.OrderedMap, *Sources) {
	merged, node := mergeConfig(base, override, baseSrc.node(), overrideSrc.node())
	return merged, sourcesOf(node)
}

// Annotate prefixes every message that names a config key — one that starts "config.<key>",
// the spelling every validator uses — with where that key was written:
//
//	~/.config/yolo-jail/profiles.jsonc:3:19: config.use_profiles: RENAMED — …
//
// The location is the file whose value the merge kept, as "<path>:<line>:<column>" with the
// home written as ~ (the spelling the config messages use for the user config); when the key
// is written in more than one file, every other one is named after the message, highest
// precedence first: "(also written at ~/.config/yolo-jail/config.jsonc:2:19)".
//
// The path in the message is followed through the record as far as it goes: a message about
// `config.network.ports[0].host` names where `ports[0]` was written, since `.host` is a part
// of that string rather than a key. A message about a key the record does not hold, or one
// that does not start with "config.", is returned unchanged — including one a validator has
// already located itself, which starts with its path instead.
func (s *Sources) Annotate(msgs []string) []string {
	if s == nil || len(msgs) == 0 {
		return msgs
	}
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = s.AnnotateOne(m)
	}
	return out
}

// AnnotateOne is Annotate for one message.
func (s *Sources) AnnotateOne(msg string) string {
	rest, ok := strings.CutPrefix(msg, "config.")
	if !ok || s == nil {
		return msg
	}
	n := s.root.follow(rest)
	if n == nil {
		return msg
	}
	locs := n.locations()
	if len(locs) == 0 {
		return msg
	}
	out := locs[0] + ": " + msg
	if len(locs) > 1 {
		out += " (also written at " + strings.Join(locs[1:], ", ") + ")"
	}
	return out
}

// Locations is every place Annotate would name for path, spelled as the validators spell it
// ("config.packs[1]", "config.required_capabilities"), highest precedence first; nil when the
// record does not hold it. For a caller whose message cannot lead with a location — one
// printed in a sentence of its own, or one another reader splits at its first ": ".
func (s *Sources) Locations(path string) []string {
	rest, ok := strings.CutPrefix(path, "config.")
	if !ok || s == nil {
		return nil
	}
	return s.root.follow(rest).locations()
}

// follow walks a message's config path — `key`, `.key` and `[index]` steps, the spelling the
// validators print after "config." — through the record, and returns the deepest node it
// reached, nil when not even the first key is recorded. Keys are matched against the record
// rather than split on dots, longest first, because a provider, profile or server name may
// itself contain one.
func (n *srcNode) follow(rest string) *srcNode {
	var reached *srcNode
	cur := n
	for cur != nil {
		if strings.HasPrefix(rest, "[") {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				break
			}
			i, err := strconv.Atoi(rest[1:end])
			next := cur.elem(i)
			if err != nil || next == nil {
				break
			}
			cur, reached, rest = next, next, rest[end+1:]
		} else {
			k, ok := cur.longestKeyAt(rest)
			if !ok {
				break
			}
			cur, reached, rest = cur.keys[k], cur.keys[k], rest[len(k):]
		}
		switch {
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
		case strings.HasPrefix(rest, "["):
		default:
			return reached
		}
	}
	return reached
}

// longestKeyAt is the longest recorded key rest starts with that ends where a path step
// does: at a ".", a "[", the ":" or space that ends the path, or the end of the message.
func (n *srcNode) longestKeyAt(rest string) (string, bool) {
	best, found := "", false
	for k := range n.keys {
		if k == "" || len(k) <= len(best) || !strings.HasPrefix(rest, k) {
			continue
		}
		if len(rest) > len(k) && !strings.ContainsRune(".[: ", rune(rest[len(k)])) {
			continue
		}
		best, found = k, true
	}
	return best, found
}

// locations is where the node was written, the highest-precedence writer first, each as
// "<path>:<line>:<column>", or the path alone where the file no longer locates the value
// (a key written twice in one file, which Locate refuses to guess at). Duplicates collapse.
func (n *srcNode) locations() []string {
	ws := n.writerList()
	seen := make(map[string]bool, len(ws))
	var out []string
	for i := len(ws) - 1; i >= 0; i-- {
		loc := ws[i].String()
		if loc == "" || seen[loc] {
			continue
		}
		seen[loc] = true
		out = append(out, loc)
	}
	return out
}

// String is the origin as a person types it: the file's path with the home as ~, then the
// line and column the value starts at.
func (o srcOrigin) String() string {
	if o.file == nil {
		return ""
	}
	where := tildePath(o.file.path)
	span, ok := o.file.locate(o.steps)
	if !ok {
		return where
	}
	line, col := json5.Position(o.file.data, span.Start)
	return where + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(col)
}

// locateInUserScope is problems, each about a key of the user scope, located in the user
// scope's record (Annotate) — for a plain user-scope reader (LoadPacks, LoadProfiles, …) that
// warns about an entry it skips. The record is read again for them, and only when there is a
// problem, so a reader with nothing to say pays nothing for it.
func locateInUserScope(problems []string) []string {
	if len(problems) == 0 {
		return problems
	}
	return UserScopeSources().Annotate(problems)
}

// locatedAt is msg prefixed with where n was written — its highest-precedence writer, as
// Annotate leads — for a validator that knows the record its message is about better than
// the merged one Annotate would follow (the message then starts with the path, and Annotate
// leaves it alone). msg is returned unchanged when n has no record.
func locatedAt(n *srcNode, msg string) string {
	if locs := n.locations(); len(locs) > 0 {
		return locs[0] + ": " + msg
	}
	return msg
}

// includeProblem is a problem with one file's `include_if_found` (problem starts with the key,
// and steps are the path to the value it is about): "<file>:<line>:<col>:
// config.include_if_found…", the spelling a validator's refusal takes, else
// "<label>.include_if_found…" as before where there is no file, label being the name the
// loader gives the file. It takes the file rather than a record because a loader that keeps
// none (loadJSONCFile's record) still has the bytes the problem is in.
func includeProblem(f *srcFile, label, problem string, steps ...json5.Step) string {
	if f == nil {
		return label + "." + problem
	}
	return srcOrigin{file: f, steps: steps}.String() + ": config." + problem
}

// tildePath writes a path under the home as ~/…, the spelling the config messages already use
// for the user config (~/.config/yolo-jail/config.jsonc). The home is matched as given and as
// its symlinks resolve, because the include walk resolves the paths it joins
// (resolveJoin), and on macOS a home under /var is /private/var once resolved.
func tildePath(p string) string {
	home := paths.Home()
	if home == "" || home == "/" {
		return p
	}
	candidates := []string{home}
	if r, err := resolve(home); err == nil && r != home {
		candidates = append(candidates, r)
	}
	for _, h := range candidates {
		h = strings.TrimRight(h, "/")
		if strings.HasPrefix(p, h+"/") {
			return "~" + p[len(h):]
		}
	}
	return p
}
