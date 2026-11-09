package config

// envlocate.go answers "which config, in which scope, sets this name, and in which set" for the
// env-set verbs and lines (docs/design/secret-env-scoping.md SE-D9, §3.6;
// secret-env-scoping-plan.md SEP-D6, SEP-D11). It reads each scope's own top-level file, as the
// generic edit verbs select it, because that is the file a move writes: an include's entry is
// not a site a verb can edit. It never returns a value.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Env scope flags, as the generic edit verbs spell them.
const (
	EnvScopeUser    = "--user"
	EnvScopeProject = "--project"
	EnvScopeLocal   = "--local"
)

// EnvScopeFile is one scope's top-level config file as the env-set verbs read it.
type EnvScopeFile struct {
	// Scope is the scope flag that selects this file: "--user", "--project" or "--local".
	Scope string
	// ConfigFile is the file's path.
	ConfigFile string
	// Legacy is true for a JSONC (or JSON) file, which the verbs do not edit
	// (secret-env-scoping.md SE-D6, secret-env-scoping-plan.md SEP-D18).
	Legacy bool
	// Anchor is the directory a relative dotenv entry of this file sits beside.
	Anchor string
	values *jsonx.OrderedMap
}

// EnvSite is one place a scope's file sets a name, or one dotenv entry it lists.
type EnvSite struct {
	Place      EnvPlace
	Scope      string
	Legacy     bool
	ConfigFile string
	// Dotenv is the dotenv file the entry resolves to; "" for an inline table.
	Dotenv string
	// Raw is the entry as written in the config file; "" for an inline table.
	Raw string
	// Index is the entry's index in its list (env.files, env.sources, env_sources,
	// env.secrets.files); -1 for a `set` table.
	Index int
}

// IsValidEnvName reports whether name is a variable name a dotenv line or a `set` table can carry.
func IsValidEnvName(name string) bool { return envVarNameRe.MatchString(name) }

// Inline reports whether the site is an inline assignment rather than a dotenv line.
func (s EnvSite) Inline() bool { return s.Dotenv == "" }

// Describe spells a place the way every env-set line and verb prints it
// (secret-env-scoping-plan.md#SEP-D7). It never carries a value.
func (p EnvPlace) Describe() string {
	switch p.Key {
	case "env.set":
		return "([env] set)"
	case "env.secrets.set":
		return "([env.secrets] set)"
	case "env.files":
		return "([env], from " + p.Entry + ")"
	case "env.secrets.files":
		return "([env.secrets], from " + p.Entry + ")"
	case "env.sources":
		if p.Entry == "" {
			return "([env] sources)"
		}
		return "([env], from " + p.Entry + ")"
	case "env_sources":
		if p.Entry == "" {
			return "(env_sources)"
		}
		return "(env_sources, from " + p.Entry + ")"
	}
	return "(" + p.Key + ")"
}

// ReadEnvScopeFiles reads the user config and, unless userOnly or workspace is "", the
// workspace's project and local configs, in that order. A scope with no file is absent.
func ReadEnvScopeFiles(workspace string, userOnly bool) ([]EnvScopeFile, error) {
	userDir := filepath.Dir(paths.UserConfigDisplayPath())
	type candidate struct {
		scope, dir string
		names      []string
	}
	candidates := []candidate{{EnvScopeUser, userDir,
		[]string{paths.UserConfigTOMLName, paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName}}}
	if !userOnly && workspace != "" {
		candidates = append(candidates,
			candidate{EnvScopeProject, workspace, []string{paths.WorkspaceConfigTOMLName,
				paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONName}},
			candidate{EnvScopeLocal, workspace, []string{paths.WorkspaceLocalConfigTOMLName,
				paths.WorkspaceLocalConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONName}})
	}
	var out []EnvScopeFile
	for _, c := range candidates {
		for _, name := range c.names {
			path := filepath.Join(c.dir, name)
			data, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			format := configdoc.JSONC
			if strings.HasSuffix(name, ".toml") {
				format = configdoc.TOML
			}
			doc, err := configdoc.Parse(data, format)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			values := doc.Values
			if values == nil {
				values = jsonx.NewOrderedMap()
			}
			out = append(out, EnvScopeFile{Scope: c.scope, ConfigFile: path, Legacy: format != configdoc.TOML,
				Anchor: c.dir, values: values})
			break
		}
	}
	return out, nil
}

// fileSite is the site of one dotenv entry of this file.
func (f EnvScopeFile) fileSite(key, raw string, index int, private bool) EnvSite {
	entry := raw
	if expanded := expandUser(raw); !strings.HasPrefix(raw, "~") && !filepath.IsAbs(expanded) {
		entry = filepath.Join(f.Anchor, raw)
	}
	return EnvSite{Place: EnvPlace{Private: private, Key: key, Entry: entry}, Scope: f.Scope, Legacy: f.Legacy,
		ConfigFile: f.ConfigFile, Dotenv: ResolveEnvSourcePath(raw, f.Anchor), Raw: raw, Index: index}
}

