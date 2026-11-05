package mcpauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Adapter reads and rewrites the JSON entry map described by a pack declaration.
type Adapter struct {
	EntriesAt  []string
	Companions [][]string
	EmptyStore string
	Lock       packdecl.MCPAuthLock
	Fields     packdecl.MCPAuthFields
}

// AdapterFor builds a file adapter from a pack's declaration.
func AdapterFor(declaration *packdecl.MCPAuth) (Adapter, error) {
	if declaration == nil {
		return Adapter{}, errors.New("mcp-auth: missing store declaration")
	}
	if declaration.Format != "json" {
		return Adapter{}, fmt.Errorf("mcp-auth: unsupported store format %q; use a JSON store until its adapter is available", declaration.Format)
	}
	adapter := Adapter{
		EntriesAt:  append([]string(nil), declaration.EntriesAt...),
		Companions: clonePathList(declaration.Companions), EmptyStore: declaration.EmptyStore,
		Fields: declaration.Fields,
	}
	if declaration.Lock != nil {
		adapter.Lock = *declaration.Lock
	}
	switch adapter.Lock.Protocol {
	case "flock", "none":
	case "proper-lockfile", "mkdir-heartbeat":
		return Adapter{}, fmt.Errorf("mcp-auth: lock protocol %q arrives with slice 2", adapter.Lock.Protocol)
	default:
		return Adapter{}, fmt.Errorf("mcp-auth: unsupported lock protocol %q", adapter.Lock.Protocol)
	}
	return adapter, nil
}

// Read decodes a store and exposes the selected entries as raw JSON. The boolean is false for
// a malformed document or entries map, so callers cannot mistake parse failure for sign-out.
func (a Adapter) Read(data []byte) (Store, bool, error) {
	root, err := decodeObject(data)
	if err != nil {
		return Store{}, false, err
	}
	entriesRaw, err := rawAtPath(root, a.EntriesAt)
	if err != nil {
		return Store{}, false, err
	}
	entries, err := decodeObject(entriesRaw)
	if err != nil {
		return Store{}, false, err
	}
	return Store{Root: append(json.RawMessage(nil), data...), Entries: entries, EmptyStore: a.EmptyStore}, true, nil
}

// ReadFile reads a JSON store beneath root without following a leaf symlink.
func (a Adapter) ReadFile(root *os.Root, path string) (Store, bool, error) {
	data, err := paths.ReadRegularFileBeneath(root, path)
	if errors.Is(err, fs.ErrNotExist) {
		return Store{Entries: map[string]json.RawMessage{}, EmptyStore: a.EmptyStore}, true, nil
	}
	if err != nil {
		return Store{}, false, err
	}
	return a.Read(data)
}

// Encode applies a planned entries map while keeping all other values in the original root.
func (a Adapter) Encode(store Store) ([]byte, error) {
	if store.Deleted {
		return nil, nil
	}
	root := map[string]json.RawMessage{}
	if len(store.Root) != 0 {
		var err error
		root, err = decodeObject(store.Root)
		if err != nil {
			return nil, err
		}
	}
	entries, err := marshalRawObject(store.Entries)
	if err != nil {
		return nil, err
	}
	if err := setRawAtPath(root, a.EntriesAt, entries); err != nil {
		return nil, err
	}
	if len(store.Entries) == 0 && store.EmptyStore == "{}" && len(a.EntriesAt) == 0 {
		return []byte("{}"), nil
	}
	return marshalRawObject(root)
}

