package entrypoint

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// claudeLSPPluginOrder pins the iteration order used when enabling LSP plugins;
// the effect on enabledPlugins is order-independent for distinct keys, but the
// order is fixed for deterministic output.
var claudeLSPPluginOrder = []struct{ lsp, plugin string }{
	{"python", "pyright-lsp@claude-plugins-official"},
	{"typescript", "typescript-lsp@claude-plugins-official"},
	{"go", "gopls-lsp@claude-plugins-official"},
}

// oauthTokenKeys / oauthMetadataKeys are the OAuth credential field names.
var oauthTokenKeys = []string{"accessToken", "refreshToken", "expiresAt"}
var oauthMetadataKeys = []string{"scopes", "subscriptionType", "rateLimitTier"}

// hostClaudeDir is the read-only mount of the host's ~/.claude/ (a var so tests
// can point it at a temp dir, mirroring hostPiDir). The prism reads the host
// settings source from here, gated by the host_claude_files allow-list.
var hostClaudeDir = "/ctx/host-claude"

// configureClaudeSideEffects runs the three non-content side effects that
// ConfigureClaudePrism must perform, in order: the credentials symlink
// (harvest/link ~/.claude/.credentials.json into the shared dir), the host-file
// staging (copy every host_claude_files entry EXCEPT settings.json into
// ~/.claude/), and per-jail history isolation (symlink history.jsonl to a
// per-workspace file). These are runtime-state / filesystem side effects, NOT
// surface content, so they stay bespoke under the prism — only settings.json is
// prism-rendered.
func configureClaudeSideEffects(e *Env) error {
	if err := e.ensureCredentialsSymlink(); err != nil {
		return err
	}
	if err := e.syncHostClaudeFiles(); err != nil {
		return err
	}
	return e.isolateClaudeHistory()
}

// writeClaudeJSON builds ~/.claude.json (the user-scoped MCP + workspace-project
// surface) and its managed-MCP sidecar from the reconciled MCP-server table.
// This is claude's RUNTIME-STATE config file — the AGENTS.md invariant is that
// it must NEVER be wiped — so it stays BESPOKE under the prism: the mcpServers
// block is reconciled against the yolo-managed-mcp-servers.json sidecar (prune
// previously-managed names, then re-add the freshly configured set) and the
// workspace project is force-trusted. Extracted so ConfigureClaude and
// ConfigureClaudePrism share exactly one implementation of the .claude.json
// write. `configured` is the LoadMCPServers() table the caller already loaded.
func writeClaudeJSON(e *Env, configured *jsonx.OrderedMap) error {
	claudeJSONPath := e.ClaudeJSONPath()
	claudeJSON := loadObject(claudeJSONPath)
	mcpServers := setDefaultMap(claudeJSON, "mcpServers")
	for _, name := range loadManagedSet(e.ClaudeManagedMCPPath()) {
		mcpServers.Delete(name)
	}
	updateFrom(mcpServers, configured)

	projects := setDefaultMap(claudeJSON, "projects")
	workspaceProject := setDefaultMap(projects, e.WorkspaceDir())
	workspaceProject.Set("enableAllProjectMcpServers", true)
	setDefault(workspaceProject, "hasTrustDialogAccepted", true)

	if err := writeInPlaceString(claudeJSONPath, dumpJSONIndent2(claudeJSON)); err != nil {
		return err
	}
	return writeInPlaceString(e.ClaudeManagedMCPPath(), managedSidecar(configured.Keys()))
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

// ~/.claude/ files (except settings.json) into the jail. This is a filesystem
// side effect that materializes real files; it belongs to content generation
// insofar as it produces files, but its SOURCE is /ctx/host-claude which the
// tree golden's env matrix controls. Best-effort per file.
func (e *Env) syncHostClaudeFiles() error {
	files := e.hostClaudeFiles()
	hostDir := hostClaudeDir
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
		// Copy the file content; mode preservation is a best-effort detail not
		// exercised by the golden's env matrix.
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.WriteFile(dst, data, 0o644)
	}
	return nil
}

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
		// perJail is absolute, so a matching absolute symlink target means done.
		if target == perJail {
			return nil
		}
	}
	_ = os.Remove(historyFile)
	return os.Symlink(perJail, historyFile)
}

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
	// the entrypoint
	// dir is a rw DIRECTORY bind mount where rename works, unlike the file->file
	// bind mounts WriteInPlace guards). Preserve it exactly.
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
		// A non-numeric string yields 0; a numeric string parses. Real records
		// store an integer, so this is rare.
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
