package entrypoint

// yamltrivia.go is the `yaml` codec's half of the RMW mechanism (surfacecodec.go): an
// order-preserving read of a YAML surface into the RMW writer's value model, and a write that
// keeps the user's layout and comments wherever the render left their value alone.
//
// # Why not codec.YAML
//
// The shared codec (internal/agentcfg/codec) decodes with yaml.Unmarshal into Go maps, which
// is right for the `stateful` engine and wrong for a read-modify-write of the user's own file.
// MEASURED on oh-omp's config.yml: it keeps only the first document of a multi-document file,
// loses the key order and every comment, and fails outright on an unquoted date (yaml.v3
// decodes one to time.Time, which the engine's value model has no slot for). RMW promises to
// keep what yolo does not declare, so its read has to see the file as written. This one walks
// yaml.v3's node tree instead, which carries order, style and comments.
//
// # The two rules, the same two the TOML half follows
//
//   - A FILE THIS CANNOT ROUND-TRIP IS REFUSED, BYTE-UNTOUCHED. More than one document, an
//     anchor or alias, a `<<` merge key, a mapping key that is not a string, a duplicate key,
//     and a value whose tag is outside YAML's JSON-shaped core (!!timestamp, !!binary, a custom
//     tag): each is something a rewrite from the value model would silently change, so the
//     refusal names it and says what to change.
//   - A COMMENT SURVIVES IFF ITS VALUE DID (E4's rule ①, rmwTriviaKeeper — the predicate the
//     TOML half uses, so the two codecs cannot disagree about which comments stay). The write
//     REUSES the original node for every value the render did not change, so an untouched
//     subtree comes back as the user wrote it — quoting, flow style, comments and all — and only
//     what changed is re-emitted. Every comment the write could not keep is RETURNED, by key,
//     for HostRenderResult.Formatting.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"gopkg.in/yaml.v3"
)

// yamlDocuments parses every document in raw, in order. An error is a parse error; the
// caller decides what a document count means.
func yamlDocuments(raw []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []*yaml.Node
	for {
		doc := &yaml.Node{}
		if err := dec.Decode(doc); err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}
			return nil, err
		}
		docs = append(docs, doc)
	}
}

// yamlRoot is the one document's content node, or nil for a file holding no content (only
// comments, or an empty document).
func yamlRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		return nil // an explicit empty document (`---` alone, `~`)
	}
	return root
}

// decodeYAMLObject is decodeSurfaceBytes' `yaml` arm: raw (non-empty) decoded into an
// order-preserving object, or a refusal naming what about the file yolo cannot write back.
func decodeYAMLObject(surface manifest.Surface, path string, raw []byte) (*jsonx.OrderedMap, error) {
	docs, err := yamlDocuments(raw)
	if err != nil {
		return nil, refuseRMW(surface, "%s is not valid YAML (%v) — refusing to rewrite it, "+
			"because a read that cannot see your keys cannot preserve them; fix or move the "+
			"file and re-run", path, err)
	}
	if len(docs) > 1 {
		return nil, refuseRMW(surface, "%s holds %d YAML documents and yolo reads and writes "+
			"one — refusing to rewrite it, because every document after the first would be "+
			"lost; keep this file to one document and re-run", path, len(docs))
	}
	if len(docs) == 0 {
		return jsonx.NewOrderedMap(), nil
	}
	root := yamlRoot(docs[0])
	if root == nil {
		return jsonx.NewOrderedMap(), nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, refuseRMW(surface, "%s is valid YAML but not a mapping, so there are no "+
			"keys to merge into — refusing to replace it (the file is untouched); move it aside "+
			"(yolo then writes a fresh one) or make its top level a mapping, and re-run", path)
	}
	v, err := yamlNodeValue(root, nil)
	if err != nil {
		return nil, refuseRMW(surface, "refusing to rewrite %s, which yolo could not write back "+
			"as you wrote it (the file is left untouched): it %v", path, err)
	}
	return v.(*jsonx.OrderedMap), nil
}

