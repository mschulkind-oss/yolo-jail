package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// validateEnvSourceScope refuses a FILE entry of `env_sources` (`env.files` in the TOML
// spelling) that a WORKSPACE-scope config — yolo-jail.toml or yolo-jail.jsonc, its local
// sibling, and whatever either includes — points outside the workspace: the owner's ruling
// on OQ-AS3 (docs/research/agent-safehouse.md, 2026-10-29), "env sources in a workspace
// should not be able to specify outside the workspace". A workspace config travels with the
// repository, so an entry naming ~/.aws.env or /etc/secrets.env would hand a cloned repo the
// user's host files as the jail's environment. The user's own config (and its per-workspace
// files) may name any path, and inline entries are not paths, so neither is checked.
//
// The path checked is the path the launch reads: ResolveEnvSourcePath over the entry as the
// loader left it, which anchors a relative entry beside its declaring file (AnchorEnvSources)
// and expands "~". Symlinks are then resolved — the deepest existing ancestor, for a file not
// there yet — because os.ReadFile follows them, so an in-workspace link to a host file is the
// host file.
//
// As validateMountScope does, the merged map carries no provenance, so the workspace config
// is re-read to learn which entries are its own — through the format-aware reader, since a
// TOML workspace file is invisible to the legacy one. In a jail validating its own workspace
// the re-read config's absolute entries are host paths, which the host already judged at
// launch, so the check is the host's (validateRWMountSources' reason).
func validateEnvSourceScope(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	if !hasEnvSourceFile(config) || (inJail() && jailOwnWorkspace(workspace)) {
		return
	}
	if workspace == "" {
		workspace = cwd()
	}
	var wsCfg *jsonx.OrderedMap
	var wsNode *srcNode
	var err error
	if len(edit) != 0 && edit[0] != nil {
		wsCfg, wsNode, err = loadValidationWorkspaceConfigWithNode(workspace, edit...)
	} else {
		wsCfg, wsNode, err = loadReadableWorkspaceConfig(workspace, false, func(string) {}, true)
	}
	if err != nil || wsCfg == nil {
		return
	}
	root, err := resolve(workspace)
	if err != nil {
		return
	}
	userPath, _, userTOML, err := resolveUserConfigPath(paths.UserConfigPath())
	if err != nil {
		userPath, userTOML = paths.UserConfigPath(), false
	}
	check := func(key string, list []any, node *srcNode) {
		for idx, entry := range list {
			s, ok := asStr(entry)
			if !ok || s == "" {
				continue
			}
			real := resolveExistingPrefix(ResolveEnvSourcePath(s, workspace))
			if pathWithin(real, root) {
				continue
			}
			add(errs, locatedAt(node.elem(idx), envSourceScopeRefusal(key, s, real, root, userPath, userTOML)))
		}
	}
	check("env_sources", getListOrNilFalsy(wsCfg, "env_sources"), wsNode.key("env_sources"))
	if env, ok := envTable(wsCfg); ok {
		check("env.files", getListOrNilFalsy(env, "files"), wsNode.key("env").key("files"))
	}
}

// envSourceScopeRefusal is the refusal for one entry: what it is, why, and the same entry in
// the user config's own spelling. An absolute or "~" entry means the same thing there; a
// relative one would anchor beside the user config instead, and a symlink is the workspace's to
// repoint, so either is given as the file it reads.
func envSourceScopeRefusal(key, entry, real, root, userPath string, userTOML bool) string {
	fix := real
	reads := " and reads " + real
	if filepath.Clean(expandUser(entry)) == real {
		reads = ""
		if strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "~") {
			fix = entry
		}
	}
	spelled := fmt.Sprintf(`"env_sources": [%q]`, fix)
	if userTOML {
		// The readable key has a verb that appends to it (docs/design/config-commands.md); a
		// legacy file keeps `env_sources`, beside which that verb's `env.files` is refused.
		spelled = fmt.Sprintf("[env] files = [%q] (`yolo config add env.files %s --user`)",
			fix, shquote.Quote(strconv.Quote(fix)))
	}
	return fmt.Sprintf("config.%s: %s is in the workspace config%s, outside the workspace %s. "+
		"A workspace config travels with the repository, so its env files must be inside the "+
		"workspace — move the entry to your user config %s as %s "+
		"(docs/reference/agent-credentials.md#where-an-env_sources-file-may-be)",
		key, entry, reads, root, userPath, spelled)
}

// hasEnvSourceFile reports whether cfg holds any env file entry, in either spelling — the
// merged map holds every workspace entry, so without one there is nothing to re-read.
func hasEnvSourceFile(cfg *jsonx.OrderedMap) bool {
	return HasEnvFileEntry(cfg)
}

// resolveExistingPrefix resolves every symlink in p's longest existing ancestor and appends
// the rest, so a file that does not exist yet is judged by the directory it would be read
// from.
func resolveExistingPrefix(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for cur := p; ; cur = filepath.Dir(cur) {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{real}, rest...)...)
		} else if !os.IsNotExist(err) {
			return p
		}
		if filepath.Dir(cur) == cur {
			return p
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
	}
}

// envTable is cfg's `env` table, the TOML spelling's home for `files`.
func envTable(cfg *jsonx.OrderedMap) (*jsonx.OrderedMap, bool) {
	v, _ := cfg.Get("env")
	return asMap(v)
}

// pathWithin reports whether p is root or below it; both are clean absolute paths.
func pathWithin(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
