package packsrc

// naming.go parses one `packs` entry and names the pack for agents
// (docs/reference/pack-system.md#pack-namespace, #delivered-names): its namespace, whose word that is, and its landing.
// Every one is a function of the entry and its source alone, never of what else is selected
// (PN-D3); a clash between two entries is refused elsewhere, never resolved here.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packname"
)

// ParsePackReference resolves an authored source handle, a `{ pack = HANDLE }` table, or a
// direct-repository table using already parsed definitions. localRoot and home are supplied by
// the caller so this function has no ambient config or credential reads.
func ParsePackReference(raw any, sources map[string]SourceDefinition, localRoot, home, origin string,
	shippedNames map[string]bool) (PackReference, error) {
	if text, ok := raw.(string); ok {
		ref, scope, err := parseHandle(text, sources, localRoot, home, origin, shippedNames)
		if err != nil {
			return PackReference{}, err
		}
		return named(ref, scope, false, false, false, "", origin)
	}

	obj, ok := raw.(Object)
	if !ok || len(obj.Keys()) == 0 {
		return PackReference{}, fmt.Errorf("%s: expected a source/pack handle, { pack = HANDLE }, or { git = URL }", origin)
	}
	for _, key := range obj.Keys() {
		if key != "git" && key != "ref" && key != "pack" && key != "bare" && key != "namespace" {
			return PackReference{}, fmt.Errorf("%s.%s: unknown pack entry field (pack, git, ref, bare or namespace)", origin, key)
		}
	}
	bare, err := explicitBare(obj, origin)
	if err != nil {
		return PackReference{}, err
	}
	_, hasBare := obj.Get("bare")
	_, hasNamespace := obj.Get("namespace")
	marked := hasBare || hasNamespace
	namespace := ""
	if rawNS, has := obj.Get("namespace"); has {
		ns, isString := rawNS.(string)
		if !isString {
			return PackReference{}, fmt.Errorf("%s.namespace: expected a string", origin)
		}
		if err := packname.Word("namespace", ns); err != nil {
			return PackReference{}, fmt.Errorf("%s.namespace: %w", origin, err)
		}
		if bare {
			return PackReference{}, fmt.Errorf("%s: an entry is bare or has a namespace, not both; drop one", origin)
		}
		namespace = ns
	}
	if rawPack, has := obj.Get("pack"); has {
		if _, both := obj.Get("git"); both {
			return PackReference{}, fmt.Errorf("%s: an entry names a pack or a repository, not both", origin)
		}
		if _, hasRef := obj.Get("ref"); hasRef {
			return PackReference{}, fmt.Errorf("%s.ref: a ref belongs on the source, not on { pack = HANDLE }", origin)
		}
		text, isString := rawPack.(string)
		if !isString {
			return PackReference{}, fmt.Errorf("%s.pack: expected a pack handle", origin)
		}
		ref, scope, err := parseHandle(text, sources, localRoot, home, origin, shippedNames)
		if err != nil {
			return PackReference{}, err
		}
		return named(ref, scope, true, marked, bare, namespace, origin)
	}
	rawRepo, has := obj.Get("git")
	repoURL, isString := rawRepo.(string)
	if !has || !isString {
		return PackReference{}, fmt.Errorf("%s: expected { pack = HANDLE } or { git = URL }", origin)
	}
	ref, err := explicitRef(obj, origin)
	if err != nil {
		return PackReference{}, err
	}
	repo, err := normalizeRepository(repoURL)
	if err != nil {
		return PackReference{}, fmt.Errorf("%s.git: %w", origin, err)
	}
	name, err := ownerRepositoryName(repo)
	if err != nil {
		return PackReference{}, fmt.Errorf("%s.git: %w", origin, err)
	}
	out := PackReference{Source: gitPackAddress(repo, "", ref), Name: name,
		SourceID: repo, AllowDefaultRef: ref == "", Origin: origin}
	return named(out, handleScope{kind: scopeOwn, owner: "repo:" + repo, pack: name[strings.LastIndex(name, "/")+1:]},
		true, marked, bare, namespace, origin)
}

// scopeKind is where a pack's namespace comes from (docs/reference/pack-system.md#pack-namespace).
type scopeKind int

const (
	scopeShipped scopeKind = iota // a shipped pack: delivered bare
	scopeSource                   // the [pack_sources] key
	scopeOwn                      // its own word: a local pack's name, a repository's last segment
)

type handleScope struct {
	kind   scopeKind
	source SourceDefinition // scopeSource
	owner  string           // scopeOwn: whose word the default namespace is
	pack   string           // the handle's last segment, the author's name for the pack
}