// yamlNodeValue lowers one node to the RMW writer's value model, refusing (with a sentence
// naming the key and the fix) everything a rewrite could not reproduce.
func yamlNodeValue(n *yaml.Node, at []string) (any, error) {
	where := yamlPathText(at)
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("uses a YAML anchor or alias at %s; write the value out in "+
			"full there and re-run", where)
	}
	switch n.Kind {
	case yaml.MappingNode:
		if tag := n.ShortTag(); tag != "!!map" {
			return nil, fmt.Errorf("tags the mapping at %s %s; remove the tag and re-run", where, tag)
		}
		m := jsonx.NewOrderedMap()
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
				return nil, fmt.Errorf("uses a `<<` merge key in the mapping at %s; write the "+
					"merged keys out in full there and re-run", where)
			}
			if k.Anchor != "" || k.Kind == yaml.AliasNode {
				return nil, fmt.Errorf("uses a YAML anchor or alias on a key in the mapping at "+
					"%s; write the key out in full there and re-run", where)
			}
			if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
				return nil, fmt.Errorf("has a mapping key that is not a string (%q, %s) in the "+
					"mapping at %s; quote the key and re-run", k.Value, k.ShortTag(), where)
			}
			if _, dup := m.Get(k.Value); dup {
				return nil, fmt.Errorf("defines %s twice; keep one and re-run",
					yamlPathText(append(append([]string(nil), at...), k.Value)))
			}
			val, err := yamlNodeValue(v, append(append([]string(nil), at...), k.Value))
			if err != nil {
				return nil, err
			}
			m.Set(k.Value, val)
		}
		return m, nil
	case yaml.SequenceNode:
		if tag := n.ShortTag(); tag != "!!seq" {
			return nil, fmt.Errorf("tags the list at %s %s; remove the tag and re-run", where, tag)
		}
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			val, err := yamlNodeValue(c, append(append([]string(nil), at...), "["+strconv.Itoa(i)+"]"))
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		return out, nil
	case yaml.ScalarNode:
		return yamlScalarValue(n, where)
	}
	return nil, fmt.Errorf("has a YAML node yolo does not read at %s", where)
}

// yamlScalarValue is one scalar in the RMW value model: an integer as a jsonx integer literal
// (so it is written back without a ".0"), a float, a bool, nil, or the string itself. Every
// other tag is refused, with the fix for the one users meet: an unquoted date.
func yamlScalarValue(n *yaml.Node, where string) (any, error) {
	switch tag := n.ShortTag(); tag {
	case "!!str":
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, fmt.Errorf("has a boolean yolo cannot read at %s (%v)", where, err)
		}
		return b, nil
	case "!!int":
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, fmt.Errorf("has an integer yolo cannot read at %s (%v)", where, err)
		}
		switch i := v.(type) {
		case int:
			return jsonx.IntValue(int64(i)), nil
		case int64:
			return jsonx.IntValue(i), nil
		case uint64:
			lit, _ := jsonx.IntLiteral(strconv.FormatUint(i, 10))
			return lit, nil
		case float64:
			return i, nil // yaml.v3 widens an integer past uint64 to a float
		}
		return nil, fmt.Errorf("has an integer yolo cannot read at %s (%q)", where, n.Value)
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, fmt.Errorf("has a number yolo cannot read at %s (%v)", where, err)
		}
		return f, nil
	case "!!timestamp":
		return nil, fmt.Errorf("has an unquoted date at %s (%s), which YAML reads as a "+
			"timestamp rather than text; quote it (\"%s\") and re-run", where, n.Value, n.Value)
	default:
		return nil, fmt.Errorf("has a %s value at %s, outside the plain string, number, "+
			"boolean and null types yolo writes back; replace it with one of those and re-run",
			tag, where)
	}
}

