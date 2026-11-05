package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// CanonicalStore serializes all canonical reads and writes on the pack lock file.
type CanonicalStore struct {
	Dir      string
	Filename string
	LockName string
	Wait     time.Duration
}

// NewCanonicalStore opens the host-only state directory for one pack. The data file defaults
// to codex.json and its stable advisory lock to codex.lock.
func NewCanonicalStore(dir string) (*CanonicalStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := paths.OpenStateDirRoot(dir)
	if err != nil {
		return nil, err
	}
	root.Close()
	return &CanonicalStore{Dir: dir, Filename: "codex.json", LockName: "codex.lock", Wait: 10 * time.Second}, nil
}

// WithLock executes fn while holding codex.lock. Wait is injectable for bounded tests.
func (s *CanonicalStore) WithLock(fn func(*CanonicalFile) error) error {
	return s.withLock(true, fn)
}

func (s *CanonicalStore) withLock(persist bool, fn func(*CanonicalFile) error) error {
	if s.Wait <= 0 {
		return errors.New("mcp-auth: lock wait must be positive")
	}
	if !validLeaf(s.Filename) || !validLeaf(s.LockName) {
		return fmt.Errorf("mcp-auth: canonical store and lock names must be single path components")
	}
	root, err := paths.OpenStateDirRoot(s.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	lockFile, err := paths.OpenLockFileBeneath(root, s.LockName)
	if err != nil {
		return err
	}
	lock, err := lockFileWithWait(lockFile, s.Wait)
	if err != nil {
		lockFile.Close()
		return err
	}
	defer lock.Close()
	state, err := readCanonical(root, s.Filename)
	if err != nil {
		return err
	}
	if err := fn(&state); err != nil {
		return err
	}
	if !persist {
		return nil
	}
	return writeCanonical(root, s.Filename, state)
}

// LockRefresh takes the independent refresher lock for one canonical record. The identity is
// already a fixed-width hex digest, so it can safely form the documented lock filename.
func (s *CanonicalStore) LockRefresh(server, identity string) (func() error, error) {
	if s.Wait <= 0 {
		return nil, errors.New("mcp-auth: lock wait must be positive")
	}
	if server == "" || !validDigest(identity) {
		return nil, fmt.Errorf("mcp-auth: invalid record refresh-lock key")
	}
	name := "codex." + identity + ".refresh.lock"
	root, err := paths.OpenStateDirRoot(s.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := paths.OpenLockFileBeneath(root, name)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(file, s.Wait)
	if err != nil {
		file.Close()
		return nil, err
	}
	return lock.Close, nil
}

// Read returns a consistent snapshot under the same pack lock as read-modify-writes.
func (s *CanonicalStore) Read() (CanonicalFile, error) {
	var snapshot CanonicalFile
	err := s.withLock(false, func(file *CanonicalFile) error {
		snapshot = *file
		snapshot.Records = append([]Record(nil), file.Records...)
		return nil
	})
	return snapshot, err
}

func readCanonical(root *os.Root, filename string) (CanonicalFile, error) {
	f, err := paths.OpenRegularFileBeneath(root, filename)
	if errors.Is(err, fs.ErrNotExist) {
		return CanonicalFile{Version: storeVersion, Records: []Record{}}, nil
	}
	if err != nil {
		return CanonicalFile{}, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return CanonicalFile{}, err
	}
	data, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return CanonicalFile{}, readErr
	}
	if closeErr != nil {
		return CanonicalFile{}, closeErr
	}
	var file CanonicalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return CanonicalFile{}, fmt.Errorf("mcp-auth: parse canonical store: %w", err)
	}
	if err := validateCanonical(file); err != nil {
		return CanonicalFile{}, err
	}
	return file, nil
}

func validateCanonical(file CanonicalFile) error {
	if file.Version != storeVersion {
		return fmt.Errorf("mcp-auth: unsupported canonical store version %d", file.Version)
	}
	seen := make(map[string]bool, len(file.Records))
	for _, record := range file.Records {
		if err := validateRecord(record); err != nil {
			return err
		}
		key := keyFor(record.Server, record.Identity)
		if seen[key] {
			return fmt.Errorf("mcp-auth: duplicate canonical record %s", key)
		}
		seen[key] = true
	}
	return nil
}

func writeCanonical(root *os.Root, filename string, file CanonicalFile) error {
	if err := validateCanonical(file); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := paths.OpenRegularFileBeneath(root, filename)
	if err == nil {
		// Existing canonical data must not be writable by group or other users.
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return paths.WriteRegularFileBeneath(root, filename, data, 0o600)
}

// writeReplica replaces the named JSON store beneath root without following a planted leaf
// symlink. Parent directories must exist; lock ownership remains the adapter caller's job.
func writeReplica(root *os.Root, name string, data []byte) error {
	if filepath.IsAbs(name) || name == "" || strings.Contains(name, "..") {
		return fmt.Errorf("mcp-auth: invalid replica path %q", name)
	}
	return paths.WriteRegularFileBeneath(root, name, data, 0o600)
}

func validLeaf(name string) bool {
	return name != "" && filepath.Base(name) == name && name != "." && name != ".."
}

func lockFileWithWait(file *os.File, wait time.Duration) (*fileLock, error) {
	return lockFile(file, wait)
}