// writeFile is only called while UpdateFile owns the replica's declared lock.
func (a Adapter) writeFile(root *os.Root, path string, store Store) error {
	if store.Deleted {
		if err := root.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := a.Encode(store)
	if err != nil {
		return err
	}
	return writeReplica(root, path, data)
}

// UpdateFile reads, plans, and writes beneath root while holding the agent's flock.
// It refuses a linked replica leaf: an adoption must not interpret it as a clean sign-out.
// A none protocol is allowed only at an explicitly supplied launch boundary.
func (a Adapter) UpdateFile(root *os.Root, path string, wait time.Duration, launchBoundary bool, plan func(Store) (Store, error)) error {
	return a.updateFile(root, path, wait, launchBoundary, false, plan)
}

// SeedFile is UpdateFile for a canonical-to-replica seed. A linked leaf is replaceable
// after acquiring the lock, without opening its target or treating it as an adoption.
func (a Adapter) SeedFile(root *os.Root, path string, wait time.Duration, launchBoundary bool, plan func(Store) (Store, error)) error {
	return a.updateFile(root, path, wait, launchBoundary, true, plan)
}

func (a Adapter) updateFile(root *os.Root, path string, wait time.Duration, launchBoundary, replaceLinkedLeaf bool, plan func(Store) (Store, error)) error {
	if a.Lock.Protocol == "none" && !launchBoundary {
		return errors.New("mcp-auth: lock protocol none requires a launch boundary")
	}
	unlock, err := a.lockReplica(root, wait)
	if err != nil {
		return err
	}
	defer unlock()
	if filepath.IsAbs(path) {
		return fmt.Errorf("mcp-auth: invalid replica path %q", path)
	}
	parent, leaf, err := openParentWithoutLinks(root, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	var store Store
	var parsed bool
	if replaceLinkedLeaf {
		store, parsed, err = a.readSeedFile(parent, leaf)
	} else {
		store, parsed, err = a.ReadFile(parent, leaf)
	}
	if err != nil {
		return err
	}
	if !parsed {
		return errors.New("mcp-auth: replica did not parse; leave it unchanged and retry after correcting the store")
	}
	before, beforeErr := a.Encode(store)
	next, err := plan(store)
	if err != nil {
		return err
	}
	if next.Deleted && len(store.Root) == 0 {
		return nil
	}
	if !next.Deleted {
		after, afterErr := a.Encode(next)
		if beforeErr == nil && afterErr == nil && bytes.Equal(before, after) && len(store.Root) > 0 {
			return nil
		}
	}
	return a.writeFile(parent, leaf, next)
}

// InspectFile observes an agent's store under its own lock, without writing it. A missing
// store is a clean absence; a malformed or linked store is an error, never a sign-out.
func (a Adapter) InspectFile(root *os.Root, path string, wait time.Duration, inspect func(Store) error) error {
	unlock, err := a.lockReplica(root, wait)
	if err != nil {
		return err
	}
	defer unlock()
	parent, leaf, err := openParentWithoutLinks(root, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	store, parsed, err := a.ReadFile(parent, leaf)
	if err != nil {
		return err
	}
	if !parsed {
		return errors.New("mcp-auth: malformed replica; correct the store and retry")
	}
	return inspect(store)
}

// readSeedFile distinguishes a replaceable linked leaf from an unreadable store.
// The caller holds a link-refusing parent root through read, plan, and write.
func (a Adapter) readSeedFile(parent *os.Root, leaf string) (Store, bool, error) {
	if info, err := parent.Lstat(leaf); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return Store{Entries: map[string]json.RawMessage{}, EmptyStore: a.EmptyStore}, true, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Store{}, false, err
	}
	store, parsed, err := a.ReadFile(parent, leaf)
	// A leaf installed between Lstat and the no-follow open is still replaceable.
	if err != nil && (errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ELOOP)) {
		if info, statErr := parent.Lstat(leaf); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return Store{Entries: map[string]json.RawMessage{}, EmptyStore: a.EmptyStore}, true, nil
		}
	}
	return store, parsed, err
}

// lockReplica takes the agent's declared flock; none is only used at a launch boundary.
func (a Adapter) lockReplica(root *os.Root, wait time.Duration) (func() error, error) {
	if a.Lock.Protocol == "none" {
		return func() error { return nil }, nil
	}
	if a.Lock.Protocol != "flock" {
		return nil, fmt.Errorf("mcp-auth: lock protocol %q arrives with slice 2", a.Lock.Protocol)
	}
	name := a.Lock.Path
	if filepath.IsAbs(name) || name == "" {
		return nil, fmt.Errorf("mcp-auth: invalid lock path %q", name)
	}
	parent, leaf, err := openParentWithoutLinks(root, name)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	f, err := paths.OpenLockFileBeneath(parent, leaf)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(f, wait)
	if err != nil {
		f.Close()
		return nil, err
	}
	return lock.Close, nil
}

