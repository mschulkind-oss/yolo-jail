package entrypoint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// dumpJSONIndent2 renders v as json.dumps(v, indent=2) + "\n" — the form every
// agent-config writer uses (insertion-order preserving, ensure_ascii).
func dumpJSONIndent2(v any) string {
	s, _ := jsonx.DumpsIndent(v, 2)
	return s + "\n"
}

// managedSidecar renders json.dumps(sorted(keys), indent=2) + "\n" — the
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
// object. This unifies the various `if path.exists(): try json.loads except
// JSONDecodeError: {}` patterns across the writers. (A file that is valid JSON
// but not an object never occurs in real agent configs or the test corpus; the
// Python code would raise-and-catch on that edge, writing nothing — noted, not
// reproduced.)
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

// setDefaultMap mirrors dict.setdefault(key, {}) where the default is a fresh
// object: returns the existing value if it is an OrderedMap, otherwise sets and
// returns a new empty OrderedMap. (If the existing value is a non-object,
// Python's later item access would raise; not reproduced — see loadObject.)
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

// setDefault mirrors dict.setdefault(key, default) for scalar defaults: only
// sets (and returns) default when key is absent.
func setDefault(m *jsonx.OrderedMap, key string, def any) any {
	if v, ok := m.Get(key); ok {
		return v
	}
	m.Set(key, def)
	return def
}

// updateFrom mirrors dict.update(other): for each key in other (insertion
// order), set it in m (existing key keeps position, new key appended).
func updateFrom(m, other *jsonx.OrderedMap) {
	for _, k := range other.Keys() {
		v, _ := other.Get(k)
		m.Set(k, v)
	}
}

// baseName mirrors pathlib.Path(cmd).name — the final path component. An empty
// string or a trailing-slash path follow Python's PurePosixPath.name rules.
func baseName(p string) string {
	return string(pathName(p))
}

// pathName replicates PurePosixPath(p).name: drop trailing slashes, take the
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