func (f EnvScopeFile) inlineSite(key string, index int, private bool) EnvSite {
	return EnvSite{Place: EnvPlace{Private: private, Key: key}, Scope: f.Scope, Legacy: f.Legacy,
		ConfigFile: f.ConfigFile, Index: index}
}

// FileEntries is every dotenv entry the file lists in one set, in the order a launch reads them:
// env_sources, env.sources and env.files for the public set, env.secrets.files for the private.
func (f EnvScopeFile) FileEntries(private bool) []EnvSite {
	var out []EnvSite
	if private {
		secrets, _ := asMap(f.envValue("secrets"))
		if secrets == nil {
			return nil
		}
		for i, entry := range getListOrNilFalsy(secrets, "files") {
			if s, ok := asStr(entry); ok {
				out = append(out, f.fileSite("env.secrets.files", s, i, true))
			}
		}
		return out
	}
	for i, entry := range getListOrNilFalsy(f.values, "env_sources") {
		if s, ok := asStr(entry); ok {
			out = append(out, f.fileSite("env_sources", s, i, false))
		}
	}
	env, _ := envTable(f.values)
	if env == nil {
		return out
	}
	for i, entry := range getListOrNilFalsy(env, "sources") {
		if s, ok := asStr(entry); ok {
			out = append(out, f.fileSite("env.sources", s, i, false))
		}
	}
	for i, entry := range getListOrNilFalsy(env, "files") {
		if s, ok := asStr(entry); ok {
			out = append(out, f.fileSite("env.files", s, i, false))
		}
	}
	return out
}

func (f EnvScopeFile) envValue(key string) any {
	env, _ := envTable(f.values)
	if env == nil {
		return nil
	}
	v, _ := env.Get(key)
	return v
}

// InlineValue is the string an inline site assigns name, for a move to carry; ok is false when
// the site holds none.
func (f EnvScopeFile) InlineValue(site EnvSite, name string) (string, bool) {
	var set *jsonx.OrderedMap
	switch site.Place.Key {
	case "env.set":
		set, _ = asMap(f.envValue("set"))
	case "env.secrets.set":
		secrets, _ := asMap(f.envValue("secrets"))
		if secrets != nil {
			v, _ := secrets.Get("set")
			set, _ = asMap(v)
		}
	}
	if set == nil {
		return "", false
	}
	v, present := set.Get(name)
	if !present {
		return "", false
	}
	return asStr(v)
}

// Sites is every place this file sets name, public places first. A dotenv entry is a site
// when the file it names (read now; a missing one is skipped) assigns name.
func (f EnvScopeFile) Sites(name string) []EnvSite {
	var out []EnvSite
	for i, entry := range getListOrNilFalsy(f.values, "env_sources") {
		if m, ok := asMap(entry); ok {
			if _, present := m.Get(name); present {
				out = append(out, f.inlineSite("env_sources", i, false))
			}
		}
	}
	if env, _ := envTable(f.values); env != nil {
		for i, entry := range getListOrNilFalsy(env, "sources") {
			if table, ok := asMap(entry); ok {
				if inline, ok := envSourcesInline(table); ok {
					if set, _ := asMap(mustGet(inline, "set")); set != nil {
						if _, present := set.Get(name); present {
							out = append(out, f.inlineSite("env.sources", i, false))
						}
					}
				}
			}
		}
	}
	for _, private := range []bool{false, true} {
		for _, site := range f.FileEntries(private) {
			data, err := os.ReadFile(site.Dotenv)
			if err != nil {
				continue
			}
			if _, present := ParseDotenv(string(data)).Get(name); present {
				out = append(out, site)
			}
		}
		key := "env.set"
		if private {
			key = "env.secrets.set"
		}
		probe := f.inlineSite(key, -1, private)
		if _, ok := f.InlineValue(probe, name); ok {
			out = append(out, probe)
		}
	}
	return out
}

// LocateEnvName is every site that sets name across the scopes ReadEnvScopeFiles reads.
func LocateEnvName(workspace, name string, userOnly bool) ([]EnvSite, error) {
	files, err := ReadEnvScopeFiles(workspace, userOnly)
	if err != nil {
		return nil, err
	}
	var out []EnvSite
	for _, f := range files {
		out = append(out, f.Sites(name)...)
	}
	return out, nil
}

// EnvNameScope is DisclosureNotes.Scope's answer (secret-env-scoping-plan.md#SEP-D6): " " plus
// the scope flag of the one scope whose config sets name, "" when none or several do, or the
// configs cannot be read. Read on the cold path only, when a line is printed.
func EnvNameScope(workspace string) func(name string) string {
	return func(name string) string {
		sites, err := LocateEnvName(workspace, name, false)
		if err != nil {
			return ""
		}
		scope := ""
		for _, s := range sites {
			if scope != "" && s.Scope != scope {
				return ""
			}
			scope = s.Scope
		}
		if scope == "" {
			return ""
		}
		return " " + scope
	}
}