// yamlPathText names a key path for a refusal: `a.b[0]`, or "the top level".
func yamlPathText(at []string) string {
	if len(at) == 0 {
		return "the top level"
	}
	var b strings.Builder
	for i, seg := range at {
		if i > 0 && !strings.HasPrefix(seg, "[") {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return "`" + b.String() + "`"
}

// encodeYAMLObject is encodeSurfaceObjectReporting's `yaml` arm: obj as the file text to
// write, plus every comment of the original the write could not keep.
//
// orig and before are the file's bytes and its decoded state before this render (nil for a
// first apply). The original node tree is re-parsed from orig and PATCHED to obj rather than
// replaced: an unchanged value keeps its original node, so its quoting, its flow style and the
// comments inside it survive. A comment on a key survives iff rmwTriviaKeeper keeps that key —
// E4's rule ①, the TOML half's predicate. If orig cannot be re-parsed (it decoded once, so this
// is not expected) the write falls back to a fresh emit and the blanket loss line, as the TOML
// half falls back when its scanner cannot place a comment.
func encodeYAMLObject(surface manifest.Surface, obj *jsonx.OrderedMap, orig []byte,
	before *jsonx.OrderedMap) (string, []string, error) {
	if before == nil {
		before = jsonx.NewOrderedMap()
	}
	var doc *yaml.Node
	var losses []string
	if len(bytes.TrimSpace(orig)) > 0 {
		docs, err := yamlDocuments(orig)
		switch {
		case err != nil || len(docs) > 1:
			if yamlHasComments(orig) {
				losses = append(losses, yamlBlanketCommentLoss)
			}
		case len(docs) == 1:
			doc = docs[0]
		default:
			// Comments and nothing else: yaml.v3 yields no document to hang them on.
			if bytes.Contains(orig, []byte("#")) {
				losses = append(losses, yamlBlanketCommentLoss)
			}
		}
	}
	p := yamlPatcher{keep: rmwTriviaKeeper(before, obj), before: before,
		changed: map[string]bool{}, gone: map[string]bool{}}
	out := &yaml.Node{Kind: yaml.DocumentNode}
	var origRoot *yaml.Node
	if doc != nil {
		// The file's header and footer blocks belong to no key, so they mean the same thing
		// wherever the render moves the keys; both are kept.
		out.HeadComment, out.LineComment, out.FootComment = doc.HeadComment, doc.LineComment, doc.FootComment
		origRoot = yamlRoot(doc)
	}
	out.Content = []*yaml.Node{p.patch(origRoot, obj, nil)}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return "", nil, refuseRMW(surface, "the composed value cannot be written as YAML "+
			"(%v) — the file is left untouched", err)
	}
	if err := enc.Close(); err != nil {
		return "", nil, refuseRMW(surface, "the composed value cannot be written as YAML "+
			"(%v) — the file is left untouched", err)
	}
	return buf.String(), append(losses, p.report()...), nil
}

// yamlBlanketCommentLoss is the E4 fallback line, for a source whose comments the patcher
// cannot reach.
const yamlBlanketCommentLoss = "comments in this file are NOT preserved — yolo could not read " +
	"their positions, so it re-emits the file from the decoded values (every value survives; " +
	"the comments do not)"

// yamlPatcher rebuilds a node tree for a new value, reusing the original nodes the render
// left alone and recording, by key, every comment it had to drop.
type yamlPatcher struct {
	keep   func(string) bool
	before *jsonx.OrderedMap
	// changed and gone are the dotted keys whose comments were dropped: changed because the
	// render changed the value they explain, gone because the key is not in the rendered file.
	changed, gone map[string]bool
}

// unchanged reports whether the value at path is the one the file held before the render.
func (p *yamlPatcher) unchanged(path []string, next any) bool {
	prev, had := lookupOrderedPath(p.before, path)
	return had && sameJSON(prev, next)
}

// patch returns the node for value v at path, given the original node there (nil if none).
func (p *yamlPatcher) patch(orig *yaml.Node, v any, path []string) *yaml.Node {
	if orig != nil && len(path) > 0 && p.unchanged(path, v) {
		return orig // the user's own node: style, order and every comment inside it
	}
	keys, get, isMap := yamlMapping(v)
	if !isMap {
		if orig != nil {
			p.dropSubtree(orig, path, false)
		}
		return yamlFreshNode(v)
	}
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var origPairs map[string][2]*yaml.Node
	if orig != nil && orig.Kind == yaml.MappingNode {
		out.Style = orig.Style
		out.HeadComment, out.LineComment, out.FootComment = orig.HeadComment, orig.LineComment, orig.FootComment
		origPairs = map[string][2]*yaml.Node{}
		for i := 0; i+1 < len(orig.Content); i += 2 {
			origPairs[orig.Content[i].Value] = [2]*yaml.Node{orig.Content[i], orig.Content[i+1]}
		}
	} else if orig != nil {
		p.dropSubtree(orig, path, false)
	}
	for _, k := range keys {
		child := append(append([]string(nil), path...), k)
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		var origVal *yaml.Node
		if pair, had := origPairs[k]; had {
			key.Style = pair[0].Style
			if p.keep(strings.Join(child, tomlPathSep)) {
				key.HeadComment, key.LineComment, key.FootComment =
					pair[0].HeadComment, pair[0].LineComment, pair[0].FootComment
			} else if yamlNodeHasComment(pair[0]) {
				p.changed[yamlDotted(child)] = true
			}
			origVal = pair[1]
		}
		out.Content = append(out.Content, key, p.patch(origVal, get(k), child))
	}
	for k, pair := range origPairs {
		if _, kept := yamlLookup(keys, k); kept {
			continue
		}
		child := append(append([]string(nil), path...), k)
		if yamlNodeHasComment(pair[0]) {
			p.gone[yamlDotted(child)] = true
		}
		p.dropSubtree(pair[1], child, true)
	}
	return out
}

// dropSubtree records every comment in a subtree the write does not reuse, against the
// deepest key path it was attached to.
func (p *yamlPatcher) dropSubtree(n *yaml.Node, path []string, gone bool) {
	if n == nil {
		return
	}
	mark := func(at []string) {
		if len(at) == 0 {
			at = []string{"(top level)"}
		}
		if gone {
			p.gone[yamlDotted(at)] = true
		} else {
			p.changed[yamlDotted(at)] = true
		}
	}
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		mark(path)
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			child := append(append([]string(nil), path...), n.Content[i].Value)
			if yamlNodeHasComment(n.Content[i]) {
				mark(child)
			}
			p.dropSubtree(n.Content[i+1], child, gone)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			p.dropSubtree(c, path, gone)
		}
	}
}