// ConfigureCopilot mirrors agent_configs.configure_copilot.
func ConfigureCopilot(e *Env) error {
	dir := e.CopilotDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// config.json: write {"yolo": true}\n only if missing (literal string).
	configJSON := filepath.Join(dir, "config.json")
	if !pathExists(configJSON) {
		if err := writeInPlaceString(configJSON, "{\"yolo\": true}\n"); err != nil {
			return err
		}
	}
	// mcp-config.json.
	mcpConfig := jsonx.NewOrderedMap()
	mcpConfig.Set("mcpServers", e.LoadMCPServers())
	if err := writeInPlaceString(filepath.Join(dir, "mcp-config.json"), dumpJSONIndent2(mcpConfig)); err != nil {
		return err
	}
	// lsp-config.json.
	servers := LoadLSPServers(e)
	lspConfig := jsonx.NewOrderedMap()
	lspServers := jsonx.NewOrderedMap()
	for _, name := range servers.Keys() {
		v, _ := servers.Get(name)
		cfg, _ := v.(*jsonx.OrderedMap)
		entry := jsonx.NewOrderedMap()
		entry.Set("command", getOr(cfg, "command", nil))
		entry.Set("args", getOr(cfg, "args", []any{}))
		entry.Set("fileExtensions", getOr(cfg, "fileExtensions", jsonx.NewOrderedMap()))
		lspServers.Set(name, entry)
	}
	lspConfig.Set("lspServers", lspServers)
	return writeInPlaceString(filepath.Join(dir, "lsp-config.json"), dumpJSONIndent2(lspConfig))
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

// ConfigurePi mirrors agent_configs.configure_pi.
func ConfigurePi(e *Env) error {
	dir := e.PiDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	settingsPath := filepath.Join(dir, "settings.json")
	settings := loadObject(settingsPath)

	// Host→jail three-way merge (mirrors agent_configs.configure_pi): fill from
	// the host's ~/.pi/agent/settings.json, reusing the SAME agent-agnostic
	// merge the claude path uses, against the pi snapshot. The jail-managed
	// defaultProjectTrust is forced AFTER the merge so it always wins.
	hostSettings := e.loadHostPiSettings()
	prevSynced := loadObject(e.PiHostSettingsSnapshotPath())
	syncHostSettings(settings, hostSettings, prevSynced)
	if err := writeInPlaceString(e.PiHostSettingsSnapshotPath(), dumpJSONIndent2(hostSettings)); err != nil {
		return err
	}

	settings.Set("defaultProjectTrust", "always")
	return writeInPlaceString(settingsPath, dumpJSONIndent2(settings))
}

// loadHostPiSettings mirrors agent_configs._load_host_pi_settings: reads
// /ctx/host-pi/settings.json only when YOLO_HOST_PI_FILES lists it.
func (e *Env) loadHostPiSettings() *jsonx.OrderedMap {
	files := e.hostPiFiles()
	if !contains(files, "settings.json") {
		return jsonx.NewOrderedMap()
	}
	return loadObject("/ctx/host-pi/settings.json")
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

// ConfigureOpencode mirrors agent_configs.configure_opencode.
func ConfigureOpencode(e *Env) error {
	dir := e.OpencodeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "opencode.json")
	managedPath := filepath.Join(dir, "yolo-managed-mcp-servers.json")

	// Translate shared MCP servers into opencode's native schema.
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

	current := loadObject(configPath)
	setDefault(current, "$schema", "https://opencode.ai/config.json")
	current.Set("permission", "allow")

	// Reconcile yolo-managed MCP servers.
	var mcp *jsonx.OrderedMap
	if v, ok := current.Get("mcp"); ok {
		if m, isMap := v.(*jsonx.OrderedMap); isMap {
			mcp = m
		}
	}
	if mcp == nil {
		mcp = jsonx.NewOrderedMap()
	}
	for _, name := range loadManagedSet(managedPath) {
		mcp.Delete(name)
	}
	updateFrom(mcp, opencodeMCP)
	if mcp.Len() > 0 {
		current.Set("mcp", mcp)
	} else if _, ok := current.Get("mcp"); ok {
		current.Delete("mcp")
	}

	if err := writeInPlaceString(configPath, dumpJSONIndent2(current)); err != nil {
		return err
	}
	return writeInPlaceString(managedPath, managedSidecar(opencodeMCP.Keys()))
}

// ConfigureGemini mirrors agent_configs.configure_gemini.
func ConfigureGemini(e *Env) error {
	dir := e.GeminiDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "settings.json")
	managedPath := e.GeminiManagedMCPPath()
	goBin := e.GoBin()

	configured := e.LoadMCPServers()

	// Add LSP servers wrapped as MCP via mcp-language-server.
	lspServers := LoadLSPServers(e)
	for _, name := range lspServers.Keys() {
		v, _ := lspServers.Get(name)
		cfg, _ := v.(*jsonx.OrderedMap)
		cmd, _ := stringValue(cfg, "command")
		bareCmd := baseName(cmd)
		mcpArgs := []any{"-lsp", bareCmd, "-workspace", "/workspace"}
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

	current := loadObject(configPath)
	currentMCP := setDefaultMap(current, "mcpServers")

	// previous_managed: the sidecar list, or the migration fallback.
	previousManaged, ok := loadManagedSetOK(managedPath)
	if !ok {
		previousManaged = map[string]struct{}{
			"chrome-devtools":     {},
			"sequential-thinking": {},
		}
		for _, name := range currentMCP.Keys() {
			v, _ := currentMCP.Get(name)
			cfg, isMap := v.(*jsonx.OrderedMap)
			if !isMap {
				continue
			}
			command := ""
			if cv, ok := cfg.Get("command"); ok {
				command = pyStr(cv)
			}
			if strings.HasSuffix(name, "-lsp") && command == filepath.Join(goBin, "mcp-language-server") {
				previousManaged[name] = struct{}{}
			}
			if strings.HasPrefix(command, "/workspace/") {
				previousManaged[name] = struct{}{}
			}
		}
	}
	for name := range previousManaged {
		currentMCP.Delete(name)
	}
	updateFrom(currentMCP, configured)

	security := setDefaultMap(current, "security")
	setDefault(security, "approvalMode", "yolo")
	setDefault(security, "enablePermanentToolApproval", true)
	general := setDefaultMap(current, "general")
	general.Set("enableAutoUpdate", false)
	general.Set("enableAutoUpdateNotification", false)

	if err := writeInPlaceString(configPath, dumpJSONIndent2(current)); err != nil {
		return err
	}
	return writeInPlaceString(managedPath, managedSidecar(configured.Keys()))
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
