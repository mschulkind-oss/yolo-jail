package entrypoint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// dumpJSONIndent2 renders v as indent-2 JSON + "\n" — the form every
// agent-config writer uses (insertion-order preserving, ASCII-only).
func dumpJSONIndent2(v any) string {
	s, _ := jsonx.DumpsIndent(v, 2)
	return s + "\n"
}

// managedSidecar renders the sorted keys as an indent-2 JSON array + "\n" — the
// yolo-managed-mcp-servers.json sidecar format.
func managedSidecar(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	arr := make([]any, len(sorted))
	for i, k := range sorted {
		arr[i] = k
	}
	s, _ := jsonx.DumpsIndent(arr, 2)
	return s + "\n"
}

// loadObject reads path and decodes it as a JSON object, returning an empty
// OrderedMap when the file is missing, unreadable, unparseable, or not an
// object. This unifies the "read a JSON object, defaulting to {}" pattern used
// across the writers. (A file that is valid JSON but not an object never occurs
// in real agent configs or the test corpus, so that edge writes nothing.)
func loadObject(path string) *jsonx.OrderedMap {
	raw, err := os.ReadFile(path)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	decoded, err := jsonx.Decode(raw)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return jsonx.NewOrderedMap()
	}
	return m
}

// object: returns the existing value if it is an OrderedMap, otherwise sets and
// returns a new empty OrderedMap. (A non-object at the key never occurs in
// practice — see loadObject.)
func setDefaultMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if v, ok := m.Get(key); ok {
		if om, isMap := v.(*jsonx.OrderedMap); isMap {
			return om
		}
	}
	sub := jsonx.NewOrderedMap()
	m.Set(key, sub)
	return sub
}

// sets (and returns) default when key is absent.
func setDefault(m *jsonx.OrderedMap, key string, def any) any {
	if v, ok := m.Get(key); ok {
		return v
	}
	m.Set(key, def)
	return def
}

// order), set it in m (existing key keeps position, new key appended).
func updateFrom(m, other *jsonx.OrderedMap) {
	for _, k := range other.Keys() {
		v, _ := other.Get(k)
		m.Set(k, v)
	}
}

// baseName returns the final path segment; an empty string or a trailing-slash
// path resolve per pathName's rules.
func baseName(p string) string {
	return string(pathName(p))
}

