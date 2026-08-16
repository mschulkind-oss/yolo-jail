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

// linkThroughShared replaces link with a symlink to target, harvesting an existing REAL
// file at link into shared first.
//
// The harvest is the load-bearing part and the reason this is not three lines. link may
// hold a credential this jail just obtained, so replacing it with a symlink outright would
// destroy a live login. So: if link is already the right symlink, done; if it is a real
// file, merge its OAuth material into shared (newest token wins) before relinking; and if
// the merge does not apply, copy it over an empty shared file rather than dropping it.
//
// A failure to remove link RETURNS NIL deliberately: the tool then keeps using its own
// local file, which works. Failing the boot because a credential could not be moved to the
// shared tier would trade a working jail for a tidier layout.
//
// Reached by the shared_credentials HOOK (packhooks.go), which is how a pack asks for this
// without core switching on a tool name. It was ensureCredentialsSymlink, with claude's
// three paths baked in as constants.
func (e *Env) linkThroughShared(link, shared, target string) error {
	if cur, err := os.Readlink(link); err == nil {
		// It's a symlink.
		if cur == target {
			return nil
		}
		_ = os.Remove(link)
	} else if pathExists(link) {
		// A regular file: harvest or legacy-copy, then re-link.
		if !e.harvestCredentialsFile(link, shared) {
			if fi, err := os.Stat(shared); err != nil || fi.Size() == 0 {
				if data, rerr := os.ReadFile(link); rerr == nil {
					_ = os.MkdirAll(filepath.Dir(shared), 0o755)
					_ = os.WriteFile(shared, data, 0o600)
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
