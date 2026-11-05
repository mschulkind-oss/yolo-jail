package mcpauthdaemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// managedCopy is host-private evidence of the exact replica bytes the broker
// wrote, not an assertion about a later native login at the same key.
type managedCopy struct {
	Workspace string          `json:"workspace"`
	Pack      string          `json:"pack"`
	Server    string          `json:"server"`
	Key       string          `json:"key"`
	URL       string          `json:"url"`
	ClientID  string          `json:"client_id"`
	Identity  string          `json:"identity"`
	Entry     json.RawMessage `json:"entry"`
}

func (b *broker) managedName(replica mcpauth.Replica, server mcpauth.SharedServer) string {
	sum := sha256.Sum256([]byte(b.share.Workspace + "\x00" + replica.Pack + "\x00" + server.Server + "\x00" + server.Key))
	return replica.Pack + "." + hex.EncodeToString(sum[:]) + ".managed.json"
}

func (b *broker) readManaged(replica mcpauth.Replica, server mcpauth.SharedServer) (*managedCopy, error) {
	root, err := paths.OpenStateDirRoot(b.canonical.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := paths.OpenRegularFileBeneath(root, b.managedName(replica, server))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, fmt.Errorf("mcp-auth: managed replica provenance is not private and singly linked; repair its permissions and retry")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var copy managedCopy
	if err := json.Unmarshal(data, &copy); err != nil || copy.Workspace != b.share.Workspace || copy.Pack != replica.Pack || copy.Server != server.Server || copy.Key != server.Key || copy.URL != server.URL || copy.ClientID != server.ClientID || len(copy.Identity) != 64 || len(copy.Entry) == 0 {
		return nil, fmt.Errorf("mcp-auth: invalid managed replica provenance; repair private broker state before retrying")
	}
	if _, err := hex.DecodeString(copy.Identity); err != nil {
		return nil, fmt.Errorf("mcp-auth: invalid managed replica identity; repair private broker state before retrying")
	}
	return &copy, nil
}

func (b *broker) writeManaged(replica mcpauth.Replica, server mcpauth.SharedServer, entry json.RawMessage) error {
	root, err := paths.OpenStateDirRoot(b.canonical.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	name := b.managedName(replica, server)
	if len(entry) == 0 {
		if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(managedCopy{Workspace: b.share.Workspace, Pack: replica.Pack, Server: server.Server, Key: server.Key, URL: server.URL, ClientID: server.ClientID, Identity: server.Identity, Entry: entry})
	if err != nil {
		return err
	}
	return paths.WriteRegularFileBeneath(root, name, data, 0o600)
}

func sameNativeJSON(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var value, trailing any
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		if err := dec.Decode(&trailing); err != io.EOF {
			return nil, false
		}
		return value, true
	}
	left, leftOK := decode(a)
	right, rightOK := decode(b)
	return leftOK && rightOK && reflect.DeepEqual(left, right)
}