// pathName returns the POSIX path basename: drop trailing slashes, take the
// last segment; "" for "/" or "".
func pathName(p string) string {
	// Strip trailing slashes.
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	if p == "" || p == "/" {
		return ""
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// getOr returns m[key] if present, else def. A nil map yields def.
func getOr(m *jsonx.OrderedMap, key string, def any) any {
	if m == nil {
		return def
	}
	if v, ok := m.Get(key); ok {
		return v
	}
	return def
}

// hostPiDir is the read-only mount of the host's ~/.pi/agent/ (a var so tests
// can point it at a temp dir; mirrors boot.go's hostNvimConfig).
var hostPiDir = "/ctx/host-pi"

// syncHostPiFiles copies each host_pi_files entry (except settings.json, which
// is prism-rendered by ConfigurePiPrism) from the read-only /ctx/host-pi mount
// into the jail's ~/.pi/agent/. Mirrors claude's syncHostClaudeFiles;
// best-effort per file.
func (e *Env) syncHostPiFiles() error {
	files := e.hostPiFiles()
	for _, fname := range files {
		if fname == "settings.json" {
			continue
		}
		src := filepath.Join(hostPiDir, fname)
		dst := filepath.Join(e.PiDir(), fname)
		if !pathExists(src) {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			e.warn("Warning: could not copy host pi file " + fname + ": " + err.Error())
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.WriteFile(dst, data, 0o644)
	}
	return nil
}

// hostPiFiles parses YOLO_HOST_PI_FILES (a JSON list, default []).
func (e *Env) hostPiFiles() []string {
	raw := e.Getenv("YOLO_HOST_PI_FILES")
	if raw == "" {
		raw = "[]"
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	arr, ok := decoded.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// buildOpencodeMCPServers translates the live shared MCP servers
// (LoadMCPServers) into opencode's NATIVE mcp schema: each server becomes an
// object {type:"local", command:[cmd, ...args], enabled:true, environment:{...}}
// (environment omitted when empty). This is the raw yolo-owned table BEFORE any
// sidecar/overlay reconciliation — shared by the bespoke ConfigureOpencode
// (which reconciles it against the yolo-managed sidecar) and
// ConfigureOpencodePrism (which hands it to the engine as the computed layer,
// where the last_render anchor makes the sidecar reconcile unnecessary).
func buildOpencodeMCPServers(e *Env) *jsonx.OrderedMap {
	configured := e.LoadMCPServers()
	opencodeMCP := jsonx.NewOrderedMap()
	for _, name := range configured.Keys() {
		v, _ := configured.Get(name)
		cfg, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		command := []any{getOr(cfg, "command", "")}
		if args, ok := cfg.Get("args"); ok {
			if arr, isArr := args.([]any); isArr {
				command = append(command, arr...)
			}
		}
		entry := jsonx.NewOrderedMap()
		entry.Set("type", "local")
		entry.Set("command", command)
		entry.Set("enabled", true)
		if envVal, ok := cfg.Get("env"); ok {
			if envMap, isMap := envVal.(*jsonx.OrderedMap); isMap && envMap.Len() > 0 {
				entry.Set("environment", envMap)
			}
		}
		opencodeMCP.Set(name, entry)
	}
	return opencodeMCP
}

// buildGeminiMCPServers builds the full MCP-server table gemini owns each boot:
// the live shared MCP servers (LoadMCPServers) plus every configured LSP server
// wrapped as an MCP server via mcp-language-server (keyed "<lsp>-lsp"). This is
// the raw yolo-owned set BEFORE any sidecar/overlay reconciliation — shared by
// the bespoke ConfigureGemini (which reconciles it against the managed sidecar)
// and ConfigureGeminiPrism (which hands it to the engine as the computed layer,
// where the last_render anchor makes the sidecar reconcile unnecessary).
func buildGeminiMCPServers(e *Env) *jsonx.OrderedMap {
	goBin := e.GoBin()
	configured := e.LoadMCPServers()

	lspServers := LoadLSPServers(e)
	for _, name := range lspServers.Keys() {
		v, _ := lspServers.Get(name)
		cfg, _ := v.(*jsonx.OrderedMap)
		cmd, _ := stringValue(cfg, "command")
		bareCmd := baseName(cmd)
		mcpArgs := []any{"-lsp", bareCmd, "-workspace", e.WorkspaceDir()}
		if lspArgs, ok := cfg.Get("args"); ok {
			if arr, isArr := lspArgs.([]any); isArr && len(arr) > 0 {
				mcpArgs = append(mcpArgs, "--")
				mcpArgs = append(mcpArgs, arr...)
			}
		}
		entry := jsonx.NewOrderedMap()
		entry.Set("command", filepath.Join(goBin, "mcp-language-server"))
		entry.Set("args", mcpArgs)
		configured.Set(name+"-lsp", entry)
	}
	return configured
}

// loadManagedSet returns the sidecar's server names as a set, or an empty set
// on any error (the reconcile only needs the names for pop).
func loadManagedSet(path string) []string {
	s, _ := loadManagedSetOK(path)
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	return out
}

// loadManagedSetOK returns the sidecar names as a set and whether the file was
// present-and-a-valid-string-list (the "not a migration" signal for gemini).
func loadManagedSetOK(path string) (map[string]struct{}, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	decoded, err := jsonx.Decode(raw)
	if err != nil {
		return nil, false
	}
	arr, ok := decoded.([]any)
	if !ok {
		return nil, false
	}
	set := map[string]struct{}{}
	for _, e := range arr {
		if s, isStr := e.(string); isStr {
			set[s] = struct{}{}
		}
	}
	return set, true
}
