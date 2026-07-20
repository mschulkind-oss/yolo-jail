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
	agentsJSON := jsonDumpStrings(config.SelectedAgents(merged))

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
		"YOLO_AGENTS":       agentsJSON,
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
		entrypoint.GenerateMiseConfig,
		entrypoint.GenerateMCPWrappers,
	}
	for _, gen := range generators {
		if err := gen(e); err != nil {
			return err.Error()
		}
	}

	agentWriters := map[string]func(*entrypoint.Env) error{
		"copilot":  entrypoint.ConfigureCopilot,
		"gemini":   entrypoint.ConfigureGemini,
		"claude":   entrypoint.ConfigureClaude,
		"opencode": entrypoint.ConfigureOpencode,
		"pi":       entrypoint.ConfigurePi,
		"codex":    entrypoint.ConfigureCodex,
	}
	for _, agent := range entrypoint.LoadAgents(e) {
		if writer, ok := agentWriters[agent]; ok {
			if err := writer(e); err != nil {
				return err.Error()
			}
		}
	}

	// Validate that each agent's output files are parseable.
	type outputSpec struct {
		path  string
		parse func([]byte) error
	}
	parseJSON := func(data []byte) error {
		_, err := jsonx.Decode(data)
		return err
	}
	agentOutputs := map[string][]outputSpec{
		"copilot": {
			{filepath.Join(e.CopilotDir(), "mcp-config.json"), parseJSON},
			{filepath.Join(e.CopilotDir(), "lsp-config.json"), parseJSON},
		},
		"gemini":   {{filepath.Join(e.GeminiDir(), "settings.json"), parseJSON}},
		"claude":   {{filepath.Join(e.ClaudeDir(), "settings.json"), parseJSON}},
		"opencode": {{filepath.Join(e.OpencodeDir(), "opencode.json"), parseJSON}},
		"pi":       {{filepath.Join(e.PiDir(), "settings.json"), parseJSON}},
		"codex":    {{filepath.Join(e.CodexDir(), "config.toml"), parseToml}},
	}
	for _, agent := range entrypoint.LoadAgents(e) {
		for _, spec := range agentOutputs[agent] {
			data, err := os.ReadFile(spec.path)
			if err != nil {
				return agent + ": " + err.Error()
			}
			if err := spec.parse(data); err != nil {
				return agent + " config parse error: " + err.Error()
			}
		}
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

func jsonDumpStrings(ss []string) string {
	arr := make([]any, len(ss))
	for i, s := range ss {
		arr[i] = s
	}
	s, _ := jsonx.DumpsCompact(arr)
	return s
}

// parseToml is a minimal TOML validity check — the codex config.toml is simple
// enough that checking for decode errors via the tomlx package suffices.
func parseToml(data []byte) error {
	_, err := tomlx.Decode(data)
	return err
}
