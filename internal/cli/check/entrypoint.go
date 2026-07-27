package check

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// runEntrypointPreflight runs the Go entrypoint generators in a temp home with
// the same YOLO_* environment the real jail boot uses. Returns "" on success, or
// the error detail on failure.
func (o *Options) runEntrypointPreflight(r *reporter, _, workspace string, merged *jsonx.OrderedMap) string {
	tmp, err := os.MkdirTemp("", "yolo-check-")
	if err != nil {
		return "could not create temp home: " + err.Error()
	}
	defer os.RemoveAll(tmp)

	normalizedBlocked := securitySection(merged)
	blockedJSON := jsonDump(config.NormalizeBlockedTools(normalizedBlocked))
	miseJSON := jsonDump(config.MergeMiseTools(merged))
	lspJSON := jsonDumpOrEmptyObj(mapOrNil(merged, "lsp_servers"))
	mcpJSON := jsonDumpOrEmptyObj(mapOrNil(merged, "mcp_servers"))
	presetsJSON := jsonDumpOrEmptyList(listOrNil(merged, "mcp_presets"))

	workspaceResolved := workspace
	if r, e := filepath.Abs(workspace); e == nil {
		workspaceResolved = r
	}

	vars := map[string]string{
		"JAIL_HOME":         tmp,
		"HOME":              tmp,
		"NPM_CONFIG_PREFIX": filepath.Join(tmp, ".npm-global"),
		"GOPATH":            filepath.Join(tmp, "go"),
		"MISE_DATA_DIR":     "/mise",
		"YOLO_HOST_DIR":     workspaceResolved,
		"YOLO_BLOCK_CONFIG": blockedJSON,
		"YOLO_MISE_TOOLS":   miseJSON,
		"YOLO_LSP_SERVERS":  lspJSON,
		"YOLO_MCP_SERVERS":  mcpJSON,
		"YOLO_MCP_PRESETS":  presetsJSON,
		// Point prism writers' §5 sidecars (<workspace>/.yolo/prism/) at the temp
		// home, not the real workspace — the preflight is a dry run and must not
		// touch the live workspace. agy (born on the prism) is the first writer
		// here that emits sidecars.
		"YOLO_WORKSPACE": filepath.Join(tmp, "workspace"),
	}

	// env_sources overrides (resolved against the workspace).
	resolvedEnv := config.ResolveEnvSources(workspace, merged, r.warningLine)
	for _, k := range resolvedEnv.Keys() {
		v, _ := resolvedEnv.Get(k)
		vars[k] = asString(v)
	}

	e := entrypoint.NewEnv(vars)

	generators := []func(*entrypoint.Env) error{
		entrypoint.GenerateShims,
		entrypoint.GenerateAgentLaunchers,
		entrypoint.GenerateBashrc,
		entrypoint.GenerateBootstrapScript,
		entrypoint.GenerateVenvPrecreateScript,
		entrypoint.ConfigureMisePrism,
		entrypoint.GenerateMCPWrappers,
	}
	for _, gen := range generators {
		if err := gen(e); err != nil {
			return err.Error()
		}
	}

	// Render EVERY embedded pack's surfaces, then parse each one back.
	//
	// Was two agent-keyed maps: which writer to call per agent, and which files to parse
	// per agent. Both are now derivable from the pack's own declarations — the surface
	// says where it writes and in which codec — so the preflight covers a pack it has
	// never heard of, which is the point of the whole transition. These write §5 sidecars
	// under YOLO_WORKSPACE, pointed at the temp home above so the dry run never touches
	// the live workspace.
	for _, name := range entrypoint.EmbeddedPackNames() {
		if err := entrypoint.ConfigurePackByName(e, name); err != nil {
			return err.Error()
		}
	}
	for _, sf := range entrypoint.EmbeddedPackSurfaces(e) {
		if sf.Unrendered {
			// yolo does not write it, so there is nothing to parse. Reading it would
			// report a missing file for a surface behaving exactly as declared.
			continue
		}
		data, err := os.ReadFile(sf.Path)
		if err != nil {
			return sf.Label + ": " + err.Error()
		}
		switch sf.Codec {
		case "toml":
			if err := parseToml(data); err != nil {
				return sf.Label + " config parse error: " + err.Error()
			}
		case "json":
			if _, err := jsonx.Decode(data); err != nil {
				return sf.Label + " config parse error: " + err.Error()
			}
		}
	}

	// The mise global config renders unconditionally (it is not agent-gated), so
	// validate its TOML separately from the per-agent outputs.
	miseCfg := filepath.Join(e.MiseConfigDir(), "config.toml")
	if data, err := os.ReadFile(miseCfg); err != nil {
		return "mise: " + err.Error()
	} else if err := parseToml(data); err != nil {
		return "mise config parse error: " + err.Error()
	}

	return ""
}

func securitySection(merged *jsonx.OrderedMap) *jsonx.OrderedMap {
	if v, _ := merged.Get("security"); v != nil {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			return m
		}
	}
	return nil
}

func mapOrNil(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if v, _ := m.Get(key); v != nil {
		if mm, ok := v.(*jsonx.OrderedMap); ok {
			return mm
		}
	}
	return nil
}

func listOrNil(m *jsonx.OrderedMap, key string) []any {
	if v, _ := m.Get(key); v != nil {
		if l, ok := v.([]any); ok {
			return l
		}
	}
	return nil
}

func jsonDump(v any) string {
	s, _ := jsonx.DumpsCompact(v)
	return s
}

func jsonDumpOrEmptyObj(m *jsonx.OrderedMap) string {
	if m == nil {
		return "{}"
	}
	s, _ := jsonx.DumpsCompact(m)
	return s
}

func jsonDumpOrEmptyList(l []any) string {
	if l == nil {
		return "[]"
	}
	s, _ := jsonx.DumpsCompact(l)
	return s
}

// parseToml is a minimal TOML validity check — the codex config.toml is simple
// enough that checking for decode errors via the tomlx package suffices.
func parseToml(data []byte) error {
	_, err := tomlx.Decode(data)
	return err
}
