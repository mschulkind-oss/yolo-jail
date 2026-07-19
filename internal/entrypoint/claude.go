package entrypoint

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// claudeLSPPluginMap mirrors agent_configs.CLAUDE_LSP_PLUGIN_MAP. Order is used
// only for iteration where Python iterates dict.items(); the effect on
// enabledPlugins is order-independent for distinct keys, but we keep the same
// declaration order for faithfulness.
var claudeLSPPluginOrder = []struct{ lsp, plugin string }{
	{"python", "pyright-lsp@claude-plugins-official"},
	{"typescript", "typescript-lsp@claude-plugins-official"},
	{"go", "gopls-lsp@claude-plugins-official"},
}

// oauthTokenKeys / oauthMetadataKeys mirror the module constants.
var oauthTokenKeys = []string{"accessToken", "refreshToken", "expiresAt"}
var oauthMetadataKeys = []string{"scopes", "subscriptionType", "rateLimitTier"}

// ConfigureClaude mirrors agent_configs.configure_claude's CONTENT generation:
// settings.json (three-way host merge + permissions + plugins + LSP tool),
// the host-settings snapshot, ~/.claude.json (MCP + workspace project), the
// managed-MCP sidecar, the credentials symlink/harvest, and per-jail history
// isolation. The `claude plugins install/uninstall` subprocesses
// (_install_claude_plugins) are a side effect, not content, and are deferred to
// the boot sub-phase.
func ConfigureClaude(e *Env) error {
	dir := e.ClaudeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	settingsPath := filepath.Join(dir, "settings.json")
	claudeJSONPath := e.ClaudeJSONPath()

	configured := e.LoadMCPServers()

	if err := e.ensureCredentialsSymlink(); err != nil {
		return err
	}
	if err := e.syncHostClaudeFiles(); err != nil {
		return err
	}
	if err := e.isolateClaudeHistory(); err != nil {
		return err
	}

	// The whole settings/claude.json block is wrapped in try/except in Python
	// (prints "Error configuring Claude: {e}" on failure). We surface IO errors
	// as returned errors, matching best-effort-never-abort at the boot layer.
	hostSettings := e.loadHostClaudeSettings()

	settings := loadObject(settingsPath)

	prevSynced := loadObject(e.ClaudeHostSettingsSnapshotPath())
	syncHostSettings(settings, hostSettings, prevSynced)
	if err := writeInPlaceString(e.ClaudeHostSettingsSnapshotPath(), dumpJSONIndent2(hostSettings)); err != nil {
		return err
	}

	settings.Delete("mcpServers")

	permissions := setDefaultMap(settings, "permissions")
	permissions.Set("allow", []any{})
	permissions.Set("deny", []any{})
	permissions.Set("defaultMode", "acceptEdits")
	permissions.Set("additionalDirectories", []any{"/"})
	settings.Set("skipDangerousModePermissionPrompt", true)

	setDefaultMap(settings, "preferences").Set("autoUpdaterStatus", "disabled")

	lspServers := LoadLSPServers(e)
	enabledPlugins := setDefaultMap(settings, "enabledPlugins")
	for _, pm := range claudeLSPPluginOrder {
		if _, ok := lspServers.Get(pm.lsp); ok {
			enabledPlugins.Set(pm.plugin, true)
		} else {
			enabledPlugins.Delete(pm.plugin)
		}
	}

	// ENABLE_LSP_TOOL handling.
	if lspServers.Len() > 0 {
		setDefaultMap(settings, "env").Set("ENABLE_LSP_TOOL", "1")
	} else if v, ok := settings.Get("env"); ok {
		if envBlock, isMap := v.(*jsonx.OrderedMap); isMap {
			envBlock.Delete("ENABLE_LSP_TOOL")
			if envBlock.Len() == 0 {
				settings.Delete("env")
			}
		}
	}

	if err := writeInPlaceString(settingsPath, dumpJSONIndent2(settings)); err != nil {
		return err
	}

	// ~/.claude.json: user-scoped MCP servers.
	claudeJSON := loadObject(claudeJSONPath)
	mcpServers := setDefaultMap(claudeJSON, "mcpServers")
	for _, name := range loadManagedSet(e.ClaudeManagedMCPPath()) {
		mcpServers.Delete(name)
	}
	updateFrom(mcpServers, configured)

	projects := setDefaultMap(claudeJSON, "projects")
	workspaceProject := setDefaultMap(projects, "/workspace")
	workspaceProject.Set("enableAllProjectMcpServers", true)
	setDefault(workspaceProject, "hasTrustDialogAccepted", true)

	if err := writeInPlaceString(claudeJSONPath, dumpJSONIndent2(claudeJSON)); err != nil {
		return err
	}
	return writeInPlaceString(e.ClaudeManagedMCPPath(), managedSidecar(configured.Keys()))
}

