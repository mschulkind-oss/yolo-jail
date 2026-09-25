package storage

import (
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// claudeJSONSeedKeys are the login-state keys the GLOBAL_HOME seed carries, in BOTH
// directions: back-propagated from a logged-in workspace's claude.json into the seed, and
// forwarded from the seed into a workspace that lacks them. Allowlist only — mcpServers,
// projects, and other workspace-specific keys must never leak into the shared seed, nor out
// of it.
var claudeJSONSeedKeys = []string{"oauthAccount", "hasCompletedOnboarding"}

// SyncClaudeJSONSeed performs the two-way sync of Claude login/onboarding state
// between the GLOBAL_HOME seed and a per-workspace overlay's claude.json.
//
//   - Forward (seed → workspace): fill the allowlisted login keys the workspace is
//     missing from the seed, preserving workspace-specific config; rewrite the
//     workspace file only if something was actually merged. Only claudeJSONSeedKeys:
//     a seed written by a yolo whose home was shared and writable also holds another
//     workspace's `projects` and `mcpServers`, and forwarding every key carried those
//     into each new workspace on the machine
//     (docs/design/base-home-legacy-state.md#27-the-seed).
//   - Reverse (workspace → seed): if the workspace has oauthAccount and the
//     seed lacks it, write the allowlisted login keys up into the seed,
//     preserving unrelated seed keys.
//
// Never raises: a parse/IO error degrades to a no-op for that direction (an
// unparseable file reads as {}). Output uses json.dumps(indent=2) + "\n".
//
// NEITHER PATH IS FOLLOWED THROUGH A LINK, and a side that is a link, or anything else that
// is not a regular file, takes no part in the sync: it is not read and not written. wsPath
// is a file the JAIL can replace (wsState/claude/claude.json is bound read-write at
// ~/.claude/claude.json on podman, and on Apple Container wsState/.claude.json IS the jail's
// ~/.claude.json), while this runs on the host, as the host user. Following a link there
// let a jail choose which host file the forward pass overwrote with the seed's JSON, and
// which host file's login the reverse pass copied into the machine seed
// (docs/design/base-home-legacy-state.md#22-where-it-lives-host-only-never-in-wsstate names
// jail-planted links in wsState as the hazard). A missing file is still fine: it reads as
// {}, and the forward pass creates it.
func SyncClaudeJSONSeed(seedPath, wsPath string) {
	seedData, seedOK := readJSONDict(seedPath)
	wsData, wsOK := readJSONDict(wsPath)
	if !wsOK {
		// Nothing learned from, and nothing written into, a workspace side that is not a
		// regular file.
		return
	}

	// Forward: seed → workspace (fill missing allowlisted keys, in allowlist order).
	if seedOK && seedData.Len() > 0 {
		merged := false
		for _, key := range claudeJSONSeedKeys {
			val, inSeed := seedData.Get(key)
			if !inSeed {
				continue
			}
			if _, ok := wsData.Get(key); !ok {
				wsData.Set(key, val)
				merged = true
			}
		}
		if merged {
			writeJSONDict(wsPath, wsData)
		}
	}

	// Reverse: workspace → seed (allowlisted login keys) when the workspace is
	// logged in but the seed is not.
	if seedOK && truthy(wsData, "oauthAccount") && !truthy(seedData, "oauthAccount") {
		for _, key := range claudeJSONSeedKeys {
			if val, ok := wsData.Get(key); ok {
				seedData.Set(key, val)
			}
		}
		writeJSONDict(seedPath, seedData)
	}
}

// readJSONDict reads path as a JSON object, returning an empty OrderedMap on any
// error or when the top-level value is not an object
// "data if isinstance(data, dict) else {}").
//
// ok is false when path EXISTS and is not a regular file — a symlink, a directory, a FIFO —
// and the caller must then leave that side alone (SyncClaudeJSONSeed says why). The open is
// O_NOFOLLOW, and the OPENED file is checked, so a link swapped in after a Stat is refused
// too; O_NONBLOCK keeps a planted FIFO from hanging the launch on the open.
func readJSONDict(path string) (m *jsonx.OrderedMap, ok bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		// Missing is the ordinary first-launch state. Anything else that fails the open
		// (ELOOP: a link) must not be written either.
		return jsonx.NewOrderedMap(), os.IsNotExist(err)
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return jsonx.NewOrderedMap(), false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return jsonx.NewOrderedMap(), true
	}
	v, err := jsonx.Decode(data)
	if err != nil {
		return jsonx.NewOrderedMap(), true
	}
	if m, ok := v.(*jsonx.OrderedMap); ok {
		return m, true
	}
	return jsonx.NewOrderedMap(), true
}

// writeJSONDict writes m as json.dumps(m, indent=2) + "\n", best-effort
// (mkdir -p the parent, ignore IO errors — matches the Python except: pass).
//
// A REPLACE, never a write through: the bytes go to a new temp file beside path, which is
// then renamed over it. A rename replaces a link rather than following it, so even a link
// planted between readJSONDict's check and this write is replaced, never written through.
// An existing regular file keeps its mode; a new one is 0644, as before.
func writeJSONDict(path string, m *jsonx.OrderedMap) {
	s, err := jsonx.DumpsIndent(m, 2)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(s + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmpName, mode) != nil || os.Rename(tmpName, path) != nil {
		_ = os.Remove(tmpName)
	}
}

// truthy reports whether m[key] is present and Python-truthy. The only values
// stored here are the decoded oauthAccount object (truthy when non-empty) or a
// bool; we treat a present non-nil, non-empty value as truthy — matching
// `ws_data.get("oauthAccount")` used as a boolean.
func truthy(m *jsonx.OrderedMap, key string) bool {
	v, ok := m.Get(key)
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case *jsonx.OrderedMap:
		return t.Len() > 0
	case []any:
		return len(t) > 0
	default:
		// Numbers/other: present and non-nil ⇒ truthy unless it's a zero int.
		return true
	}
}