// report is the losses in the TOML half's words, so a reader sees one vocabulary for both.
func (p *yamlPatcher) report() []string {
	var out []string
	if changed := sortedTrue(p.changed); len(changed) > 0 {
		out = append(out, "comments are preserved, EXCEPT above "+
			strings.Join(quoteAll(changed), ", ")+" — this render changes those keys' "+
			"values, and a comment explaining a value that is no longer there is worse "+
			"than no comment")
	}
	if gone := sortedTrue(p.gone); len(gone) > 0 {
		out = append(out, "the comments above "+strings.Join(quoteAll(gone), ", ")+
			" are dropped — those keys are not in the rendered file")
	}
	return out
}

// yamlMapping returns an object value's keys in write order and a getter — an OrderedMap in
// its own order (the user's, plus each added key at the end), a plain map sorted, so the write
// is deterministic — or isMap=false for any other value.
func yamlMapping(v any) (keys []string, get func(string) any, isMap bool) {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return t.Keys(), func(k string) any { val, _ := t.Get(k); return val }, true
	case map[string]any:
		return sortedKeys(t), func(k string) any { return t[k] }, true
	}
	return nil, nil, false
}

// yamlFreshNode is a new node for v, with no original to reuse.
func yamlFreshNode(v any) *yaml.Node {
	if keys, get, isMap := yamlMapping(v); isMap {
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range keys {
			n.Content = append(n.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, yamlFreshNode(get(k)))
		}
		return n
	}
	scalar := func(tag, value string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	}
	switch t := v.(type) {
	case nil:
		return scalar("!!null", "null")
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range t {
			n.Content = append(n.Content, yamlFreshNode(e))
		}
		return n
	case string:
		// Tagged !!str, so the encoder quotes any text that would read back as another type
		// ("true", "1", "2026-10-04").
		return scalar("!!str", t)
	case bool:
		return scalar("!!bool", strconv.FormatBool(t))
	case float64:
		switch {
		case math.IsInf(t, 1):
			return scalar("!!float", ".inf")
		case math.IsInf(t, -1):
			return scalar("!!float", "-.inf")
		case math.IsNaN(t):
			return scalar("!!float", ".nan")
		}
		return scalar("!!float", jsonx.FormatFloatRepr(t))
	case int:
		return scalar("!!int", strconv.Itoa(t))
	case int64:
		return scalar("!!int", strconv.FormatInt(t, 10))
	}
	if lit, isInt := jsonx.AsIntLiteral(v); isInt {
		return scalar("!!int", lit)
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return scalar("!!str", fmt.Sprint(v))
	}
	return n
}

// yamlNodeHasComment reports whether n itself carries a comment.
func yamlNodeHasComment(n *yaml.Node) bool {
	return n != nil && (n.HeadComment != "" || n.LineComment != "" || n.FootComment != "")
}

// yamlTreeHasComment reports whether any node in the tree carries a comment.
func yamlTreeHasComment(n *yaml.Node) bool {
	if yamlNodeHasComment(n) {
		return true
	}
	for _, c := range n.Content {
		if yamlTreeHasComment(c) {
			return true
		}
	}
	return false
}

// yamlHasComments reports whether YAML bytes carry a comment, so the `own` render — which
// composes the whole file through the shared codec and keeps none — can say so before it
// writes. It asks the parser rather than scanning for `#`, so a `#` inside a quoted string or
// a block scalar is not a comment. A file that does not parse answers false: the render
// refuses it, and one problem gets one message.
func yamlHasComments(data []byte) bool {
	docs, err := yamlDocuments(data)
	if err != nil {
		return false
	}
	if len(docs) == 0 {
		return bytes.Contains(data, []byte("#")) // nothing but comments and blank lines
	}
	for _, d := range docs {
		if yamlTreeHasComment(d) {
			return true
		}
	}
	return false
}

// yamlDotted is a key path as the report names it.
func yamlDotted(path []string) string { return strings.Join(path, ".") }

// yamlLookup reports whether k is in keys.
func yamlLookup(keys []string, k string) (int, bool) {
	for i, key := range keys {
		if key == k {
			return i, true
		}
	}
	return 0, false
}

// sortedTrue is a set's members in order.
func sortedTrue(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