func parseHandle(text string, sources map[string]SourceDefinition, localRoot, home, origin string,
	shippedNames map[string]bool) (PackReference, handleScope, error) {
	if text == "" || strings.TrimSpace(text) != text {
		return PackReference{}, handleScope{}, fmt.Errorf("%s: expected a non-empty pack handle", origin)
	}
	if !strings.Contains(text, "/") {
		if shippedNames[text] {
			return PackReference{Source: "embedded:" + text, Name: text, SourceID: "shipped", EmbeddedName: text, Origin: origin},
				handleScope{kind: scopeShipped, pack: text}, nil
		}
		return PackReference{}, handleScope{}, fmt.Errorf("%s: only shipped packs may be named without a source", origin)
	}
	parts := strings.Split(text, "/")
	if len(parts) != 2 || !validHandleSegment(parts[0]) || !validHandleSegment(parts[1]) {
		return PackReference{}, handleScope{}, fmt.Errorf("%s: expected source/pack, such as local/matt", origin)
	}
	if parts[0] == "yolo" {
		if !shippedNames[parts[1]] {
			return PackReference{}, handleScope{}, fmt.Errorf("%s: yolo/%s does not name a shipped pack", origin, parts[1])
		}
		return PackReference{Source: "embedded:" + parts[1], Name: text, SourceID: "shipped",
			EmbeddedName: parts[1], Origin: origin}, handleScope{kind: scopeShipped, pack: parts[1]}, nil
	}
	if parts[0] == "local" {
		if localRoot == "" {
			return PackReference{}, handleScope{}, fmt.Errorf("%s: local pack directory is unavailable", origin)
		}
		path := filepath.Join(localRoot, parts[1])
		return PackReference{Source: "file://" + filepath.ToSlash(path), Name: text,
				SourceID: "local", LocalPath: path, Origin: origin},
			handleScope{kind: scopeOwn, owner: "local:" + parts[1], pack: parts[1]}, nil
	}
	def, ok := sources[parts[0]]
	if !ok {
		return PackReference{}, handleScope{}, fmt.Errorf("%s: unknown pack source %q — add it under [pack_sources], "+
			"or, if it was renamed, write the new key in this handle, then run `yolo check`", origin, parts[0])
	}
	scope := handleScope{kind: scopeSource, source: def, pack: parts[1]}
	if def.Path != "" {
		root := expandTilde(def.Path, home)
		path := filepath.Join(root, parts[1])
		return PackReference{Source: "file://" + filepath.ToSlash(path), Name: text,
			SourceID: def.Name, LocalPath: path, Origin: origin}, scope, nil
	}
	return PackReference{Source: gitPackAddress(def.Repo, parts[1], def.Ref), Name: text,
		SourceID: def.Name, AllowDefaultRef: def.Ref == "", Origin: origin}, scope, nil
}

// named fills ref's namespace, its owner and its landing (docs/reference/pack-system.md#pack-namespace, #delivered-names).
//
// A BARE pack is left exactly as main named it (PN-D7): no landing of its own, so its files and
// /ctx directory keep their old spelling, and no character or length rule on its names — those
// apply only to a name yolo composes.
func named(ref PackReference, scope handleScope, table, marked, bare bool, namespace, origin string) (PackReference, error) {
	switch scope.kind {
	case scopeShipped:
		if namespace != "" {
			return PackReference{}, fmt.Errorf("%s.namespace: a shipped pack is delivered under its own names; drop namespace", origin)
		}
		return ref, nil
	case scopeSource:
		if namespace != "" {
			return PackReference{}, fmt.Errorf("%s.namespace: a pack of source %q is in that source's namespace, its key "+
				"(PN-D11); to change it, rename the key under [pack_sources] and in every handle that uses it, "+
				"then run `yolo check`", origin, scope.source.Name)
		}
		if bare || scope.source.Bare {
			return ref, nil
		}
		// The key becomes the namespace only here, so only here must it be a word agents carry.
		if !packname.IsWord(scope.source.Name) {
			return PackReference{}, fmt.Errorf("%s: the source key %q is the namespace of its packs' skills, and "+
				"a namespace must be lowercase letters, digits and single hyphens. Keep its packs' own names with %s, "+
				"or rename the key to %q under [pack_sources] and in every handle that uses it",
				origin, scope.source.Name, scope.source.KeepNamesStep(), packname.Normalize(scope.source.Name))
		}
		pack := packname.Normalize(scope.pack)
		if pack == "" {
			return PackReference{}, fmt.Errorf("%s: the pack name %q has no letter or digit, so no agent can carry a "+
				"name made from it; keep its own names with %s", origin, scope.pack, scope.source.KeepNamesStep())
		}
		ref.Namespace, ref.NamespaceOwner = scope.source.Name, "source:"+scope.source.Name
		ref.Landing = packname.Delivered(ref.Namespace, pack)
		ref.BareStep = entryBareStep(origin, ref.Name, table, true)
		if !marked && !scope.source.Marked {
			ref.KeepNamesStep, ref.AcceptNamesStep = scope.source.KeepNamesStep(), scope.source.AcceptNamesStep()
		}
	default:
		if bare {
			return ref, nil
		}
		keep := entryBareStep(origin, ref.Name, table, true)
		ref.Namespace, ref.NamespaceOwner = namespace, scope.owner
		if namespace == "" {
			ref.Namespace = packname.Normalize(scope.pack)
			if ref.Namespace == "" {
				return PackReference{}, fmt.Errorf("%s: the pack name %q has no letter or digit, so no agent can carry "+
					"a name made from it; keep its own names with %s, or give the entry `namespace = \"…\"`", origin, scope.pack, keep)
			}
		}
		ref.BareStep = keep
		if !marked {
			ref.KeepNamesStep, ref.AcceptNamesStep = keep, entryBareStep(origin, ref.Name, table, false)
		}
		// A pack that is its own namespace lands under the namespace alone (§3.4).
		ref.Landing = ref.Namespace
	}
	if err := packname.TooLong(ref.Landing, ref.Name, "landing"); err != nil {
		return PackReference{}, fmt.Errorf("%s: %w", origin, err)
	}
	return ref, nil
}

func explicitBare(obj Object, origin string) (bool, error) {
	raw, has := obj.Get("bare")
	if !has {
		return false, nil
	}
	bare, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s.bare: expected true or false", origin)
	}
	return bare, nil
}