// loadHostClaudeSettings mirrors agent_configs._load_host_claude_settings.
func (e *Env) loadHostClaudeSettings() *jsonx.OrderedMap {
	files := e.hostClaudeFiles()
	if !contains(files, "settings.json") {
		return jsonx.NewOrderedMap()
	}
	return loadObject("/ctx/host-claude/settings.json")
}

// hostClaudeFiles parses YOLO_HOST_CLAUDE_FILES (a JSON list, default []).
func (e *Env) hostClaudeFiles() []string {
	raw := e.Getenv("YOLO_HOST_CLAUDE_FILES")
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
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// syncHostSettings mirrors agent_configs._sync_host_settings / _sync_settings_level.
func syncHostSettings(jail, host, prev *jsonx.OrderedMap) {
	syncSettingsLevel(jail, host, prev, true)
}

func syncSettingsLevel(jail, host, prev *jsonx.OrderedMap, deep bool) {
	// Roll back keys we synced before that the host no longer has.
	for _, key := range prev.Keys() {
		prevVal, _ := prev.Get(key)
		if _, inHost := host.Get(key); inHost {
			continue
		}
		jailVal, inJail := jail.Get(key)
		if !inJail {
			continue
		}
		if pyEqual(jailVal, prevVal) {
			jail.Delete(key)
		} else if deep {
			prevMap, pOk := prevVal.(*jsonx.OrderedMap)
			jailMap, jOk := jailVal.(*jsonx.OrderedMap)
			if pOk && jOk {
				for _, k := range prevMap.Keys() {
					v, _ := prevMap.Get(k)
					if jv, ok := jailMap.Get(k); ok && pyEqual(jv, v) {
						jailMap.Delete(k)
					}
				}
			}
		}
	}
	// Adds + updates from the current host file.
	for _, key := range host.Keys() {
		hostVal, _ := host.Get(key)
		jailVal, inJail := jail.Get(key)
		if !inJail {
			jail.Set(key, hostVal)
			continue
		}
		hostMap, hOk := hostVal.(*jsonx.OrderedMap)
		jailMap, jOk := jailVal.(*jsonx.OrderedMap)
		if deep && hOk && jOk {
			var prevSub *jsonx.OrderedMap
			if pv, ok := prev.Get(key); ok {
				if pm, isMap := pv.(*jsonx.OrderedMap); isMap {
					prevSub = pm
				}
			}
			if prevSub == nil {
				prevSub = jsonx.NewOrderedMap()
			}
			syncSettingsLevel(jailMap, hostMap, prevSub, false)
			continue
		}
		// elif key in prev and jail[key] == prev[key] and jail[key] != host_val
		if prevVal, inPrev := prev.Get(key); inPrev && pyEqual(jailVal, prevVal) && !pyEqual(jailVal, hostVal) {
			jail.Set(key, hostVal)
		}
	}
}

// syncHostClaudeFiles mirrors agent_configs._sync_host_claude_files: copy host
// ~/.claude/ files (except settings.json) into the jail. This is a filesystem
// side effect that materializes real files; it belongs to content generation
// insofar as it produces files, but its SOURCE is /ctx/host-claude which the
// tree golden's env matrix controls. Best-effort per file.
func (e *Env) syncHostClaudeFiles() error {
	files := e.hostClaudeFiles()
	hostDir := "/ctx/host-claude"
	for _, fname := range files {
		if fname == "settings.json" {
			continue
		}
		src := filepath.Join(hostDir, fname)
		dst := filepath.Join(e.ClaudeDir(), fname)
		if !pathExists(src) {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			e.warn("Warning: could not copy host claude file " + fname + ": " + err.Error())
			continue
		}
		// shutil.copy2 preserves mode; we mirror content (mode preservation is a
		// best-effort detail not exercised by the golden's env matrix).
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.WriteFile(dst, data, 0o644)
	}
	return nil
}

// isolateClaudeHistory mirrors agent_configs._isolate_claude_history: symlink
// ~/.claude/history.jsonl to a per-host-workspace file. YOLO_HOST_DIR keys the
// hash; absent -> no-op.
func (e *Env) isolateClaudeHistory() error {
	hostDir := e.Getenv("YOLO_HOST_DIR")
	if hostDir == "" {
		return nil
	}
	historyDir := filepath.Join(e.ClaudeDir(), "jail-history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return err
	}
	h := sha256Hex(hostDir)[:12]
	perJail := filepath.Join(historyDir, h+".jsonl")
	// touch(exist_ok=True)
	if !pathExists(perJail) {
		f, err := os.OpenFile(perJail, os.O_CREATE, 0o644)
		if err == nil {
			_ = f.Close()
		}
	}
	historyFile := filepath.Join(e.ClaudeDir(), "history.jsonl")
	// If it's already the right symlink, nothing to do.
	if target, err := os.Readlink(historyFile); err == nil {
		// Python compares resolved paths; perJail is absolute so a matching
		// absolute symlink target means done.
		if target == perJail {
			return nil
		}
	}
	_ = os.Remove(historyFile)
	return os.Symlink(perJail, historyFile)
}

// ensureCredentialsSymlink mirrors agent_configs._ensure_credentials_symlink.
func (e *Env) ensureCredentialsSymlink() error {
	link := filepath.Join(e.ClaudeDir(), ".credentials.json")
	target := filepath.Join("..", ".claude-shared-credentials", ".credentials.json")

	if cur, err := os.Readlink(link); err == nil {
		// It's a symlink.
		if cur == target {
			return nil
		}
		_ = os.Remove(link)
	} else if pathExists(link) {
		// A regular file: harvest or legacy-copy, then re-link.
		shared := filepath.Join(e.ClaudeSharedCredentialsDir(), ".credentials.json")
		if !e.harvestCredentialsFile(link, shared) {
			if fi, err := os.Stat(shared); err != nil || fi.Size() == 0 {
				if data, rerr := os.ReadFile(link); rerr == nil {
					_ = os.MkdirAll(filepath.Dir(shared), 0o755)
					_ = os.WriteFile(shared, data, 0o644)
				}
			}
		}
		if err := os.Remove(link); err != nil {
			return nil // can't remove — leave as-is (still works via fallback write)
		}
	}
	return os.Symlink(target, link)
}

// harvestCredentialsFile mirrors agent_configs._harvest_credentials_file.
// Returns false when the local file has no claudeAiOauth dict (caller falls
// back to legacy copy).
func (e *Env) harvestCredentialsFile(link, shared string) bool {
	localRaw, err := os.ReadFile(link)
	if err != nil {
		return false
	}
	localDecoded, err := jsonx.Decode(localRaw)
	if err != nil {
		return false
	}
	localDoc, ok := localDecoded.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	localOAuthVal, _ := localDoc.Get("claudeAiOauth")
	localOAuth, ok := localOAuthVal.(*jsonx.OrderedMap)
	if !ok {
		return false
	}

	sharedDoc := loadObject(shared)
	var sharedOAuth *jsonx.OrderedMap
	if v, ok := sharedDoc.Get("claudeAiOauth"); ok {
		if m, isMap := v.(*jsonx.OrderedMap); isMap {
			sharedOAuth = m
		}
	}
	if sharedOAuth == nil {
		sharedOAuth = jsonx.NewOrderedMap()
	}

	// merged = dict(shared_oauth)
	merged := jsonx.NewOrderedMap()
	updateFrom(merged, sharedOAuth)
	for _, key := range oauthMetadataKeys {
		if v, ok := localOAuth.Get(key); ok && truthy(v) {
			merged.Set(key, v)
		}
	}
	if expiresAtMs(localOAuth) > expiresAtMs(sharedOAuth) {
		for _, key := range oauthTokenKeys {
			if v, ok := localOAuth.Get(key); ok {
				merged.Set(key, v)
			}
		}
	}
	sharedDoc.Set("claudeAiOauth", merged)

	// Atomic tmp+rename with 0o600 — this is the ONE sanctioned tmp+rename in
	// the entrypoint (mirrors the broker's _write_tokens; the shared credentials
	// dir is a rw DIRECTORY bind mount where rename works, unlike the file->file
	// bind mounts fsx.WriteInPlace guards). Preserve it exactly.
	blob := []byte(func() string { s, _ := jsonx.DumpsIndent(sharedDoc, 2); return s }())
	tmp, err := os.CreateTemp(filepath.Dir(shared), filepath.Base(shared)+".tmp.")
	if err != nil {
		return false
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return false
	}
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return false
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return false
	}
	if err := os.Rename(tmpName, shared); err != nil {
		_ = os.Remove(tmpName)
		return false
	}
	return true
}

// expiresAtMs mirrors agent_configs._expires_at_ms: int(oauth["expiresAt"] or 0),
// missing/garbage -> 0.
func expiresAtMs(oauth *jsonx.OrderedMap) int64 {
	v, ok := oauth.Get("expiresAt")
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		// int("...") in Python would raise on non-numeric -> caught -> 0. A
		// numeric string parses. Real records store an integer, so this is rare.
		var n int64
		neg := false
		s := t
		if s == "" {
			return 0
		}
		if s[0] == '-' {
			neg = true
			s = s[1:]
		}
		if s == "" {
			return 0
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int64(c-'0')
		}
		if neg {
			return -n
		}
		return n
	default:
		// jsonInt literal.
		if isJSONInt(v) {
			cs, _ := jsonx.DumpsCompact(v)
			var n int64
			neg := false
			s := cs
			if s != "" && s[0] == '-' {
				neg = true
				s = s[1:]
			}
			for _, c := range s {
				n = n*10 + int64(c-'0')
			}
			if neg {
				return -n
			}
			return n
		}
		return 0
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