// ExtractFields strictly extracts the configured token and expiry values from one entry.
// Failure makes that entry ineligible for adoption rather than affecting other keys.
func (a Adapter) ExtractFields(entry json.RawMessage) (accessToken, refreshToken string, expiresAtMS int64, clientID string, err error) {
	if a.Fields.AccessToken != "" {
		accessToken, err = stringAt(entry, a.Fields.AccessToken)
		if err != nil || accessToken == "" {
			return "", "", 0, "", fmt.Errorf("mcp-auth: access token missing or invalid")
		}
	}
	if a.Fields.RefreshToken != "" {
		refreshToken, err = stringAt(entry, a.Fields.RefreshToken)
		if err != nil {
			return "", "", 0, "", fmt.Errorf("mcp-auth: refresh token missing or invalid")
		}
	}
	if a.Fields.ExpiresAt.Path != "" {
		value, pathErr := rawAt(entry, a.Fields.ExpiresAt.Path)
		if pathErr != nil {
			return "", "", 0, "", pathErr
		}
		expiresAtMS, err = decodeExpiry(value, a.Fields.ExpiresAt.Unit)
		if err != nil {
			return "", "", 0, "", err
		}
	}
	if a.Fields.ClientID != "" {
		clientID, err = stringAt(entry, a.Fields.ClientID)
		if err != nil {
			return "", "", 0, "", err
		}
	}
	return accessToken, refreshToken, expiresAtMS, clientID, nil
}

func (a Adapter) SharedServer(server, identity, url, key string) SharedServer {
	return SharedServer{
		Server: server, Identity: identity, URL: url, Key: key,
		RefreshTokenPath: a.Fields.RefreshToken, AccessTokenPath: a.Fields.AccessToken,
		ExpiresAtPath: a.Fields.ExpiresAt.Path, ExpiresAtUnit: a.Fields.ExpiresAt.Unit,
		ClientIDPath: a.Fields.ClientID,
	}
}

func clonePathList(paths [][]string) [][]string {
	if paths == nil {
		return nil
	}
	out := make([][]string, len(paths))
	for i := range paths {
		out[i] = append([]string(nil), paths[i]...)
	}
	return out
}

func rawAtPath(root map[string]json.RawMessage, path []string) (json.RawMessage, error) {
	var current = root
	for i, part := range path {
		raw, ok := current[part]
		if !ok {
			return nil, fmt.Errorf("mcp-auth: JSON path %q is absent", strings.Join(path, "."))
		}
		if i == len(path)-1 {
			return raw, nil
		}
		current, _ = objectOrNil(raw)
		if current == nil {
			return nil, fmt.Errorf("mcp-auth: JSON path %q is not an object", strings.Join(path[:i+1], "."))
		}
	}
	return marshalRawObject(root)
}

func objectOrNil(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	err := json.Unmarshal(raw, &object)
	return object, err
}

func setRawAtPath(root map[string]json.RawMessage, path []string, value json.RawMessage) error {
	if len(path) == 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil || object == nil {
			return fmt.Errorf("mcp-auth: entries map is not an object")
		}
		for key := range root {
			delete(root, key)
		}
		for key, entry := range object {
			root[key] = entry
		}
		return nil
	}
	return setRawAtPathRecursive(root, path, value)
}

func setRawAtPathRecursive(root map[string]json.RawMessage, path []string, value json.RawMessage) error {
	if len(path) == 1 {
		root[path[0]] = append(json.RawMessage(nil), value...)
		return nil
	}
	child := map[string]json.RawMessage{}
	if raw, ok := root[path[0]]; ok {
		var err error
		child, err = decodeObject(raw)
		if err != nil {
			return err
		}
	}
	if err := setRawAtPathRecursive(child, path[1:], value); err != nil {
		return err
	}
	encoded, err := marshalRawObject(child)
	if err != nil {
		return err
	}
	root[path[0]] = encoded
	return nil
}

func openParentWithoutLinks(root *os.Root, name string) (*os.Root, string, error) {
	clean := filepath.Clean(name)
	if clean != name || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("mcp-auth: invalid lock path %q", name)
	}
	return walkRoot(root, strings.Split(filepath.ToSlash(clean), "/"))
}

func walkRoot(root *os.Root, parts []string) (*os.Root, string, error) {
	// os.Root has no duplicate operation; create a child root for all parent components.
	// For a path at the root, opening "\." is still rooted at root.
	if len(parts) == 1 {
		child, err := root.OpenRoot(".")
		return child, parts[0], err
	}
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, "", err
	}
	for _, component := range parts[:len(parts)-1] {
		info, statErr := current.Lstat(component)
		if statErr != nil {
			current.Close()
			return nil, "", statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, "", fmt.Errorf("mcp-auth: refusing linked or non-directory lock parent %q", component)
		}
		next, openErr := current.OpenRoot(component)
		if openErr == nil {
			opened, statErr := next.Stat(".")
			if statErr != nil || !os.SameFile(info, opened) {
				next.Close()
				openErr = fmt.Errorf("mcp-auth: lock parent %q changed during open", component)
			}
		}
		current.Close()
		if openErr != nil {
			return nil, "", openErr
		}
		current = next
	}
	return current, parts[len(parts)-1], nil
}
