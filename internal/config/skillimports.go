package config

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// SkillImport is one validated workspace declaration for a skill-only Git projection.
type SkillImport struct {
	Handle  string
	Git     string
	Rev     string
	Ref     string
	Path    string
	Only    []string
	OnlySet bool
}

// SkillImports returns declarations in stable handle order. Callers use this after config
// validation; malformed entries are omitted rather than being interpreted partially.
func SkillImports(cfg *jsonx.OrderedMap) []SkillImport {
	if cfg == nil {
		return nil
	}
	value, ok := cfg.Get("skill_imports")
	if !ok {
		return nil
	}
	imports, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	out := make([]SkillImport, 0, imports.Len())
	for _, handle := range imports.Keys() {
		entry, ok := asMap(getMapValue(imports, handle))
		if !ok {
			continue
		}
		git, gitOK := asStr(getMapValue(entry, "git"))
		rev, _ := asStr(getMapValue(entry, "rev"))
		ref, _ := asStr(getMapValue(entry, "ref"))
		pathValue, _ := asStr(getMapValue(entry, "path"))
		if !gitOK || git == "" {
			continue
		}
		if pathValue == "" {
			pathValue = "skills"
		}
		imp := SkillImport{Handle: handle, Git: git, Rev: rev, Ref: ref, Path: pathValue}
		if rawOnly, present := entry.Get("only"); present {
			imp.OnlySet = true
			values, ok := rawOnly.([]any)
			if !ok {
				continue
			}
			for _, value := range values {
				name, ok := value.(string)
				if !ok {
					imp.Only = nil
					break
				}
				imp.Only = append(imp.Only, name)
			}
		}
		out = append(out, imp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

// WorkspaceSkillImportsEnabled is the user-only veto; absence means enabled.
func WorkspaceSkillImportsEnabled(cfg *jsonx.OrderedMap) bool {
	if cfg == nil {
		return true
	}
	value, present := cfg.Get("workspace_skill_imports")
	if !present {
		return true
	}
	enabled, ok := value.(bool)
	return !ok || enabled
}

func validateSkillImports(cfg *jsonx.OrderedMap, errs *[]string) {
	if cfg == nil {
		return
	}
	if raw, present := cfg.Get("workspace_skill_imports"); present {
		if _, ok := raw.(bool); !ok {
			add(errs, "config.workspace_skill_imports: expected a boolean")
		}
	}
	raw, present := cfg.Get("skill_imports")
	if !present {
		return
	}
	imports, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		add(errs, "config.skill_imports: expected a table keyed by handle")
		return
	}
	for _, handle := range imports.Keys() {
		entryPath := "config.skill_imports." + handle
		if !validSkillImportHandle(handle) {
			add(errs, entryPath+": invalid handle; use the pack_sources key characters")
		}
		entry, ok := asMap(getMapValue(imports, handle))
		if !ok {
			add(errs, entryPath+": expected a table")
			continue
		}
		for _, key := range entry.Keys() {
			switch key {
			case "git", "rev", "ref", "path", "only":
			default:
				add(errs, entryPath+"."+key+": unknown skill import field (git, rev, ref, path or only)")
			}
		}
		git, ok := asStr(getMapValue(entry, "git"))
		if !ok || git == "" {
			add(errs, entryPath+".git: expected a non-empty supported Git repository URL")
		} else if err := validateSkillImportGitURL(git); err != nil {
			add(errs, entryPath+".git: "+err.Error())
		}
		rev, hasRev, revOK := optionalString(entry, "rev")
		ref, hasRef, refOK := optionalString(entry, "ref")
		if !revOK {
			add(errs, entryPath+".rev: expected a full commit object id")
		} else if hasRev && !validSkillImportRevision(rev) {
			add(errs, entryPath+".rev: expected a full commit object id (40 or 64 hexadecimal characters)")
		}
		if !refOK {
			add(errs, entryPath+".ref: expected a non-empty branch or tag name")
		} else if hasRef && !validSkillImportRef(ref) {
			add(errs, entryPath+".ref: invalid branch or tag ref")
		}
		if !hasRev && !hasRef {
			add(errs, entryPath+": requires rev or ref")
		}
		if rawPath, hasPath := entry.Get("path"); hasPath {
			pathValue, ok := rawPath.(string)
			if !ok || !validSkillImportPath(pathValue) {
				add(errs, entryPath+".path: must be a canonical repository-relative directory without '..' or '.git'")
			}
		}
		if rawOnly, hasOnly := entry.Get("only"); hasOnly {
			names, ok := rawOnly.([]any)
			if !ok {
				add(errs, entryPath+".only: expected a list of unique immediate-child skill names")
				continue
			}
			seen := map[string]bool{}
			for i, rawName := range names {
				name, ok := rawName.(string)
				if !ok || !validSkillImportName(name) {
					add(errs, fmt.Sprintf("%s.only[%d]: expected a non-empty immediate-child skill name without slash or backslash", entryPath, i))
					continue
				}
				if seen[name] {
					add(errs, fmt.Sprintf("%s.only[%d]: duplicate skill name %q", entryPath, i, name))
				}
				seen[name] = true
			}
		}
	}
}

func optionalString(entry *jsonx.OrderedMap, key string) (value string, present, valid bool) {
	raw, present := entry.Get(key)
	if !present {
		return "", false, true
	}
	value, valid = raw.(string)
	return value, true, valid
}

func validSkillImportHandle(name string) bool {
	return name != "" && name != "." && name != ".." && strings.TrimSpace(name) == name && !strings.ContainsAny(name, `\\:/?#`)
}

func validateSkillImportGitURL(remote string) error {
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return fmt.Errorf("invalid repository URL: %v", err)
		}
		if u.User != nil {
			return fmt.Errorf("URL credentials are not allowed")
		}
	}
	if _, err := gitproto.Identity(remote); err != nil {
		return fmt.Errorf("unsupported transport or repository URL: %v", err)
	}
	return nil
}

