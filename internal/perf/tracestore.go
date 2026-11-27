package perf

// Admission owns every content effect, independently of the legacy TraceSink. Callers supply
// explicit host-only roots. No constructor here reads HOME, registers jail sources, or wires a
// launch. The protocol is nonblocking flock on Linux/Darwin; uncertainty declines recording.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	MaxRetainedTraces    = 50
	MaxTraceContentBytes = int64(200000)
	MaxStoreContentBytes = int64(10000000)
	diagnosticBytes      = 512
	identityLimit        = 512
)

// TraceMetadata retains only the seven public metadata keys. Workspace is a short code,
// never a locator; source registration belongs to the separate, host-private root.
type TraceMetadata struct {
	Trace     string `json:"trace"`
	Kind      string `json:"kind"`
	Start     int64  `json:"start"`
	Jail      string `json:"jail"`
	Workspace string `json:"workspace"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
}

// TraceUsage exposes counts, not filesystem paths or private source payloads.
type TraceUsage struct {
	Traces int
	Bytes  int64
}

// TraceStore serializes physical-peak admission across independent processes. Each operation
// opens a separate admission descriptor: unlocking another operation's file description cannot
// release its protection. Close requires callers first to close their claims/writers.
type TraceStore struct {
	root, private *os.Root
	rootInfo      fs.FileInfo
	privateInfo   fs.FileInfo
	admissionInfo fs.FileInfo
}

// TraceClaim holds a shared lock on one admitted directory/lock identity. A parent keeps its
// claim until its child's successful Join is acknowledged, then closes only its own descriptor.
type TraceClaim struct {
	store              *TraceStore
	id                 string
	dir                *os.Root
	dirInfo            fs.FileInfo
	lock               *os.File
	lockInfo           fs.FileInfo
	mu                 sync.Mutex
	closed             bool
	diagnostic         bool
	publisher          bool
	launcherCreated    bool
	publication        TracePublicationState
	publicationMeta    *TraceMetadata
	pendingControl     *pendingRegistration
	publicationObserve func(string, *os.File) // Fault tests observe real descriptor boundaries; never substitute I/O.
}

// TraceWriter is one exclusively created lane, appended synchronously without buffering or
// fsync. It holds its own live claim even when the creating claim closes. A failed/short append
// disables the lane; any physically written tail stays charged and is never retried.
type TraceWriter struct {
	claim    *TraceClaim
	file     *os.File
	name     string
	info     fs.FileInfo
	mu       sync.Mutex
	disabled bool
	launcher bool
}

type storeIdentity struct {
	Root      [2]uint64 `json:"root"`
	Private   [2]uint64 `json:"private"`
	Admission [2]uint64 `json:"admission"`
	Directory [2]uint64 `json:"directory"`
	Lock      [2]uint64 `json:"lock"`
}

type storeEntry struct {
	id       string
	bytes    int64
	meta     TraceMetadata
	identity storeIdentity
	known    bool
	pending  *pendingRegistration
}

type storeInventory struct {
	bytes   int64
	entries []storeEntry
}

const (
	metadataSlotBytes    = 452
	publicationSealBytes = 33
)

type pendingRegistration struct {
	Version       uint8     `json:"version"`
	Trace         string    `json:"trace"`
	Start         int64     `json:"start"`
	LauncherLane  bool      `json:"launcher_lane"`
	MetaSlotBytes int64     `json:"meta_slot_bytes"`
	MetaSlot      [2]uint64 `json:"meta_slot"`
	SealSlot      [2]uint64 `json:"seal_slot"`
}

// TracePublicationState distinguishes exposure, an identical no-op, and frozen refusal.
type TracePublicationState uint8

const (
	TracePublicationUnavailable TracePublicationState = iota
	TracePublished
	TraceAlreadyPublished
	TracePublicationConflict
)

type TracePublicationResult struct{ State TracePublicationState }

func readStoreBytes(r *os.Root, name string, size int) ([]byte, fs.FileInfo, error) {
	f, err := safeStoreFile(r, name, os.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	i, se := f.Stat()
	p, re := io.ReadAll(io.LimitReader(f, int64(size+1)))
	ce := f.Close()
	if se != nil || re != nil || ce != nil || len(p) != size || i.Size() != int64(size) {
		return nil, nil, traceDeclined()
	}
	return p, i, nil
}

func exactStoreJSON(p []byte, target any, keys []string) bool {
	d := json.NewDecoder(bytes.NewReader(p))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return false
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		switch name {
		case "root", "private", "admission", "directory", "lock", "meta_slot", "seal_slot":
			var pair []json.RawMessage
			if json.Unmarshal(value, &pair) != nil || len(pair) != 2 {
				return false
			}
			for _, part := range pair {
				var number uint64
				if bytes.Equal(bytes.TrimSpace(part), []byte("null")) || json.Unmarshal(part, &number) != nil {
					return false
				}
			}
		}
	}
	if _, err := d.Token(); err != nil || d.Decode(new(any)) != io.EOF || len(seen) != len(keys) {
		return false
	}
	for _, key := range keys {
		if !seen[key] {
			return false
		}
	}
	return json.Unmarshal(p, target) == nil
}

func pendingControl(r *os.Root, id string) (*pendingRegistration, error) {
	f, err := safeStoreFile(r, ".pending", os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	p, re := io.ReadAll(io.LimitReader(f, identityLimit+1))
	ce := f.Close()
	var control pendingRegistration
	if re != nil || ce != nil || len(p) > identityLimit || !exactStoreJSON(p, &control, []string{"version", "trace", "start", "launcher_lane", "meta_slot_bytes", "meta_slot", "seal_slot"}) || control.Version != 1 || control.Trace != id || control.Start <= 0 || !control.LauncherLane || control.MetaSlotBytes != metadataSlotBytes || control.MetaSlot == [2]uint64{} || control.SealSlot == [2]uint64{} {
		return nil, traceDeclined()
	}
	return &control, nil
}

func validPendingMetadata(p []byte, id string, start int64) (TraceMetadata, error) {
	var m TraceMetadata
	var fields map[string]json.RawMessage
	if json.Unmarshal(p, &fields) != nil || len(fields) != 7 {
		return m, traceDeclined()
	}
	for _, key := range []string{"trace", "kind", "start", "jail", "workspace", "version", "commit"} {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return m, traceDeclined()
		}
	}
	if !exactStoreJSON(p, &m, []string{"trace", "kind", "start", "jail", "workspace", "version", "commit"}) || !validPendingSnapshot(m, id, start) {
		return m, traceDeclined()
	}
	return m, nil
}

func validPendingSnapshot(m TraceMetadata, id string, start int64) bool {
	if m.Trace != id || m.Start != start || start <= 0 || (m.Kind != "launch" && m.Kind != "attach" && m.Kind != "incomplete") {
		return false
	}
	for _, v := range []string{m.Kind, m.Jail, m.Workspace, m.Version, m.Commit} {
		if len(v) > 64 || strings.IndexFunc(v, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r))
		}) >= 0 {
			return false
		}
	}
	return true
}

// Recognition is private admission evidence, not an offline public-reader requirement.
func recognizePending(r *os.Root, id string) (*pendingRegistration, TraceMetadata, error) {
	f, err := safeStoreFile(r, ".identity", os.O_RDONLY)
	if err != nil {
		return nil, TraceMetadata{}, err
	}
	identityBytes, readErr := io.ReadAll(io.LimitReader(f, identityLimit+1))
	closeErr := f.Close()
	var identity storeIdentity
	if readErr != nil || closeErr != nil || len(identityBytes) > identityLimit || !exactStoreJSON(identityBytes, &identity, []string{"root", "private", "admission", "directory", "lock"}) {
		return nil, TraceMetadata{}, traceDeclined()
	}
	control, err := pendingControl(r, id)
	if err != nil {
		return nil, TraceMetadata{}, err
	}
	seal, si, err := readStoreBytes(r, ".publication-seal", publicationSealBytes)
	if err != nil || inode(si) != control.SealSlot {
		return nil, TraceMetadata{}, traceDeclined()
	}
	_, publicErr := r.Lstat("meta.json")
	_, slotErr := r.Lstat(".meta-slot")
	name := ".meta-slot"
	published := publicErr == nil
	if published {
		if !errors.Is(slotErr, fs.ErrNotExist) {
			return nil, TraceMetadata{}, traceDeclined()
		}
		name = "meta.json"
	} else if !errors.Is(publicErr, fs.ErrNotExist) || slotErr != nil {
		return nil, TraceMetadata{}, traceDeclined()
	}
	p, mi, err := readStoreBytes(r, name, metadataSlotBytes)
	if err != nil || inode(mi) != control.MetaSlot {
		return nil, TraceMetadata{}, traceDeclined()
	}
	m := TraceMetadata{Trace: id, Start: control.Start}
	if published {
		digest := sha256.Sum256(p)
		if seal[0] != 1 || !bytes.Equal(seal[1:], digest[:]) {
			return nil, m, traceDeclined()
		}
		m, err = validPendingMetadata(p, id, control.Start)
		if err != nil {
			return nil, m, err
		}
	} else if !bytes.Equal(seal, make([]byte, publicationSealBytes)) || !bytes.Equal(p, bytes.Repeat([]byte(" "), metadataSlotBytes)) {
		return nil, m, traceDeclined()
	}
	return control, m, nil
}

func openClaimDirFD(c *TraceClaim) (*os.File, error) {
	f, err := c.dir.Open(".")
	if err != nil {
		return nil, traceDeclined()
	}
	i, err := f.Stat()
	if err != nil || !owned(i, true) || !os.SameFile(i, c.dirInfo) {
		f.Close()
		return nil, traceDeclined()
	}
	return f, nil
}

func (c *TraceClaim) writePendingSlot(name string, p []byte, identity [2]uint64) error {
	f, err := safeStoreFile(c.dir, name, os.O_RDWR)
	if err != nil {
		return err
	}
	i, se := f.Stat()
	if se != nil || inode(i) != identity || i.Size() != int64(len(p)) {
		f.Close()
		return traceDeclined()
	}
	if c.publicationObserve != nil {
		c.publicationObserve(name+".write", f)
	}
	n, we := f.WriteAt(p, 0)
	if c.publicationObserve != nil {
		c.publicationObserve(name+".close", f)
	}
	ce := f.Close()
	if we != nil || n != len(p) || ce != nil {
		return traceDeclined()
	}
	return nil
}

// PublishPending performs at most one physical attempt. Uncertainty freezes the owner; an
// identical successful request only revalidates the exact registered snapshot, never rewrites.
func (c *TraceClaim) PublishPending(meta TraceMetadata) (TracePublicationResult, error) {
	unavailable := TracePublicationResult{State: TracePublicationUnavailable}
	if !c.mu.TryLock() {
		return unavailable, traceDeclined()
	}
	defer c.mu.Unlock()
	if !c.publisher || c.closed {
		return unavailable, traceDeclined()
	}
	repeated := c.publicationMeta != nil
	if repeated {
		if *c.publicationMeta != meta {
			c.publication = TracePublicationConflict
			return TracePublicationResult{State: c.publication}, traceDeclined()
		}
		if c.publication != TracePublished {
			return TracePublicationResult{State: c.publication}, traceDeclined()
		}
	} else {
		frozen := meta
		c.publicationMeta = &frozen
		c.publication = TracePublicationUnavailable
	}
	if c.pendingControl == nil || !validPendingSnapshot(meta, c.id, c.pendingControl.Start) {
		return unavailable, traceDeclined()
	}
	p, err := json.Marshal(meta)
	if err != nil || len(p) > metadataSlotBytes {
		return unavailable, traceDeclined()
	}
	p = append(p, bytes.Repeat([]byte(" "), metadataSlotBytes-len(p))...)
	f, err := c.store.admission()
	if err != nil {
		return unavailable, err
	}
	defer f.Close()
	if repeated {
		_, _, err := recognizePending(c.dir, c.id)
		actual, _, re := readStoreBytes(c.dir, "meta.json", metadataSlotBytes)
		if c.validate() != nil || err != nil || re != nil || !bytes.Equal(actual, p) {
			c.publication = TracePublicationUnavailable
			return unavailable, traceDeclined()
		}
		return TracePublicationResult{State: TraceAlreadyPublished}, nil
	}
	c.publication = TracePublicationUnavailable
	if c.validate() != nil {
		return unavailable, traceDeclined()
	}
	control, _, err := recognizePending(c.dir, c.id)
	if err != nil {
		return unavailable, err
	}
	if _, err = validPendingMetadata(p, c.id, control.Start); err != nil {
		return unavailable, err
	}
	if c.admit(0) != nil {
		return unavailable, traceDeclined()
	}
	dirfd, err := openClaimDirFD(c)
	if err != nil {
		return unavailable, err
	}
	if err = c.writePendingSlot(".meta-slot", p, control.MetaSlot); err != nil {
		dirfd.Close()
		return unavailable, err
	}
	digest := sha256.Sum256(p)
	seal := append([]byte{1}, digest[:]...)
	if err = c.writePendingSlot(".publication-seal", seal, control.SealSlot); err != nil {
		dirfd.Close()
		return unavailable, err
	}
	if c.validate() != nil {
		dirfd.Close()
		return unavailable, traceDeclined()
	}
	if c.publicationObserve != nil {
		c.publicationObserve("rename", dirfd)
	}
	renameErr := renameNoReplaceAt(int(dirfd.Fd()), ".meta-slot", "meta.json")
	if c.publicationObserve != nil {
		c.publicationObserve("directory.close", dirfd)
	}
	closeErr := dirfd.Close()
	// Even uncertain syscall/close results cannot retry. Reconcile only the registered inode,
	// sealed digest, and exact bytes; other content is retained and never adopted.
	finalControl, _, recognitionErr := recognizePending(c.dir, c.id)
	actual, _, readErr := readStoreBytes(c.dir, "meta.json", metadataSlotBytes)
	exactSnapshot := c.validate() == nil && recognitionErr == nil && *finalControl == *c.pendingControl && readErr == nil && bytes.Equal(actual, p)
	if renameErr != nil || closeErr != nil {
		if !exactSnapshot {
			return unavailable, traceDeclined()
		}
	} else if !exactSnapshot {
		return unavailable, traceDeclined()
	}
	c.publication = TracePublished
	return TracePublicationResult{State: TracePublished}, nil
}

var errTraceLive = errors.New("trace has a live claim")

func traceDeclined() error {
	return errors.New("trace recording declined: storage ownership, locking, accounting, or room is unavailable; keep the legacy timing log and ask the yolo maintainer to inspect the host trace store")
}

// traceDeclinedAt declines naming the path it judged and why; the next step is to keep the
// legacy timing log or make that path a real directory the user owns.
func traceDeclinedAt(path, why string) error {
	return fmt.Errorf("trace recording declined: %s %s; keep the legacy timing log, or make that path a real directory you own", path, why)
}

func validStoreID(id string) bool {
	p, err := hex.DecodeString(id)
	return len(id) == 32 && strings.ToLower(id) == id && err == nil && len(p) == 16 && id != strings.Repeat("0", 32)
}

func inode(info fs.FileInfo) [2]uint64 {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return [2]uint64{}
	}
	return [2]uint64{uint64(s.Dev), uint64(s.Ino)}
}

func owned(info fs.FileInfo, directory bool) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || inode(info) == [2]uint64{} {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0o700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && s.Nlink == 1
}

func openStoreRoot(path string) (*os.Root, fs.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, traceDeclined()
	}
	// No symlink guessing for what yolo writes: the root itself is judged by name (Lstat), and
	// OpenRoot plus O_NOFOLLOW guard everything created beneath it. Ancestors above the root
	// belong to the user's own layout (macOS /var, Silverblue /home), so a link there is followed.
	for p := path; ; p = filepath.Dir(p) {
		stat := os.Stat
		if p == path {
			stat = os.Lstat
		}
		i, err := stat(p)
		if err != nil && !(p == path && errors.Is(err, fs.ErrNotExist)) {
			return nil, nil, traceDeclinedAt(p, "cannot be inspected: "+err.Error())
		}
		if err == nil && !i.IsDir() {
			why := "is not a directory"
			if i.Mode()&fs.ModeSymlink != 0 {
				why = "is a symbolic link, and yolo never follows a link at the trace store itself"
			}
			return nil, nil, traceDeclinedAt(p, why)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, nil, traceDeclined()
	}
	i, err := os.Lstat(path)
	if err != nil || !owned(i, true) {
		return nil, nil, traceDeclined()
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, traceDeclined()
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(i, opened) {
		r.Close()
		return nil, nil, traceDeclined()
	}
	return r, i, nil
}

// OpenTraceStore accepts disjoint, canonical, owner-only host roots. The private root is an
// explicit seam for future source bookkeeping, outside public logs and service retirement.
// Only empty admission locks/directories may be created here; all content requires admission.
func OpenTraceStore(root, private string) (*TraceStore, error) {
	within := func(a, b string) bool { return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) }
	if within(root, private) || within(private, root) {
		return nil, traceDeclined()
	}
	r, ri, err := openStoreRoot(root)
	if err != nil {
		return nil, err
	}
	p, pi, err := openStoreRoot(private)
	if err != nil {
		r.Close()
		return nil, err
	}
	s := &TraceStore{root: r, private: p, rootInfo: ri, privateInfo: pi}
	f, err := paths.OpenRootCreate(r, ".admission", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		s.Close()
		return nil, traceDeclined()
	}
	defer f.Close()
	s.admissionInfo, err = f.Stat()
	if err != nil || !owned(s.admissionInfo, false) || s.admissionInfo.Size() != 0 {
		s.Close()
		return nil, traceDeclined()
	}
	// Prove this backend rejects a conflicting, independently opened file description.
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		s.Close()
		return nil, traceDeclined()
	}
	probe, err := r.OpenFile(".admission", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		s.Close()
		return nil, traceDeclined()
	}
	probeErr := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	probe.Close()
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if !errors.Is(probeErr, syscall.EWOULDBLOCK) && !errors.Is(probeErr, syscall.EAGAIN) {
		s.Close()
		return nil, traceDeclined()
	}
	return s, nil
}

func (s *TraceStore) Close() error {
	rootErr := s.root.Close()
	privateErr := s.private.Close()
	if rootErr != nil || privateErr != nil {
		return traceDeclined()
	}
	return nil
}

func (s *TraceStore) admission() (*os.File, error) {
	for _, pair := range []struct {
		r *os.Root
		i fs.FileInfo
	}{{s.root, s.rootInfo}, {s.private, s.privateInfo}} {
		i, err := os.Lstat(pair.r.Name())
		if err != nil || !owned(i, true) || !os.SameFile(i, pair.i) {
			return nil, traceDeclined()
		}
	}
	f, err := safeStoreFile(s.root, ".admission", os.O_RDWR)
	if err != nil {
		return nil, err
	}
	i, err := f.Stat()
	if err != nil || !os.SameFile(i, s.admissionInfo) || i.Size() != 0 || syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, traceDeclined()
	}
	return f, nil
}

func safeStoreFile(r *os.Root, name string, flags int) (*os.File, error) {
	before, err := r.Lstat(name)
	if err != nil || !owned(before, false) {
		return nil, traceDeclined()
	}
	f, err := r.OpenFile(name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, traceDeclined()
	}
	i, err := f.Stat()
	if err != nil || !owned(i, false) || !os.SameFile(before, i) {
		f.Close()
		return nil, traceDeclined()
	}
	return f, nil
}

// All physical content, including unknown files, controls and crash-left copies, is counted.
// A link, nonregular file, incompatible permissions or unbounded tree cannot be safely counted.
func storeTreeBytes(r *os.Root) (int64, error) {
	var total int64
	entries := 0
	err := fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		entries++
		if walkErr != nil || entries > 100000 {
			return traceDeclined()
		}
		i, err := r.Lstat(name)
		if err != nil || !owned(i, d.IsDir()) {
			return traceDeclined()
		}
		if d.IsDir() {
			return nil
		}
		f, err := safeStoreFile(r, name, os.O_RDONLY)
		if err != nil {
			return err
		}
		opened, err := f.Stat()
		f.Close()
		if err != nil || opened.Size() < 0 || opened.Size() > MaxStoreContentBytes || total > MaxStoreContentBytes-opened.Size() {
			return traceDeclined()
		}
		total += opened.Size()
		return nil
	})
	return total, err
}

func storeJSON(r *os.Root, name string, target any, limit int64) error {
	f, err := safeStoreFile(r, name, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	p, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(p)) > limit || json.Unmarshal(p, target) != nil {
		return traceDeclined()
	}
	return nil
}

func (s *TraceStore) identity(di, li fs.FileInfo) storeIdentity {
	return storeIdentity{Root: inode(s.rootInfo), Private: inode(s.privateInfo), Admission: inode(s.admissionInfo), Directory: inode(di), Lock: inode(li)}
}

func (s *TraceStore) inventory() (storeInventory, error) {
	var inv storeInventory
	public, err := storeTreeBytes(s.root)
	if err != nil {
		return inv, err
	}
	private, err := storeTreeBytes(s.private)
	if err != nil || public > MaxStoreContentBytes-private {
		return inv, traceDeclined()
	}
	inv.bytes = public + private
	privateEntries, err := fs.ReadDir(s.private.FS(), ".")
	if err != nil {
		return inv, traceDeclined()
	}
	for _, d := range privateEntries {
		if d.IsDir() && validStoreID(d.Name()) {
			r, err := s.private.OpenRoot(d.Name())
			if err != nil {
				return inv, traceDeclined()
			}
			n, err := storeTreeBytes(r)
			r.Close()
			if err != nil || n > MaxTraceContentBytes {
				return inv, traceDeclined()
			}
		}
	}
	list, err := fs.ReadDir(s.root.FS(), ".")
	if err != nil {
		return inv, traceDeclined()
	}
	for _, d := range list {
		if !d.IsDir() {
			continue
		}
		e := storeEntry{id: d.Name()}
		r, err := s.root.OpenRoot(e.id)
		if err != nil {
			return inv, traceDeclined()
		}
		e.bytes, err = storeTreeBytes(r)
		if err != nil {
			r.Close()
			return inv, err
		}
		if validStoreID(e.id) {
			di, de := r.Stat(".")
			li, le := r.Lstat("lock")
			ie := storeJSON(r, ".identity", &e.identity, identityLimit)
			if de == nil && le == nil && ie == nil && owned(li, false) && li.Size() == 0 && e.identity == s.identity(di, li) {
				if _, pe := r.Lstat(".pending"); pe == nil {
					registration, meta, pe := recognizePending(r, e.id)
					if pe == nil {
						e.pending, e.meta, e.known = registration, meta, true
					}
				} else if errors.Is(pe, fs.ErrNotExist) && storeJSON(r, "meta.json", &e.meta, MaxTraceContentBytes) == nil && e.meta.Trace == e.id {
					_, slotErr := r.Lstat(".meta-slot")
					_, sealErr := r.Lstat(".publication-seal")
					e.known = errors.Is(slotErr, fs.ErrNotExist) && errors.Is(sealErr, fs.ErrNotExist)
				}
			}
			if !e.known {
				r.Close()
				return inv, traceDeclined()
			}
		}
		r.Close()
		if pi, err := s.private.Lstat(e.id); err == nil {
			if !owned(pi, true) {
				return inv, traceDeclined()
			}
			p, err := s.private.OpenRoot(e.id)
			if err != nil {
				return inv, traceDeclined()
			}
			n, err := storeTreeBytes(p)
			p.Close()
			if err != nil {
				return inv, err
			}
			e.bytes += n
		} else if !errors.Is(err, fs.ErrNotExist) {
			return inv, traceDeclined()
		}
		if e.bytes > MaxTraceContentBytes {
			return inv, traceDeclined()
		}
		inv.entries = append(inv.entries, e)
	}
	if len(inv.entries) > MaxRetainedTraces {
		return inv, traceDeclined()
	}
	return inv, nil
}

func (s *TraceStore) Usage() (TraceUsage, error) {
	f, err := s.admission()
	if err != nil {
		return TraceUsage{}, err
	}
	defer f.Close()
	i, err := s.inventory()
	return TraceUsage{Traces: len(i.entries), Bytes: i.bytes}, err
}

func (c *TraceClaim) validate() error {
	if c.closed {
		return traceDeclined()
	}
	di, err := c.store.root.Lstat(c.id)
	if err != nil || !owned(di, true) || !os.SameFile(di, c.dirInfo) {
		return traceDeclined()
	}
	li, err := c.dir.Lstat("lock")
	if err != nil || !owned(li, false) || li.Size() != 0 || !os.SameFile(li, c.lockInfo) {
		return traceDeclined()
	}
	var id storeIdentity
	if storeJSON(c.dir, ".identity", &id, identityLimit) != nil || id != c.store.identity(di, li) {
		return traceDeclined()
	}
	if c.pendingControl != nil {
		control, err := pendingControl(c.dir, c.id)
		if err != nil || *control != *c.pendingControl {
			return traceDeclined()
		}
	}
	return nil
}

// Join never creates a directory or lock. Each child/writer opens and claims its own description.
func (s *TraceStore) Join(id string) (*TraceClaim, error) {
	f, err := s.admission()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := s.inventory(); err != nil {
		return nil, err
	}
	return s.join(id)
}

func (s *TraceStore) join(id string) (*TraceClaim, error) {
	if !validStoreID(id) {
		return nil, traceDeclined()
	}
	di, err := s.root.Lstat(id)
	if err != nil || !owned(di, true) {
		return nil, traceDeclined()
	}
	r, err := s.root.OpenRoot(id)
	if err != nil {
		return nil, traceDeclined()
	}
	f, err := safeStoreFile(r, "lock", os.O_RDWR)
	if err != nil {
		r.Close()
		return nil, err
	}
	li, err := f.Stat()
	if err != nil || syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		f.Close()
		r.Close()
		return nil, traceDeclined()
	}
	c := &TraceClaim{store: s, id: id, dir: r, dirInfo: di, lock: f, lockInfo: li}
	if _, pe := r.Lstat(".pending"); pe == nil {
		c.pendingControl, err = pendingControl(r, id)
		if err != nil {
			c.Close()
			return nil, traceDeclined()
		}
	} else if !errors.Is(pe, fs.ErrNotExist) {
		c.Close()
		return nil, traceDeclined()
	}
	if c.validate() != nil {
		c.Close()
		return nil, traceDeclined()
	}
	return c, nil
}

func storeCreate(r *os.Root, name string, p []byte) error {
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return traceDeclined()
	}
	n, err := f.Write(p)
	ce := f.Close()
	if err != nil || n != len(p) || ce != nil {
		return traceDeclined()
	}
	return nil
}

// Mint admits before allocating metadata, identity or diagnostic content. At capacity, only
// positively identified inactive traces are pruned, oldest start then lexicographically least ID.
func (s *TraceStore) Mint(meta TraceMetadata) (*TraceClaim, error) {
	return s.mint(meta, false)
}

// MintPending protects the original start and physically reserves publication capacity.
func (s *TraceStore) MintPending(id string, originalStart int64) (*TraceClaim, error) {
	if originalStart <= 0 {
		return nil, traceDeclined()
	}
	return s.mint(TraceMetadata{Trace: id, Start: originalStart}, true)
}

func (s *TraceStore) mint(meta TraceMetadata, pending bool) (*TraceClaim, error) {
	if !validStoreID(meta.Trace) {
		return nil, traceDeclined()
	}
	for _, value := range []string{meta.Kind, meta.Jail, meta.Workspace, meta.Version, meta.Commit} {
		if len(value) > 64 || strings.IndexFunc(value, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-+", r))
		}) >= 0 {
			return nil, traceDeclined()
		}
	}
	data, err := json.Marshal(meta)
	if err != nil || int64(len(data)+identityLimit+diagnosticBytes) > MaxTraceContentBytes {
		return nil, traceDeclined()
	}
	f, err := s.admission()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	inv, err := s.inventory()
	if err != nil {
		return nil, err
	}
	if _, err := s.root.Lstat(meta.Trace); !errors.Is(err, fs.ErrNotExist) {
		return nil, traceDeclined()
	}
	// An orphan private entry must never be adopted by a new trace with the same ID.
	if _, err := s.private.Lstat(meta.Trace); !errors.Is(err, fs.ErrNotExist) {
		return nil, traceDeclined()
	}
	need := int64(len(data) + identityLimit + diagnosticBytes)
	if pending {
		need = identityLimit + diagnosticBytes + identityLimit + metadataSlotBytes + publicationSealBytes
	}
	sort.Slice(inv.entries, func(i, j int) bool {
		a, b := inv.entries[i], inv.entries[j]
		return a.meta.Start < b.meta.Start || (a.meta.Start == b.meta.Start && a.id < b.id)
	})
	for _, e := range inv.entries {
		if len(inv.entries) < MaxRetainedTraces && inv.bytes <= MaxStoreContentBytes-need {
			break
		}
		if !e.known {
			continue
		}
		if err := s.prune(e); err != nil {
			if errors.Is(err, errTraceLive) {
				continue
			}
			return nil, err
		}
		inv, err = s.inventory()
		if err != nil {
			return nil, err
		}
	}
	if len(inv.entries) >= MaxRetainedTraces || inv.bytes > MaxStoreContentBytes-need {
		return nil, traceDeclined()
	}
	if s.root.Mkdir(meta.Trace, 0o700) != nil {
		return nil, traceDeclined()
	}
	r, err := s.root.OpenRoot(meta.Trace)
	if err != nil {
		return nil, traceDeclined()
	}
	defer r.Close()
	if storeCreate(r, "lock", nil) != nil {
		return nil, traceDeclined()
	}
	lock, err := safeStoreFile(r, "lock", os.O_RDWR)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		return nil, traceDeclined()
	}
	di, err := r.Stat(".")
	if err != nil {
		return nil, traceDeclined()
	}
	li, err := lock.Stat()
	if err != nil {
		return nil, traceDeclined()
	}
	identity, err := json.Marshal(s.identity(di, li))
	if err != nil || len(identity) > identityLimit {
		return nil, traceDeclined()
	}
	if storeCreate(r, ".identity", identity) != nil || storeCreate(r, ".diagnostic", []byte(strings.Repeat(" ", diagnosticBytes))) != nil {
		return nil, traceDeclined()
	}
	if pending {
		if storeCreate(r, ".meta-slot", bytes.Repeat([]byte(" "), metadataSlotBytes)) != nil || storeCreate(r, ".publication-seal", make([]byte, publicationSealBytes)) != nil {
			return nil, traceDeclined()
		}
		mi, me := r.Lstat(".meta-slot")
		si, se := r.Lstat(".publication-seal")
		if me != nil || se != nil {
			return nil, traceDeclined()
		}
		registration := pendingRegistration{Version: 1, Trace: meta.Trace, Start: meta.Start, LauncherLane: true, MetaSlotBytes: metadataSlotBytes, MetaSlot: inode(mi), SealSlot: inode(si)}
		control, err := json.Marshal(registration)
		if err != nil || len(control) > identityLimit || storeCreate(r, ".pending", control) != nil {
			return nil, traceDeclined()
		}
	} else if storeCreate(r, "meta.json", data) != nil {
		return nil, traceDeclined()
	}
	// Initial claim remains held until the returned independent claim is acquired.
	claim, err := s.join(meta.Trace)
	if err == nil {
		claim.publisher = pending
	}
	return claim, err
}

func (s *TraceStore) prune(e storeEntry) error {
	r, err := s.root.OpenRoot(e.id)
	if err != nil {
		return traceDeclined()
	}
	defer r.Close()
	f, err := safeStoreFile(r, "lock", os.O_RDWR)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errTraceLive
		}
		return traceDeclined()
	}
	di, de := s.root.Lstat(e.id)
	li, le := f.Stat()
	var identity storeIdentity
	if de != nil || le != nil || storeJSON(r, ".identity", &identity, identityLimit) != nil || identity != e.identity || identity != s.identity(di, li) {
		return traceDeclined()
	}
	if e.pending != nil {
		registration, _, err := recognizePending(r, e.id)
		if err != nil || *registration != *e.pending {
			return traceDeclined()
		}
	}
	// Tree contents have already been conservatively verified under admission. Neither private
	// source locators nor workspace source cleanup is implemented or authorized by this removal.
	if err := s.private.RemoveAll(e.id); err != nil {
		return traceDeclined()
	}
	if err := s.root.RemoveAll(e.id); err != nil {
		return traceDeclined()
	}
	return nil
}

func (c *TraceClaim) ID() string { return c.id }

func (c *TraceClaim) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	lockErr := c.lock.Close()
	dirErr := c.dir.Close()
	if lockErr != nil || dirErr != nil {
		return traceDeclined()
	}
	return nil
}

func (c *TraceClaim) admit(n int64) error {
	if n < 0 || c.validate() != nil {
		return traceDeclined()
	}
	inv, err := c.store.inventory()
	if err != nil || inv.bytes > MaxStoreContentBytes-n {
		return traceDeclined()
	}
	for _, e := range inv.entries {
		if e.id == c.id && e.known && e.bytes <= MaxTraceContentBytes-n {
			return nil
		}
	}
	return traceDeclined()
}

func (c *TraceClaim) Usage() (TraceUsage, error) {
	if !c.mu.TryLock() {
		return TraceUsage{}, traceDeclined()
	}
	defer c.mu.Unlock()
	f, err := c.store.admission()
	if err != nil {
		return TraceUsage{}, err
	}
	defer f.Close()
	if c.validate() != nil {
		return TraceUsage{}, traceDeclined()
	}
	inv, err := c.store.inventory()
	for _, e := range inv.entries {
		if e.id == c.id {
			return TraceUsage{Traces: 1, Bytes: e.bytes}, err
		}
	}
	return TraceUsage{}, traceDeclined()
}

func validStoreName(name string) bool {
	return name != "" && len(name) <= 128 && name != "meta.json" && name != "lock" && !strings.HasPrefix(name, ".") && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00")
}

func (c *TraceClaim) CreateWriter(name string) (*TraceWriter, error) {
	return c.createWriter(name, false)
}

// CreateLauncherWriter is reserved to the original pending publisher, never a joined claim.
func (c *TraceClaim) CreateLauncherWriter() (*TraceWriter, error) {
	return c.createWriter(TraceFileName("launcher", os.Getpid()), true)
}

func (c *TraceClaim) createWriter(name string, launcher bool) (*TraceWriter, error) {
	if !c.mu.TryLock() {
		return nil, traceDeclined()
	}
	defer c.mu.Unlock()
	if !validStoreName(name) {
		return nil, traceDeclined()
	}
	f, err := c.store.admission()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if c.admit(0) != nil {
		return nil, traceDeclined()
	}
	_, pe := c.dir.Lstat(".pending")
	if launcher {
		if !c.publisher || c.launcherCreated || pe != nil {
			return nil, traceDeclined()
		}
		entries, err := fs.ReadDir(c.dir.FS(), ".")
		if err != nil {
			return nil, traceDeclined()
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "launcher-") && strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil, traceDeclined()
			}
		}
	} else if pe == nil && strings.HasPrefix(name, "launcher-") && strings.HasSuffix(name, ".jsonl") {
		return nil, traceDeclined()
	}
	if launcher {
		c.launcherCreated = true
	}
	claim, err := c.store.join(c.id)
	if err != nil {
		return nil, err
	}
	file, err := claim.dir.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		claim.Close()
		return nil, traceDeclined()
	}
	info, err := file.Stat()
	if err != nil || !owned(info, false) || syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		file.Close()
		claim.Close()
		return nil, traceDeclined()
	}
	return &TraceWriter{claim: claim, file: file, name: name, info: info, launcher: launcher}, nil
}

func (w *TraceWriter) Write(p []byte) (int, error) {
	if !w.mu.TryLock() {
		return 0, traceDeclined()
	}
	defer w.mu.Unlock()
	if w.disabled {
		return 0, traceDeclined()
	}
	f, err := w.claim.store.admission()
	if err != nil {
		w.disabled = true
		return 0, err
	}
	defer f.Close()
	i, err := w.claim.dir.Lstat(w.name)
	if err != nil || !owned(i, false) || !os.SameFile(i, w.info) || w.claim.admit(int64(len(p))) != nil {
		w.disabled = true
		return 0, traceDeclined()
	}
	n, err := w.file.Write(p)
	if err != nil || n != len(p) {
		w.disabled = true
		return n, traceDeclined()
	}
	return n, nil
}

func (w *TraceWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.disabled = true
	fileErr := w.file.Close()
	claimErr := w.claim.Close()
	if fileErr != nil || claimErr != nil {
		return traceDeclined()
	}
	return nil
}

// Replace and ReplacePrivate admit the full new temporary copy, without discounting the old
// snapshot. Failed publication retains old data; unknown temporary cleanup retains charged bytes.
func (c *TraceClaim) Replace(name string, p []byte) error {
	return c.replace(name, p, false)
}

func (c *TraceClaim) ReplacePrivate(name string, p []byte) error {
	return c.replace(name, p, true)
}

func (c *TraceClaim) replace(name string, p []byte, private bool) error {
	if !c.mu.TryLock() {
		return traceDeclined()
	}
	defer c.mu.Unlock()
	if !validStoreName(name) {
		return traceDeclined()
	}
	if !private && c.pendingControl != nil && strings.HasPrefix(name, "launcher-") && strings.HasSuffix(name, ".jsonl") {
		return traceDeclined()
	}
	f, err := c.store.admission()
	if err != nil {
		return err
	}
	defer f.Close()
	if c.admit(int64(len(p))) != nil {
		return traceDeclined()
	}
	r := c.dir
	if private {
		if err := c.store.private.Mkdir(c.id, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return traceDeclined()
		}
		r, err = c.store.private.OpenRoot(c.id)
		if err != nil {
			return traceDeclined()
		}
		defer r.Close()
	}
	old, err := r.Lstat(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) || err == nil && !owned(old, false) {
		return traceDeclined()
	}
	if old != nil {
		// An active lane must not become unlinked content hidden from physical accounting.
		previous, err := safeStoreFile(r, name, os.O_RDWR)
		if err != nil {
			return err
		}
		defer previous.Close() // Close the old copy before releasing machine admission.
		if syscall.Flock(int(previous.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			return traceDeclined()
		}
	}
	temp := ".replace-" + newTraceID().String()
	if err := storeCreate(r, temp, p); err != nil {
		return err // Any partial temp remains counted; do not reclaim unknown publication credit.
	}
	now, err := r.Lstat(name)
	if old == nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return traceDeclined()
		}
	} else if err != nil || !owned(now, false) || !os.SameFile(old, now) {
		return traceDeclined()
	}
	if c.validate() != nil || r.Rename(temp, name) != nil {
		return traceDeclined()
	}
	return nil
}

// Diagnostic has a single, bounded, physically preallocated slot. It may write only a sanitized
// category once; no raw payload, paths or private locator enter the diagnostic. The allocation
// was charged at mint before creating it, and its length never grows, even after a short write.
func (c *TraceClaim) Diagnostic(category string) error {
	if !c.mu.TryLock() {
		return traceDeclined()
	}
	defer c.mu.Unlock()
	if c.diagnostic || len(category) > 64 || category == "" || SegmentName(category) != category {
		return traceDeclined()
	}
	f, err := c.store.admission()
	if err != nil {
		return err
	}
	defer f.Close()
	if c.admit(0) != nil {
		return traceDeclined()
	}
	d, err := safeStoreFile(c.dir, ".diagnostic", os.O_RDWR)
	if err != nil {
		return err
	}
	defer d.Close()
	info, err := d.Stat()
	if err != nil || info.Size() != diagnosticBytes {
		return traceDeclined()
	}
	initial, err := io.ReadAll(io.LimitReader(d, diagnosticBytes+1))
	if err != nil || string(initial) != strings.Repeat(" ", diagnosticBytes) {
		return traceDeclined()
	}
	p := []byte(fmt.Sprintf("{\"category\":%q}\n", category))
	p = append(p, []byte(strings.Repeat(" ", diagnosticBytes-len(p)))...)
	c.diagnostic = true
	n, err := d.WriteAt(p, 0)
	if err != nil || n != len(p) {
		return traceDeclined()
	}
	return nil
}
