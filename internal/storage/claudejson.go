package storage

import (
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
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
// THE WORKSPACE SIDE IS NAMED BENEATH ws, an os.Root on the workspace overlay (wsRel is
// relative to it), and NEITHER SIDE IS FOLLOWED THROUGH A LINK: a side that is a link, or
// anything else that is not a regular file, takes no part in the sync, and is not read and not
// written. The workspace file is one the JAIL can replace (wsState/claude/claude.json is bound
// read-write at ~/.claude/claude.json on podman, and on Apple Container wsState/.claude.json IS
// the jail's ~/.claude.json), and so is every directory above it, while this runs on the host,
// as the host user. Following a link at the file let a jail choose which host file the forward
// pass overwrote with the seed's JSON, and which host file's login the reverse pass copied
// into the machine seed; following one ABOVE it (wsState/claude -> a host directory) let the
// forward pass create <that directory>/claude.json holding the seed's login. The root refuses
// every path that leaves it, so the second is closed by construction
// (docs/design/base-home-legacy-state.md#22-where-it-lives-host-only-never-in-wsstate names
// jail-planted links in wsState as the hazard). A missing file is still fine: it reads as {},
// and the forward pass creates it.
//
// The seed side is a host path no jail can write (<state>/home/.claude is mounted into none),
// and is opened as a root on its own directory.
func SyncClaudeJSONSeed(seedPath string, ws *os.Root, wsRel string) {
	seedDir, seedName := filepath.Dir(seedPath), filepath.Base(seedPath)
	seedRoot, err := os.OpenRoot(seedDir)
	seedData, seedOK := jsonx.NewOrderedMap(), os.IsNotExist(err)
	if err == nil {
		defer seedRoot.Close()
		seedData, seedOK = readJSONDict(seedRoot, seedName)
	}
	wsData, wsOK := readJSONDict(ws, wsRel)
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
			writeJSONDict(ws, wsRel, wsData)
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
		if seedRoot == nil {
			if os.MkdirAll(seedDir, 0o755) != nil {
				return
			}
			if seedRoot, err = os.OpenRoot(seedDir); err != nil {
				return
			}
			defer seedRoot.Close()
		}
		writeJSONDict(seedRoot, seedName, seedData)
	}
}

// readJSONDict reads name below r as a JSON object, returning an empty OrderedMap on any
// error or when the top-level value is not an object
// "data if isinstance(data, dict) else {}").
//
// ok is false when name EXISTS and is not a regular file — a symlink, a directory, a FIFO —
// or cannot be reached below r (a linked directory above it that leaves the root), and the
// caller must then leave that side alone (SyncClaudeJSONSeed says why). The OPENED file is
// checked to be the one that was Lstat'ed, so a link swapped in between is refused too;
// O_NONBLOCK keeps a planted FIFO from hanging the launch on the open.
func readJSONDict(r *os.Root, name string) (m *jsonx.OrderedMap, ok bool) {
	fi, err := r.Lstat(name)
	if err != nil {
		// Missing is the ordinary first-launch state. Anything else (a path that leaves
		// the root through a linked directory) must not be written either.
		return jsonx.NewOrderedMap(), os.IsNotExist(err)
	}
	if !fi.Mode().IsRegular() {
		return jsonx.NewOrderedMap(), false
	}
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return jsonx.NewOrderedMap(), false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || !os.SameFile(fi, st) {
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

// writeJSONDict writes m as json.dumps(m, indent=2) + "\n" at name below r, best-effort
// (mkdir -p the parent, ignore IO errors — matches the Python except: pass).
//
// A REPLACE, never a write through: the bytes go to a new temp file beside name, created
// O_EXCL, which is then renamed over it, all below r. A rename replaces a link rather than
// following it, so even a link planted between readJSONDict's check and this write is
// replaced, never written through. An existing regular file keeps its mode; a new one is
// 0644, as before.
func writeJSONDict(r *os.Root, name string, m *jsonx.OrderedMap) {
	s, err := jsonx.DumpsIndent(m, 2)
	if err != nil {
		return
	}
	dir := filepath.Dir(name)
	if dir != "." {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			return
		}
	}
	mode := os.FileMode(0o644)
	if fi, err := r.Lstat(name); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}
	tmp, tmpName, err := createTempBeneath(r, dir, "."+filepath.Base(name)+".tmp-")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(s + "\n")
	merr := tmp.Chmod(mode)
	cerr := tmp.Close()
	if werr != nil || merr != nil || cerr != nil || r.Rename(tmpName, name) != nil {
		_ = r.Remove(tmpName)
	}
}

// createTempBeneath is os.CreateTemp below r: a new file in dir named prefix plus a random
// suffix, created O_EXCL (so never through a link), returning it and its name below r.
func createTempBeneath(r *os.Root, dir, prefix string) (*os.File, string, error) {
	var err error
	for range 10 {
		name := filepath.Join(dir, prefix+strconv.FormatUint(rand.Uint64(), 36))
		var f *os.File
		f, err = r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !os.IsExist(err) {
			break
		}
	}
	return nil, "", err
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