func validSkillImportRevision(rev string) bool {
	if len(rev) != 40 && len(rev) != 64 {
		return false
	}
	for _, c := range rev {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func validSkillImportRef(ref string) bool {
	if ref == "" || ref == "HEAD" {
		return false
	}
	name := ref
	if strings.HasPrefix(ref, "refs/heads/") {
		name = strings.TrimPrefix(ref, "refs/heads/")
	} else if strings.HasPrefix(ref, "refs/tags/") {
		name = strings.TrimPrefix(ref, "refs/tags/")
	} else if strings.HasPrefix(ref, "refs/") {
		return false
	}
	if name == "" || name == "HEAD" || name == "@" || strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return false
	}
	for _, c := range []byte(name) {
		if c < 0x20 || c == 0x7f || strings.IndexByte(" ~^:?*[\\", c) >= 0 {
			return false
		}
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func validSkillImportPath(pathValue string) bool {
	if pathValue == "" || strings.HasPrefix(pathValue, "/") || strings.Contains(pathValue, "\\") {
		return false
	}
	if path.Clean(pathValue) != pathValue || pathValue == "." {
		return false
	}
	for _, component := range strings.Split(pathValue, "/") {
		if component == ".." || component == ".git" {
			return false
		}
	}
	return true
}

func validSkillImportName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\\`)
}

func getMapValue(m *jsonx.OrderedMap, key string) any {
	value, _ := m.Get(key)
	return value
}

// validateSkillImportScopes covers legacy JSONC scopes, whose reader predates the readable
// schema. TOML scope is rejected by its source reader before merge.
func validateSkillImportScopes(workspace string, errs *[]string) {
	userPath := paths.UserConfigPath()
	userCfg, err := loadUserScopeConfig(userPath, userPath, false, func(string) {})
	if err == nil && userCfg != nil {
		if _, present := userCfg.Get("skill_imports"); present {
			userFile, _ := UserConfigFile()
			add(errs, "config.skill_imports: workspace-only key; remove it from "+userFile+" and declare it in the workspace's yolo-jail.toml")
		}
	}
	workspaceCfg, err := LoadReadableWorkspaceConfig(workspace, true, func(string) {})
	if err == nil && workspaceCfg != nil {
		if _, present := workspaceCfg.Get("workspace_skill_imports"); present {
			add(errs, "config.workspace_skill_imports: user-scope only; remove it from the workspace config and set it in the user config")
		}
	}
}
