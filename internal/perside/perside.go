// Package perside is the per-side path SET: the workspace directories whose contents
// must differ between the host and the jail, because what is in them is built for one
// side's platform (a `.venv` holds interpreter links, a `node_modules` native builds).
//
// The set is `.venv` ∪ `node_modules` ∪ the venv path a workspace mise config declares ∪
// config `per_side_paths`. The container backends shadow every entry with a per-workspace
// bind (internal/cli/run's venvShadowMountArgs); macos-user has no mount namespace, so it
// discloses the entries the host and the sandbox share and redirects the one tool it can
// (internal/macosuser's buildPlan). One package, so the two backends cannot disagree about
// which paths are in the set. Pure apart from reading the workspace's mise configs.
package perside

import (
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// DefaultRels is the set every workspace gets without declaring anything: `.venv`,
// `node_modules` and, when a workspace mise config declares one, its venv path. Sorted.
func DefaultRels(workspace string) []string {
	rels := map[string]struct{}{".venv": {}, "node_modules": {}}
	if miseVenv, ok := MiseConfigVenvPathFromDir(workspace); ok && miseVenv != "" {
		rels[miseVenv] = struct{}{}
	}
	return sortedKeys(rels)
}

// UserRels is config `per_side_paths` as written: its string entries, in order,
// unvalidated. Non-string entries are dropped, as every config list reader here drops them.
func UserRels(cfg *jsonx.OrderedMap) []string {
	if cfg == nil {
		return nil
	}
	v, ok := cfg.Get("per_side_paths")
	if !ok || v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ShadowCandidates is the whole per-side set, sorted, deduped and unvalidated: DefaultRels
// ∪ UserRels. A caller that acts on an entry validates it first (ValidRel).
func ShadowCandidates(cfg *jsonx.OrderedMap, workspace string) []string {
	rels := map[string]struct{}{}
	for _, r := range DefaultRels(workspace) {
		rels[r] = struct{}{}
	}
	for _, r := range UserRels(cfg) {
		rels[r] = struct{}{}
	}
	return sortedKeys(rels)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for r := range m {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// ValidRel reports whether rel is a shadowable workspace sub-path: non-empty, not ".",
// relative (no leading "/"), no ".." traversal component, and no unresolved tera template
// ("{{" or "{%").
func ValidRel(rel string) bool {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "/") {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return false
		}
	}
	if strings.Contains(rel, "{{") || strings.Contains(rel, "{%") {
		return false
	}
	return true
}

var configRootRe = regexp.MustCompile(`^\{\{\s*config_root\s*\}\}/`)

// miseVenvConfigFiles is the fixed 4-file parse order, LAST hit wins (base then
// jail-env, plain then dotted).
var miseVenvConfigFiles = []string{"mise.toml", ".mise.toml", "mise.jail.toml", ".mise.jail.toml"}

// MiseConfigVenvPath resolves env._.python.venv from a workspace's mise configs — DISTINCT
// from tomlx.MiseVenvPath (the entrypoint's first-hit-wins/create-gated discovery). Here:
// parse the 4 files in fixed order, LAST hit wins; a string value is the path; a table's
// "path" (default ".venv") is used with NO create requirement; a leading "{{config_root}}/"
// template is stripped (any other template left verbatim); parse errors read as absent.
// resolveFile(fname) returns the decoded TOML map + whether the file exists/parsed (inject a
// real reader; see MiseConfigVenvPathFromDir).
func MiseConfigVenvPath(resolveFile func(fname string) (map[string]any, bool)) (string, bool) {
	found := ""
	haveFound := false
	for _, fname := range miseVenvConfigFiles {
		data, ok := resolveFile(fname)
		if !ok {
			continue
		}
		node := venvNode(data)
		switch t := node.(type) {
		case string:
			found, haveFound = t, true
		case map[string]any:
			path, ok := t["path"].(string)
			if !ok {
				path = ".venv"
			}
			found, haveFound = path, true
		}
	}
	if haveFound && found != "" {
		found = configRootRe.ReplaceAllString(found, "")
		return found, true
	}
	return "", false
}

// MiseConfigVenvPathFromDir is MiseConfigVenvPath backed by the real filesystem, decoding
// each <dir>/<fname> via tomlx. A missing/undecodable file is skipped.
func MiseConfigVenvPathFromDir(dir string) (string, bool) {
	return MiseConfigVenvPath(func(fname string) (map[string]any, bool) {
		data, err := tomlx.DecodeFile(dir + "/" + fname)
		if err != nil {
			return nil, false
		}
		return data, true
	})
}

// venvNode walks data["env"]["_"]["python"]["venv"], returning the leaf (string or map) or
// nil.
func venvNode(data map[string]any) any {
	var node any = data
	for _, key := range []string{"env", "_", "python", "venv"} {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[key]
	}
	return node
}
