package skillimports

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	runtimeidentity "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const resolutionVersion = 1

const (
	AccessAuthenticated = "authenticated"
	AccessAnonymous     = "anonymous"
)

// Resolution is host-only state describing the commit last selected for one workspace import.
type Resolution struct {
	Version     int       `json:"version"`
	Handle      string    `json:"handle"`
	Git         string    `json:"git"`
	Rev         string    `json:"rev,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	ResolvedRef string    `json:"resolved_ref,omitempty"`
	Repo        string    `json:"repo"`
	Commit      string    `json:"commit"`
	AccessClass string    `json:"access_class"`
	ResolvedAt  time.Time `json:"resolved_at"`
}

// Store is host-owned, per-workspace import state. Open computes paths without creating state.
type Store struct {
	workspace string
	directory string
}

// Open returns the host store partitioned by runtime.FromWorkspace. It does not create files.
func Open(workspace string) (*Store, error) {
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	resolved := runtimeidentity.ResolveWorkspace(workspace)
	key := runtimeidentity.FromWorkspace(resolved)
	if key == "" || strings.ContainsAny(key, `/\\`) {
		return nil, errors.New("workspace identity is not a safe store component")
	}
	return &Store{
		workspace: resolved,
		directory: filepath.Join(paths.GlobalStorage(), "skill-imports", key),
	}, nil
}

// Directory is the host-only root for this workspace's resolution records and Git objects.
func (s *Store) Directory() string { return s.directory }

// ObjectDirectory is the bare Git object store for this workspace.
func (s *Store) ObjectDirectory() string { return filepath.Join(s.directory, "objects.git") }

// RecordPath is the on-disk path for handle's resolution record; handles are encoded as bytes,
// never interpreted as a path component.
func (s *Store) RecordPath(handle string) string {
	return filepath.Join(s.directory, "records", hex.EncodeToString([]byte(handle))+".json")
}

// Resolution reads one record and returns it only if it still describes entry's configured
// source and revision/ref. A damaged, stale or inaccessible record is absent, not authority.
func (s *Store) Resolution(handle string, entry config.SkillImport) (Resolution, bool) {
	record, err := s.readResolution(handle)
	if err != nil || !validResolution(record, handle) || record.Git != entry.Git {
		return Resolution{}, false
	}
	if entry.Rev != "" {
		if record.Rev != entry.Rev || record.Ref != entry.Ref || !strings.EqualFold(record.Commit, entry.Rev) {
			return Resolution{}, false
		}
	} else if record.Rev != "" || record.Ref != entry.Ref {
		return Resolution{}, false
	}
	return record, true
}

// WriteResolution atomically replaces one validated record. It does not initialize or mutate
// the Git object store.
func (s *Store) WriteResolution(record Resolution) error {
	record.Version = resolutionVersion
	if !validResolution(record, record.Handle) {
		return errors.New("invalid skill import resolution record")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.directory, "records")
	if err := ensurePrivateDir(s.directory); err != nil {
		return err
	}
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".resolution-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.RecordPath(record.Handle)); err != nil {
		return err
	}
	return syncDir(dir)
}

func (s *Store) readResolution(handle string) (Resolution, error) {
	var record Resolution
	if !validHandle(handle) {
		return record, errors.New("invalid import handle")
	}
	file, err := os.OpenFile(s.RecordPath(handle), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return record, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return record, errors.New("resolution record is not a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Resolution{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Resolution{}, errors.New("resolution record has trailing data")
	}
	return record, nil
}

func validResolution(record Resolution, handle string) bool {
	if record.Version != resolutionVersion {
		return false
	}
	if record.Handle != handle || !validHandle(handle) || record.Git == "" || !validObjectID(record.Commit) ||
		record.ResolvedAt.IsZero() ||
		(record.AccessClass != AccessAuthenticated && record.AccessClass != AccessAnonymous) {
		return false
	}
	repo, err := gitproto.Identity(record.Git)
	if err != nil || repo != record.Repo {
		return false
	}
	if record.Rev != "" && !validObjectID(record.Rev) {
		return false
	}
	if record.Rev == "" && record.Ref == "" || record.Ref != "" && !validImportRef(record.Ref) {
		return false
	}
	if record.Rev != "" && record.ResolvedRef != "" || record.Rev == "" && !validFullImportRef(record.ResolvedRef) {
		return false
	}
	return true
}

func validHandle(handle string) bool {
	return handle != "" && handle != "." && handle != ".." && strings.TrimSpace(handle) == handle && !strings.ContainsAny(handle, `\\:/?#`)
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func validImportRef(ref string) bool {
	return ref != "" && ref != "HEAD" && !strings.ContainsAny(ref, " \t\r\n\\") && !strings.Contains(ref, "..") && !strings.Contains(ref, "@{")
}

func validFullImportRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/heads/") && validImportRef(strings.TrimPrefix(ref, "refs/heads/")) ||
		strings.HasPrefix(ref, "refs/tags/") && validImportRef(strings.TrimPrefix(ref, "refs/tags/"))
}

// EnsureObjectStore creates an empty bare store with no templates, hooks, remotes or user Git
// configuration. Existing stores are checked but never rewritten.
func (s *Store) EnsureObjectStore() error {
	if err := ensurePrivateDir(s.directory); err != nil {
		return err
	}
	objectDir := s.ObjectDirectory()
	info, err := os.Lstat(objectDir)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("import object store is not a real directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	cmd := exec.Command("git", "init", "--bare", "--template=", objectDir)
	cmd.Env = isolatedGitEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initialize import object store: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// HasCommit reports whether commit exists in this store as a commit object.
func (s *Store) HasCommit(commit string) bool {
	if !validObjectID(commit) {
		return false
	}
	cmd := exec.Command("git", "--git-dir", s.ObjectDirectory(), "cat-file", "-e", commit+"^{commit}")
	cmd.Env = isolatedGitEnv()
	return cmd.Run() == nil
}

// WithLock holds one exclusive process-lifetime lock for fetch-and-record writers. Readers do
// not take the lock; the Git store and records have their own atomicity guarantees.
func (s *Store) WithLock(fn func() error) error {
	locked, err := s.withLock(fn, false)
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("blocking import lock returned without acquiring the lock")
	}
	return nil
}

// TryWithLock returns immediately when another live process owns the import lock.
func (s *Store) TryWithLock(fn func() error) (bool, error) {
	return s.withLock(fn, true)
}

func (s *Store) withLock(fn func() error, nonblocking bool) (bool, error) {
	if fn == nil {
		return false, errors.New("lock callback is required")
	}
	if err := ensurePrivateDir(s.directory); err != nil {
		return false, err
	}
	path := filepath.Join(s.directory, ".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return false, err
	}
	defer file.Close()
	mode := syscall.LOCK_EX
	if nonblocking {
		mode |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), mode); err != nil {
		if nonblocking && (err == syscall.EWOULDBLOCK || err == syscall.EAGAIN) {
			return false, nil
		}
		return false, err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return true, fn()
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("import state directory %s is not a real directory", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func isolatedGitEnv() []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
}
