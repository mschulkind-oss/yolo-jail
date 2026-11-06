package mcpauth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// SourceStore is an agent's own sign-in store that every jail on the machine reads: Claude's
// credential file in the machine-scope shared directory (CL-D22's bridge), whose `mcpOAuth`
// map holds each MCP server's OAuth. The broker adopts a one-sign-in server's entry from it and
// writes each refreshed access token back into it (MA-D17, docs/design/mcp-auth-sharing.md).
//
// Two rules hold for every write (§12): it happens only while the broker holds the agent's own
// proper-lockfile lock, a directory beside the file, and never without it; and it replaces the
// file by an O_EXCL temp file renamed over it, never through a link, so a reader sees the old
// file or the new one.
type SourceStore struct {
	// Dir is the absolute host directory holding the file, opened refusing a link.
	Dir string
	// File and LockDir are single components beneath Dir.
	File    string
	LockDir string
	// Stale is proper-lockfile's own `stale`: a lock directory older than this is abandoned.
	Stale time.Duration
	// Wait bounds how long a write waits for the agent's lock before giving up unwritten.
	Wait      time.Duration
	EntriesAt []string
	Now       func() time.Time
	Sleep     func(time.Duration)
}

// sourcePoll is how often a held lock is re-tried; proper-lockfile's own minTimeout is 100 ms.
const sourcePoll = 50 * time.Millisecond

func (s SourceStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s SourceStore) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (s SourceStore) open() (*os.Root, error) {
	if !validLeaf(s.File) || !validLeaf(s.LockDir) {
		return nil, errors.New("mcp-auth: source store and lock names must be single path components")
	}
	return paths.OpenStateDirRoot(s.Dir)
}

// Read returns a snapshot of the entries map without taking the lock: the agent replaces the
// file by rename, so an unlocked read is never torn. parsed is false for an absent, unreadable
// or malformed file, and for an entries value that is not an object, so a caller cannot take
// any of them for a sign-out (OQ-MA4 needs a cleanly parsed store). An absent entries map in a
// parsed file is an empty one.
func (s SourceStore) Read() (map[string]json.RawMessage, bool, error) {
	root, err := s.open()
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	entries, _, err := s.readIn(root)
	if err != nil {
		return nil, false, err
	}
	return entries, true, nil
}

func (s SourceStore) readIn(root *os.Root) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	data, err := paths.ReadRegularFileBeneath(root, s.File)
	if err != nil {
		return nil, nil, err
	}
	doc, err := decodeObject(data)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp-auth: source store is not a JSON object")
	}
	raw, err := rawAtPath(doc, s.EntriesAt)
	if err != nil {
		// Only an absent final member is an empty map; anything else is malformed.
		parent := doc
		for i, part := range s.EntriesAt {
			value, ok := parent[part]
			if !ok {
				if i == len(s.EntriesAt)-1 {
					return map[string]json.RawMessage{}, doc, nil
				}
				break
			}
			if parent, err = decodeObject(value); err != nil {
				break
			}
		}
		return nil, nil, err
	}
	entries, err := decodeObject(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp-auth: source store's entries are not an object")
	}
	return entries, doc, nil
}

// Update read-modify-writes the entries map under the agent's lock. mutate edits the map in
// place and reports whether anything changed; nothing is written when it did not. A store that
// is absent or malformed is never rewritten, and a lock that cannot be taken within Wait means
// no write at all.
func (s SourceStore) Update(mutate func(entries map[string]json.RawMessage) (bool, error)) error {
	root, err := s.open()
	if err != nil {
		return err
	}
	defer root.Close()
	release, err := s.lock(root)
	if err != nil {
		return err
	}
	defer release()
	entries, doc, err := s.readIn(root)
	if err != nil {
		return err
	}
	changed, err := mutate(entries)
	if err != nil || !changed {
		return err
	}
	encoded, err := marshalRawObject(entries)
	if err != nil {
		return err
	}
	if err := setRawAtPath(doc, s.EntriesAt, encoded); err != nil {
		return err
	}
	data, err := marshalRawObject(doc)
	if err != nil {
		return err
	}
	return renameInto(root, s.File, data)
}

// lock takes proper-lockfile's lock: mkdir of the lock directory, which fails while another
// holder has it. A directory older than Stale is abandoned and removed (only when empty; a
// lock with something inside is waited on like a held one). Something other than a directory
// at the name is refused and left alone. Every way round the loop ends at the deadline.
func (s SourceStore) lock(root *os.Root) (func(), error) {
	if s.Stale <= 0 || s.Wait <= 0 {
		return nil, errors.New("mcp-auth: source lock needs a positive stale limit and wait")
	}
	deadline := s.now().Add(s.Wait)
	for {
		err := root.Mkdir(s.LockDir, 0o755)
		if err == nil {
			return func() { _ = root.Remove(s.LockDir) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		fi, lerr := root.Lstat(s.LockDir)
		if lerr == nil && !fi.IsDir() {
			return nil, fmt.Errorf("mcp-auth: something other than the agent's lock directory is at %s; remove it and retry", s.LockDir)
		}
		if lerr == nil && s.now().Sub(fi.ModTime()) > s.Stale && root.Remove(s.LockDir) == nil {
			continue
		}
		if !s.now().Before(deadline) {
			return nil, errors.New("mcp-auth: timed out waiting for the agent's store lock; the write was not made")
		}
		s.sleep(sourcePoll)
	}
}

// renameInto writes data to an O_EXCL temp file beside name and renames it over name.
func renameInto(root *os.Root, name string, data []byte) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := name + ".tmp." + hex.EncodeToString(suffix[:])
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}
