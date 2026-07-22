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
	return writeCopilotDynamicConfigs(e, dir)
}

// writeCopilotDynamicConfigs writes the two dynamic sibling files —
// mcp-config.json and lsp-config.json — that copilot regenerates from the live
// mcp_servers / lsp_servers config every boot. They are pure overwrites (no
// in-jail edits are preserved), so they stay bespoke even under the prism, which
// owns only the static config.json. Shared by ConfigureCopilot and
// ConfigureCopilotPrism so the siblings are byte-identical on either path.
func writeCopilotDynamicConfigs(e *Env, dir string) error {
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

// hostPiDir is the read-only mount of the host's ~/.pi/agent/ (a var so tests
// can point it at a temp dir; mirrors boot.go's hostNvimConfig).
var hostPiDir = "/ctx/host-pi"

func ConfigurePi(e *Env) error {
	dir := e.PiDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Install every host_pi_files entry except settings.json into ~/.pi/agent/
	// (settings.json is instead three-way merged below). This mirrors claude's
	// syncHostClaudeFiles — without it, a listed file like models.json is
	// mounted at /ctx/host-pi/ but never lands where pi reads it.
	if err := e.syncHostPiFiles(); err != nil {
		return err
	}
	settingsPath := filepath.Join(dir, "settings.json")
	settings := loadObject(settingsPath)

	// Host→jail three-way merge: fill from
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

// loadHostPiSettings reads
// /ctx/host-pi/settings.json only when YOLO_HOST_PI_FILES lists it.
func (e *Env) loadHostPiSettings() *jsonx.OrderedMap {
	files := e.hostPiFiles()
	if !contains(files, "settings.json") {
		return jsonx.NewOrderedMap()
	}
	return loadObject(filepath.Join(hostPiDir, "settings.json"))
}

// syncHostPiFiles copies each host_pi_files entry (except settings.json, which
// is three-way merged in ConfigurePi) from the read-only /ctx/host-pi mount
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

func ConfigureOpencode(e *Env) error {
	dir := e.OpencodeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "opencode.json")
	managedPath := filepath.Join(dir, "yolo-managed-mcp-servers.json")

	// Translate shared MCP servers into opencode's native schema.
	opencodeMCP := buildOpencodeMCPServers(e)

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

func ConfigureGemini(e *Env) error {
	dir := e.GeminiDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "settings.json")
	managedPath := e.GeminiManagedMCPPath()
	goBin := e.GoBin()

	configured := buildGeminiMCPServers(e)

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
			if strings.HasPrefix(command, e.WorkspaceDir()+"/") {
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
