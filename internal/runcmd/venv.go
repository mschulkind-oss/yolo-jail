package runcmd

import (
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// ValidPerSideRel reports whether rel is a shadowable workspace sub-path:
// non-empty, not ".", relative (no leading "/"), no ".." traversal component,
// and no unresolved tera template ("{{" or "{%").
func ValidPerSideRel(rel string) bool {
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

// miseVenvConfigFiles is the fixed 4-file order _mise_config_venv_path parses,
// LAST hit wins (base then jail-env, plain then dotted).
var miseVenvConfigFiles = []string{"mise.toml", ".mise.toml", "mise.jail.toml", ".mise.jail.toml"}

// MiseConfigVenvPath resolves env._.python.venv from a workspace's mise configs,
// mirroring run_cmd._mise_config_venv_path — DISTINCT from tomlx.MiseVenvPath
// (the entrypoint's first-hit-wins/create-gated discovery). Here: parse the 4
// files in fixed order, LAST hit wins; a string value is the path; a table's
// "path" (default ".venv") is used with NO create requirement; a leading
// "{{config_root}}/" template is stripped (any other template left verbatim);
// parse errors read as absent. resolveFile(fname) returns the decoded TOML map +
// whether the file exists/parsed (inject a real reader; see MiseConfigVenvPathFromDir).
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

// MiseConfigVenvPathFromDir is MiseConfigVenvPath backed by the real filesystem,
// decoding each <dir>/<fname> via tomlx. A missing/undecodable file is skipped.
func MiseConfigVenvPathFromDir(dir string) (string, bool) {
	return MiseConfigVenvPath(func(fname string) (map[string]any, bool) {
		data, err := tomlx.DecodeFile(dir + "/" + fname)
		if err != nil {
			return nil, false
		}
		return data, true
	})
}

// venvNode walks data["env"]["_"]["python"]["venv"], returning the leaf
// (string or map) or nil.
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
